package chstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ServiceMapNode is one service in the global topology graph. The
// frontend renders these as nodes in a force-directed layout; node
// size scales with span count so visually-heavy services sit at the
// top of the operator's attention.
type ServiceMapNode struct {
	Service   string `json:"service"`
	SpanCount int    `json:"spanCount"`
	// ErrorRate is computed across all spans of this service in the
	// sampled traces. Used to colour the node — green=healthy,
	// red=error-heavy — without re-querying per-node.
	ErrorRate float64 `json:"errorRate"`
}

// ServiceMapEdge is a directed call: caller → callee. Weight =
// number of distinct sampled traces in which the edge appeared
// (so a one-off edge from a single trace doesn't visually equal
// a hot path that runs every request). ErrorCount is the count
// of CALLEE spans on this edge that returned an error status.
type ServiceMapEdge struct {
	Caller     string `json:"caller"`
	Callee     string `json:"callee"`
	TraceCount int    `json:"traceCount"`
	SpanCount  int    `json:"spanCount"`
	ErrorCount int    `json:"errorCount"`
}

// ServiceMap is the wire format returned to the frontend.
type ServiceMap struct {
	Nodes        []ServiceMapNode `json:"nodes"`
	Edges        []ServiceMapEdge `json:"edges"`
	SampledFrom  int              `json:"sampledFrom"`  // traces actually inspected
	TotalSpans   int              `json:"totalSpans"`   // span count across them
}

// GetServiceMap derives the global service-level topology from a
// bounded sample of recent traces. Mirrors the ServiceNeighbors
// approach but globally — no anchor service. Two queries:
//
//  1. Pick the heaviest N traces by span-count over the last
//     `since` window, ORDER BY count() DESC. This biases the
//     map toward the request paths that actually drive load,
//     not edge-case 1-span traces.
//  2. Pull only the four columns the edge walk needs
//     (trace_id, span_id, parent_id, service_name, status_code)
//     for those traces. Skips event blobs / attributes — the
//     edge walk doesn't read them.
//
// In-memory walk: for every span S whose parent's service ≠ S's
// service, emit an edge (parent.service → S.service). Errors are
// counted on the callee side (status_code == STATUS_CODE_ERROR /
// 2 in OTel). Result is bounded by the sample size so a billion-
// span/day deployment still answers in <2s.
//
// The IN (?,...) construct holds N=200-ish trace IDs; ClickHouse
// happily plans this against the partition key + bloom-filter on
// trace_id, granule pruning keeps the second query cheap.
func (s *Store) GetServiceMap(
	ctx context.Context, since time.Duration, sampleCount int,
) (*ServiceMap, error) {
	if since <= 0 {
		since = 15 * time.Minute
	}
	if sampleCount <= 0 || sampleCount > 500 {
		sampleCount = 200
	}

	tr, err := s.conn.Query(ctx, `
		SELECT trace_id FROM spans
		WHERE time >= now() - toIntervalSecond(?)
		GROUP BY trace_id
		ORDER BY count() DESC
		LIMIT ?
		SETTINGS max_execution_time = 30`,
		int64(since.Seconds()), sampleCount)
	if err != nil {
		return nil, err
	}
	var traceIDs []string
	for tr.Next() {
		var id string
		if err := tr.Scan(&id); err != nil {
			tr.Close()
			return nil, err
		}
		traceIDs = append(traceIDs, id)
	}
	tr.Close()
	if len(traceIDs) == 0 {
		return &ServiceMap{}, nil
	}

	holders := make([]string, len(traceIDs))
	args := make([]any, len(traceIDs))
	for i, id := range traceIDs {
		holders[i] = "?"
		args[i] = id
	}

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT trace_id, span_id, parent_id, service_name, status_code
		FROM spans
		WHERE trace_id IN (%s)
		SETTINGS max_execution_time = 30`, strings.Join(holders, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type spanInfo struct {
		parent string
		svc    string
		errSp  bool
	}
	byTrace := map[string]map[string]spanInfo{}
	nodeSpan := map[string]int{}
	nodeErr  := map[string]int{}
	totalSpans := 0
	for rows.Next() {
		var traceID, spanID, parentID, svc, statusCode string
		if err := rows.Scan(&traceID, &spanID, &parentID, &svc, &statusCode); err != nil {
			return nil, err
		}
		isErr := statusCode == "STATUS_CODE_ERROR" || statusCode == "ERROR" || statusCode == "Error"
		m, ok := byTrace[traceID]
		if !ok {
			m = map[string]spanInfo{}
			byTrace[traceID] = m
		}
		m[spanID] = spanInfo{parent: parentID, svc: svc, errSp: isErr}
		nodeSpan[svc]++
		if isErr {
			nodeErr[svc]++
		}
		totalSpans++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type edgeKey struct{ caller, callee string }
	edgeSpan := map[edgeKey]int{}
	edgeErr  := map[edgeKey]int{}
	edgeTraces := map[edgeKey]map[string]struct{}{}

	for traceID, spans := range byTrace {
		for _, sp := range spans {
			parent, ok := spans[sp.parent]
			if !ok || parent.svc == sp.svc {
				continue
			}
			k := edgeKey{caller: parent.svc, callee: sp.svc}
			edgeSpan[k]++
			if sp.errSp {
				edgeErr[k]++
			}
			if edgeTraces[k] == nil {
				edgeTraces[k] = map[string]struct{}{}
			}
			edgeTraces[k][traceID] = struct{}{}
		}
	}

	out := &ServiceMap{
		Nodes:       make([]ServiceMapNode, 0, len(nodeSpan)),
		Edges:       make([]ServiceMapEdge, 0, len(edgeSpan)),
		SampledFrom: len(traceIDs),
		TotalSpans:  totalSpans,
	}
	for svc, n := range nodeSpan {
		rate := 0.0
		if n > 0 {
			rate = float64(nodeErr[svc]) / float64(n)
		}
		out.Nodes = append(out.Nodes, ServiceMapNode{
			Service: svc, SpanCount: n, ErrorRate: rate,
		})
	}
	for k, n := range edgeSpan {
		out.Edges = append(out.Edges, ServiceMapEdge{
			Caller:     k.caller,
			Callee:     k.callee,
			SpanCount:  n,
			ErrorCount: edgeErr[k],
			TraceCount: len(edgeTraces[k]),
		})
	}
	// Stable order so the frontend layout doesn't jitter between
	// 30s polls when nodes are tied.
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].Service < out.Nodes[j].Service })
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].Caller != out.Edges[j].Caller {
			return out.Edges[i].Caller < out.Edges[j].Caller
		}
		return out.Edges[i].Callee < out.Edges[j].Callee
	})
	return out, nil
}
