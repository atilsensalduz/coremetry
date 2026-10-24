package api

// trace_column_values.go — v0.10.1511 (trace list column filters; prod user
// request, operator approval 2026-10-24).
//
//	GET /api/traces/column-values?col=service|operation|attr&key=&q=&limit=
//	    &from=&to=&service=&search=&hasError=&minMs=&maxMs=&env=&cluster=
//	    &filters=|filterGroup=|dsl=
//
// Feeds the /traces header filter popover: top values of ONE list column
// with trace counts, inside the operator's current window AND filters, with
// optional server-side search (`q`). Fetched only when a popover opens.
//
// Own file, not api.go (TestApiGoDoesNotGrow). Why a new route instead of
// /api/attribute-values or /api/traces/aggregate: see the chstore header
// (trace_column_values.go) — the first is filter-blind, the second pays for
// quantiles + an in_raw DISTINCT and resolves attribute keys differently
// from the list cell. The scope parsing below is the aggregate handler's
// (same params, same 400s), so share links and both tabs agree.
//
// No role gate — read-only, viewer must see it (global auth middleware still
// 401s anonymous). serveCached 30s; the key is the canonicalised raw query
// (cacheRawQuery: every input, from/to snapped to the grid) with `limit`
// clamped first so a crafted limit can't mint unbounded entries.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("trace-column-values", (*Server).registerTraceColumnValuesRoutes) }

func (s *Server) registerTraceColumnValuesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/traces/column-values", s.getTraceColumnValues)
}

// traceColValuesKey — SAF: cache key = canonical raw query with the limit
// clamped (and `refresh` dropped by cacheRawQueryString). Pinned in
// trace_column_values_test.go.
func traceColValuesKey(rawQuery string) string {
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "traces-colvals:" + cacheRawQueryString(rawQuery)
	}
	q.Set("limit", strconv.Itoa(chstore.ClampTraceColValuesLimit(parseInt(q.Get("limit"), 20))))
	return "traces-colvals:" + cacheRawQueryString(q.Encode())
}

func (s *Server) getTraceColumnValues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	col := strings.TrimSpace(q.Get("col"))
	key := strings.TrimSpace(q.Get("key"))
	switch col {
	case "service", "operation":
		key = ""
	case "attr":
		if key == "" || !isSafeAttrKey(strings.TrimPrefix(strings.TrimPrefix(key, "resource."), "span.")) {
			writeJSONError(w, http.StatusBadRequest, "attr column needs a valid key")
			return
		}
	default:
		writeJSONError(w, http.StatusBadRequest, "col must be service, operation or attr")
		return
	}
	scope := chstore.AggregateFilter{
		Service:  q.Get("service"),
		Search:   q.Get("search"),
		From:     parseTime(q.Get("from")),
		To:       parseTime(q.Get("to")),
		HasError: q.Get("hasError") == "true",
		MinMs:    parseFloat(q.Get("minMs")),
		MaxMs:    parseFloat(q.Get("maxMs")),
		Env:      strings.TrimSpace(q.Get("env")),
		Cluster:  strings.TrimSpace(q.Get("cluster")),
	}
	if scope.From.IsZero() || scope.To.IsZero() || !scope.To.After(scope.From) {
		// Spans rule: never an unbounded window.
		writeJSONError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	filters, ferr := parseFiltersAndDSL(q.Get("filters"), q.Get("dsl"))
	if ferr != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid query DSL: "+ferr.Error())
		return
	}
	scope.Filters = filters
	root, gerr := parseFilterGroup(q.Get("filterGroup"))
	if gerr != nil {
		writeErr(w, gerr)
		return
	}
	scope.FilterRoot = root
	f := chstore.TraceColumnValuesFilter{
		Scope:  scope,
		Column: col,
		Key:    key,
		Q:      strings.TrimSpace(q.Get("q")),
		Limit:  chstore.ClampTraceColValuesLimit(parseInt(q.Get("limit"), 20)),
	}
	s.serveCached(w, r, traceColValuesKey(r.URL.RawQuery), 30*time.Second, func(ctx context.Context) (any, error) {
		rows, err := s.store.GetTraceColumnValues(ctx, f)
		if err != nil {
			return nil, err
		}
		return rows, nil
	})
}
