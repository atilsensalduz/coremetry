package chstore

import (
	"context"
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
