package chstore

// trace_column_values.go — v0.10.1511 (trace list column filters, prod user
// request approved 2026-10-24: "Excel'deki gibi filtrelenebilsin, aynı
// fonksiyon kodu içeren trace'leri filtrelemek iyi olabilir").
//
// The /traces header filter popover asks ONE question per column: "which
// values does this column take across the traces in my current window and
// filters, and how many traces carry each?". Answered here as
// `value → trace count`, top-N, with an optional server-side substring
// search (`q`) so a long-tail FUNCTION_CODE is reachable without an eager
// full list (picker rule, CLAUDE.md).
//
// Why NOT /api/attribute-values: it is filter-blind (window only), so its
// counts would disagree with the list the operator is looking at.
// Why NOT /api/traces/aggregate as-is: it computes four quantiles per group,
// splits rows by root service (group_extra) and its MV path pays a
// DISTINCT trace_id over raw spans for the `in_raw` badge — a popover needs
// none of it. And its GroupAttr branch reads only promoted columns + the
// attribute arrays, so `openshift.cluster.name` / `http.status_code` (typed
// columns in the list's own projection, traceExtrasProjection) would answer
// from a different place than the cell shows.
//
// What IS shared (not copied): the span WHERE + trace-level inner HAVING
// come from aggregateInnerWhere — the exact scope of the Aggregated tab, so
// the two views of one page cannot drift; the builtin group keys come from
// aggregateGroupExpr (root-first, narrowed fallback v0.10.1505).
//
// Bounds (house rule for spans): time-bounded WHERE, inner GROUP BY trace_id
// capped at the newest traceColValuesTraceCap traces, outer LIMIT ≤ 100,
// max_execution_time. MV-first: service / operation with no span-level
// predicate read trace_summary_5m (same gate as GetTraceAggregate).

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// TraceColumnValuesFilter — one header popover request. Scope fields mirror
// AggregateFilter (the HTTP layer fills both from the same query params).
type TraceColumnValuesFilter struct {
	Scope AggregateFilter // window + filters; GroupBy/GroupAttr/Sort ignored
	// Column: "service" | "operation" | "attr". attr requires Key.
	Column string
	Key    string // attribute column key (sanitised by the HTTP layer)
	Q      string // optional substring; `*` / `?` wildcards like /api/attribute-values
	Limit  int
}

// TraceColumnValue — one popover row.
type TraceColumnValue struct {
	Value string `json:"value"`
	Count uint64 `json:"count"`
}

// traceColValuesTraceCap — inner per-trace set cap (same order as the
// aggregate's 200k; counts are "within the newest N traces" beyond it).
const traceColValuesTraceCap = 200000

// ClampTraceColValuesLimit — SAF: 0/negative → 20, ceiling 100. The popover
// shows a top list, not a catalogue; the clamp also bounds the cache key.
func ClampTraceColValuesLimit(n int) int {
	if n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return n
}

// traceColLike — SAF: q → ILIKE pattern. Wildcards (`*`, `?`) are honoured
// verbatim (anchored by the operator); a plain string is a substring match.
// Empty q → "" (no predicate).
func traceColLike(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	like := strings.NewReplacer(`*`, `%`, `?`, `_`).Replace(q)
	if !strings.ContainsAny(q, "*?") {
		like = "%" + like + "%"
	}
	return like
}

// traceColumnAttrValue — SAF: the per-span value expression of an attribute
// column, resolved in the SAME order as the list cell (traceExtrasProjection):
// cluster alias → promoted column → well-known typed column → span/resource
// arrays. Returns a Nullable-ish String where "absent" is NULL or ”.
//
// http.status_code: the typed column stores 0 for "no status"; 0 is the
// cell's "—", not a value an operator filters on, so it maps to NULL here and
// never appears in the popover.
func traceColumnAttrValue(key string) (string, []any) {
	if clusterAliasKeys[key] {
		return "nullIf(cluster, '')", nil
	}
	if col, ok := promotedCols()[key]; ok {
		return "nullIf(" + col + ", '')", nil
	}
	if col, ok := WellKnownTraceCol[key]; ok {
		if col == "toString(http_status)" {
			return "nullIf(toString(http_status), '0')", nil
		}
		return "nullIf(toString(" + col + "), '')", nil
	}
	return "coalesce(nullIf(attr_values[indexOf(attr_keys, ?)], ''), " +
		"nullIf(res_values[indexOf(res_keys, ?)], ''))", []any{key, key}
}

// traceColumnGroupExpr — SAF: the per-trace group key for the raw path.
// Builtins reuse aggregateGroupExpr (root-first + v0.10.1505 narrowed
// fallback); attribute columns use the v0.9.635 shape (root's value, else
// the EARLIEST span carrying one — deterministic argMinIf).
func traceColumnGroupExpr(f TraceColumnValuesFilter) (string, []any) {
	switch f.Column {
	case "service", "operation":
		af := f.Scope
		af.GroupAttr = ""
		af.GroupBy = f.Column
		g, _, args := aggregateGroupExpr(af)
		return g, args
	}
	val, vargs := traceColumnAttrValue(f.Key)
	expr := "coalesce(" +
		"nullIf(anyIf(" + val + ", " + aggRootPred + "), ''), " +
		"argMinIf(" + val + ", time, " + val + " != ''), '')"
	// val appears three times (root anyIf, argMin value, argMin condition).
	args := make([]any, 0, 3*len(vargs))
	for i := 0; i < 3; i++ {
		args = append(args, vargs...)
	}
	return expr, args
}

// traceColumnValuesRawSQL — SAF: raw-path SQL + args. Placeholder order:
// group-expr args → WHERE args → inner HAVING (search/chips) → q → limit.
// Duration bounds ride the WHERE exactly like the list (buildGetTracesWhere:
// span duration), not the aggregate's per-group avg.
func traceColumnValuesRawSQL(f TraceColumnValuesFilter, clusterExpr string) (string, []any) {
	groupExpr, groupArgs := traceColumnGroupExpr(f)
	wc, innerHaving, innerArgs := aggregateInnerWhere(f.Scope, clusterExpr)
	if f.Scope.MinMs > 0 {
		wc.add("duration >= ?", int64(f.Scope.MinMs*1e6))
	}
	if f.Scope.MaxMs > 0 {
		wc.add("duration <= ?", int64(f.Scope.MaxMs*1e6))
	}
	qHaving := ""
	var qArgs []any
	if like := traceColLike(f.Q); like != "" {
		qHaving = " AND group_key ILIKE ?"
		qArgs = []any{like}
	}
	sql := `
		SELECT group_key, count() AS c
		FROM (
		    SELECT trace_id,
		           ` + groupExpr + ` AS group_key,
		           min(time) AS trace_start
		    FROM spans ` + wc.sql() + `
		    GROUP BY trace_id
		    HAVING group_key != ''` + innerHaving + qHaving + `
		    ORDER BY trace_start DESC
		    LIMIT ` + strconv.Itoa(traceColValuesTraceCap) + `
		)
		GROUP BY group_key
		ORDER BY c DESC, group_key ASC
		LIMIT ?
		SETTINGS max_execution_time = 15, max_threads = 8`
	args := append([]any{}, groupArgs...)
	args = append(args, wc.args...)
	args = append(args, innerArgs...)
	args = append(args, qArgs...)
	args = append(args, ClampTraceColValuesLimit(f.Limit))
	return sql, args
}

// traceColumnValuesMVEligible — SAF: the GetTraceAggregate MV gate, minus
// what this read never supports on the MV (duration bounds: the list's
// semantics are span-level, the MV only knows trace duration).
func traceColumnValuesMVEligible(f TraceColumnValuesFilter) bool {
	sc := f.Scope
	return (f.Column == "service" || f.Column == "operation") &&
		sc.Search == "" && sc.Env == "" && sc.Cluster == "" &&
		len(sc.Filters) == 0 && !sc.FilterRoot.hasPredicate() &&
		sc.MinMs == 0 && sc.MaxMs == 0 &&
		!sc.From.IsZero() && !sc.To.IsZero() &&
		sc.To.Sub(sc.From) >= 5*time.Minute
}

// traceColumnValuesMVSQL — SAF: trace_summary_5m read. keyExpr is the
// Store's display-service / root-name merge expression. Placeholder order:
// bucket from/to (+ service index triple) → q → limit.
func traceColumnValuesMVSQL(f TraceColumnValuesFilter, keyExpr string) (string, []any) {
	sc := f.Scope
	where := "WHERE time_bucket >= ? AND time_bucket < ?"
	args := []any{sc.From, aggWindowEnd(sc.From, sc.To, sc.Service != "")}
	if sc.Service != "" {
		// Same narrowing as getTraceAggregateFromMV (v0.6.43 / v0.10.102):
		// every trace the service TOUCHED, rooted or not.
		where += ` AND trace_id GLOBAL IN (
		    SELECT DISTINCT trace_id FROM trace_service_index_5m
		    WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		)`
		args = append(args, sc.Service, sc.From, sc.To)
	}
	having := "HAVING group_key != ''"
	if sc.HasError {
		having += " AND countMerge(error_count_state) > 0"
	}
	if like := traceColLike(f.Q); like != "" {
		having += " AND group_key ILIKE ?"
		args = append(args, like)
	}
	sql := `
		SELECT group_key, count() AS c
		FROM (
		    SELECT trace_id, ` + keyExpr + ` AS group_key
		    FROM trace_summary_5m
		    ` + where + `
		    GROUP BY trace_id
		    ` + having + `
		    LIMIT ` + strconv.Itoa(traceColValuesTraceCap) + `
		)
		GROUP BY group_key
		ORDER BY c DESC, group_key ASC
		LIMIT ?
		SETTINGS max_execution_time = 15`
	args = append(args, ClampTraceColValuesLimit(f.Limit))
	return sql, args
}

// GetTraceColumnValues — top values of one /traces list column within the
// current window + filters, with trace counts. Never nil (JSON [] not null —
// v0.10.1505 class).
func (s *Store) GetTraceColumnValues(ctx context.Context, f TraceColumnValuesFilter) ([]TraceColumnValue, error) {
	var sql string
	var args []any
	if traceColumnValuesMVEligible(f) && !s.TraceMVGap(ctx, f.Scope.From, f.Scope.To) {
		key := "argMaxIfMerge(root_name_state)"
		if f.Column == "service" {
			key = s.traceDisplaySvcExpr()
		}
		sql, args = traceColumnValuesMVSQL(f, key)
	} else {
		sql, args = traceColumnValuesRawSQL(f, s.clusterExpr())
	}
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TraceColumnValue{}
	for rows.Next() {
		var v TraceColumnValue
		if err := rows.Scan(&v.Value, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
