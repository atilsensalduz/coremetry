package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// Preset dashboards seeded into a fresh install — APM-focused
// snapshots inspired by SigNoz / Uptrace's stock collection.
//
// Versioning: the seeded set carries an opaque version string stored
// in system_settings under "preset_dashboards_version". On each boot
// we compare; if it doesn't match, we wipe rows whose ID starts with
// "preset-" (old + current bundle) and re-seed the new set, then
// write the new version.
//
// User-created dashboards (any ID without the preset- prefix) are
// never touched — even an admin who renamed a preset retains their
// rename because the old preset row gets deleted on bundle upgrade
// while their renamed copy keeps living under its new ID.
//
// Bump presetVersion when changing the bundle.

const presetVersion = "apm-v1"

func (s *Store) SeedPresetDashboards(ctx context.Context) error {
	storedVersion, _ := s.GetSetting(ctx, "preset_dashboards_version")
	current := string(storedVersion)

	// Fresh install? Seed everything and stamp the version.
	row := s.conn.QueryRow(ctx, `SELECT count() FROM dashboards FINAL`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("count dashboards: %w", err)
	}
	if n == 0 {
		return s.seedAndStamp(ctx)
	}

	// Already seeded with this bundle? Nothing to do.
	if current == presetVersion {
		return nil
	}

	// Bundle change → drop the old preset-* rows so the new set takes
	// over their tiles cleanly. ALTER DELETE is async but the
	// subsequent UpsertDashboard inserts a row at higher version, and
	// ReplacingMergeTree's merge sorts that out for us.
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
		presetAPMOverview(),
		presetAPMService(),
		presetAPMHTTP(),
		presetAPMDatabase(),
		presetAPMMessaging(),
		presetAPMExternal(),
		presetAPMErrors(),
	}
}

// Panel construction helpers — keep the JSON shape close to the
// frontend Panel type (id, type, title, width, config). Width is the
// 4-col grid factor: 1=quarter, 2=half, 3=three-quarters, 4=full.

type panel struct {
	ID     string `json:"id"`
	Type   string `json:"type"`   // metric | spanmetric | stat | markdown | row
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Config any    `json:"config"`
}

// row builds a Grafana-style row marker — a full-width "header" panel
// that the dashboard renderer treats as a collapsible group separator.
func row(id, title string) panel {
	return panel{ID: id, Type: "row", Title: title, Width: 4, Config: map[string]any{}}
}

// spanmetric config: queries spans table on the fly via /api/spans/metric.
type spanCfg struct {
	Agg     string `json:"agg"`
	Field   string `json:"field,omitempty"`
	GroupBy string `json:"groupBy,omitempty"`
	DSL     string `json:"dsl,omitempty"`
	Filters string `json:"filters,omitempty"`
	Step    int    `json:"step,omitempty"`
}

type statCfg struct {
	Source   string   `json:"source"` // "spanmetric" or "metric"
	Span     *spanCfg `json:"span,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Decimals int      `json:"decimals,omitempty"`
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
// literals.
func stat(id, title, agg string, opts ...statOpt) panel {
	cfg := statCfg{Source: "spanmetric", Decimals: 1, Span: &spanCfg{Agg: agg}}
	for _, o := range opts {
		o(&cfg)
	}
	return panel{ID: id, Type: "stat", Title: title, Width: 1, Config: cfg}
}

type statOpt func(*statCfg)

func unit(u string) statOpt          { return func(c *statCfg) { c.Unit = u } }
func decimals(n int) statOpt         { return func(c *statCfg) { c.Decimals = n } }
func dsl(s string) statOpt           { return func(c *statCfg) { c.Span.DSL = s } }
func field(f string) statOpt         { return func(c *statCfg) { c.Span.Field = f } }
func filtersOpt(f string) statOpt    { return func(c *statCfg) { c.Span.Filters = f } } //nolint:unused // reserved for future presets

// line is the equivalent for time-series spanmetric panels.
func line(id, title string, w int, cfg spanCfg) panel {
	return panel{ID: id, Type: "spanmetric", Title: title, Width: w, Config: cfg}
}

func md(id, text string) panel {
	return panel{ID: id, Type: "markdown", Title: "", Width: 4, Config: mdCfg{Text: text}}
}

// ── 1. APM Overview ─────────────────────────────────────────────────────────
//
// Inspired by Uptrace's "APM" landing — system-wide KPI strip plus
// the four most useful breakdown lines. Operator's first stop on a
// page.
func presetAPMOverview() Dashboard {
	return dash(
		"preset-apm-overview",
		"APM Overview",
		"System-wide RED + latency at a glance. Per-service breakdowns underneath. Start here on a paged incident, then drill into Service detail or HTTP.",
		[]panel{
			md("intro",
				"**APM overview — system level.** RPS / error rate / latency across every "+
					"service. For a single service, open the **APM: Service detail** dashboard "+
					"and pick from the `$service` dropdown."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "Requests/sec", "rate", unit("rps"), decimals(1)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2)),
			stat("k-p50", "P50", "p50", field("duration_ms"), unit("ms"), decimals(1)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(1)),

			row("row-traffic", "Traffic"),
			line("rps-svc", "RPS by service", 4,
				spanCfg{Agg: "rate", GroupBy: "service.name"}),

			row("row-errors", "Errors"),
			line("err-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", GroupBy: "service.name"}),
			line("err-cnt-svc", "Errors/sec by service", 2,
				spanCfg{Agg: "errors", GroupBy: "service.name"}),

			row("row-latency", "Latency"),
			line("p95-svc", "P95 by service", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", GroupBy: "service.name"}),
			line("p99-svc", "P99 by service", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "service.name"}),
		},
	)
}

// ── 2. APM: Service detail (with $service variable) ─────────────────────────
//
// The "click into a service" view. Inspired by SigNoz's per-service
// page: KPI strip + RED-by-operation + slow operations.
func presetAPMService() Dashboard {
	const svc = `service.name = "${service}"`
	return dash(
		"preset-apm-service",
		"APM: Service detail",
		"Per-service drilldown. Pick a service from the variable bar above to scope every panel to it. Empty selection shows aggregates across all services.",
		[]panel{
			md("intro",
				"**Service detail.** Pick a service from the `$service` variable above. "+
					"Every panel below scopes to that service. Use this dashboard during "+
					"a service-specific incident — for system-wide views go back to "+
					"**APM Overview**."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "RPS", "rate", unit("rps"), decimals(2), dsl(svc)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(svc)),
			stat("k-p50", "P50", "p50", field("duration_ms"), unit("ms"), decimals(1), dsl(svc)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(1), dsl(svc)),

			row("row-throughput", "Throughput"),
			line("rps-op", "RPS by operation", 2,
				spanCfg{Agg: "rate", DSL: svc, GroupBy: "name"}),
			line("rps-kind", "RPS by span kind", 2,
				spanCfg{Agg: "rate", DSL: svc, GroupBy: "kind"}),

			row("row-errors", "Errors"),
			line("err-op", "Error rate (%) by operation", 2,
				spanCfg{Agg: "error_rate", DSL: svc, GroupBy: "name"}),
			line("err-cnt-op", "Errors/sec by operation", 2,
				spanCfg{Agg: "errors", DSL: svc, GroupBy: "name"}),

			row("row-latency", "Latency by operation"),
			line("p95-op", "P95 by operation", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: svc, GroupBy: "name"}),
			line("p99-op", "P99 by operation", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", DSL: svc, GroupBy: "name"}),
		},
		dashVar{Name: "service", Label: "Service", Type: "service"},
	)
}

// ── 3. HTTP performance ─────────────────────────────────────────────────────
func presetAPMHTTP() Dashboard {
	const http = `http.method != ""`
	return dash(
		"preset-apm-http",
		"HTTP Performance",
		"HTTP-only RED metrics broken down by route, method and status code. Open this when an incident is HTTP-shaped (frontend, REST API, gateway).",
		[]panel{
			md("intro",
				"**HTTP layer.** All spans where `http.method` is set. Use the per-route "+
					"panels to identify a specific endpoint that's slowing down or erroring."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "HTTP RPS", "rate", unit("rps"), decimals(1), dsl(http)),
			stat("k-err", "HTTP error rate", "error_rate", unit("%"), decimals(2), dsl(http)),
			stat("k-p95", "HTTP P95", "p95", field("duration_ms"), unit("ms"), decimals(1), dsl(http)),
			stat("k-p99", "HTTP P99", "p99", field("duration_ms"), unit("ms"), decimals(1), dsl(http)),

			row("row-traffic", "Traffic"),
			line("rps-method", "RPS by HTTP method", 2,
				spanCfg{Agg: "rate", DSL: http, GroupBy: "http.method"}),
			line("rps-status", "RPS by status code", 2,
				spanCfg{Agg: "rate", DSL: http, GroupBy: "http.status_code"}),

			row("row-routes", "Routes"),
			line("p95-route", "P95 latency by route", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: http, GroupBy: "http.route"}),
			line("err-route", "Error rate (%) by route", 2,
				spanCfg{Agg: "error_rate", DSL: http, GroupBy: "http.route"}),
			line("rps-route", "RPS by route", 2,
				spanCfg{Agg: "rate", DSL: http, GroupBy: "http.route"}),
			line("p99-method", "P99 by HTTP method", 2,
				spanCfg{Agg: "p99", Field: "duration_ms", DSL: http, GroupBy: "http.method"}),
		},
	)
}

// ── 4. Database performance ─────────────────────────────────────────────────
func presetAPMDatabase() Dashboard {
	const db = `db.system != ""`
	return dash(
		"preset-apm-database",
		"Database Performance",
		"Query rate, latency and errors by db.system, db.operation and service. Storage layers are the most common saturation culprit — start here when latency rises but CPU stays flat.",
		[]panel{
			md("intro",
				"**Database calls.** All spans with `db.system` set. PostgreSQL, MySQL, "+
					"Redis, MongoDB — same view across them. The per-operation P95 graph "+
					"is the fastest way to spot a slowing query."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "DB QPS", "rate", unit("qps"), decimals(1), dsl(db)),
			stat("k-err", "DB error rate", "error_rate", unit("%"), decimals(2), dsl(db)),
			stat("k-p95", "DB P95", "p95", field("duration_ms"), unit("ms"), decimals(1), dsl(db)),
			stat("k-p99", "DB P99", "p99", field("duration_ms"), unit("ms"), decimals(1), dsl(db)),

			row("row-throughput", "Throughput"),
			line("rps-system", "QPS by db.system", 2,
				spanCfg{Agg: "rate", DSL: db, GroupBy: "db.system"}),
			line("rps-op", "QPS by db.operation", 2,
				spanCfg{Agg: "rate", DSL: db, GroupBy: "db.operation"}),

			row("row-latency", "Latency"),
			line("p95-op", "P95 by db.operation", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: db, GroupBy: "db.operation"}),
			line("p95-svc", "P95 by service", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: db, GroupBy: "service.name"}),

			row("row-errors", "Errors"),
			line("err-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", DSL: db, GroupBy: "service.name"}),
			line("err-cnt-system", "Errors/sec by db.system", 2,
				spanCfg{Agg: "errors", DSL: db, GroupBy: "db.system"}),
		},
	)
}

// ── 5. Messaging (Kafka / RabbitMQ / Redis-pub-sub / NATS) ──────────────────
func presetAPMMessaging() Dashboard {
	const msg = `messaging.system != ""`
	return dash(
		"preset-apm-messaging",
		"Messaging",
		"Producer/consumer rates, error rate and latency for messaging systems. Kafka, RabbitMQ, Redis pub/sub, NATS — same panels across all.",
		[]panel{
			md("intro",
				"**Messaging spans.** Producers, consumers and any span tagged with "+
					"`messaging.system`. Watch for sudden destination latency jumps — "+
					"that's the canary for a slow consumer."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "Messages/sec", "rate", unit("msg/s"), decimals(1), dsl(msg)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(msg)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(1), dsl(msg)),

			row("row-throughput", "Throughput"),
			line("rps-system", "Rate by messaging.system", 2,
				spanCfg{Agg: "rate", DSL: msg, GroupBy: "messaging.system"}),
			line("rps-op", "Rate by messaging.operation", 2,
				spanCfg{Agg: "rate", DSL: msg, GroupBy: "messaging.operation"}),
			line("rps-dest", "Rate by destination", 4,
				spanCfg{Agg: "rate", DSL: msg, GroupBy: "messaging.destination.name"}),

			row("row-latency", "Latency"),
			line("p95-dest", "P95 by destination", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: msg, GroupBy: "messaging.destination.name"}),
			line("p95-system", "P95 by system", 2,
				spanCfg{Agg: "p95", Field: "duration_ms", DSL: msg, GroupBy: "messaging.system"}),

			row("row-errors", "Errors"),
			line("err-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", DSL: msg, GroupBy: "service.name"}),
			line("err-cnt-system", "Errors/sec by system", 2,
				spanCfg{Agg: "errors", DSL: msg, GroupBy: "messaging.system"}),
		},
	)
}

// ── 6. External calls (outbound) ────────────────────────────────────────────
//
// Client-kind spans calling out to a remote service. Useful when a
// 3rd-party API or downstream service is the thing that just went
// bad — not your own code.
func presetAPMExternal() Dashboard {
	const ext = `kind = "client"`
	return dash(
		"preset-apm-external",
		"External Calls",
		"Outbound (client-kind) span performance — third-party APIs and downstream services. Pinpoint when the thing degrading isn't yours.",
		[]panel{
			md("intro",
				"**Outbound dependencies.** All client-kind spans. Group-by `peer.service` "+
					"surfaces which downstream is slowing down — useful when your own "+
					"service-internal latency is fine but request P95 is rising."),

			row("row-kpi", "Key indicators"),
			stat("k-rps", "Outbound RPS", "rate", unit("rps"), decimals(1), dsl(ext)),
			stat("k-err", "Error rate", "error_rate", unit("%"), decimals(2), dsl(ext)),
			stat("k-p95", "P95", "p95", field("duration_ms"), unit("ms"), decimals(1), dsl(ext)),
			stat("k-p99", "P99", "p99", field("duration_ms"), unit("ms"), decimals(1), dsl(ext)),

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

// ── 7. Errors & reliability ─────────────────────────────────────────────────
func presetAPMErrors() Dashboard {
	const errOnly = `status_code = "error"`
	return dash(
		"preset-apm-errors",
		"Errors & Reliability",
		"Error trends with deep-links into the Problems, Exceptions and SLO pages. When the overview shows error rate climbing, drill in here.",
		[]panel{
			md("intro",
				"**Reliability inbox.** When errors trend up, work top-to-bottom: "+
					"open the [Problems page](/problems) for active rule alerts, the "+
					"[Exceptions page](/errors) for grouped stack-traces, and the "+
					"[SLOs page](/slos) for error-budget burn-rate."),

			row("row-kpi", "Key indicators"),
			stat("k-err", "System error rate", "error_rate", unit("%"), decimals(2)),
			stat("k-err-rate", "Errors/sec", "errors", unit("rps"), decimals(2)),
			stat("k-svc-cnt", "Total RPS", "rate", unit("rps"), decimals(1)),

			row("row-by-svc", "By service"),
			line("err-rate-svc", "Error rate (%) by service", 2,
				spanCfg{Agg: "error_rate", GroupBy: "service.name"}),
			line("err-cnt-svc", "Errors/sec by service", 2,
				spanCfg{Agg: "errors", GroupBy: "service.name"}),

			row("row-by-op", "By operation"),
			line("err-by-op", "Errors/sec by operation", 2,
				spanCfg{Agg: "errors", DSL: errOnly, GroupBy: "name"}),
			line("err-by-status", "Errors/sec by HTTP status", 2,
				spanCfg{Agg: "errors", DSL: errOnly, GroupBy: "http.status_code"}),
		},
	)
}
