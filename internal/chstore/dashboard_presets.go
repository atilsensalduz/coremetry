package chstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// SRE-oriented preset dashboards seeded into a fresh install. Cover the
// four golden signals (Latency / Traffic / Errors / Saturation) at the
// system level, plus protocol-specific (HTTP+RPC, DB) and a per-service
// template the user can clone.
//
// Seeding is idempotent + non-destructive: only inserts the preset set
// when the dashboards table is completely empty. Once a user has
// modified or created any dashboard we never touch this set again, so
// upgrades don't blow away their work.
//
// Preset IDs are deterministic (preset-* prefix) so re-seeding into a
// table that already has them stays a no-op via ReplacingMergeTree.

func (s *Store) SeedPresetDashboards(ctx context.Context) error {
	// Only seed when there's nothing here at all.
	row := s.conn.QueryRow(ctx, `SELECT count() FROM dashboards FINAL`)
	var n uint64
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("count dashboards: %w", err)
	}
	if n > 0 {
		return nil
	}

	for _, d := range presetDashboards() {
		if err := s.UpsertDashboard(ctx, d); err != nil {
			return fmt.Errorf("seed dashboard %s: %w", d.ID, err)
		}
		log.Printf("[chstore] seeded preset dashboard %q", d.Name)
	}
	return nil
}

// ── Preset definitions ──────────────────────────────────────────────────────

func presetDashboards() []Dashboard {
	return []Dashboard{
		presetGoldenSignals(),
		presetServiceRED(),
		presetHTTPRPC(),
		presetDatabase(),
		presetReliability(),
	}
}

// Panel construction helpers — keep the JSON shape close to the
// frontend Panel type (id, type, title, width, config). Width is the
// 12-col grid factor: 1=quarter, 2=third, 3=half, 4=full.

type panel struct {
	ID     string `json:"id"`
	Type   string `json:"type"`   // metric | spanmetric | stat | markdown | row
	Title  string `json:"title"`
	Width  int    `json:"width"`
	Config any    `json:"config"`
}

// row builds a Grafana-style row marker — a full-width "header" panel
// that the dashboard renderer treats as a collapsible group separator.
// All non-row panels following it (until the next row) belong to its
// group.
func row(id, title string) panel {
	return panel{ID: id, Type: "row", Title: title, Width: 4, Config: map[string]any{}}
}

// spanmetric config: queries spans table on the fly via /api/spans/metric.
type spanCfg struct {
	Agg     string `json:"agg"`              // count | rate | errors | error_rate | avg | p50/p95/p99 / etc.
	Field   string `json:"field,omitempty"`  // duration_ms (default) or attribute path
	GroupBy string `json:"groupBy,omitempty"`
	DSL     string `json:"dsl,omitempty"`    // multi-line "k op v" expressions, AND-joined
	Filters string `json:"filters,omitempty"`
	Step    int    `json:"step,omitempty"`
}

type statCfg struct {
	Source   string   `json:"source"`             // "spanmetric" or "metric"
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

func dash(id, name, desc string, panels []panel) Dashboard {
	return Dashboard{
		ID:          id,
		Name:        name,
		Description: desc,
		Panels:      mustJSON(panels),
	}
}

// ── Dashboard 1: SRE Golden Signals (system-wide) ───────────────────────────
//
// The Four Golden Signals from the Google SRE book at the cluster level.
// Latency (p95/p99), Traffic (rps), Errors (rate), Saturation (queue
// depth proxied via the system status page).
func presetGoldenSignals() Dashboard {
	return dash(
		"preset-sre-overview",
		"SRE: Golden Signals (system)",
		"System-wide Latency / Traffic / Errors / Saturation. The Four Golden Signals view from the Google SRE book — start here on a paged incident.",
		[]panel{
			{
				ID: "intro", Type: "markdown", Width: 4, Title: "",
				Config: mdCfg{Text: "**Four Golden Signals — system level.** Pages here typically mean a multi-service incident. " +
					"For per-service drill-down, open `Service: RED` and filter by `service.name`."},
			},
			row("row-summary", "At a glance"),
			{
				ID: "stat-rps", Type: "stat", Width: 1, Title: "Total RPS",
				Config: statCfg{Source: "spanmetric", Unit: "rps", Decimals: 1,
					Span: &spanCfg{Agg: "rate"}},
			},
			{
				ID: "stat-err", Type: "stat", Width: 1, Title: "Error rate",
				Config: statCfg{Source: "spanmetric", Unit: "%", Decimals: 2,
					Span: &spanCfg{Agg: "error_rate"}},
			},
			{
				ID: "stat-p95", Type: "stat", Width: 1, Title: "P95 latency",
				Config: statCfg{Source: "spanmetric", Unit: "ms", Decimals: 1,
					Span: &spanCfg{Agg: "p95", Field: "duration_ms"}},
			},
			{
				ID: "stat-p99", Type: "stat", Width: 1, Title: "P99 latency",
				Config: statCfg{Source: "spanmetric", Unit: "ms", Decimals: 1,
					Span: &spanCfg{Agg: "p99", Field: "duration_ms"}},
			},
			row("row-traffic", "Traffic"),
			{
				ID: "rps-by-svc", Type: "spanmetric", Width: 4, Title: "RPS by service",
				Config: spanCfg{Agg: "rate", GroupBy: "service.name"},
			},
			row("row-errors", "Errors"),
			{
				ID: "err-by-svc", Type: "spanmetric", Width: 4, Title: "Error rate (%) by service",
				Config: spanCfg{Agg: "error_rate", GroupBy: "service.name"},
			},
			row("row-latency", "Latency"),
			{
				ID: "p95-by-svc", Type: "spanmetric", Width: 2, Title: "P95 latency by service",
				Config: spanCfg{Agg: "p95", Field: "duration_ms", GroupBy: "service.name"},
			},
			{
				ID: "p99-by-svc", Type: "spanmetric", Width: 2, Title: "P99 latency by service",
				Config: spanCfg{Agg: "p99", Field: "duration_ms", GroupBy: "service.name"},
			},
		},
	)
}

// ── Dashboard 2: Service RED (template — clone per service) ─────────────────
//
// RED method (Rate / Errors / Duration) for a single service. Filters
// on service.name=java-demo as a starting point — clone this dashboard
// and change the filter for each service you want focused on.
func presetServiceRED() Dashboard {
	const svcDSL = `service.name = "java-demo"`
	return dash(
		"preset-sre-service-red",
		"Service: RED (template)",
		"Rate / Errors / Duration for a single service. Hardcoded to service.name=java-demo as a template — clone this dashboard and change the filter to monitor any other service the same way.",
		[]panel{
			{
				ID: "intro", Type: "markdown", Width: 4, Title: "",
				Config: mdCfg{Text: "**Service RED** — Rate / Errors / Duration for a single service. " +
					"This dashboard filters on `service.name = \"java-demo\"`. " +
					"Clone it (top-right ⋮ → Duplicate) and change the filter on each panel to monitor a different service."},
			},
			{
				ID: "stat-rps", Type: "stat", Width: 1, Title: "RPS",
				Config: statCfg{Source: "spanmetric", Unit: "rps", Decimals: 2,
					Span: &spanCfg{Agg: "rate", DSL: svcDSL}},
			},
			{
				ID: "stat-err", Type: "stat", Width: 1, Title: "Error rate",
				Config: statCfg{Source: "spanmetric", Unit: "%", Decimals: 2,
					Span: &spanCfg{Agg: "error_rate", DSL: svcDSL}},
			},
			{
				ID: "stat-p95", Type: "stat", Width: 1, Title: "P95",
				Config: statCfg{Source: "spanmetric", Unit: "ms", Decimals: 1,
					Span: &spanCfg{Agg: "p95", Field: "duration_ms", DSL: svcDSL}},
			},
			{
				ID: "stat-p99", Type: "stat", Width: 1, Title: "P99",
				Config: statCfg{Source: "spanmetric", Unit: "ms", Decimals: 1,
					Span: &spanCfg{Agg: "p99", Field: "duration_ms", DSL: svcDSL}},
			},
			{
				ID: "rps-by-op", Type: "spanmetric", Width: 2, Title: "RPS by operation",
				Config: spanCfg{Agg: "rate", DSL: svcDSL, GroupBy: "name"},
			},
			{
				ID: "err-by-op", Type: "spanmetric", Width: 2, Title: "Error rate (%) by operation",
				Config: spanCfg{Agg: "error_rate", DSL: svcDSL, GroupBy: "name"},
			},
			{
				ID: "p95-by-op", Type: "spanmetric", Width: 2, Title: "P95 by operation",
				Config: spanCfg{Agg: "p95", Field: "duration_ms", DSL: svcDSL, GroupBy: "name"},
			},
			{
				ID: "p99-by-op", Type: "spanmetric", Width: 2, Title: "P99 by operation",
				Config: spanCfg{Agg: "p99", Field: "duration_ms", DSL: svcDSL, GroupBy: "name"},
			},
		},
	)
}

// ── Dashboard 3: HTTP & RPC Performance ─────────────────────────────────────
//
// Protocol-level views — useful when an oncall wants to scope an incident
// to "is this HTTP? gRPC? a DB problem?"
func presetHTTPRPC() Dashboard {
	const httpDSL = `http.method != ""`
	const rpcDSL = `rpc.system != ""`
	return dash(
		"preset-sre-http-rpc",
		"HTTP & RPC Performance",
		"Protocol-level RED metrics. HTTP requests by method/route/status, RPC calls by method/system. Useful when scoping an incident to a specific protocol layer.",
		[]panel{
			{
				ID: "h-rate-method", Type: "spanmetric", Width: 2, Title: "HTTP requests/sec by method",
				Config: spanCfg{Agg: "rate", DSL: httpDSL, GroupBy: "http.method"},
			},
			{
				ID: "h-rate-status", Type: "spanmetric", Width: 2, Title: "HTTP requests/sec by status code",
				Config: spanCfg{Agg: "rate", DSL: httpDSL, GroupBy: "http.status_code"},
			},
			{
				ID: "h-err-route", Type: "spanmetric", Width: 2, Title: "HTTP error rate (%) by route",
				Config: spanCfg{Agg: "error_rate", DSL: httpDSL, GroupBy: "http.route"},
			},
			{
				ID: "h-p95-route", Type: "spanmetric", Width: 2, Title: "HTTP P95 latency by route",
				Config: spanCfg{Agg: "p95", Field: "duration_ms", DSL: httpDSL, GroupBy: "http.route"},
			},
			{
				ID: "r-rate-method", Type: "spanmetric", Width: 2, Title: "RPC calls/sec by method",
				Config: spanCfg{Agg: "rate", DSL: rpcDSL, GroupBy: "rpc.method"},
			},
			{
				ID: "r-p95-system", Type: "spanmetric", Width: 2, Title: "RPC P95 by system",
				Config: spanCfg{Agg: "p95", Field: "duration_ms", DSL: rpcDSL, GroupBy: "rpc.system"},
			},
		},
	)
}

// ── Dashboard 4: Database & Cache Performance ───────────────────────────────
//
// Storage layer focus — DBs are the most common saturation culprit in
// production. Slow query identification + per-system breakdown.
func presetDatabase() Dashboard {
	const dbDSL = `db.system != ""`
	return dash(
		"preset-sre-database",
		"Database Performance",
		"DB query rate, latency, and errors broken down by db.system and db.operation. The storage layer is the most common saturation culprit — start here when latency rises but CPU doesn't.",
		[]panel{
			{
				ID: "db-rate-system", Type: "spanmetric", Width: 2, Title: "Queries/sec by db.system",
				Config: spanCfg{Agg: "rate", DSL: dbDSL, GroupBy: "db.system"},
			},
			{
				ID: "db-rate-op", Type: "spanmetric", Width: 2, Title: "Queries/sec by operation",
				Config: spanCfg{Agg: "rate", DSL: dbDSL, GroupBy: "db.operation"},
			},
			{
				ID: "db-p95-system", Type: "spanmetric", Width: 2, Title: "DB P95 by system",
				Config: spanCfg{Agg: "p95", Field: "duration_ms", DSL: dbDSL, GroupBy: "db.system"},
			},
			{
				ID: "db-p99-system", Type: "spanmetric", Width: 2, Title: "DB P99 by system",
				Config: spanCfg{Agg: "p99", Field: "duration_ms", DSL: dbDSL, GroupBy: "db.system"},
			},
			{
				ID: "db-err-svc", Type: "spanmetric", Width: 2, Title: "DB error rate (%) by service",
				Config: spanCfg{Agg: "error_rate", DSL: dbDSL, GroupBy: "service.name"},
			},
			{
				ID: "db-stat-rps", Type: "stat", Width: 1, Title: "DB RPS (total)",
				Config: statCfg{Source: "spanmetric", Unit: "rps", Decimals: 1,
					Span: &spanCfg{Agg: "rate", DSL: dbDSL}},
			},
			{
				ID: "db-stat-p95", Type: "stat", Width: 1, Title: "DB P95",
				Config: statCfg{Source: "spanmetric", Unit: "ms", Decimals: 1,
					Span: &spanCfg{Agg: "p95", Field: "duration_ms", DSL: dbDSL}},
			},
		},
	)
}

// ── Dashboard 5: Errors & Reliability ───────────────────────────────────────
//
// Failure-focused view: error trends, top error-producing services,
// links into the Problems / Exceptions / SLOs pages.
func presetReliability() Dashboard {
	const errDSL = `status_code = "error"`
	return dash(
		"preset-sre-reliability",
		"Errors & Reliability",
		"Error trends and links to the Problems, Exceptions and SLO pages. When the Golden Signals dashboard shows error rate climbing, drill in here.",
		[]panel{
			{
				ID: "intro", Type: "markdown", Width: 4, Title: "",
				Config: mdCfg{Text: "**Reliability inbox.** When errors trend up, work top-to-bottom: " +
					"open the [Problems page](/problems) for active rule alerts, the [Exceptions page](/errors) for grouped stack-traces, " +
					"and the [SLOs page](/slos) for error-budget burn-rate."},
			},
			{
				ID: "err-rate-svc", Type: "spanmetric", Width: 2, Title: "Error rate (%) by service",
				Config: spanCfg{Agg: "error_rate", GroupBy: "service.name"},
			},
			{
				ID: "err-count-svc", Type: "spanmetric", Width: 2, Title: "Error count by service",
				Config: spanCfg{Agg: "errors", GroupBy: "service.name"},
			},
			{
				ID: "err-by-op", Type: "spanmetric", Width: 2, Title: "Top error-producing operations",
				Config: spanCfg{Agg: "errors", DSL: errDSL, GroupBy: "name"},
			},
			{
				ID: "err-by-status", Type: "spanmetric", Width: 2, Title: "Errors by HTTP status",
				Config: spanCfg{Agg: "errors", DSL: errDSL, GroupBy: "http.status_code"},
			},
			{
				ID: "stat-err-pct", Type: "stat", Width: 1, Title: "Error rate (system)",
				Config: statCfg{Source: "spanmetric", Unit: "%", Decimals: 2,
					Span: &spanCfg{Agg: "error_rate"}},
			},
			{
				ID: "stat-err-rps", Type: "stat", Width: 1, Title: "Errors/sec",
				Config: statCfg{Source: "spanmetric", Unit: "rps", Decimals: 2,
					Span: &spanCfg{Agg: "errors"}},
			},
		},
	)
}
