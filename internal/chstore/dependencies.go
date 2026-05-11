package chstore

import (
	"context"
	"strings"
	"time"
)

// DBInstance is one row of the /databases overview — Dynatrace's
// "Technologies → Databases" surface. Each row is a unique
// (system, instance) pair observed in span traffic over the
// requested window. Drives the top-level Databases page so an
// operator can answer "which DBs is the platform calling, and
// which are slow / erroring" without per-service drill-down.
//
// Caller list is bounded to top-5 by call count so a long-tail
// noisy caller doesn't drown the bigger consumers; UI shows the
// full list on click-through to the instance detail.
type DBInstance struct {
	System     string   `json:"system"`     // db.system: postgresql / redis / oracle / mongo / mysql / cassandra / elasticsearch / …
	Instance   string   `json:"instance"`   // peer.service when populated, else 'unknown'
	SpanCount  uint64   `json:"spanCount"`
	ErrorCount uint64   `json:"errorCount"`
	ErrorRate  float64  `json:"errorRate"`  // 0..100
	AvgMs      float64  `json:"avgDurationMs"`
	P99Ms      float64  `json:"p99DurationMs"`
	Callers    []string `json:"callers"`    // top-5 calling services
}

// MessagingInstance is the parallel structure for /messaging —
// Kafka / RabbitMQ / IBM MQ / NATS / etc. Same shape as
// DBInstance so the frontend renders both tables with a shared
// component.
//
// Destination tries to be the queue / topic name. messaging
// SDKs in OTel populate `messaging.destination.name` as an
// attribute; we resolve it via the attr_keys/attr_values arrays.
// peer.service is the fallback (Kafka brokers register
// themselves there).
type MessagingInstance struct {
	System      string   `json:"system"`      // kafka / rabbitmq / ibmmq / nats / sqs / kinesis
	Destination string   `json:"destination"` // queue / topic name (resolved from messaging.destination.name or peer.service)
	SpanCount   uint64   `json:"spanCount"`
	ErrorCount  uint64   `json:"errorCount"`
	ErrorRate   float64  `json:"errorRate"`
	AvgMs       float64  `json:"avgDurationMs"`
	P99Ms       float64  `json:"p99DurationMs"`
	Callers     []string `json:"callers"`
}

// DBCallerBreakdown is one row of the per-(service, pod)
// breakdown shown in the DB detail drawer. Pod is derived from
// resource.host.name on the calling span — k8s pod name on
// Kubernetes deployments, VM hostname elsewhere. Same shape
// works for the messaging detail drawer below.
//
// Role is populated only by the messaging detail (span.kind
// promoted into the row: "producer" / "consumer" / "client" /
// "server" / "internal"). For DB rows it's empty since DB
// calls are always CLIENT-kind by OTel convention; the column
// would always read the same.
type DBCallerBreakdown struct {
	Service    string  `json:"service"`
	Pod        string  `json:"pod"`
	Role       string  `json:"role,omitempty"`
	SpanCount  uint64  `json:"spanCount"`
	ErrorCount uint64  `json:"errorCount"`
	ErrorRate  float64 `json:"errorRate"`
	AvgMs      float64 `json:"avgDurationMs"`
	P99Ms      float64 `json:"p99DurationMs"`
}

// DBOpStat is one row of the top-operations table in the DB
// detail drawer. Statement is truncated to 80 chars server-side
// so a 4 KB SQL string doesn't bloat the JSON envelope.
type DBOpStat struct {
	Statement string  `json:"statement"`
	Count     uint64  `json:"count"`
	AvgMs     float64 `json:"avgDurationMs"`
}

// DBDetail is the full payload for /api/databases/detail. The
// frontend renders it as a three-section drawer: time-series
// (call rate), per-(service, pod) breakdown, top operations.
type DBDetail struct {
	System     string              `json:"system"`
	Instance   string              `json:"instance"`
	SpanCount  uint64              `json:"spanCount"`
	ErrorCount uint64              `json:"errorCount"`
	ErrorRate  float64             `json:"errorRate"`
	AvgMs      float64             `json:"avgDurationMs"`
	P99Ms      float64             `json:"p99DurationMs"`
	Callers    []DBCallerBreakdown `json:"callers"`
	TopOps     []DBOpStat          `json:"topOps"`
}

// GetDatabaseDetail returns per-(service, pod) breakdown + top
// operations for one (db_system, instance) tuple. Driven by the
// detail drawer on /databases. Two bounded GROUP BYs (LIMIT
// 100 and LIMIT 20) keep the query cheap even on multi-billion
// span tables; the same idx_db_system + service_name primary
// key prune that powers the overview applies here.
func (s *Store) GetDatabaseDetail(
	ctx context.Context, system, instance string, from, to time.Time,
) (*DBDetail, error) {
	if system == "" {
		return nil, nil
	}
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	// instance == "unknown" maps to "peer_service is empty"; the
	// instance string is otherwise compared verbatim against
	// peer_service so a typo in the URL doesn't accidentally
	// match more spans than intended.
	instancePredicate := "peer_service = ?"
	instanceArg := instance
	if instance == "unknown" {
		instancePredicate = "(peer_service = '' OR peer_service IS NULL)"
		instanceArg = ""
	}

	// Initialize empty slices so the JSON marshal emits [] rather
	// than null — the SPA's drawer does `[...data.callers]` /
	// `data.topOps.length` and a null spread / null property
	// access crashes the page boundary.
	out := &DBDetail{
		System: system, Instance: instance,
		Callers: []DBCallerBreakdown{},
		TopOps:  []DBOpStat{},
	}

	// Aggregate stats for the (system, instance) pair.
	row := s.conn.QueryRow(ctx, `
		SELECT count(),
		       countIf(status_code = 'error'),
		       avg(duration) / 1e6,
		       quantile(0.99)(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND db_system = ? AND `+instancePredicate+`
		SETTINGS max_execution_time = 15`,
		append([]any{from, to, system}, argIfNeeded(instancePredicate, instanceArg)...)...)
	if err := row.Scan(&out.SpanCount, &out.ErrorCount, &out.AvgMs, &out.P99Ms); err != nil {
		return nil, err
	}
	if out.SpanCount > 0 {
		out.ErrorRate = float64(out.ErrorCount) / float64(out.SpanCount) * 100
	}

	// Per-(service, pod) breakdown. host_name carries the
	// resource.host.name set by the OTel SDK; for k8s deployments
	// that's the pod name, which is what operators want to see.
	rows, err := s.conn.Query(ctx, `
		SELECT service_name,
		       coalesce(nullIf(host_name, ''), '(unknown)') AS pod,
		       count(), countIf(status_code = 'error'),
		       avg(duration) / 1e6,
		       quantile(0.99)(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND db_system = ? AND `+instancePredicate+`
		GROUP BY service_name, pod
		ORDER BY count() DESC
		LIMIT 100
		SETTINGS max_execution_time = 15`,
		append([]any{from, to, system}, argIfNeeded(instancePredicate, instanceArg)...)...)
	if err != nil {
		return out, nil // partial result fine — overview-only mode
	}
	defer rows.Close()
	for rows.Next() {
		var b DBCallerBreakdown
		if err := rows.Scan(&b.Service, &b.Pod, &b.SpanCount, &b.ErrorCount, &b.AvgMs, &b.P99Ms); err != nil {
			continue
		}
		if b.SpanCount > 0 {
			b.ErrorRate = float64(b.ErrorCount) / float64(b.SpanCount) * 100
		}
		out.Callers = append(out.Callers, b)
	}

	// Top operations — first 80 chars of db_statement. We collapse
	// duplicate SQL by truncating because real-world SQL has
	// inline parameters (`SELECT … WHERE id = 17`) that explode
	// the cardinality; 80 chars catches the SELECT / UPDATE /
	// INSERT prefix + table name which is what an SRE actually
	// pivots on.
	opRows, err := s.conn.Query(ctx, `
		SELECT substring(db_statement, 1, 80) AS stmt,
		       count(), avg(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND db_system = ? AND `+instancePredicate+`
		  AND db_statement != ''
		GROUP BY stmt
		ORDER BY count() DESC
		LIMIT 20
		SETTINGS max_execution_time = 15`,
		append([]any{from, to, system}, argIfNeeded(instancePredicate, instanceArg)...)...)
	if err != nil {
		return out, nil
	}
	defer opRows.Close()
	for opRows.Next() {
		var op DBOpStat
		if err := opRows.Scan(&op.Statement, &op.Count, &op.AvgMs); err != nil {
			continue
		}
		op.Statement = strings.TrimSpace(op.Statement)
		out.TopOps = append(out.TopOps, op)
	}
	return out, nil
}

// MessagingDetail mirrors DBDetail for queues / topics. Op stats
// here are per-(operation name) since messaging spans don't
// carry a SQL-equivalent; the operation (send / receive /
// process) plus the destination already discriminates work.
type MessagingDetail struct {
	System      string              `json:"system"`
	Destination string              `json:"destination"`
	SpanCount   uint64              `json:"spanCount"`
	ErrorCount  uint64              `json:"errorCount"`
	ErrorRate   float64             `json:"errorRate"`
	AvgMs       float64             `json:"avgDurationMs"`
	P99Ms       float64             `json:"p99DurationMs"`
	Callers     []DBCallerBreakdown `json:"callers"` // same shape — service / pod / RED
	TopOps      []DBOpStat          `json:"topOps"`  // statement = span name (send / receive / process)
}

func (s *Store) GetMessagingDetail(
	ctx context.Context, system, destination string, from, to time.Time,
) (*MessagingDetail, error) {
	if system == "" {
		return nil, nil
	}
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	// Destination resolution mirrors the overview: try
	// messaging.destination.name → messaging.destination →
	// peer.service. We pass the same destination string back as
	// the constraint by reconstructing the coalesce expression
	// in the WHERE.
	destExpr := `coalesce(
		nullIf(attr_values[indexOf(attr_keys, 'messaging.destination.name')], ''),
		nullIf(attr_values[indexOf(attr_keys, 'messaging.destination')], ''),
		nullIf(peer_service, ''),
		'unknown'
	)`

	out := &MessagingDetail{
		System: system, Destination: destination,
		Callers: []DBCallerBreakdown{},
		TopOps:  []DBOpStat{},
	}

	row := s.conn.QueryRow(ctx, `
		SELECT count(),
		       countIf(status_code = 'error'),
		       avg(duration) / 1e6,
		       quantile(0.99)(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND msg_system = ?
		  AND `+destExpr+` = ?
		SETTINGS max_execution_time = 15`,
		from, to, system, destination)
	if err := row.Scan(&out.SpanCount, &out.ErrorCount, &out.AvgMs, &out.P99Ms); err != nil {
		return nil, err
	}
	if out.SpanCount > 0 {
		out.ErrorRate = float64(out.ErrorCount) / float64(out.SpanCount) * 100
	}

	// Messaging breakdown groups by kind too so producers and
	// consumers surface as separate rows even when they share
	// service + pod (a service that both publishes to one topic
	// and consumes from another, common in event-driven
	// architectures). The frontend renders a role badge per row.
	rows, err := s.conn.Query(ctx, `
		SELECT service_name,
		       coalesce(nullIf(host_name, ''), '(unknown)') AS pod,
		       coalesce(nullIf(kind, ''), 'client')         AS role,
		       count(), countIf(status_code = 'error'),
		       avg(duration) / 1e6,
		       quantile(0.99)(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND msg_system = ?
		  AND `+destExpr+` = ?
		GROUP BY service_name, pod, role
		ORDER BY count() DESC
		LIMIT 100
		SETTINGS max_execution_time = 15`,
		from, to, system, destination)
	if err != nil {
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var b DBCallerBreakdown
		if err := rows.Scan(&b.Service, &b.Pod, &b.Role, &b.SpanCount, &b.ErrorCount, &b.AvgMs, &b.P99Ms); err != nil {
			continue
		}
		if b.SpanCount > 0 {
			b.ErrorRate = float64(b.ErrorCount) / float64(b.SpanCount) * 100
		}
		out.Callers = append(out.Callers, b)
	}

	// Top operations — for messaging the span name is the
	// useful pivot (e.g. "publish kafka.orders" / "consume
	// kafka.orders"). No truncation needed; OTel span names
	// are short by spec.
	opRows, err := s.conn.Query(ctx, `
		SELECT name AS stmt, count(), avg(duration) / 1e6
		FROM spans
		WHERE time >= ? AND time <= ? AND msg_system = ?
		  AND `+destExpr+` = ?
		GROUP BY stmt
		ORDER BY count() DESC
		LIMIT 20
		SETTINGS max_execution_time = 15`,
		from, to, system, destination)
	if err != nil {
		return out, nil
	}
	defer opRows.Close()
	for opRows.Next() {
		var op DBOpStat
		if err := opRows.Scan(&op.Statement, &op.Count, &op.AvgMs); err != nil {
			continue
		}
		out.TopOps = append(out.TopOps, op)
	}
	return out, nil
}

// argIfNeeded returns []any{arg} when the predicate contains a
// "?" placeholder, otherwise nil. Lets the detail queries share
// one SQL string between "instance = ?" and the special
// "(peer_service = '' OR IS NULL)" no-arg branch.
func argIfNeeded(predicate string, arg string) []any {
	if strings.Contains(predicate, "?") {
		return []any{arg}
	}
	return nil
}

// GetDatabases returns one row per (db_system, peer_service)
// over the window. Skips spans where db_system is empty so we
// don't count non-DB traffic. Uses the idx_db_system skip-index
// for partition pruning so the scan stays bounded at billion-
// span scale.
//
// Top-5 callers per row come from a paired groupArray + LIMIT
// in a subquery — single query trip, no per-row fan-out.
func (s *Store) GetDatabases(ctx context.Context, from, to time.Time) ([]DBInstance, error) {
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	// Two-step query: aggregate stats per (system, instance) +
	// fetch top callers separately. The callers query groups by
	// (system, instance, service_name) then arrayJoin-folds; one
	// trip, no correlated subquery.
	rows, err := s.conn.Query(ctx, `
		SELECT db_system,
		       coalesce(nullIf(peer_service, ''), 'unknown') AS instance,
		       count()                                       AS span_count,
		       countIf(status_code = 'error')                AS error_count,
		       avg(duration) / 1e6                           AS avg_ms,
		       quantile(0.99)(duration) / 1e6                AS p99_ms
		FROM spans
		WHERE time >= ? AND time <= ? AND db_system != ''
		GROUP BY db_system, instance
		ORDER BY span_count DESC
		LIMIT 200
		SETTINGS max_execution_time = 20`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DBInstance{}
	type key struct{ system, instance string }
	idxByKey := map[key]int{}
	for rows.Next() {
		var r DBInstance
		if err := rows.Scan(&r.System, &r.Instance, &r.SpanCount, &r.ErrorCount, &r.AvgMs, &r.P99Ms); err != nil {
			return nil, err
		}
		if r.SpanCount > 0 {
			r.ErrorRate = float64(r.ErrorCount) / float64(r.SpanCount) * 100
		}
		r.Callers = []string{}
		out = append(out, r)
		idxByKey[key{r.System, r.Instance}] = len(out) - 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	// Top callers per (system, instance) — single GROUP BY pass
	// across the same window. Ordered by per-key span count then
	// trimmed in Go to top-5 so a 100-caller DB doesn't blow up
	// the response. Could be done with topK aggregate but the
	// readability tradeoff isn't worth it at this row count.
	cRows, err := s.conn.Query(ctx, `
		SELECT db_system,
		       coalesce(nullIf(peer_service, ''), 'unknown') AS instance,
		       service_name, count() AS c
		FROM spans
		WHERE time >= ? AND time <= ? AND db_system != ''
		GROUP BY db_system, instance, service_name
		ORDER BY db_system, instance, c DESC
		SETTINGS max_execution_time = 15`, from, to)
	if err != nil {
		return out, nil // partial result is fine — callers are optional
	}
	defer cRows.Close()
	for cRows.Next() {
		var system, instance, svc string
		var c uint64
		if err := cRows.Scan(&system, &instance, &svc, &c); err != nil {
			continue
		}
		i, ok := idxByKey[key{system, instance}]
		if !ok {
			continue
		}
		if len(out[i].Callers) < 5 && svc != "" {
			out[i].Callers = append(out[i].Callers, svc)
		}
	}
	return out, nil
}

// GetMessaging is the structural parallel for messaging systems.
// Resolves the destination name from messaging.destination.name
// when present (OTel semconv), falling back to peer.service.
// arrayElement / indexOf is cheap because attr_keys is bounded
// per row + the WHERE prunes by msg_system on the indexed column
// first.
func (s *Store) GetMessaging(ctx context.Context, from, to time.Time) ([]MessagingInstance, error) {
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	rows, err := s.conn.Query(ctx, `
		SELECT msg_system,
		       coalesce(
		         nullIf(attr_values[indexOf(attr_keys, 'messaging.destination.name')], ''),
		         nullIf(attr_values[indexOf(attr_keys, 'messaging.destination')], ''),
		         nullIf(peer_service, ''),
		         'unknown'
		       ) AS destination,
		       count()                                       AS span_count,
		       countIf(status_code = 'error')                AS error_count,
		       avg(duration) / 1e6                           AS avg_ms,
		       quantile(0.99)(duration) / 1e6                AS p99_ms
		FROM spans
		WHERE time >= ? AND time <= ? AND msg_system != ''
		GROUP BY msg_system, destination
		ORDER BY span_count DESC
		LIMIT 200
		SETTINGS max_execution_time = 20`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MessagingInstance{}
	type key struct{ system, destination string }
	idxByKey := map[key]int{}
	for rows.Next() {
		var r MessagingInstance
		if err := rows.Scan(&r.System, &r.Destination, &r.SpanCount, &r.ErrorCount, &r.AvgMs, &r.P99Ms); err != nil {
			return nil, err
		}
		if r.SpanCount > 0 {
			r.ErrorRate = float64(r.ErrorCount) / float64(r.SpanCount) * 100
		}
		r.Callers = []string{}
		out = append(out, r)
		idxByKey[key{r.System, r.Destination}] = len(out) - 1
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	cRows, err := s.conn.Query(ctx, `
		SELECT msg_system,
		       coalesce(
		         nullIf(attr_values[indexOf(attr_keys, 'messaging.destination.name')], ''),
		         nullIf(attr_values[indexOf(attr_keys, 'messaging.destination')], ''),
		         nullIf(peer_service, ''),
		         'unknown'
		       ) AS destination,
		       service_name, count() AS c
		FROM spans
		WHERE time >= ? AND time <= ? AND msg_system != ''
		GROUP BY msg_system, destination, service_name
		ORDER BY msg_system, destination, c DESC
		SETTINGS max_execution_time = 15`, from, to)
	if err != nil {
		return out, nil
	}
	defer cRows.Close()
	for cRows.Next() {
		var system, destination, svc string
		var c uint64
		if err := cRows.Scan(&system, &destination, &svc, &c); err != nil {
			continue
		}
		i, ok := idxByKey[key{system, destination}]
		if !ok {
			continue
		}
		if len(out[i].Callers) < 5 && svc != "" {
			out[i].Callers = append(out[i].Callers, svc)
		}
	}
	return out, nil
}
