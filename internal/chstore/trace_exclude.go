package chstore

// trace_exclude.go — v0.10.1517 — trace-level negation for the /traces filters
// (operator-approved follow-up to v0.10.1511, 2026-10-24).
//
// The problem: the filter model's `!=` / `NOT IN` is SPAN-level — "some span
// has a different value". On a multi-span trace the spans that do not carry
// the key (`'' != X`) satisfy it, so `function_code != X` KEEPS a trace whose
// child span carries X. The column filter's − button promised "exclude".
//
// Decision (docs/DECISIONS.md): a DISTINCT operator, `NOT HAS`, not a new
// meaning for `!=`. `!=` stays span-level everywhere (saved views, shared
// links, Explore, alerts, DQL all keep their answer); the column filter's −
// writes `key NOT HAS [v…]` = "the trace has NO span in the window whose
// key ∈ {v…}". Repeated excludes on one column merge into the same chip.
//
// Execution — an anti-join on trace_id, never a span predicate:
//
//   - raw spans paths (list stages, aggregate, column values, histogram
//     strip): one always-AND conjunct
//     `trace_id GLOBAL NOT IN (<sub>)`, sub = `SELECT trace_id FROM spans WHERE
//     <window> AND (<k = v> OR …) ORDER BY time DESC LIMIT M SETTINGS
//     max_execution_time`.
//     trace_id is constant within a trace, so the conjunct removes EVERY span
//     of an excluded trace and leaves kept traces whole — it narrows the trace
//     set, not the span set (Root / Errors / grouping keep their meaning).
//   - trace_summary_5m fast path (getTracesFromMV): the exclusion does NOT
//     close the MV path. Stage 1's candidate ids (≤ traceStage2MaxIDs) are
//     probed against spans (`trace_id IN (ids) AND <k = v>`, idx_trace bloom
//     + kvh/promoted index) and the hits are dropped before Stage 2. Exact
//     — no set cap — and bounded by the candidate list. The exclusion counts
//     as a post-aggregate predicate for Stage 1's budget (the newest-N slice,
//     RankedWithin reported), like Root / duration.
//
// The positive predicate is compiled by the SAME FilterExpr compiler as an
// `=` / `IN` chip (promoted column → typed column → kvh bloom + array), so
// `k NOT HAS v` excludes exactly the traces `k = v` would select.
//
// Where the operator is not trace-aware (Explore span lists, span metrics
// outside the /traces strip, metric_points), FilterExpr.sql() compiles it as
// span-level `NOT IN` — the only meaning a single span can have. Inside an
// OR / nested group it is rejected at the API boundary (FilterGroup.Validate):
// an anti-join leaf under OR has no cheap bounded shape.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// OpNotHas — the trace-level exclusion operator ("the trace has no span with
// key ∈ values"). One or more values.
const OpNotHas = "NOT HAS"

// traceExcludeSetMax — anti-join set budget (newest M matching spans). Same
// budget as the semi-join chain (traceChipSetMax): no unbounded GLOBAL set
// (v0.10.238's 241 class). Beyond it the OLDEST matching traces may slip
// through the set on the raw path — GetTraces' page guard probes the page's
// ids exactly and drops them (short page, never a wrong row); the MV path is
// exact by construction (candidate probe). Aggregate / column-value counts
// are exact below the cap (their inner set is capped at 200k traces anyway).
const traceExcludeSetMax = traceChipSetMax

// traceExcludeDefaultWindow — when a caller has no window (a bare trace-id
// lookup), the anti-join subquery is still time-bounded (house rule): the
// last 24 h, server-side now64().
const traceExcludeDefaultWindow = 24 * time.Hour

func isNotHasOp(op string) bool { return normOp(op) == OpNotHas }

// splitNotHas — SAF: NOT HAS chips vs the rest (order kept). rest is nil when
// empty so the `len(Filters) == 0` gates downstream read it as "no chips".
func splitNotHas(fs []FilterExpr) (excl, rest []FilterExpr) {
	for _, fe := range fs {
		if isNotHasOp(fe.Op) {
			excl = append(excl, fe)
		} else {
			rest = append(rest, fe)
		}
	}
	return excl, rest
}

// hoistNotHas — SAF: lifts the NOT HAS chips out of a (flat filters, group)
// pair. Only AND-joined positions are hoistable (a top-level AND leaf is a
// conjunct, so moving it into its own always-AND conjunct keeps the meaning);
// leaves under an OR root or inside sub-groups stay where they are (Validate
// rejects them at the boundary). flatSuperseded: the TraceFilter /
// AggregateFilter rule "FilterRoot SUPERSEDES Filters" — flat chips are not
// hoisted when a root exists, because today they are ignored.
func hoistNotHas(filters []FilterExpr, root *FilterGroup, flatSuperseded bool) (excl, outFilters []FilterExpr, outRoot *FilterGroup) {
	outFilters, outRoot = filters, root
	if !(flatSuperseded && root != nil) {
		e, rest := splitNotHas(filters)
		if len(e) > 0 {
			excl, outFilters = append(excl, e...), rest
		}
	}
	if root != nil && joinOp(root.Join) == "AND" {
		e, rest := splitNotHas(root.Filters)
		if len(e) > 0 {
			excl = append(excl, e...)
			g := *root
			g.Filters = rest
			outRoot = &g
			if len(g.Filters) == 0 && len(g.Groups) == 0 {
				outRoot = nil
			}
		}
	}
	return excl, outFilters, outRoot
}

// hoistTraceExcludes — SAF + idempotent: NOT HAS chips move from Filters /
// FilterRoot to f.Excludes. Every Store entry that takes a TraceFilter calls
// it first, so the span-chip helpers (filtersTraceLevel, splitServiceChips,
// spanScopedChips, the MV gates …) never see a NOT HAS chip.
func hoistTraceExcludes(f TraceFilter) TraceFilter {
	excl, fs, root := hoistNotHas(f.Filters, f.FilterRoot, true)
	if len(excl) == 0 {
		return f
	}
	f.Filters, f.FilterRoot = fs, root
	f.Excludes = append(append([]FilterExpr(nil), f.Excludes...), excl...)
	return f
}

// hoistAggExcludes — AggregateFilter twin (Aggregated tab, column values).
func hoistAggExcludes(f AggregateFilter) AggregateFilter {
	excl, fs, root := hoistNotHas(f.Filters, f.FilterRoot, true)
	if len(excl) == 0 {
		return f
	}
	f.Filters, f.FilterRoot = fs, root
	f.Excludes = append(append([]FilterExpr(nil), f.Excludes...), excl...)
	return f
}

// traceExcludeWindow — SAF: the window the exclusion looks at = the list's
// own raw WHERE window (5-minute aligned start when the window is wider than
// 5 minutes — buildGetTracesWhere / aggregateInnerWhere v0.5.356), so "a span
// in the window" means the same spans the list aggregates.
func traceExcludeWindow(from, to time.Time) (time.Time, time.Time) {
	if !from.IsZero() && !to.IsZero() && to.Sub(from) > 5*time.Minute {
		from = from.Truncate(5 * time.Minute)
	}
	return from, to
}

// traceExcludeMatch — SAF: the positive span predicate of a set of NOT HAS
// chips, OR-joined (a trace is excluded when ANY chip matches one of its
// spans). Each chip compiles as `k = v` (one value) or `k IN (…)`, through the
// same compiler as an include chip. Uncompilable chips are skipped and the
// caller learns it from the empty result (ok=false).
func traceExcludeMatch(excl []FilterExpr, pm map[string]string) (string, []any, bool) {
	var parts []string
	var args []any
	for _, fe := range excl {
		if len(fe.Values) == 0 {
			continue
		}
		pos := FilterExpr{Key: fe.Key, Op: "IN", Values: fe.Values}
		if len(fe.Values) == 1 {
			pos.Op = "="
		}
		sql, a, err := pos.spansSQL("", pm)
		if err != nil || sql == "" {
			continue
		}
		parts = append(parts, sql)
		args = append(args, a...)
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	if len(parts) == 1 {
		return parts[0], args, true
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}

// traceExcludeSubquerySQL — SAF: the bounded set of trace ids to exclude.
// Placeholder order: window bounds (when given), match args, limit.
func traceExcludeSubquerySQL(match string, bounded bool) string {
	win := "time >= now64(9) - INTERVAL " + fmt.Sprint(int(traceExcludeDefaultWindow.Hours())) + " HOUR"
	if bounded {
		win = "time >= ? AND time <= ?"
	}
	return "SELECT trace_id FROM spans WHERE " + win + " AND " + match +
		" ORDER BY time DESC LIMIT ? SETTINGS max_execution_time = 15"
}

// traceExcludeConjunct — SAF: the always-AND anti-join conjunct for a raw
// spans WHERE, or ("", nil) when there is nothing to exclude.
func traceExcludeConjunct(excl []FilterExpr, from, to time.Time, pm map[string]string) (string, []any) {
	if len(excl) == 0 {
		return "", nil
	}
	match, margs, ok := traceExcludeMatch(excl, pm)
	if !ok {
		return "", nil
	}
	from, to = traceExcludeWindow(from, to)
	bounded := !from.IsZero() && !to.IsZero()
	var args []any
	if bounded {
		args = append(args, from, to)
	}
	args = append(args, margs...)
	args = append(args, traceExcludeSetMax)
	return "trace_id GLOBAL NOT IN (" + traceExcludeSubquerySQL(match, bounded) + ")", args
}

// addTraceExcludeConjunct — the one writer of the anti-join into a WHERE.
func addTraceExcludeConjunct(wc *whereClause, excl []FilterExpr, from, to time.Time, pm map[string]string) {
	if cond, args := traceExcludeConjunct(excl, from, to, pm); cond != "" {
		wc.add(cond, args...)
	}
}

// traceExcludeProbeSQL — SAF: which of Stage 1's candidate ids carry a
// matching span (MV path). Placeholder order: window, ids, match args, limit.
func traceExcludeProbeSQL(nIDs int, match string) string {
	return "SELECT DISTINCT trace_id FROM spans WHERE time >= ? AND time <= ? AND trace_id IN (" +
		chPlaceholders(nIDs) + ") AND " + match + " LIMIT ? SETTINGS max_execution_time = 10"
}

// keepNotExcluded — SAF: candidate ids minus the probe's hits, order kept.
func keepNotExcluded(ids []any, hit map[string]bool) []any {
	if len(hit) == 0 {
		return ids
	}
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		if s, ok := id.(string); ok && hit[s] {
			continue
		}
		out = append(out, id)
	}
	return out
}

// dropExcludedCandidates — MV path: removes the candidates that carry a
// matching span in the window. Exact (the probe is bounded by the id list).
func (s *Store) dropExcludedCandidates(ctx context.Context, f TraceFilter, ids []any) ([]any, error) {
	if len(f.Excludes) == 0 || len(ids) == 0 {
		return ids, nil
	}
	pm := promotedCols()
	if f.NoPromoted {
		pm = nil
	}
	match, margs, ok := traceExcludeMatch(f.Excludes, pm)
	if !ok {
		return ids, nil
	}
	from, to := traceExcludeWindow(f.From, f.To)
	args := make([]any, 0, len(ids)+len(margs)+3)
	args = append(args, from, to)
	args = append(args, ids...)
	args = append(args, margs...)
	args = append(args, len(ids))
	t0 := time.Now()
	sql := traceExcludeProbeSQL(len(ids), match)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		f.Explain.step("trace-exclude-probe", sql, args, t0, 0, err)
		return nil, fmt.Errorf("trace exclude probe: %w", err)
	}
	defer rows.Close()
	hit := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		hit[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	f.Explain.step("trace-exclude-probe", sql, args, t0, len(hit), nil)
	return keepNotExcluded(ids, hit), nil
}

// traceExcludePageGuard — GetTraces' deferred guard for trace-level NOT HAS.
// The raw paths' anti-join set is capped (traceExcludeSetMax newest matching
// spans); past the cap the oldest matching traces would survive it —
// measured: 24 h, sort=duration, ~450k matching traces → 11 of 50 rows still
// carried the value. The page's ids (≤ limit+1) are probed exactly
// (dropExcludedCandidates) and survivors dropped: a short page, never a wrong
// row. exact = the MV path already probe-verified every candidate → skipped.
// f is the entry filter (full requested window).
func (s *Store) traceExcludePageGuard(ctx context.Context, f TraceFilter, exact bool, rows []TraceRow, err error) ([]TraceRow, error) {
	if err != nil || exact || len(f.Excludes) == 0 || len(rows) == 0 {
		return rows, err
	}
	ids := make([]any, len(rows))
	for i, r := range rows {
		ids[i] = r.TraceID
	}
	kept, perr := s.dropExcludedCandidates(ctx, f, ids)
	if perr != nil {
		return nil, perr
	}
	if len(kept) == len(rows) {
		return rows, nil
	}
	f.Explain.note("trace-exclude=page-guard dropped %d row(s) past the anti-join cap", len(rows)-len(kept))
	return keepRows(rows, kept), nil
}

// keepRows — SAF: rows whose id survived the probe, order kept.
func keepRows(rows []TraceRow, kept []any) []TraceRow {
	ok := make(map[string]bool, len(kept))
	for _, id := range kept {
		if s, isStr := id.(string); isStr {
			ok[s] = true
		}
	}
	out := rows[:0:0]
	for _, r := range rows {
		if ok[r.TraceID] {
			out = append(out, r)
		}
	}
	return out
}

// traceFilterHasExcludes — SAF: does the filter carry a NOT HAS chip (before
// or after hoisting)?
func traceFilterHasExcludes(f TraceFilter) bool {
	return len(hoistTraceExcludes(f).Excludes) > 0
}

// validateNotHasPlacement — SAF: NOT HAS only where it can be hoisted (top
// level of an AND group). Under OR / in a sub-group → 400 at the boundary.
func validateNotHasPlacement(g FilterGroup) error {
	if joinOp(g.Join) != "AND" {
		for _, fe := range g.Filters {
			if isNotHasOp(fe.Op) {
				return fmt.Errorf("operator %s (trace-level exclusion) cannot sit under an OR group; add it as a top-level AND filter", OpNotHas)
			}
		}
	}
	for _, sub := range g.Groups {
		for _, fe := range sub.Filters {
			if isNotHasOp(fe.Op) {
				return fmt.Errorf("operator %s (trace-level exclusion) cannot sit inside a nested group; add it as a top-level AND filter", OpNotHas)
			}
		}
	}
	return nil
}
