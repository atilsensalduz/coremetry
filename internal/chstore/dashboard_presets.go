package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// Preset dashboards seeded into a fresh install — SRE-incident
// perspective inspired by patterns from Datadog, Honeycomb,
// and Dynatrace. The bundle is intentionally smaller than a
// dump-everything-set: each dashboard answers a specific
// operator question (Golden Signals, RED, war room, latency,
// error hunting, infra, dependencies, database) so operators
// jump to the right dashboard for the situation.
//
// Versioning: the seeded set carries an opaque version string
// stored in system_settings under "preset_dashboards_version".
// On each boot we compare; if it doesn't match, we wipe rows
// whose ID starts with "preset-" (old + current bundle) and
// re-seed the new set, then write the new version. Bumping
// presetVersion forces a re-seed.
//
// User-created dashboards (any ID without the preset- prefix)
// are never touched — even an admin who renamed a preset
// retains their rename because the old preset row gets deleted
// on bundle upgrade while their renamed copy lives under its
// new ID.

const presetVersion = "sre-v1"

func (s *Store) SeedPresetDashboards(ctx context.Context) error {
	storedVersion, _ := s.GetSetting(ctx, "preset_dashboards_version")
	current := string(storedVersion)

	row := s.conn.QueryRow(ctx, `SELECT count() FROM dashboards FINAL`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("count dashboards: %w", err)
	}
	if n == 0 {
		return s.seedAndStamp(ctx)
	}
	if current == presetVersion {
		return nil
	}
	// Bundle change → drop old preset-* rows so the new bundle
	// takes their tiles cleanly.
	if err := s.conn.Exec(ctx, `ALTER TABLE dashboards DELETE WHERE id LIKE 'preset-%'`); err != nil {
		return fmt.Errorf("delete old preset dashboards: %w", err)
	}
	return s.seedAndStamp(ctx)
}

func (s *Store) seedAndStamp(ctx context.Context) error {
	for _, d := range presetDashboards() {
		if err := s.UpsertDashboard(ctx, d); err != nil {
			return fmt.Errorf("seed dashboard %s: %w", d.ID, err)
		}
		log.Printf("[chstore] seeded preset dashboard %q", d.Name)
	}
	if err := s.PutSetting(ctx, "preset_dashboards_version", []byte(presetVersion)); err != nil {
		return fmt.Errorf("stamp preset version: %w", err)
	}
	return nil
}

// ── Preset bundle ───────────────────────────────────────────────────────────

func presetDashboards() []Dashboard {
	return []Dashboard{
		presetSREGoldenSignals(),
		presetServiceRED(),
		presetIncidentWarRoom(),
		presetLatencyInvestigation(),
		presetErrorHunting(),
		presetInfrastructure(),
		presetDatabase(),
		presetExternalDeps(),
	}
}

// Panel construction helpers — keep the JSON shape close to the
// frontend Panel type (id, type, title, width, config). Width is
// the 4-col grid factor: 1=quarter, 2=half, 3=three-quarters,
// 4=full.

type panel struct {
	ID     string `json:"id"`
	Type   string `json:"type"`   // metric | spanmetric | stat | markdown | row
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Config any    `json:"config"`
}

// row builds a Grafana-style row marker — full-width "header"
// panel that the dashboard renderer treats as a collapsible
// group separator.
func row(id, title string) panel {
	return panel{ID: id, Type: "row", Title: title, Width: 4, Config: map[string]any{}}
}

type spanCfg struct {
	Agg     string `json:"agg"`
	Field   string `json:"field,omitempty"`
	GroupBy string `json:"groupBy,omitempty"`
	DSL     string `json:"dsl,omitempty"`
	Filters string `json:"filters,omitempty"`
	Step    int    `json:"step,omitempty"`
}

// metricCfg backs metric-panel queries against the metric_points
// table — used by the Infrastructure dashboard for CPU/memory/
// heap timeseries.
type metricCfg struct {
	MetricName string `json:"metricName"`
	Service    string `json:"service,omitempty"`
	Agg        string `json:"agg,omitempty"`
	GroupBy    string `json:"groupBy,omitempty"`
	Step       int    `json:"step,omitempty"`
	Filters    string `json:"filters,omitempty"`
}

type statCfg struct {
	Source   string     `json:"source"` // "spanmetric" or "metric"
	Span     *spanCfg   `json:"span,omitempty"`
	Metric   *metricCfg `json:"metric,omitempty"`
	Unit     string     `json:"unit,omitempty"`
	Decimals int        `json:"decimals,omitempty"`
}

type mdCfg struct {
	Text string `json:"text"`
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("preset dashboard JSON: %v", err))
	}
	return b
}

func dash(id, name, desc string, panels []panel, vars ...dashVar) Dashboard {
	return Dashboard{
		ID:          id,
		Name:        name,
		Description: desc,
		Panels:      mustJSON(panels),
		Variables:   mustJSON(vars),
	}
}

type dashVar struct {
	Name         string   `json:"name"`
	Label        string   `json:"label,omitempty"`
	Type         string   `json:"type"`
	Options      []string `json:"options,omitempty"`
	DefaultValue string   `json:"defaultValue,omitempty"`
}

// stat is a tiny helper so the kpi tiles aren't a wall of struct
// literals. Default Source is spanmetric; for metric-based stats
// use statMetric below.
func stat(id, title, agg string, opts ...statOpt) panel {
	cfg := statCfg{Source: "spanmetric", Decimals: 1, Span: &spanCfg{Agg: agg}}
	for _, o := range opts {
		o(&cfg)
	}
	return panel{ID: id, Type: "stat", Title: title, Width: 1, Config: cfg}
}

type statOpt func(*statCfg)

func unit(u string) statOpt           { return func(c *statCfg) { c.Unit = u } }
func decimals(n int) statOpt          { return func(c *statCfg) { c.Decimals = n } }
func dsl(s string) statOpt            { return func(c *statCfg) { c.Span.DSL = s } }
func field(f string) statOpt          { return func(c *statCfg) { c.Span.Field = f } }
func groupByOpt(gb string) statOpt    { return func(c *statCfg) { c.Span.GroupBy = gb } } //nolint:unused // reserved
func filtersOpt(f string) statOpt     { return func(c *statCfg) { c.Span.Filters = f } } //nolint:unused // reserved

// line is the time-series spanmetric panel.
func line(id, title string, w int, cfg spanCfg) panel {
	return panel{ID: id, Type: "spanmetric", Title: title, Width: w, Config: cfg}
}

// metric is the time-series metric_points panel — for CPU,
// memory, heap timeseries on the Infrastructure dashboard.
func metric(id, title string, w int, cfg metricCfg) panel {
	return panel{ID: id, Type: "metric", Title: title, Width: w, Config: cfg}
}

func md(id, text string) panel {
	return panel{ID: id, Type: "markdown", Title: "", Width: 4, Config: mdCfg{Text: text}}
}

// ── 1. SRE Golden Signals ───────────────────────────────────────────────────
//
// Google SRE Book's four golden signals — latency, traffic,
// errors, saturation — at the system level. The first dashboard
// the operator opens; everything else drills deeper.
func presetSREGoldenSignals() Dashboard {
	return dash(
		"preset-sre-golden-signals",
		"SRE: Golden Signals",
		"Latency, Traffic, Errors, Saturation across the whole system. The Google SRE Book starting view — open this first when something feels off, then drill into a specific dashboard for the offending signal.",
		[]panel{
			md("intro",
				"**The four signals.** From [Google's SRE Book](https://sre.google/sre-book/monitoring-distributed-systems/): "+
					"\n\n• **Latency** — time to serve a request (broken down by service below)"+
					"\n• **Traffic** — RPS demand on the system"+
					"\n• **Errors** — rate of failed requests"+
					"\n• **Saturation** — how full the resources are (heap / CPU per service)"+
					"\n\nWhen any signal trends, jump to the matching dashboard: "+
					"[Service detail (RED)](/dashboards) for per-service drill-down, "+
					"[Latency](/dashboards) for slow operations, "+
					"[Errors](/dashboards) for failure modes, "+
					"[Infrastructure](/dashboards) for resource saturation."),

			row("row-kpi", "Headline numbers"),
			stat("k-rps", "Requests/sec", "rate", unit("rps"), decimals(1)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2)),
			stat("k-p95", "P95 latency", "p95", field("duration_ms"), unit("ms"), decimals(0)),
			stat("k-p99", "P99 latency", "p99", field("duration_ms"), unit("ms"), decimals(0)),

			row("row-latency", "Latency"),
			line("p50-svc", "P50 by service", 2,
				spanCfg{Agg: "p50", Field: "duration_ms", GroupBy: "service.name"}),
			line("p99-svc", "P99 by service", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "service.name"}),

			row("row-traffic", "Traffic"),
			line("rps-svc", "RPS by service", 2,
				spanCfg{Agg: "rate", GroupBy: "service.name"}),
			line("rps-kind", "RPS by span kind", 2,
				spanCfg{Agg: "rate", GroupBy: "kind"}),

			row("row-errors", "Errors"),
			line("err-rate-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", GroupBy: "service.name"}),
			line("err-cnt-svc", "Errors/sec by service", 2,
				spanCfg{Agg: "errors", GroupBy: "service.name"}),

			row("row-saturation", "Saturation (resource pressure)"),
			metric("cpu-svc", "CPU utilisation by service", 2,
				metricCfg{MetricName: "process.runtime.cpu.utilization", GroupBy: "service.name"}),
			metric("mem-svc", "Memory by service", 2,
				metricCfg{MetricName: "process.runtime.memory.rss", GroupBy: "service.name"}),
		},
	)
}

// ── 2. Service Health (RED) ─────────────────────────────────────────────────
//
// Tom Wilkie's RED method — Rate, Errors, Duration — applied
// per-service via the $service variable. Pair of the Golden
// Signals dashboard at the service granularity.
func presetServiceRED() Dashboard {
	const svc = `service.name = "${service}"`
	return dash(
		"preset-service-red",
		"Service Health (RED)",
		"Per-service Rate / Errors / Duration plus infrastructure saturation. Pick a service from the variable bar; every panel scopes to that service. The single dashboard for an oncall investigating a specific service.",
		[]panel{
			md("intro",
				"**RED method per service.** Pick a service from `$service` above. "+
					"Honeycomb / Dynatrace pattern — Rate / Errors / Duration plus "+
					"the runtime metrics that explain saturation when those signals "+
					"degrade. For cross-service comparisons go back to "+
					"**SRE: Golden Signals**."),

			row("row-kpi", "Service KPIs"),
			stat("k-rps", "RPS", "rate", unit("rps"), decimals(2), dsl(svc)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(svc)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(0), dsl(svc)),
			stat("k-p99", "P99", "p99", field("duration_ms"), unit("ms"), decimals(0), dsl(svc)),

			row("row-rate", "Rate (request volume)"),
			line("rps-op", "RPS by operation", 2,
				spanCfg{Agg: "rate", DSL: svc, GroupBy: "name"}),
			line("rps-route", "RPS by HTTP route (if HTTP)", 2,
				spanCfg{Agg: "rate", DSL: svc + ` AND http.method != ""`, GroupBy: "http.route"}),

			row("row-errors", "Errors (failed requests)"),
			line("err-op", "Error rate (%) by operation", 2,
				spanCfg{Agg: "error_rate", DSL: svc, GroupBy: "name"}),
			line("err-cnt-op", "Errors/sec by operation", 2,
				spanCfg{Agg: "errors", DSL: svc, GroupBy: "name"}),

			row("row-duration", "Duration (latency distribution)"),
			line("p50-op", "P50 by operation", 2,
				spanCfg{Agg: "p50", Field: "duration_ms", DSL: svc, GroupBy: "name"}),
			line("p99-op", "P99 by operation", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", DSL: svc, GroupBy: "name"}),

			row("row-saturation", "Saturation (runtime + infra)"),
			metric("cpu", "CPU utilisation", 1,
				metricCfg{MetricName: "process.runtime.cpu.utilization", Service: "${service}"}),
			metric("mem", "Memory RSS", 1,
				metricCfg{MetricName: "process.runtime.memory.rss", Service: "${service}"}),
			metric("heap", "Heap (Go runtime)", 1,
				metricCfg{MetricName: "process.runtime.go.mem.heap_alloc", Service: "${service}"}),
			metric("gc", "GC pause / runtime", 1,
				metricCfg{MetricName: "process.runtime.go.gc.duration", Service: "${service}"}),
		},
		dashVar{Name: "service", Label: "Service", Type: "service"},
	)
}

// ── 3. Incident War Room ────────────────────────────────────────────────────
//
// Datadog "Incident Response" / PagerDuty-style combat
// dashboard — high-density tiles + deep links into the live
// problem queues. Designed to be projected on a TV during an
// outage.
func presetIncidentWarRoom() Dashboard {
	const errOnly = `status_code = "error"`
	return dash(
		"preset-incident-warroom",
		"Incident War Room",
		"High-density combat dashboard for an active incident. Live error rate + traffic, top services / operations driving the incident, deep links into Problems / Anomalies / Traces. Project this on a screen during an outage.",
		[]panel{
			md("intro",
				"**War room.** Live state. Investigation links:"+
					"\n\n• [Problems](/problems) — alert-rule triggered events (acknowledge / resolve)"+
					"\n• [Anomalies](/anomalies) — detector-flagged log patterns + trace ops"+
					"\n• [Incidents](/incidents) — auto-grouped + manually declared incidents"+
					"\n• [Traces](/traces) — recent trace search + sample exemplars"+
					"\n• [Service map](/service-map) — topological view, hot edges"),

			row("row-pulse", "Live pulse"),
			stat("k-rps", "Total RPS", "rate", unit("rps"), decimals(0)),
			stat("k-err-rate", "Error rate", "error_rate", unit("%"), decimals(2)),
			stat("k-err-cnt", "Errors/sec", "errors", unit("eps"), decimals(1)),
			stat("k-p99", "P99 latency", "p99", field("duration_ms"), unit("ms"), decimals(0)),

			row("row-hot", "Hottest by errors (last window)"),
			line("err-svc", "Errors/sec by service", 2,
				spanCfg{Agg: "errors", GroupBy: "service.name"}),
			line("err-op", "Errors/sec by operation", 2,
				spanCfg{Agg: "errors", DSL: errOnly, GroupBy: "name"}),

			row("row-slow", "Hottest by latency"),
			line("p99-svc", "P99 by service", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "service.name"}),
			line("p99-op", "P99 by operation", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "name"}),

			row("row-status", "By HTTP status code"),
			line("rps-status", "RPS by status code", 2,
				spanCfg{Agg: "rate", DSL: `http.method != ""`, GroupBy: "http.status_code"}),
			line("err-status", "Errors/sec by status code", 2,
				spanCfg{Agg: "errors", DSL: `http.method != ""`, GroupBy: "http.status_code"}),
		},
	)
}

// ── 4. Latency Investigation ────────────────────────────────────────────────
//
// Honeycomb-style P50/P95/P99 deep-dive. Latency is the most
// common "silent degradation" signal — this dashboard shows
// the same data three different ways so the operator finds
// the slow operation regardless of which percentile shifted.
func presetLatencyInvestigation() Dashboard {
	return dash(
		"preset-latency-investigation",
		"Latency Investigation",
		"P50 / P95 / P99 across services and operations. When the SRE Golden Signals show latency rising, drill in here. Designed to surface 'a single endpoint slowed' vs 'system-wide drift'.",
		[]panel{
			md("intro",
				"**Latency drill-down.** P50 = typical user; P95 = unhappy user; "+
					"P99 = tail. Cross-reference: if P50 is steady but P99 spiked, "+
					"a small subset of requests is slow (often a database query or "+
					"a noisy neighbour); if P50 ALSO moved, the whole service is "+
					"degraded. Open the [Traces page](/traces) to find the actual "+
					"slow trace exemplars."),

			row("row-system", "System percentiles"),
			line("p50-sys", "P50 across all services", 2,
				spanCfg{Agg: "p50", Field: "duration_ms"}),
			line("p99-sys", "P99 across all services", 2,
				spanCfg{Agg: "p99", Field: "duration_ms"}),

			row("row-by-svc", "By service"),
			line("p50-svc", "P50 by service", 2,
				spanCfg{Agg: "p50", Field: "duration_ms", GroupBy: "service.name"}),
			line("p95-svc", "P95 by service", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", GroupBy: "service.name"}),
			line("p99-svc", "P99 by service", 4,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "service.name"}),

			row("row-by-op", "Slowest operations (P99)"),
			line("p99-op", "P99 by operation", 4,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "name"}),

			row("row-by-kind", "By span kind"),
			line("p99-kind", "P99 by kind (server/client/internal/db)", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "kind"}),
			line("avg-kind", "Avg by kind", 2,
				spanCfg{Agg: "avg", Field: "duration_ms", GroupBy: "kind"}),
		},
	)
}

// ── 5. Error Hunting ────────────────────────────────────────────────────────
//
// Dynatrace "error analytics" inspired — multi-axis breakdown
// of failures so the operator can answer "where do errors
// concentrate". Pairs with the Exceptions inbox for the actual
// stack-trace details.
func presetErrorHunting() Dashboard {
	const errOnly = `status_code = "error"`
	const httpErr = `http.method != "" AND status_code = "error"`
	return dash(
		"preset-error-hunting",
		"Error Hunting",
		"Multi-axis error breakdown — by service, operation, HTTP status, and span kind. Pairs with the Exceptions inbox for actual stack-trace details. Open this when error rate trends up.",
		[]panel{
			md("intro",
				"**Where do errors live?** This dashboard shows the same error "+
					"firehose sliced four different ways so the operator finds "+
					"the cluster regardless of dimension. After identifying the "+
					"hot service / operation here, jump to:"+
					"\n\n• [Exceptions](/errors) — grouped stack traces with sample occurrences"+
					"\n• [Anomalies](/anomalies) — log-pattern detector hits + trace-op spikes"+
					"\n• [Traces](/traces) (filter `status_code:error`) — sample failed traces"),

			row("row-rate", "Error rate trends"),
			stat("k-rate", "Error rate", "error_rate", unit("%"), decimals(2)),
			stat("k-eps", "Errors/sec", "errors", unit("eps"), decimals(1)),
			stat("k-cnt", "Total RPS", "rate", unit("rps"), decimals(0)),

			row("row-by-svc", "By service"),
			line("rate-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", GroupBy: "service.name"}),
			line("cnt-svc", "Errors/sec by service", 2,
				spanCfg{Agg: "errors", GroupBy: "service.name"}),

			row("row-by-op", "By operation"),
			line("cnt-op", "Errors/sec by operation", 4,
				spanCfg{Agg: "errors", DSL: errOnly, GroupBy: "name"}),

			row("row-http", "HTTP errors"),
			line("http-status", "Errors/sec by HTTP status code", 2,
				spanCfg{Agg: "errors", DSL: httpErr, GroupBy: "http.status_code"}),
			line("http-route", "Errors/sec by HTTP route", 2,
				spanCfg{Agg: "errors", DSL: httpErr, GroupBy: "http.route"}),

			row("row-kind", "By span kind"),
			line("err-kind", "Errors/sec by kind", 4,
				spanCfg{Agg: "errors", DSL: errOnly, GroupBy: "kind"}),
		},
	)
}

// ── 6. Infrastructure Saturation ────────────────────────────────────────────
//
// Brendan Gregg's USE method — Utilisation, Saturation, Errors —
// for resources. Reads metric_points; surfaces process-runtime
// + container-level indicators across services.
func presetInfrastructure() Dashboard {
	return dash(
		"preset-infrastructure",
		"Infrastructure Saturation",
		"CPU / memory / heap / GC pressure across services using the USE method (Utilisation, Saturation, Errors). Open this when latency rises but the service-level RED metrics look normal — usually means a resource is the bottleneck.",
		[]panel{
			md("intro",
				"**USE method on runtime metrics.** When latency degrades but "+
					"requests-per-second look normal, the bottleneck is usually "+
					"a saturated resource. This dashboard surfaces the standard "+
					"OTel runtime metrics across every service that emits them.\n\n"+
					"Note: panels render only for services whose SDK exports the "+
					"matching metric. Java services typically emit `jvm.*`, Go "+
					"services emit `process.runtime.go.*`, .NET emits "+
					"`process.runtime.dotnet.*`, Node.js emits `process.runtime.nodejs.*`."),

			row("row-cpu", "CPU utilisation"),
			metric("cpu-util", "CPU utilisation by service (process.runtime.cpu.utilization)", 4,
				metricCfg{MetricName: "process.runtime.cpu.utilization", GroupBy: "service.name"}),

			row("row-memory", "Memory saturation"),
			metric("mem-rss", "Memory RSS by service (process.runtime.memory.rss)", 2,
				metricCfg{MetricName: "process.runtime.memory.rss", GroupBy: "service.name"}),
			metric("heap-go", "Go heap_alloc by service (process.runtime.go.mem.heap_alloc)", 2,
				metricCfg{MetricName: "process.runtime.go.mem.heap_alloc", GroupBy: "service.name"}),
			metric("heap-jvm", "JVM heap by service (jvm.memory.heap.used)", 2,
				metricCfg{MetricName: "jvm.memory.heap.used", GroupBy: "service.name"}),
			metric("heap-dotnet", ".NET heap by service (process.runtime.dotnet.gc.heap.size)", 2,
				metricCfg{MetricName: "process.runtime.dotnet.gc.heap.size", GroupBy: "service.name"}),

			row("row-gc", "GC pressure"),
			metric("gc-go", "Go GC pause time by service (process.runtime.go.gc.pause_total_ns)", 2,
				metricCfg{MetricName: "process.runtime.go.gc.pause_total_ns", GroupBy: "service.name"}),
			metric("gc-jvm", "JVM GC duration by service (jvm.gc.duration)", 2,
				metricCfg{MetricName: "jvm.gc.duration", GroupBy: "service.name"}),

			row("row-threads", "Concurrency"),
			metric("goroutines", "Goroutines by service (process.runtime.goroutines)", 2,
				metricCfg{MetricName: "process.runtime.goroutines", GroupBy: "service.name"}),
			metric("jvm-threads", "JVM threads by service (jvm.thread.count)", 2,
				metricCfg{MetricName: "jvm.thread.count", GroupBy: "service.name"}),
		},
	)
}

// ── 7. Database Performance ─────────────────────────────────────────────────
//
// Storage layers are the most common saturation culprit. This
// dashboard shares the same RED frame as Service Health but
// scopes to spans where db.system is set.
func presetDatabase() Dashboard {
	const db = `db.system != ""`
	return dash(
		"preset-database",
		"Database Performance",
		"Query rate, latency and errors by db.system, db.operation and service. Storage is the most common saturation culprit — if latency rises but CPU stays flat, start here.",
		[]panel{
			md("intro",
				"**Database calls.** All spans with `db.system` set — PostgreSQL, "+
					"MySQL, Redis, MongoDB, etc. The per-operation P95 graph is "+
					"the fastest way to spot a slowing query type. The per-service "+
					"breakdown shows which app is feeling it. Combine with "+
					"[Traces](/traces) filtered by `db.system:postgresql` to find "+
					"the actual slow query."),

			row("row-kpi", "Database KPIs"),
			stat("k-qps", "QPS", "rate", unit("qps"), decimals(1), dsl(db)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(db)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(0), dsl(db)),
			stat("k-p99", "P99", "p99", field("duration_ms"), unit("ms"), decimals(0), dsl(db)),

			row("row-throughput", "Throughput"),
			line("rps-system", "QPS by db.system", 2,
				spanCfg{Agg: "rate", DSL: db, GroupBy: "db.system"}),
			line("rps-op", "QPS by db.operation", 2,
				spanCfg{Agg: "rate", DSL: db, GroupBy: "db.operation"}),

			row("row-latency", "Latency"),
			line("p95-op", "P95 by db.operation", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: db, GroupBy: "db.operation"}),
			line("p95-svc", "P95 by service (database calls)", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: db, GroupBy: "service.name"}),
			line("p99-system", "P99 by db.system", 4,
				spanCfg{Agg: "p99", Field: "duration_ms", DSL: db, GroupBy: "db.system"}),

			row("row-errors", "Errors"),
			line("err-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", DSL: db, GroupBy: "service.name"}),
			line("err-system", "Errors/sec by db.system", 2,
				spanCfg{Agg: "errors", DSL: db, GroupBy: "db.system"}),
		},
	)
}

// ── 8. External Dependencies ────────────────────────────────────────────────
//
// Datadog "Service Map" inspired — focuses on outbound (client-
// kind) spans. When your own service-internal latency is fine
// but request P95 is rising, the answer is usually here.
func presetExternalDeps() Dashboard {
	const ext = `kind = "client"`
	return dash(
		"preset-external-deps",
		"External Dependencies",
		"Outbound (client-kind) span performance by peer.service — third-party APIs and downstream services. When your own service-internal latency is fine but request P95 is rising, the answer is usually here.",
		[]panel{
			md("intro",
				"**Outbound calls.** All client-kind spans. Group-by `peer.service` "+
					"surfaces the downstream that's slowing down or erroring. "+
					"Cross-reference with [Service map](/service-map) to see the "+
					"hot edges in the topology graph."),

			row("row-kpi", "Outbound KPIs"),
			stat("k-rps", "Outbound RPS", "rate", unit("rps"), decimals(1), dsl(ext)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(ext)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(0), dsl(ext)),
			stat("k-p99", "P99", "p99", field("duration_ms"), unit("ms"), decimals(0), dsl(ext)),

			row("row-peer", "By peer / dependency"),
			line("rps-peer", "RPS by peer.service", 2,
				spanCfg{Agg: "rate", DSL: ext, GroupBy: "peer.service"}),
			line("p95-peer", "P95 by peer.service", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: ext, GroupBy: "peer.service"}),
			line("err-peer", "Error rate (%) by peer.service", 2,
				spanCfg{Agg: "error_rate", DSL: ext, GroupBy: "peer.service"}),
			line("p99-peer", "P99 by peer.service", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", DSL: ext, GroupBy: "peer.service"}),

			row("row-by-svc", "By calling service"),
			line("rps-svc", "Outbound RPS by service", 2,
				spanCfg{Agg: "rate", DSL: ext, GroupBy: "service.name"}),
			line("p95-svc", "P95 by service", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: ext, GroupBy: "service.name"}),
		},
	)
}
