package chstore

// trace_exclude_test.go — v0.10.1517 — trace-level NOT HAS (operator-approved follow-up to
// v0.10.1511, 2026-10-24). Contract:
//
//   - `k NOT HAS v` = "the trace has NO span in the window with k = v"; it
//     never narrows the span set of a kept trace.
//   - raw paths: one bounded anti-join conjunct (time window + ORDER BY +
//     LIMIT + max_execution_time), placeholders == args.
//   - `!=` keeps its span-level meaning (saved links unchanged).
//   - the trace_summary fast path stays open (candidate probe).
//   - OR / nested placement is a 400.

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

var exWinFrom = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
var exWinTo = time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)

func notHas(k string, v ...string) FilterExpr { return FilterExpr{Key: k, Op: OpNotHas, Values: v} }

func TestHoistTraceExcludes(t *testing.T) {
	x := notHas("function_code", "X")
	eq := FilterExpr{Key: "channel_code", Op: "=", Values: []string{"WEB"}}
	cases := []struct {
		name      string
		in        TraceFilter
		wantExcl  int
		wantFlat  int
		wantRoot  bool // FilterRoot non-nil after hoist
		wantRootN int
	}{
		{"flat chips", TraceFilter{Filters: []FilterExpr{eq, x}}, 1, 1, false, 0},
		{"only NOT HAS → Filters nil (MV gate sees no chips)", TraceFilter{Filters: []FilterExpr{x}}, 1, 0, false, 0},
		{"flat AND root", TraceFilter{FilterRoot: &FilterGroup{Join: "AND", Filters: []FilterExpr{x, eq}}}, 1, 0, true, 1},
		{"AND root emptied → nil", TraceFilter{FilterRoot: &FilterGroup{Join: "AND", Filters: []FilterExpr{x}}}, 1, 0, false, 0},
		{"AND root with sub-groups keeps the group", TraceFilter{FilterRoot: &FilterGroup{Join: "AND", Filters: []FilterExpr{x},
			Groups: []FilterGroup{{Join: "OR", Filters: []FilterExpr{eq, eq}}}}}, 1, 0, true, 0},
		{"OR root is not hoisted", TraceFilter{FilterRoot: &FilterGroup{Join: "OR", Filters: []FilterExpr{x, eq}}}, 0, 0, true, 2},
		{"flat superseded by root stays put", TraceFilter{Filters: []FilterExpr{x}, FilterRoot: &FilterGroup{Join: "AND", Filters: []FilterExpr{eq}}}, 0, 1, true, 1},
		{"lower-case op", TraceFilter{Filters: []FilterExpr{{Key: "k", Op: "not has", Values: []string{"v"}}}}, 1, 0, false, 0},
		{"no chips", TraceFilter{}, 0, 0, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var before *FilterGroup
			if c.in.FilterRoot != nil {
				g := *c.in.FilterRoot
				before = &g
			}
			got := hoistTraceExcludes(c.in)
			if len(got.Excludes) != c.wantExcl || len(got.Filters) != c.wantFlat || (got.FilterRoot != nil) != c.wantRoot {
				t.Fatalf("excl=%d flat=%d root=%v; want %d %d %v", len(got.Excludes), len(got.Filters), got.FilterRoot != nil, c.wantExcl, c.wantFlat, c.wantRoot)
			}
			if c.wantFlat == 0 && got.Filters != nil {
				t.Errorf("Filters must be nil when emptied, got %#v", got.Filters)
			}
			if got.FilterRoot != nil && len(got.FilterRoot.Filters) != c.wantRootN {
				t.Errorf("root leaves=%d want %d", len(got.FilterRoot.Filters), c.wantRootN)
			}
			if before != nil && !reflect.DeepEqual(*before, *c.in.FilterRoot) {
				t.Errorf("caller's FilterRoot was mutated")
			}
			if again := hoistTraceExcludes(got); !reflect.DeepEqual(again, got) {
				t.Errorf("not idempotent: %#v vs %#v", again, got)
			}
		})
	}
}

func TestBuildGetTracesWhereAntiJoin(t *testing.T) {
	base := TraceFilter{From: exWinFrom, To: exWinTo, Service: "frontend"}
	plain := buildGetTracesWhere(base, "''")
	cases := []struct {
		name     string
		excl     []FilterExpr
		wantFrag []string
		nVals    int
	}{
		{"single value → =", []FilterExpr{notHas("function_code", "X")},
			[]string{"attr_values[indexOf(attr_keys, ?)] = ?"}, 1},
		{"merged values → IN", []FilterExpr{notHas("function_code", "X", "Y")},
			[]string{"attr_values[indexOf(attr_keys, ?)] IN (?,?)"}, 2},
		{"two chips → one OR'ed subquery", []FilterExpr{notHas("function_code", "X"), notHas("http.route", "/a")},
			[]string{" OR ", "http_route = ?"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := base
			f.Filters = c.excl
			wc := buildGetTracesWhere(f, "''")
			sql := wc.sql()
			for _, frag := range append([]string{
				"trace_id GLOBAL NOT IN (SELECT trace_id FROM spans WHERE time >= ? AND time <= ? AND ",
				"ORDER BY time DESC LIMIT ? SETTINGS max_execution_time = 15)",
			}, c.wantFrag...) {
				if !strings.Contains(sql, frag) {
					t.Errorf("missing %q in\n%s", frag, sql)
				}
			}
			if strings.Contains(sql, "NOT IN (?") || strings.Contains(sql, "!= ?") {
				t.Errorf("NOT HAS leaked as a span-level negation:\n%s", sql)
			}
			if n := strings.Count(sql, "?"); n != len(wc.args) {
				t.Fatalf("placeholders %d != args %d\n%s", n, len(wc.args), sql)
			}
			// The scope conjuncts are untouched (prefix byte-identical).
			if !strings.HasPrefix(sql, plain.sql()) {
				t.Errorf("anti-join must be appended, scope unchanged:\n%s\nvs\n%s", sql, plain.sql())
			}
			if last := wc.args[len(wc.args)-1]; last != traceExcludeSetMax {
				t.Errorf("last arg = %v, want the set cap %d", last, traceExcludeSetMax)
			}
			// Window args = the list's own (5-min aligned) window.
			got := wc.args[len(plain.args)]
			if got != exWinFrom.Truncate(5*time.Minute) {
				t.Errorf("anti-join from = %v", got)
			}
		})
	}
	// No NOT HAS → byte-identical to today.
	f := base
	f.Filters = []FilterExpr{{Key: "function_code", Op: "!=", Values: []string{"X"}}}
	nw := buildGetTracesWhere(f, "''")
	if sql := nw.sql(); strings.Contains(sql, "GLOBAL NOT IN") || !strings.Contains(sql, "!= ?") {
		t.Errorf("`!=` must stay span-level:\n%s", sql)
	}
}

func TestTraceExcludeNoWindowStillBounded(t *testing.T) {
	cond, args := traceExcludeConjunct([]FilterExpr{notHas("k", "v")}, time.Time{}, time.Time{}, nil)
	if !strings.Contains(cond, "now64(9) - INTERVAL 24 HOUR") || !strings.Contains(cond, "LIMIT ?") {
		t.Fatalf("unbounded anti-join: %s", cond)
	}
	if strings.Count(cond, "?") != len(args) {
		t.Fatalf("placeholders/args: %s %v", cond, args)
	}
}

func TestTraceExcludeKeepsMVPath(t *testing.T) {
	f := hoistTraceExcludes(TraceFilter{From: exWinFrom, To: exWinTo, Filters: []FilterExpr{notHas("function_code", "X")}})
	if !tracesMVEligible(f) {
		t.Errorf("NOT HAS alone must keep the trace_summary fast path")
	}
	if !noServiceSlicePlan(f).postAgg || !tracePostAggFiltered(f) {
		t.Errorf("NOT HAS is a post-Stage-1 predicate (newest-N budget)")
	}
	if _, _, _, reason := traceCountPlan(f, TraceRootDefStrict, false); reason != traceCountReasonExclude {
		t.Errorf("count reason = %q", reason)
	}
	if TraceErrorHistogramEligible(TraceFilter{From: exWinFrom, To: exWinTo, HasError: true, Filters: []FilterExpr{notHas("k", "v")}}) {
		t.Errorf("error strip must stay on metric-batch with NOT HAS")
	}
}

func TestTraceExcludeProbeSQL(t *testing.T) {
	ids := []any{"a", "b", "c"}
	match, margs, ok := traceExcludeMatch([]FilterExpr{notHas("function_code", "X")}, nil)
	if !ok {
		t.Fatal("match not compiled")
	}
	sql := traceExcludeProbeSQL(len(ids), match)
	nArgs := 2 + len(ids) + len(margs) + 1
	if strings.Count(sql, "?") != nArgs || !strings.Contains(sql, "trace_id IN (?,?,?)") ||
		!strings.Contains(sql, "LIMIT ?") || !strings.Contains(sql, "max_execution_time") || !strings.Contains(sql, "time >= ? AND time <= ?") {
		t.Fatalf("probe shape: %s", sql)
	}
	kept := keepNotExcluded(ids, map[string]bool{"b": true})
	if !reflect.DeepEqual(kept, []any{"a", "c"}) {
		t.Errorf("kept = %v", kept)
	}
}

func TestAggregateAndColumnValuesAntiJoin(t *testing.T) {
	af := AggregateFilter{From: exWinFrom, To: exWinTo, Filters: []FilterExpr{notHas("function_code", "X")}}
	wc, _, _ := aggregateInnerWhere(af, "''")
	if !strings.Contains(wc.sql(), "trace_id GLOBAL NOT IN (") || strings.Count(wc.sql(), "?") != len(wc.args) {
		t.Fatalf("aggregate WHERE: %s (%d args)", wc.sql(), len(wc.args))
	}
	cv := TraceColumnValuesFilter{Scope: hoistAggExcludes(af), Column: "service", Limit: 10}
	if traceColumnValuesMVEligible(cv) {
		t.Errorf("column values must read the raw anti-join like the list")
	}
	sql, args := traceColumnValuesRawSQL(cv, "''")
	if strings.Count(sql, "?") != len(args) || !strings.Contains(sql, "GLOBAL NOT IN") {
		t.Fatalf("column values SQL/args mismatch (%d ? vs %d)\n%s", strings.Count(sql, "?"), len(args), sql)
	}
}

func TestSpanMetricBatchWhereAntiJoin(t *testing.T) {
	f := SpanMetricBatchFilter{From: exWinFrom, To: exWinTo, Filters: []FilterExpr{
		{Key: "service.name", Op: "=", Values: []string{"frontend"}}, notHas("function_code", "X")}}
	wc := spanMetricBatchWhere(f, 0, 0)
	sql := wc.sql()
	if !strings.Contains(sql, "service_name = ?") || !strings.Contains(sql, "trace_id GLOBAL NOT IN (") ||
		strings.Contains(sql, "] NOT IN (") || strings.Count(sql, "?") != len(wc.args) {
		t.Fatalf("strip WHERE: %s", sql)
	}
	if _, ok := narrowRollupEligible(f); ok {
		t.Errorf("NOT HAS must not ride the narrow rollup")
	}
}

func TestNotHasValidateAndSpanFallback(t *testing.T) {
	if err := notHas("k").Validate(); err == nil {
		t.Errorf("NOT HAS without values must be rejected")
	}
	if err := notHas("k", "v").Validate(); err != nil {
		t.Errorf("valid NOT HAS rejected: %v", err)
	}
	if err := (FilterExpr{Key: OpGroupFilterKey, Op: OpNotHas, Values: []string{"GET /x"}}).Validate(); err != nil {
		t.Errorf("op_group NOT HAS validates as its positive IN: %v", err)
	}
	or := &FilterGroup{Join: "OR", Filters: []FilterExpr{notHas("k", "v"), {Key: "a", Op: "=", Values: []string{"b"}}}}
	if err := or.Validate(); err == nil {
		t.Errorf("NOT HAS under OR must be a 400")
	}
	nested := &FilterGroup{Join: "AND", Groups: []FilterGroup{{Join: "AND", Filters: []FilterExpr{notHas("k", "v")}}}}
	if err := nested.Validate(); err == nil {
		t.Errorf("NOT HAS in a sub-group must be a 400")
	}
	flat := &FilterGroup{Join: "AND", Filters: []FilterExpr{notHas("k", "v")}}
	if err := flat.Validate(); err != nil {
		t.Errorf("top-level AND NOT HAS rejected: %v", err)
	}
	// Non-trace surfaces: the span-level meaning.
	sql, args, err := notHas("custom.k", "v1", "v2").SQLWithPromoted(nil)
	if err != nil || sql != "attr_values[indexOf(attr_keys, ?)] NOT IN (?,?)" || len(args) != 3 {
		t.Errorf("span fallback = %q %v %v", sql, args, err)
	}
}

// ── semantic fixture (clickhouse local) ──────────────────────────────────

// Fixture: t1 carries X in a CHILD span of another service (excluded); t2 a
// different code (kept); t3 no code (kept); t4 X only OUTSIDE the window
// (kept — window semantics); t5 X on the ROOT span (excluded).
const exFixtureSQL = `
CREATE TABLE spans (
  trace_id String, span_id String, parent_id String DEFAULT '',
  name String DEFAULT 'op', kind String DEFAULT 'server', service_name String,
  host_name String DEFAULT '', deploy_env String DEFAULT '', status_code String DEFAULT 'unset',
  time DateTime64(9, 'UTC'), duration Int64 DEFAULT 1000000,
  db_system String DEFAULT '', db_statement String DEFAULT '', http_method String DEFAULT '',
  http_route String DEFAULT '', http_status UInt16 DEFAULT 0, rpc_system String DEFAULT '',
  rpc_method String DEFAULT '', peer_service String DEFAULT '', msg_system String DEFAULT '',
  attr_keys Array(String), attr_values Array(String), res_keys Array(String) DEFAULT [], res_values Array(String) DEFAULT [],
  attr_kvh Array(UInt64) MATERIALIZED arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), attr_keys, attr_values),
  res_kvh Array(UInt64) MATERIALIZED arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), res_keys, res_values),
  op_group String DEFAULT '', cluster String DEFAULT ''
) ENGINE = MergeTree ORDER BY (service_name, time);
INSERT INTO spans (trace_id, span_id, parent_id, service_name, time, attr_keys, attr_values) VALUES
  ('t1', 'a', '', 'frontend', '2026-10-24 10:10:00', [], []),
  ('t1', 'b', 'a', 'backend', '2026-10-24 10:10:01', ['function_code'], ['X']),
  ('t1', 'c', 'a', 'backend', '2026-10-24 10:10:02', [], []),
  ('t2', 'a', '', 'frontend', '2026-10-24 10:20:00', [], []),
  ('t2', 'b', 'a', 'backend', '2026-10-24 10:20:01', ['function_code'], ['Y']),
  ('t3', 'a', '', 'frontend', '2026-10-24 10:30:00', [], []),
  ('t4', 'a', '', 'frontend', '2026-10-24 10:40:00', [], []),
  ('t4', 'b', 'a', 'backend', '2026-10-24 09:40:00', ['function_code'], ['X']),
  ('t5', 'a', '', 'frontend', '2026-10-24 10:50:00', ['function_code'], ['X']);
`

func runExFixture(t *testing.T, bin, q string) []string {
	t.Helper()
	out, err := exec.Command(bin, "local", "--multiquery", "--query", exFixtureSQL+q).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s\n%s", err, out, q)
	}
	var lines []string
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return lines
}

func TestTraceExcludeSemanticsLiveEngine(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı anti-join anlam testi atlandı (şekil testleri yine koşar)")
	}
	ids := func(f TraceFilter) []string {
		wc := buildGetTracesWhere(hoistTraceExcludes(f), "cluster")
		q := "SELECT DISTINCT trace_id FROM spans " + wc.sql() + " ORDER BY trace_id FORMAT TSVRaw;"
		return runExFixture(t, bin, inlineDBHealthArgs(t, q, wc.args))
	}
	base := TraceFilter{From: exWinFrom, To: exWinTo}

	f := base
	f.Filters = []FilterExpr{notHas("function_code", "X")}
	if got := ids(f); !reflect.DeepEqual(got, []string{"t2", "t3", "t4"}) {
		t.Errorf("NOT HAS X → %v, want [t2 t3 t4] (t1 child span, t5 root span excluded)", got)
	}
	// Service-narrowed list: the exclusion still sees the OTHER service's span.
	f.Service = "frontend"
	if got := ids(f); !reflect.DeepEqual(got, []string{"t2", "t3", "t4"}) {
		t.Errorf("service=frontend + NOT HAS X → %v", got)
	}
	// Merged values.
	f = base
	f.Filters = []FilterExpr{notHas("function_code", "X", "Y")}
	if got := ids(f); !reflect.DeepEqual(got, []string{"t3", "t4"}) {
		t.Errorf("NOT HAS [X Y] → %v", got)
	}
	// The kept trace stays WHOLE (span set not narrowed): t2 keeps both spans.
	wc := buildGetTracesWhere(hoistTraceExcludes(TraceFilter{From: exWinFrom, To: exWinTo, Filters: []FilterExpr{notHas("function_code", "X")}}), "cluster")
	q := "SELECT count() FROM spans " + wc.sql() + " AND trace_id = 't2' FORMAT TSVRaw;"
	if got := runExFixture(t, bin, inlineDBHealthArgs(t, q, wc.args)); !reflect.DeepEqual(got, []string{"2"}) {
		t.Errorf("kept trace lost spans: %v", got)
	}
	// Today's `!=` is span-level and still keeps t1 (its other spans differ)
	// — the 1511 limit, deliberately unchanged; only single-span t5 drops.
	f = base
	f.Filters = []FilterExpr{{Key: "function_code", Op: "!=", Values: []string{"X"}}}
	if got := ids(f); !reflect.DeepEqual(got, []string{"t1", "t2", "t3", "t4"}) {
		t.Errorf("`!=` semantics changed: %v", got)
	}

	// Column-values popover counts agree with the list (service column).
	cv := TraceColumnValuesFilter{Scope: AggregateFilter{From: exWinFrom, To: exWinTo,
		Filters: []FilterExpr{notHas("function_code", "X")}}, Column: "service", Limit: 10}
	cv.Scope = hoistAggExcludes(cv.Scope)
	sql, args := traceColumnValuesRawSQL(cv, "cluster")
	sql = strings.Replace(sql, "SETTINGS max_execution_time = 15, max_threads = 8", "FORMAT TSVRaw SETTINGS max_execution_time = 15, max_threads = 8", 1)
	if got := runExFixture(t, bin, inlineDBHealthArgs(t, sql, args)+";"); !reflect.DeepEqual(got, []string{"frontend\t3"}) {
		t.Errorf("column values with NOT HAS X → %v, want [frontend 3] (= list rows)", got)
	}
}

// Page guard (raw paths past the anti-join cap): survivors of the exact probe
// keep their order; a dropped row never comes back.
func TestKeepRowsPageGuard(t *testing.T) {
	rows := []TraceRow{{TraceID: "a"}, {TraceID: "b"}, {TraceID: "c"}}
	got := keepRows(rows, keepNotExcluded([]any{"a", "b", "c"}, map[string]bool{"b": true}))
	if len(got) != 2 || got[0].TraceID != "a" || got[1].TraceID != "c" {
		t.Fatalf("keepRows = %+v", got)
	}
	if rows[1].TraceID != "b" {
		t.Errorf("input slice was mutated: %+v", rows)
	}
}
