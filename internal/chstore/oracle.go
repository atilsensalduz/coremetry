package chstore

import (
	"context"
	"hash/fnv"
	"math"
	"time"
)

// OracleMetrics is the OracleDB-receiver-flavoured drill-down
// payload — what the operator sees when expanding a row whose
// db.system = "oracle" on /databases. The numbers come from the
// OpenTelemetry oracledb receiver, which scrapes V$ views on
// the database itself and publishes them as oracledb.*
// instrument-shaped metric_points.
//
// When the receiver isn't wired up (or the operator is still
// proving the integration on a staging cluster), we fall back
// to deterministic synthetic values so the UI doesn't look
// empty — Synthetic=true tells the frontend to render a
// "demo data" badge over the panel.
//
// The two cumulative gauges (logical/physical reads) get
// converted to per-second rates server-side; the operator
// reads "37k logical reads/sec" rather than the raw
// monotonic counter that Oracle exposes.
type OracleMetrics struct {
	Instance       string             `json:"instance"`
	Synthetic      bool               `json:"synthetic"`
	WindowSeconds  float64            `json:"windowSeconds"`
	Sessions       OracleGaugeWithCap `json:"sessions"`       // current usage + soft limit
	Processes      OracleGaugeWithCap `json:"processes"`      // current usage + soft limit
	CPUTimeSec     float64            `json:"cpuTimeSec"`     // cumulative CPU seconds over window
	PGAMemoryBytes float64            `json:"pgaMemoryBytes"` // process global area
	LogicalReadsPS float64            `json:"logicalReadsPerSec"`
	PhysicalReadsPS float64           `json:"physicalReadsPerSec"`
	CacheHitPct    float64            `json:"cacheHitPct"` // derived: 1 - physical/logical
	HardParsesPS   float64            `json:"hardParsesPerSec"`
	ParseCallsPS   float64            `json:"parseCallsPerSec"`
	ExecutionsPS   float64            `json:"executionsPerSec"`
	UserCommitsPS  float64            `json:"userCommitsPerSec"`
	RollbacksPS    float64            `json:"userRollbacksPerSec"`
	TransactionsPS float64            `json:"transactionsPerSec"`
	Tablespaces    []OracleTablespace `json:"tablespaces"`
}

// OracleGaugeWithCap is a (usage, limit) pair — Oracle exposes
// both as separate metrics (oracledb.sessions.usage,
// oracledb.sessions.limit). The frontend renders these as a
// progress bar so the operator sees "67/200 sessions" at a
// glance.
type OracleGaugeWithCap struct {
	Usage float64 `json:"usage"`
	Limit float64 `json:"limit"`
}

// OracleTablespace is one row of the per-tablespace size table.
// oracledb.tablespace_size.usage / .limit are dimensioned by
// the "tablespace_name" attribute, so the operator can spot a
// specific tablespace running out of room (the #1 reason an
// Oracle DBA gets paged at 3am).
type OracleTablespace struct {
	Name      string  `json:"name"`
	UsedBytes float64 `json:"usedBytes"`
	MaxBytes  float64 `json:"maxBytes"`
	UsedPct   float64 `json:"usedPct"`
}

// GetOracleMetrics returns the OracleDB-receiver-style drill-down
// for one instance. When no oracledb.* points exist in the
// window, returns a deterministic synthetic payload with
// Synthetic=true so the UI can still render and the operator
// can visualise what the panel will look like once their
// receiver is online.
//
// The instance argument matches peer_service on the spans the
// row was derived from — we use it both as a deterministic
// seed for synthetic generation (same instance → same fake
// numbers across reloads) and as a `instance` attribute
// filter on metric_points if the receiver tags points with
// it (newer oracledb receiver versions do).
func (s *Store) GetOracleMetrics(
	ctx context.Context, instance string, from, to time.Time,
) (*OracleMetrics, error) {
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	windowSec := to.Sub(from).Seconds()
	if windowSec <= 0 {
		windowSec = 60
	}

	out := &OracleMetrics{
		Instance:      instance,
		WindowSeconds: windowSec,
		Tablespaces:   []OracleTablespace{},
	}

	// Optional instance-scoped filter. The OracleDB receiver
	// publishes points with res_keys carrying the service it
	// scraped against; we look for either an `instance`
	// attribute (newer receivers) or a `service.name` resource
	// key fallback. The hasInstanceFilter switch lets us drop
	// the filter when the operator's setup doesn't tag points
	// per-instance (single-DB deployment).
	hasInstanceFilter := instance != "" && instance != "unknown"

	// Pull the latest value per metric over the window. We use
	// argMax(value, time) to grab the freshest reading; that's
	// the natural read for a gauge-shaped metric. Cumulative
	// counters get their delta from min/max below.
	gauges, err := s.queryOracleGauges(ctx, from, to, instance, hasInstanceFilter)
	if err == nil && len(gauges) > 0 {
		out.Sessions.Usage = gauges["oracledb.sessions.usage"]
		out.Sessions.Limit = gauges["oracledb.sessions.limit"]
		out.Processes.Usage = gauges["oracledb.processes.usage"]
		out.Processes.Limit = gauges["oracledb.processes.limit"]
		out.PGAMemoryBytes = gauges["oracledb.pga_memory"]
	}

	// Cumulative counters → per-second rates. max-min over the
	// window divided by windowSec is the OTel-recommended
	// derivation for monotonic sums when the SDK doesn't
	// already export deltas.
	rates, err := s.queryOracleRates(ctx, from, to, instance, hasInstanceFilter, windowSec)
	if err == nil && len(rates) > 0 {
		out.CPUTimeSec = rates["oracledb.cpu_time"] * windowSec // back-multiply: total over window
		out.LogicalReadsPS = rates["oracledb.logical_reads"]
		out.PhysicalReadsPS = rates["oracledb.physical_reads"]
		out.HardParsesPS = rates["oracledb.hard_parses"]
		out.ParseCallsPS = rates["oracledb.parse_calls"]
		out.ExecutionsPS = rates["oracledb.executions"]
		out.UserCommitsPS = rates["oracledb.user_commits"]
		out.RollbacksPS = rates["oracledb.user_rollbacks"]
		out.TransactionsPS = rates["oracledb.transactions"]
	}

	// Tablespace breakdown.
	tspaces, err := s.queryOracleTablespaces(ctx, from, to, instance, hasInstanceFilter)
	if err == nil && len(tspaces) > 0 {
		out.Tablespaces = tspaces
	}

	// Detect "no real data" — if every numeric field is zero
	// AND tablespace list is empty, the receiver isn't wired
	// up. Fall back to synthetic.
	if isOracleEmpty(out) {
		fillSynthetic(out, instance)
		out.Synthetic = true
	}

	// Derive cache hit % from logical / physical reads. Never
	// trust user-typed math: if logical is zero, hit % is
	// undefined; clamp to 0..100.
	if out.LogicalReadsPS > 0 {
		hit := 1 - (out.PhysicalReadsPS / out.LogicalReadsPS)
		if hit < 0 {
			hit = 0
		}
		if hit > 1 {
			hit = 1
		}
		out.CacheHitPct = hit * 100
	}

	return out, nil
}

func (s *Store) queryOracleGauges(
	ctx context.Context, from, to time.Time, instance string, withInstance bool,
) (map[string]float64, error) {
	// argMax over the window picks the freshest point per
	// metric — exactly what a gauge reads as "right now".
	q := `
		SELECT metric, argMax(value, time) AS v
		FROM metric_points
		WHERE time >= ? AND time <= ?
		  AND startsWith(metric, 'oracledb.')
		` + oracleInstanceClause(withInstance) + `
		GROUP BY metric`
	args := []any{from, to}
	if withInstance {
		args = append(args, instance, instance)
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var m string
		var v float64
		if err := rows.Scan(&m, &v); err == nil {
			out[m] = v
		}
	}
	return out, nil
}

func (s *Store) queryOracleRates(
	ctx context.Context, from, to time.Time, instance string, withInstance bool, windowSec float64,
) (map[string]float64, error) {
	// For cumulative counters: (max - min) / window seconds.
	// CH's max - min on monotonic series tolerates one reset
	// in the window cleanly (rate goes to 0 for that reading,
	// which is the safer underestimate vs a wrap-around spike).
	q := `
		SELECT metric, (max(value) - min(value)) / ? AS rate
		FROM metric_points
		WHERE time >= ? AND time <= ?
		  AND startsWith(metric, 'oracledb.')
		` + oracleInstanceClause(withInstance) + `
		GROUP BY metric`
	args := []any{windowSec, from, to}
	if withInstance {
		args = append(args, instance, instance)
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var m string
		var v float64
		if err := rows.Scan(&m, &v); err == nil {
			if v < 0 {
				v = 0 // counter reset → suppress
			}
			out[m] = v
		}
	}
	return out, nil
}

func (s *Store) queryOracleTablespaces(
	ctx context.Context, from, to time.Time, instance string, withInstance bool,
) ([]OracleTablespace, error) {
	// tablespace_name is an attribute on oracledb.tablespace_size.*
	// points. We pull both .usage and .limit, latest per
	// (tablespace, metric), and join client-side. CH's
	// arrayElement-indexOf lookup is the canonical pattern for
	// key/value attr arrays in this codebase.
	q := `
		SELECT
			attr_values[indexOf(attr_keys, 'tablespace_name')] AS ts,
			metric,
			argMax(value, time) AS v
		FROM metric_points
		WHERE time >= ? AND time <= ?
		  AND metric IN ('oracledb.tablespace_size.usage', 'oracledb.tablespace_size.limit')
		  AND has(attr_keys, 'tablespace_name')
		` + oracleInstanceClause(withInstance) + `
		GROUP BY ts, metric
		ORDER BY ts`
	args := []any{from, to}
	if withInstance {
		args = append(args, instance, instance)
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*OracleTablespace{}
	for rows.Next() {
		var name, metric string
		var v float64
		if err := rows.Scan(&name, &metric, &v); err != nil {
			continue
		}
		if name == "" {
			continue
		}
		t, ok := byName[name]
		if !ok {
			t = &OracleTablespace{Name: name}
			byName[name] = t
		}
		switch metric {
		case "oracledb.tablespace_size.usage":
			t.UsedBytes = v
		case "oracledb.tablespace_size.limit":
			t.MaxBytes = v
		}
	}
	out := make([]OracleTablespace, 0, len(byName))
	for _, t := range byName {
		if t.MaxBytes > 0 {
			t.UsedPct = (t.UsedBytes / t.MaxBytes) * 100
		}
		out = append(out, *t)
	}
	return out, nil
}

func oracleInstanceClause(withInstance bool) string {
	if !withInstance {
		return ""
	}
	// Match either an `instance` attribute or a `service.name`
	// resource key (older oracledb receiver setups tag at
	// resource level). Pass the instance value twice.
	return `AND (
		attr_values[indexOf(attr_keys, 'instance')] = ?
		OR res_values[indexOf(res_keys, 'service.name')] = ?
	)`
}

func isOracleEmpty(o *OracleMetrics) bool {
	if len(o.Tablespaces) > 0 {
		return false
	}
	return o.Sessions.Usage == 0 && o.Sessions.Limit == 0 &&
		o.Processes.Usage == 0 && o.PGAMemoryBytes == 0 &&
		o.LogicalReadsPS == 0 && o.PhysicalReadsPS == 0 &&
		o.ExecutionsPS == 0 && o.UserCommitsPS == 0 &&
		o.TransactionsPS == 0
}

// fillSynthetic populates o with plausible, deterministic
// values seeded from the instance name. Same instance string
// produces the same numbers across reloads — the operator's
// eye gets a stable preview rather than randomness that makes
// the UI look broken.
//
// Values are calibrated to read like a moderately busy OLTP
// Oracle instance (a couple hundred sessions, ~30k logical
// reads/sec, ~1% physical/logical ratio).
func fillSynthetic(o *OracleMetrics, instance string) {
	seed := oracleSeed(instance)
	rnd := func(min, max float64) float64 {
		seed = seed*1103515245 + 12345
		f := float64(seed&0x7fffffff) / float64(0x7fffffff)
		return min + f*(max-min)
	}
	sessionsLimit := math.Round(rnd(150, 400))
	processesLimit := math.Round(rnd(200, 500))
	o.Sessions = OracleGaugeWithCap{
		Usage: math.Round(sessionsLimit * rnd(0.25, 0.75)),
		Limit: sessionsLimit,
	}
	o.Processes = OracleGaugeWithCap{
		Usage: math.Round(processesLimit * rnd(0.30, 0.65)),
		Limit: processesLimit,
	}
	o.CPUTimeSec = rnd(40, 280) * (o.WindowSeconds / 60) // scale to window length
	o.PGAMemoryBytes = rnd(1.5, 6.5) * 1024 * 1024 * 1024
	o.LogicalReadsPS = rnd(15000, 60000)
	o.PhysicalReadsPS = o.LogicalReadsPS * rnd(0.005, 0.03) // 0.5–3% miss rate
	o.HardParsesPS = rnd(2, 25)
	o.ParseCallsPS = rnd(100, 800)
	o.ExecutionsPS = rnd(400, 3500)
	o.UserCommitsPS = rnd(20, 220)
	o.RollbacksPS = o.UserCommitsPS * rnd(0.005, 0.04)
	o.TransactionsPS = o.UserCommitsPS + o.RollbacksPS
	// Synthetic tablespaces — a typical Oracle DB has SYSTEM /
	// SYSAUX / USERS / TEMP / UNDOTBS1 at minimum.
	for _, name := range []string{"SYSTEM", "SYSAUX", "USERS", "UNDOTBS1", "TEMP"} {
		maxBytes := rnd(2, 32) * 1024 * 1024 * 1024
		used := maxBytes * rnd(0.20, 0.85)
		o.Tablespaces = append(o.Tablespaces, OracleTablespace{
			Name:      name,
			UsedBytes: used,
			MaxBytes:  maxBytes,
			UsedPct:   (used / maxBytes) * 100,
		})
	}
}

func oracleSeed(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}
