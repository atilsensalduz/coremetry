package chstore

// v0.10.1505 regression (operator-reported, prod): /traces Aggregated on a
// gRPC callee service (operation + kind=server + http.method=POST chips,
// ~20 min) said "Toplu sorgu hata verdi ya da zaman aşımına uğradı" on every
// retry. Two halves:
//   1. raw path grouped by the ROOT span only; the service/chip WHERE had
//      already dropped the root (it lives in the caller), so every trace got
//      group_key '' and HAVING removed them all;
//   2. the empty result was a nil slice → JSON `null` → the page's error row.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAggSpanSetNarrowed(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		f    AggregateFilter
		want bool
	}{
		{"bare window", AggregateFilter{From: now.Add(-time.Hour), To: now}, false},
		{"search only (trace-level HAVING, WHERE untouched)", AggregateFilter{Search: "x"}, false},
		{"service", AggregateFilter{Service: "orders-api"}, true},
		{"env", AggregateFilter{Env: "prod"}, true},
		{"cluster", AggregateFilter{Cluster: "c1"}, true},
		{"chip", AggregateFilter{Filters: []FilterExpr{{Key: "kind", Op: "=", Values: []string{"server"}}}}, true},
		{"group root", AggregateFilter{FilterRoot: &FilterGroup{Join: "AND", Filters: []FilterExpr{{Key: "kind", Op: "=", Values: []string{"server"}}}}}, true},
		{"hasError span-local", AggregateFilter{HasError: true}, true},
		{"hasError with search (HAVING)", AggregateFilter{HasError: true, Search: "x"}, false},
	}
	for _, c := range cases {
		if got := aggSpanSetNarrowed(c.f); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestAggregateGroupExprFallsBackWhenRootFilteredOut(t *testing.T) {
	// The reported shape: callee service + span chips.
	f := AggregateFilter{
		GroupBy: "operation", Service: "orders-grpc",
		Filters: []FilterExpr{
			{Key: "operation", Op: "=", Values: []string{"com.example.core.grpc.BagService/callBagService"}},
			{Key: "kind", Op: "=", Values: []string{"server"}},
			{Key: "http.method", Op: "=", Values: []string{"POST"}},
		},
	}
	g, extra, args := aggregateGroupExpr(f)
	if !strings.Contains(g, "argMin(name, time)") || !strings.Contains(g, "countIf("+aggRootPred+") > 0") {
		t.Fatalf("narrowed operation group must fall back to the earliest kept span:\n%s", g)
	}
	if !strings.Contains(extra, "argMin(service_name, time)") {
		t.Fatalf("group_extra must fall back too:\n%s", extra)
	}
	if len(args) != 0 {
		t.Fatalf("builtin group takes no binds: %v", args)
	}
	for _, gb := range []string{"service", "kind", "http_status", "http_route"} {
		f.GroupBy = gb
		g, _, _ := aggregateGroupExpr(f)
		if !strings.Contains(g, "argMin(") {
			t.Errorf("%s: narrowed set must fall back:\n%s", gb, g)
		}
	}
	// Un-narrowed: byte-identical root-only semantics (pre-fix SQL).
	g, extra, _ = aggregateGroupExpr(AggregateFilter{GroupBy: "operation"})
	if g != "anyIf(name, (parent_id = '' OR parent_id = '0000000000000000'))" ||
		extra != "anyIf(service_name, (parent_id = '' OR parent_id = '0000000000000000'))" {
		t.Fatalf("un-narrowed must keep root-only SQL:\n%s\n%s", g, extra)
	}
}

// Both aggregate paths return a non-nil slice (JSON [] not null).
func TestTraceAggregateNeverNil(t *testing.T) {
	src, err := os.ReadFile("repo.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "var out []AggregateRow") {
		t.Fatal("aggregate path declares a nil slice — empty result encodes as JSON null")
	}
	if n := strings.Count(s, "out := []AggregateRow{}"); n != 2 {
		t.Fatalf("want 2 non-nil aggregate slices (raw + MV), got %d", n)
	}
}
