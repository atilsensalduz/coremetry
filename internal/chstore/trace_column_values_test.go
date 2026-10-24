package chstore

// v0.10.1511 — trace list column filters (trace_column_values.go). Pure SQL
// builders: bounds, scope parity with the Aggregated tab, key resolution
// parity with the list cell, placeholder/arg lockstep.

import (
	"strings"
	"testing"
	"time"
)

func colScope() AggregateFilter {
	to := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	return AggregateFilter{From: to.Add(-time.Hour), To: to}
}

func TestClampTraceColValuesLimit(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 20}, {-3, 20}, {5, 5}, {100, 100}, {101, 100}, {1 << 20, 100}} {
		if got := ClampTraceColValuesLimit(c.in); got != c.want {
			t.Errorf("clamp(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

func TestTraceColLike(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""}, {"  ", ""}, {"FX", "%FX%"}, {"FX*", "FX%"}, {"F?X", "F_X"},
	} {
		if got := traceColLike(c.in); got != c.want {
			t.Errorf("like(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

// Placeholder count must equal len(args) for every shape (the bind-order
// bug class: one missing arg shifts every later predicate).
func TestTraceColumnValuesRawSQLArgsLockstep(t *testing.T) {
	cases := []TraceColumnValuesFilter{
		{Scope: colScope(), Column: "service"},
		{Scope: colScope(), Column: "operation", Q: "GET"},
		{Scope: colScope(), Column: "attr", Key: "function_code_custom"},
		{Scope: colScope(), Column: "attr", Key: "http.status_code", Q: "5*"},
		{Scope: colScope(), Column: "attr", Key: "openshift.cluster.name"},
	}
	sc := colScope()
	sc.Service = "orders-api"
	sc.Search = "checkout pay"
	sc.HasError = true
	sc.Env = "prod"
	sc.Cluster = "kxzone"
	sc.MinMs, sc.MaxMs = 10, 5000
	sc.Filters = []FilterExpr{{Key: "CHANNEL_CODE_X", Op: "=", Values: []string{"MOBILE"}}}
	cases = append(cases, TraceColumnValuesFilter{Scope: sc, Column: "attr", Key: "FUNCTION_CODE_X", Q: "AB"})
	for i, f := range cases {
		sql, args := traceColumnValuesRawSQL(f, "cluster")
		if n := strings.Count(sql, "?"); n != len(args) {
			t.Errorf("case %d: %d placeholders, %d args\n%s", i, n, len(args), sql)
		}
		for _, want := range []string{"time >= ?", "time <= ?", "LIMIT 200000", "max_execution_time = 15", "GROUP BY trace_id", "HAVING group_key != ''"} {
			if !strings.Contains(sql, want) {
				t.Errorf("case %d: SQL missing %q", i, want)
			}
		}
		if last := args[len(args)-1]; last != ClampTraceColValuesLimit(f.Limit) {
			t.Errorf("case %d: last arg = %v, want the clamped limit", i, last)
		}
	}
}

// The popover counts must read the Aggregated tab's scope: same WHERE and
// inner HAVING text (aggregateInnerWhere), so a chip/search/error change
// moves both views the same way.
func TestTraceColumnValuesSharesAggregateScope(t *testing.T) {
	sc := colScope()
	sc.Service = "orders-api"
	sc.Search = "pay"
	sc.Filters = []FilterExpr{{Key: "CHANNEL_CODE_X", Op: "=", Values: []string{"MOBILE"}}}
	wc, having, _ := aggregateInnerWhere(sc, "cluster")
	sql, _ := traceColumnValuesRawSQL(TraceColumnValuesFilter{Scope: sc, Column: "service"}, "cluster")
	if !strings.Contains(sql, wc.sql()) {
		t.Errorf("column-values WHERE differs from the aggregate's:\nwant %s\nin   %s", wc.sql(), sql)
	}
	if having == "" || !strings.Contains(sql, having) {
		t.Errorf("column-values inner HAVING differs from the aggregate's: %q", having)
	}
}

// Key resolution follows the LIST CELL (traceExtrasProjection), not the
// arrays-only aggregate branch.
func TestTraceColumnAttrValueResolution(t *testing.T) {
	if v, a := traceColumnAttrValue("openshift.cluster.name"); !strings.Contains(v, "cluster") || len(a) != 0 {
		t.Errorf("cluster alias → cluster column, got %s %v", v, a)
	}
	if v, a := traceColumnAttrValue("http.status_code"); v != "nullIf(toString(http_status), '0')" || len(a) != 0 {
		t.Errorf("http.status_code → typed column with 0 = absent, got %s", v)
	}
	if v, _ := traceColumnAttrValue("http.route"); v != "nullIf(toString(http_route), '')" {
		t.Errorf("well-known → typed column, got %s", v)
	}
	v, a := traceColumnAttrValue("SOME_CUSTOM_KEY_X")
	if !strings.Contains(v, "attr_values[indexOf(attr_keys, ?)]") || !strings.Contains(v, "res_values") || len(a) != 2 {
		t.Errorf("custom key → span+resource arrays, got %s %v", v, a)
	}
	// group expr binds the value expr three times (root, argMin value, argMin cond)
	g, ga := traceColumnGroupExpr(TraceColumnValuesFilter{Column: "attr", Key: "SOME_CUSTOM_KEY_X"})
	if strings.Count(g, "?") != len(ga) || len(ga) != 6 {
		t.Errorf("attr group expr placeholders %d args %d", strings.Count(g, "?"), len(ga))
	}
}

func TestTraceColumnValuesMVGate(t *testing.T) {
	base := TraceColumnValuesFilter{Scope: colScope(), Column: "service"}
	if !traceColumnValuesMVEligible(base) {
		t.Fatal("unfiltered service column should read trace_summary_5m")
	}
	op := base
	op.Column = "operation"
	if !traceColumnValuesMVEligible(op) {
		t.Fatal("unfiltered operation column should read trace_summary_5m")
	}
	for name, mut := range map[string]func(*TraceColumnValuesFilter){
		"attr":    func(f *TraceColumnValuesFilter) { f.Column = "attr"; f.Key = "x" },
		"search":  func(f *TraceColumnValuesFilter) { f.Scope.Search = "a" },
		"env":     func(f *TraceColumnValuesFilter) { f.Scope.Env = "prod" },
		"cluster": func(f *TraceColumnValuesFilter) { f.Scope.Cluster = "c" },
		"chip": func(f *TraceColumnValuesFilter) {
			f.Scope.Filters = []FilterExpr{{Key: "k", Op: "=", Values: []string{"v"}}}
		},
		"minMs":  func(f *TraceColumnValuesFilter) { f.Scope.MinMs = 1 },
		"narrow": func(f *TraceColumnValuesFilter) { f.Scope.From = f.Scope.To.Add(-time.Minute) },
	} {
		f := base
		mut(&f)
		if traceColumnValuesMVEligible(f) {
			t.Errorf("%s must disqualify the MV path", name)
		}
	}
	sc := colScope()
	sc.Service = "orders-api"
	sc.HasError = true
	sql, args := traceColumnValuesMVSQL(TraceColumnValuesFilter{Scope: sc, Column: "service", Q: "pay"}, "argMaxIfMerge(root_service_state)")
	if strings.Count(sql, "?") != len(args) {
		t.Errorf("MV placeholders %d args %d", strings.Count(sql, "?"), len(args))
	}
	for _, want := range []string{"FROM trace_summary_5m", "trace_service_index_5m", "countMerge(error_count_state) > 0", "group_key ILIKE ?", "LIMIT 200000", "max_execution_time"} {
		if !strings.Contains(sql, want) {
			t.Errorf("MV SQL missing %q", want)
		}
	}
	if strings.Contains(sql, "FROM spans") {
		t.Error("MV path must not touch raw spans (no in_raw DISTINCT)")
	}
}
