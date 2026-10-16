package chstore

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// "Where the time goes" for one endpoint (v0.9.311, brief N4).
//
// THE QUESTION IT ANSWERS. The /endpoints drawer could say a route's
// p99 is 900ms; it could not say where those 900ms went. The nearest
// existing surface, DependencyStrip, is SERVICE-level and carries no
// duration share — it says "this endpoint's service touches postgres,
// redis and payments-api", never "600 of this route's 900ms are in
// postgres".
//
// WHY A QUERY AND NOT AN MV. No materialized view carries
// route→downstream: topology_op_edges_5m is keyed on span NAME and
// holds only calls (no duration, no errors), service_callers_5m has no
// route dimension, and db_*_summary_5m is keyed on service_name.
// Adding a route dimension to the topology writer was costed and
// rejected — route × downstream cardinality on top of a 180s exec with
// a 4GB join. So this is a sampled read, and it says so on screen.
//
// THE SHAPE. Two bounded passes over raw spans, the neighbors.go
// pattern narrowed to a route:
//
//  1. sample ≤N route-scoped trace_ids using the PK prefix
//     (service_name, time) plus http_route;
//  2. ONE pass over those ids collecting the edges, from which the
//     downstream side falls out of child spans and the caller side out
//     of parent spans — two tabs, one sample.
//
// SAMPLING BIAS, STATED. neighbors.go orders by count() DESC, which
// leans toward structurally fat traces. That is the wrong lean for a
// latency question, so this samples by DURATION: the slow tail is
// exactly the population the operator opened the drawer about. It is
// still a sample, and the payload carries the count so the UI can say
// so — the Drain-templating honesty rule.

// endpointDownstreamSample bounds how many traces one answer walks.
// 200 keeps the second pass's IN list inside the parser budget the
// v0.8.363 catch established (~35 bytes per id) with room to spare,
// and a latency SHARE stabilises long before 200 samples.
const endpointDownstreamSample = 200

// EndpointEdge is one downstream dependency or one caller of a route.
type EndpointEdge struct {
	// Name — the downstream service / db_system / peer, or the calling
	// service. "self" is reserved for the entry span's own time.
	Name string `json:"name"`
	// Kind — "service" | "db" | "messaging" | "self", so the UI can
	// pick an icon without re-deriving it from the name.
	Kind  string `json:"kind"`
	Calls uint64 `json:"calls"`
	// AvgMs / P99Ms over the SAMPLED spans, not the window.
	AvgMs  float64 `json:"avgMs"`
	P99Ms  float64 `json:"p99Ms"`
	Errors uint64  `json:"errors"`
	// ShareMs — total milliseconds this edge accounts for across the
	// sample. The UI draws the share bar from it.
	ShareMs float64 `json:"shareMs"`
}

// EndpointDownstream is the payload behind the drawer's two tabs.
type EndpointDownstream struct {
	Downstream []EndpointEdge `json:"downstream"`
	Callers    []EndpointEdge `json:"callers"`
	// SampledFrom — how many traces the numbers above were derived
	// from. Zero means the window held no matching trace; the UI says
	// that rather than drawing an empty chart.
	SampledFrom int `json:"sampledFrom"`
	// TotalMs — the sampled entry spans' total duration, i.e. the
	// denominator of every share. Carried so the UI never re-derives it
	// from a rounded sum.
	TotalMs float64 `json:"totalMs"`
	// Backends (v0.9.311) — database / cache / broker time found at ANY
	// depth beneath the route, NOT just in its direct children.
	//
	// This exists because direct children alone answer the question too
	// shallowly. Verified on live data: a gateway route's 659ms had one
	// direct child (account-service, 645ms) whose OWN child was a 623ms
	// Oracle query. "645ms in account-service" is true and nearly
	// useless; "623ms of it in Oracle" is the answer the operator came
	// for — and it is the brief's own motivating example.
	//
	// Kept OUT of the Downstream share arithmetic on purpose: that
	// Oracle time is INSIDE account-service's 645ms, so listing it as a
	// sibling share would double-count the same milliseconds. Nested
	// breakdown, nested label.
	Backends []EndpointEdge `json:"backends"`
}

// EndpointWhereTheTimeGoes samples this route's traces and splits their
// time across downstream edges (plus the route's own self time), and
// collects who calls it.
func (s *Store) EndpointWhereTheTimeGoes(
	ctx context.Context, q EndpointDetailQuery,
) (*EndpointDownstream, error) {
	ids, entryTotalMs, entryIDs, err := s.sampleEndpointTraces(ctx, q)
	if err != nil {
		return nil, err
	}
	out := &EndpointDownstream{
		Downstream:  []EndpointEdge{},
		Callers:     []EndpointEdge{},
		Backends:    []EndpointEdge{},
		SampledFrom: len(ids),
		TotalMs:     entryTotalMs,
	}
	if len(ids) == 0 {
		return out, nil
	}
	return s.walkEndpointEdges(ctx, q, ids, entryIDs, out)
}

// endpointSampleOrder — SAF: örneklem sırası. random → trace kimliğinin
// hash'i (pencereden deterministik, tarafsız örnek; aynı pencere aynı sonuç
// → önbellek tutarlı), aksi en yavaş önce.
func endpointSampleOrder(sample string) string {
	if sample == EndpointSampleRandom {
		return "cityHash64(trace_id)"
	}
	return "duration DESC"
}

// EndpointSampleRandom — Sample değeri (v0.10.1228).
const EndpointSampleRandom = "random"

// sampleEndpointTraces picks the route's SLOWEST traces in the window.
//
// Ordering by duration rather than by span count (the neighbors.go
// choice) is deliberate: this panel exists to explain latency, so the
// sample must represent the tail the operator is looking at. Sampling
// the fattest traces instead would answer a structural question with a
// latency chart.
func (s *Store) sampleEndpointTraces(
	ctx context.Context, q EndpointDetailQuery,
) (traceIDs []string, totalMs float64, entrySpanIDs map[string]float64, err error) {
	wc := s.endpointDetailWhere(q)
	rows, qerr := s.conn.Query(ctx, `
		SELECT trace_id, span_id, duration / 1e6 AS dur_ms
		FROM spans `+wc.sql()+`
		ORDER BY `+endpointSampleOrder(q.Sample)+`
		LIMIT ?
		SETTINGS max_execution_time = 10,
		         `+s.shardSkipSetting()+s.heavyScanSpill(),
		append(append([]any{}, wc.args...), endpointDownstreamSample)...)
	if qerr != nil {
		return nil, 0, nil, fmt.Errorf("endpoint downstream sample: %w", qerr)
	}
	defer rows.Close()
	entrySpanIDs = map[string]float64{} // span_id → süre (ms); self trace başına bundan
	seen := map[string]struct{}{}
	for rows.Next() {
		var tid, sid string
		var dur float64
		if serr := rows.Scan(&tid, &sid, &dur); serr != nil {
			return nil, 0, nil, serr
		}
		entrySpanIDs[sid] = dur
		totalMs += dur
		if _, dup := seen[tid]; dup {
			// One trace can hit the same route twice (a retry, a loop).
			// Its entry spans both count toward the denominator, but the
			// id is fetched once.
			continue
		}
		seen[tid] = struct{}{}
		traceIDs = append(traceIDs, tid)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, 0, nil, fmt.Errorf("endpoint downstream sample: %w", rerr)
	}
	return traceIDs, totalMs, entrySpanIDs, nil
}

// walkEndpointEdges runs the SECOND bounded pass and folds it into the
// two tabs.
//
// The time predicate is mandatory, not cosmetic: v0.9.231 found that
// the same shape WITHOUT it ran bloom-filter analysis across every
// daily partition in retention. The window is padded by traceFetchPad
// so a trace that started just before the window still resolves whole.
func (s *Store) walkEndpointEdges(
	ctx context.Context, q EndpointDetailQuery,
	traceIDs []string, entrySpanIDs map[string]float64,
	out *EndpointDownstream,
) (*EndpointDownstream, error) {
	holders := make([]string, len(traceIDs))
	args := make([]any, 0, len(traceIDs)+2)
	for i, id := range traceIDs {
		holders[i] = "?"
		args = append(args, id)
	}
	args = append(args, q.From.Add(-traceFetchPad), q.To.Add(traceFetchPad))

	rows, err := s.conn.Query(ctx, fmt.Sprintf(`
		SELECT span_id, parent_id, service_name, db_system, kind,
		       duration / 1e6 AS dur_ms,
		       status_code = 'error' AS is_err,
		       coalesce(nullIf(peer_service, ''),
		                nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''),
		                nullIf(attr_values[indexOf(attr_keys, 'net.peer.name')], ''),
		                nullIf(attr_values[indexOf(attr_keys, 'http.host')], ''), '') AS peer,
		       attr_values[indexOf(attr_keys, 'rpc.service')] AS rpc_service,
		       msg_system
		FROM spans
		WHERE trace_id IN (%s)
		  AND time >= ? AND time <= ?
		SETTINGS max_execution_time = 10,
		         `+s.shardSkipSetting()+s.heavyScanSpill(),
		strings.Join(holders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("endpoint downstream walk: %w", err)
	}
	defer rows.Close()
	var spans []walkSpan
	for rows.Next() {
		var w walkSpan
		var isErr uint8
		if serr := rows.Scan(&w.sid, &w.pid, &w.svc, &w.dbSys, &w.kind, &w.durMs, &isErr, &w.peer, &w.rpcSvc, &w.msgSys); serr != nil {
			return nil, serr
		}
		w.err = isErr != 0
		spans = append(spans, w)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("endpoint downstream walk: %w", rerr)
	}
	down, callers, backends, _, selfDurs := foldEndpointEdges(q.Service, spans, entrySpanIDs)

	// self = the route's own time minus what its edges accounted
	// for, PER route span. Parallel children overlap, so the subtraction
	// can go negative; clamp at zero.
	// v0.10.1198 (operatör-bildirimi: "self neden 0ms") — self artık route
	// span'i BAŞINA (süre − altındaki kenarlar, ≥0): tail p99 bu diziden.
	// Eskiden tek bir toplamdı ve dizi boştu → p99 her zaman 0 ms.
	var selfSum float64
	for _, d := range selfDurs {
		selfSum += d
	}
	if selfSum > 0 {
		down["self"] = &edgeAcc{kind: "self", calls: uint64(len(selfDurs)), sumMs: selfSum, durs: selfDurs}
	}

	out.Downstream = finishEdges(down)
	out.Callers = finishEdges(callers)
	out.Backends = finishEdges(backends)
	return out, nil
}

// walkSpan — ikinci geçişin bir span'i.
type walkSpan struct {
	sid, pid, svc, dbSys, kind, peer, rpcSvc, msgSys string
	durMs                                            float64
	err                                              bool
}

// isExitSpan — SAF: route servisinin DIŞINA çıkan span mı (DB / broker /
// client çağrısı ya da başka servisin span'i). Aynı servisin iç span'leri
// çıkış değildir — zamanları route'un kendi zamanı (self) sayılır.
func isExitSpan(w walkSpan, routeService string) bool {
	return w.dbSys != "" || w.msgSys != "" || w.kind == "client" || w.kind == "producer" || w.svc != routeService
}

// edgeName — SAF: çıkış span'inin kenar adı + türü. Aynı servisten dış
// çağrı (S3 / HTTP) servis adıyla DEĞİL hedefiyle adlanır (v0.10.1196,
// operatör: "Amazon S3 entegrasyonu IP bilgisiyle gösterilebilir mi").
func edgeName(w walkSpan, routeService string) (string, string) {
	switch {
	case w.dbSys != "":
		return w.dbSys, "db"
	case w.msgSys != "":
		return w.msgSys, "messaging"
	case w.svc != routeService:
		return w.svc, "service"
	}
	name := w.peer
	if w.rpcSvc != "" {
		name = w.rpcSvc
		if w.peer != "" {
			name += " (" + w.peer + ")"
		}
	}
	if name == "" {
		name = "external (adres yok)"
	}
	return name, "external"
}

// foldEndpointEdges — SAF (v0.10.1196, operatör-bildirimi): kenarlar =
// route span'inin altında, aradaki her span route servisinin İÇ span'i
// olan İLK çıkış span'leri (iç sarmalayıcı span'ler düzleşir — eskiden
// "cm-get-service service" satırı S3 çağrısını ve iç işi yutuyordu).
// backends = YALNIZ aşağı akış kenarlarının İÇİNDEKİ (derinlik ≥2) DB /
// broker zamanı: eskiden trace'in TÜM span'leri taranıyordu (route'un
// altında olmayan kardeş dallar → uygulamada olmayan db2/couchbase/mssql)
// ve doğrudan DB çağrısı iki listede birden çıkıyordu (redis çift).
func foldEndpointEdges(routeService string, spans []walkSpan, entry map[string]float64) (down, callers, backends map[string]*edgeAcc, childMs float64, selfDurs []float64) {
	down, callers, backends = map[string]*edgeAcc{}, map[string]*edgeAcc{}, map[string]*edgeAcc{}
	byID := make(map[string]*walkSpan, len(spans))
	for i := range spans {
		byID[spans[i].sid] = &spans[i]
	}
	// pathState: 0 = route altında değil, 1 = route altında + yol iç span'lerden,
	// 2 = route altında + yolda bir çıkış kenarı var (aşağı akış içi).
	memo := map[string]int{}
	entryOf := map[string]string{} // span → altında olduğu route span'i
	var state func(sid string, depth int) int
	state = func(sid string, depth int) int {
		if v, ok := memo[sid]; ok {
			return v
		}
		w := byID[sid]
		if w == nil || depth > 256 {
			return 0
		}
		var v int
		if _, isEntry := entry[w.pid]; isEntry {
			v = 1
			entryOf[sid] = w.pid
		} else if p := byID[w.pid]; p != nil {
			switch ps := state(p.sid, depth+1); {
			case ps == 0:
				v = 0
			case ps == 2 || isExitSpan(*p, routeService):
				v = 2
			default:
				v = 1
			}
			if v != 0 {
				entryOf[sid] = entryOf[p.sid]
			}
		}
		memo[sid] = v
		return v
	}
	edgeMsByEntry := map[string]float64{}
	add := func(m map[string]*edgeAcc, name, kind string, ms float64, isErr bool) {
		if name == "" {
			return
		}
		a := m[name]
		if a == nil {
			a = &edgeAcc{kind: kind}
			m[name] = a
		}
		a.calls++
		a.sumMs += ms
		a.durs = append(a.durs, ms)
		if isErr {
			a.errs++
		}
	}
	for i := range spans {
		w := spans[i]
		if _, isEntry := entry[w.sid]; isEntry {
			continue // route'un kendi span'i paydadır, kenar değil
		}
		switch st := state(w.sid, 0); {
		case st == 1 && isExitSpan(w, routeService):
			name, k := edgeName(w, routeService)
			add(down, name, k, w.durMs, w.err)
			childMs += w.durMs
			edgeMsByEntry[entryOf[w.sid]] += w.durMs
		case st == 2 && (w.dbSys != "" || w.msgSys != ""):
			name := w.dbSys
			if name == "" {
				name = w.msgSys
			}
			add(backends, name, "db", w.durMs, w.err)
		case st == 0 && w.pid == "":
			add(callers, w.svc, "service", w.durMs, w.err)
		}
	}
	for sid, dur := range entry {
		self := dur - edgeMsByEntry[sid]
		if self < 0 {
			self = 0 // paralel çağrılar route süresini aşabilir
		}
		selfDurs = append(selfDurs, self)
	}
	sort.Float64s(selfDurs)
	return down, callers, backends, childMs, selfDurs
}

// edgeAcc accumulates one edge's sampled spans.
type edgeAcc struct {
	kind  string
	calls uint64
	errs  uint64
	sumMs float64
	durs  []float64
}

// finishEdges turns the accumulators into rows ordered by the share
// they account for — the answer to "where did the time go" reads top
// down. p99 is computed over the SAMPLE, so it is the sample's p99 and
// the payload's SampledFrom is what lets the UI say so.
func finishEdges(m map[string]*edgeAcc) []EndpointEdge {
	out := make([]EndpointEdge, 0, len(m))
	for name, a := range m {
		e := EndpointEdge{
			Name: name, Kind: a.kind, Calls: a.calls,
			Errors: a.errs, ShareMs: a.sumMs,
		}
		if a.calls > 0 {
			e.AvgMs = a.sumMs / float64(a.calls)
		}
		e.P99Ms = pctOf(a.durs, 0.99)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ShareMs != out[j].ShareMs {
			return out[i].ShareMs > out[j].ShareMs
		}
		return out[i].Name < out[j].Name // stable for equal shares
	})
	return out
}

// pctOf is a nearest-rank percentile over a sample. Exact by
// construction (the sample is already in memory and small), so no
// tdigest and no interpolation to explain.
func pctOf(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	i := int(math.Ceil(p*float64(len(v)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(v) {
		i = len(v) - 1
	}
	return v[i]
}
