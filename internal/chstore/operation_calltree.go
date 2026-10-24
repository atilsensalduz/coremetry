package chstore

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Operasyon kartı — trace yarısı (v0.10.1491, plan-operation-card §2.3.1 R3).
//
// SORU. Geliştirici "benim operasyonum neyi çağırıyor, hangisi yavaş/hatalı"
// diye soruyor. /endpoint sayfasının "Where the time goes" bölümü
// (endpoints_downstream.go) bunun SÜRE payını veriyor ama düz: alt çağrının
// OPERASYONU (çocuk rota / rpc metodu / db işlemi), derinlik ve örnek trace
// yok. Kart aynı örneklem şablonunu (EndpointWhereTheTimeGoes: ≤200 trace,
// iki sınırlı ham geçiş) kullanıp bir ÇAĞRI AĞACI kuruyor: derinlik ≤3,
// satır = sınır geçişi (başka servis / DB / kuyruk / dış HTTP·gRPC).
//
// NEDEN MV DEĞİL. topology_op_edges_5m yalnız servis→servis, span ADI
// bazlı ve yalnız çağrı SAYISI taşıyor (süre, hata, DB/kuyruk hedefi yok);
// rota × alt çağrı × hedef kardinalitesi MV'ye eklenmesi endpoints_downstream
// başlığında maliyetlendirilip reddedildi. MV yine KULLANILIR: pencere
// geneli servis→servis çağrı sayısı (OperationWindowEdges) örneklem
// sayısının yanında bilgi sütunu olarak.
//
// DEĞER YOK (§2.5). SELECT listelerinde db_statement, events, http.url /
// http.target / url.full, mesaj gövdesi ve ham attr_values dizisi YOK —
// yalnız adlandırılmış öznitelikler (rota, rpc metodu, db işlemi + tablo,
// kuyruk hedefi, peer host) ve promoted iş kodları. Span ADI istemci
// tarafında bazen tam URL ya da SQL taşır: opNameSafe sorgu dizesini,
// şema+host'u ve çok kelimeli SQL'i keser (operation_calltree_test.go
// metin + davranış pinleri).
//
// ÖRNEKLEM, AÇIKÇA. Yüzdelikler örneklem üzerinden (yakın-sıra, pctOf);
// payload SampledTraces taşır ve UI "örneklem · N trace" der — Drain
// dürüstlük kuralı.

// opCardDepthCap — ağaç derinliği tavanı (giriş = 0, ilk sınır geçişi = 1).
const opCardDepthCap = 3

// opCardRowCap — satır tavanı; kalanlar Others sayısında.
const opCardRowCap = 25

// opCardWalkLimit — ikinci geçişin satır tavanı (≤200 trace × ortalama
// ≤300 span; aşan trace'ler kısmi kalır, ağaç yine kurulur).
const opCardWalkLimit = 60000

// opCardSampleIDsCap — satır başına örnek trace kimliği: en yavaş, bir
// hatalı, medyan.
const opCardSampleIDsCap = 3

// opCardCodesPerKind — iş kodu türü başına en çok kaç kod.
const opCardCodesPerKind = 5

// opCardOperationKeys — operasyon KODUNU taşıyan span öznitelikleri
// (api/explain_trace_operation.go traceOperationSpanKeys ile aynı sıra).
var opCardOperationKeys = []string{"operation.code", "OPERATION_CODE", "operation_code"}

var functionCodeShapeRe = regexp.MustCompile(FunctionCodeShape)

// OpCallTreeQuery — kartın kimliği: /endpoint ailesinin (servis + rota ya
// da span adı) kimliği, aynı env/cluster/örneklem kapsamıyla.
type OpCallTreeQuery struct {
	EndpointDetailQuery
	// Entry — EntryHTTP (""): http_route kimliği; EntryRPC: span ADI
	// (RPC & Messaging girişi, http_route tanım gereği boş).
	Entry EntryKind
}

// OpCallNode — giriş operasyonunun kendi örneklem sayıları.
type OpCallNode struct {
	Service string  `json:"service"`
	Op      string  `json:"op"`
	Kind    string  `json:"kind"`
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	ErrPct  float64 `json:"errPct"`
	P50Ms   float64 `json:"p50Ms"`
	P95Ms   float64 `json:"p95Ms"`
	MaxMs   float64 `json:"maxMs"`
}

// OpCallEdge — ağacın bir satırı (bir sınır geçişi).
type OpCallEdge struct {
	Depth int `json:"depth"`
	// ParentKey — üst satırın Key'i; derinlik 1'de "entry".
	ParentKey string `json:"parentKey"`
	// Key — yol anahtarı (üst anahtar + yerel anahtar): aynı hedef iki
	// farklı üst altında iki ayrı satırdır.
	Key string `json:"key"`
	// Kind — service | db | messaging | external.
	Kind string `json:"kind"`
	// Protocol — http | grpc | db | kafka | messaging | "".
	Protocol string `json:"protocol"`
	// Target — servis adı | db:<sys> | queue:<sys>[:<dest>] | ext:<host>.
	Target string `json:"target"`
	// Op — çocuk rota / rpc metodu / db işlemi (+tablo) / kuyruk span adı.
	// db_statement ASLA.
	Op             string   `json:"op"`
	Calls          uint64   `json:"calls"`
	Errors         uint64   `json:"errors"`
	ErrPct         float64  `json:"errPct"`
	P50Ms          float64  `json:"p50Ms"`
	P95Ms          float64  `json:"p95Ms"`
	MaxMs          float64  `json:"maxMs"`
	SampleTraceIDs []string `json:"sampleTraceIds"`
}

// OpBizCode — giriş span'lerindeki iş kodu (FUNCTION_CODE / CHANNEL_CODE).
type OpBizCode struct {
	Kind  string `json:"kind"` // function_code | channel_code
	Code  string `json:"code"`
	Count uint64 `json:"count"`
}

// OpWindowEdge — topology_op_edges_5m: pencere geneli servis→servis çağrı.
type OpWindowEdge struct {
	ChildService string `json:"childService"`
	ChildOp      string `json:"childOp"`
	Calls        uint64 `json:"calls"`
}

// OpCallTree — kartın trace yarısı.
type OpCallTree struct {
	Entry         OpCallNode   `json:"entry"`
	OperationCode string       `json:"operationCode"`
	Version       string       `json:"version"`
	Children      []OpCallEdge `json:"children"`
	Others        int          `json:"others"`
	Codes         []OpBizCode  `json:"codes"`
	SampledTraces int          `json:"sampledTraces"`
	// Sampled — örneklem tavanı doldu (pencerede daha çok giriş span'i olabilir).
	Sampled     bool   `json:"sampled"`
	SampleOrder string `json:"sampleOrder"` // slow | random
	// Truncated — ikinci geçiş satır tavanına (opCardWalkLimit) değdi.
	Truncated bool `json:"truncated"`
	// WindowEdges — API katmanı doldurur (ayrı okuma, bölüm toleransı).
	WindowEdges []OpWindowEdge `json:"windowEdges"`
}

// opEntryRow — Q1 satırı: örneklenen giriş span'i.
type opEntryRow struct {
	traceID, spanID, name, kind string
	durMs                       float64
	err                         bool
}

// opWalkSpan — Q2 satırı. Yalnız adlandırılmış alanlar; değer taşıyan
// kolon yok.
type opWalkSpan struct {
	traceID, sid, pid, svc, name, kind string
	httpMethod, httpRoute              string
	rpcSystem, rpcService, rpcMethod   string
	dbSystem, dbOp, dbTable            string
	msgSystem, msgDest, peer           string
	fc, cc, opCode, ver                string
	durMs                              float64
	err                                bool
}

// opCardWhere — SAF: Q1 kapsamı. HTTP girişte endpointDetailWhere ile
// birebir aynı konjonktlar (aynı satırın aynı sorusu); RPC girişte rota
// yerine span ADI (endpoints.go EntryRPC duruşu).
func opCardWhere(q OpCallTreeQuery, clusterExpr string) whereClause {
	wc := whereClause{}
	wc.add("time >= ?", q.From)
	wc.add("time <= ?", q.To)
	wc.add("service_name = ?", q.Service)
	wc.add("kind NOT IN ('client', 'producer')")
	if q.Entry == EntryRPC {
		if q.BySignature {
			wc.add(opSigWrap("name")+" = ?", append(opSigArgs(), q.Path)...)
		} else {
			wc.add("name = ?", q.Path)
		}
	} else {
		endpointRoutePred(&wc, q.Path, q.BySignature)
	}
	if q.Env != "" {
		wc.add("deploy_env = ?", q.Env)
	}
	if q.Cluster != "" {
		wc.add(clusterExpr+" = ?", q.Cluster)
	}
	return wc
}

// opCardSampleSQL — SAF: Q1. Yalnız ucuz kolonlar (dizi kolonu YOK):
// ORDER BY … LIMIT öncesi her eşleşen satır okunur, attr dizileri burada
// pahalı olurdu. İş kodları / sürüm Q2'de giriş span'lerinden okunur.
func opCardSampleSQL(where, order, settings string) string {
	return `
		SELECT trace_id, span_id, name, kind, duration / 1e6 AS dur_ms,
		       status_code = 'error' AS is_err
		FROM spans ` + where + `
		ORDER BY ` + order + `
		LIMIT ?
		SETTINGS max_execution_time = 10,
		         ` + settings
}

// opCardWalkSQL — SAF: Q2 (walkEndpointEdges ikizi + adlandırılmış
// öznitelikler). İlk `?` sürüm ifadesinin servis kapısı, sonra trace id
// listesi, sonra zaman sınırları, sonra LIMIT.
func opCardWalkSQL(nIDs int, settings string) string {
	holders := make([]string, nIDs)
	for i := range holders {
		holders[i] = "?"
	}
	return `
		SELECT trace_id, span_id, parent_id, service_name, name, kind,
		       duration / 1e6 AS dur_ms,
		       status_code = 'error' AS is_err,
		       http_method, http_route, rpc_system, rpc_method,
		       attr_values[indexOf(attr_keys, 'rpc.service')] AS rpc_service,
		       db_system,
		       ` + promotedAttrExpr([]string{"db.operation", "db.operation.name"}) + ` AS db_op,
		       ` + promotedAttrExpr([]string{"db.sql.table", "db.collection.name"}) + ` AS db_table,
		       msg_system,
		       ` + promotedAttrExpr([]string{"messaging.destination.name", "messaging.destination"}) + ` AS msg_dest,
		       coalesce(nullIf(peer_service, ''),
		                nullIf(attr_values[indexOf(attr_keys, 'server.address')], ''),
		                nullIf(attr_values[indexOf(attr_keys, 'net.peer.name')], ''),
		                nullIf(attr_values[indexOf(attr_keys, 'http.host')], ''), '') AS peer,
		       ` + promotedAttrExpr([]string{"FUNCTION_CODE", "function_code"}) + ` AS fc,
		       ` + promotedAttrExpr([]string{"CHANNEL_CODE", "channel_code"}) + ` AS cc,
		       ` + promotedAttrExpr(opCardOperationKeys) + ` AS op_code,
		       if(service_name = ?, ` + effectiveVersionExpr + `, '') AS ver
		FROM spans
		WHERE trace_id IN (` + strings.Join(holders, ",") + `)
		  AND time >= ? AND time <= ?
		LIMIT ?
		SETTINGS max_execution_time = 10,
		         ` + settings
}

// opCardWindowSQL — SAF: Q3, MV-first (topology_op_edges_5m, düz DateTime
// kova → toDateTime(?, 'UTC') sarmalı, ListOpsForService emsali).
const opCardWindowSQL = `
		SELECT child_service, child_op, sum(calls) AS calls
		FROM topology_op_edges_5m FINAL
		WHERE time_bucket >= toStartOfFiveMinute(toDateTime(?, 'UTC'))
		  AND time_bucket <  toStartOfFiveMinute(toDateTime(?, 'UTC')) + INTERVAL 5 MINUTE
		  AND parent_service = ? AND parent_op = ?
		GROUP BY child_service, child_op
		ORDER BY calls DESC, child_service, child_op
		LIMIT 20
		SETTINGS max_execution_time = 5`

// opCardCallersSQL — v0.10.1509 (kartın "Bu metodu kim kullanıyor?"
// sıralama ipucu): pencere geneli bu operasyonu ÇAĞIRAN servisler, aynı MV
// (topology_op_edges_5m; ORDER BY time_bucket önde — çocuk tarafı süzgeci
// ebeveyn tarafıyla aynı maliyette), aynı sınırlar. Yalnız servis ADI döner.
const opCardCallersSQL = `
		SELECT parent_service, sum(calls) AS calls
		FROM topology_op_edges_5m FINAL
		WHERE time_bucket >= toStartOfFiveMinute(toDateTime(?, 'UTC'))
		  AND time_bucket <  toStartOfFiveMinute(toDateTime(?, 'UTC')) + INTERVAL 5 MINUTE
		  AND child_service = ? AND child_op = ? AND parent_service != child_service
		GROUP BY parent_service
		ORDER BY calls DESC, parent_service
		LIMIT 10
		SETTINGS max_execution_time = 5`

// OperationWindowCallers — pencere geneli bu operasyonu çağıran servisler
// (çağrı sayısına göre, ≤10). op = giriş span adı (MV span adıyla tutulur).
func (s *Store) OperationWindowCallers(ctx context.Context, service, op string, from, to time.Time) ([]string, error) {
	out := []string{}
	if service == "" || op == "" {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, opCardCallersSQL, from.Unix(), to.Unix(), service, op)
	if err != nil {
		return nil, fmt.Errorf("operation card callers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		var calls uint64
		if serr := rows.Scan(&svc, &calls); serr != nil {
			return nil, serr
		}
		out = append(out, svc)
	}
	return out, rows.Err()
}

// OperationCallTree — örneklem + yürüyüş + katlama. Örneklem boşsa boş
// (nil değil) bir ağaç döner; UI "bu pencerede çağrı yok" der.
func (s *Store) OperationCallTree(ctx context.Context, q OpCallTreeQuery) (*OpCallTree, error) {
	settings := s.shardSkipSetting() + s.heavyScanSpill()
	entries, err := s.sampleOpCardEntries(ctx, q, settings)
	if err != nil {
		return nil, err
	}

	ids := uniqueTraceIDs(entries)
	var spans []opWalkSpan
	if len(ids) > 0 {
		args := make([]any, 0, len(ids)+4)
		args = append(args, q.Service)
		for _, id := range ids {
			args = append(args, id)
		}
		args = append(args, q.From.Add(-traceFetchPad), q.To.Add(traceFetchPad), opCardWalkLimit)
		wr, werr := s.conn.Query(ctx, opCardWalkSQL(len(ids), settings), args...)
		if werr != nil {
			return nil, fmt.Errorf("operation card walk: %w", werr)
		}
		defer wr.Close()
		for wr.Next() {
			var w opWalkSpan
			var isErr uint8
			if serr := wr.Scan(&w.traceID, &w.sid, &w.pid, &w.svc, &w.name, &w.kind, &w.durMs, &isErr,
				&w.httpMethod, &w.httpRoute, &w.rpcSystem, &w.rpcMethod, &w.rpcService,
				&w.dbSystem, &w.dbOp, &w.dbTable, &w.msgSystem, &w.msgDest, &w.peer,
				&w.fc, &w.cc, &w.opCode, &w.ver); serr != nil {
				return nil, serr
			}
			w.err = isErr != 0
			spans = append(spans, w)
		}
		if rerr := wr.Err(); rerr != nil {
			return nil, fmt.Errorf("operation card walk: %w", rerr)
		}
	}
	tree := foldOpCallTree(q.Service, entries, spans)
	tree.Sampled = len(entries) >= endpointDownstreamSample
	tree.Truncated = len(spans) >= opCardWalkLimit
	tree.SampleOrder = "slow"
	if q.Sample == EndpointSampleRandom {
		tree.SampleOrder = EndpointSampleRandom
	}
	return tree, nil
}

// sampleOpCardEntries — Q1: örneklenen giriş span'leri.
func (s *Store) sampleOpCardEntries(ctx context.Context, q OpCallTreeQuery, settings string) ([]opEntryRow, error) {
	wc := opCardWhere(q, s.clusterExpr())
	rows, err := s.conn.Query(ctx, opCardSampleSQL(wc.sql(), endpointSampleOrder(q.Sample), settings),
		append(append([]any{}, wc.args...), endpointDownstreamSample)...)
	if err != nil {
		return nil, fmt.Errorf("operation card sample: %w", err)
	}
	defer rows.Close()
	var entries []opEntryRow
	for rows.Next() {
		var e opEntryRow
		var isErr uint8
		if serr := rows.Scan(&e.traceID, &e.spanID, &e.name, &e.kind, &e.durMs, &isErr); serr != nil {
			return nil, serr
		}
		e.err = isErr != 0
		entries = append(entries, e)
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, fmt.Errorf("operation card sample: %w", rerr)
	}
	return entries, nil
}

// OperationWindowEdges — pencere geneli servis→servis çağrı sayısı
// (MV). parentOp span ADIdır (rota şekli değil); çağıran şekil kipinde
// bunu çağırmaz.
func (s *Store) OperationWindowEdges(ctx context.Context, service, parentOp string, from, to time.Time) ([]OpWindowEdge, error) {
	out := []OpWindowEdge{}
	if service == "" || parentOp == "" {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, opCardWindowSQL, from.Unix(), to.Unix(), service, parentOp)
	if err != nil {
		return nil, fmt.Errorf("operation card window edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e OpWindowEdge
		if serr := rows.Scan(&e.ChildService, &e.ChildOp, &e.Calls); serr != nil {
			return nil, serr
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// uniqueTraceIDs — SAF: örneklem sırasını koruyarak tekil trace kimlikleri.
func uniqueTraceIDs(entries []opEntryRow) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, e := range entries {
		if _, dup := seen[e.traceID]; dup || e.traceID == "" {
			continue
		}
		seen[e.traceID] = struct{}{}
		out = append(out, e.traceID)
	}
	return out
}

// opSample — bir satırın tek gözlemi (yüzdelik + örnek trace seçimi).
type opSample struct {
	durMs   float64
	err     bool
	traceID string
}

type opRowAcc struct {
	edge    OpCallEdge
	samples []opSample
}

// foldOpCallTree — SAF (tablo testli): giriş span'lerinden aşağı yürüyüş.
//
// Kurallar:
//   - Aynı servisin iç (internal/server, db/kuyruk değil) span'leri GEÇİŞTİR:
//     satır olmaz, derinlik artmaz — çocukları aynı bağlamla yürünür.
//   - Aynı servisin CLIENT/PRODUCER span'i (ya da db/kuyruk span'i) bir
//     SINIR: altında başka servisin span'i varsa satır = o servis (op =
//     çocuk sunucu span'inin rotası/adı; süre ve hata ÇAĞIRANIN gördüğü,
//     yani istemci span'inden), yoksa yaprak satır (db / kuyruk / dış).
//     İstemci span'i aynı servisin başka istemci span'ini sarıyorsa
//     (enstrümantasyon sarmalayıcısı) geçiştir — çift sayım yok.
//   - İstemci span'i olmadan doğrudan başka servisin span'i → servis
//     satırı, o span'in kendi süresi/hatasıyla.
//   - Servis satırının altı o servisin bağlamında yürünür (derinlik+1);
//     derinlik opCardDepthCap'te durur.
//   - Başka bir örneklenmiş giriş span'ine inilirse durulur: o kendi
//     yürüyüşünde sayılır (aynı trace'te tekrarlanan rota).
//   - Aynı (üst, tür, hedef, op) satırları birleşir.
func foldOpCallTree(service string, entries []opEntryRow, spans []opWalkSpan) *OpCallTree {
	tree := &OpCallTree{
		Children:    []OpCallEdge{},
		Codes:       []OpBizCode{},
		WindowEdges: []OpWindowEdge{},
	}
	tree.SampledTraces = len(uniqueTraceIDs(entries))

	entrySet := make(map[string]opEntryRow, len(entries))
	var entrySamples []float64
	names, kinds := map[string]int{}, map[string]int{}
	for _, e := range entries {
		entrySet[e.spanID] = e
		entrySamples = append(entrySamples, e.durMs)
		tree.Entry.Calls++
		if e.err {
			tree.Entry.Errors++
		}
		names[e.name]++
		kinds[e.kind]++
	}
	tree.Entry.Service = service
	tree.Entry.Op = majority(names)
	tree.Entry.Kind = majority(kinds)
	if tree.Entry.Calls > 0 {
		tree.Entry.ErrPct = round2(float64(tree.Entry.Errors) * 100 / float64(tree.Entry.Calls))
	}
	tree.Entry.P50Ms = pctOf(entrySamples, 0.50)
	tree.Entry.P95Ms = pctOf(entrySamples, 0.95)
	tree.Entry.MaxMs = pctOf(entrySamples, 1)

	byID := make(map[string]*opWalkSpan, len(spans))
	children := map[string][]*opWalkSpan{}
	for i := range spans {
		w := &spans[i]
		if _, dup := byID[w.sid]; dup || w.sid == "" {
			continue
		}
		byID[w.sid] = w
	}
	for _, w := range byID {
		if w.pid != "" {
			children[w.pid] = append(children[w.pid], w)
		}
	}
	for pid := range children {
		c := children[pid]
		sort.Slice(c, func(i, j int) bool { return c[i].sid < c[j].sid })
	}

	// İş kodları / op kodu / sürüm — yalnız giriş span'lerinden.
	fcs, ccs, opCodes, vers := map[string]uint64{}, map[string]uint64{}, map[string]int{}, map[string]int{}
	for sid := range entrySet {
		w := byID[sid]
		if w == nil {
			continue
		}
		if w.fc != "" && functionCodeShapeRe.MatchString(w.fc) {
			fcs[w.fc]++
		}
		if w.cc != "" {
			ccs[w.cc]++
		}
		if w.opCode != "" {
			opCodes[w.opCode]++
		}
		if w.ver != "" {
			vers[w.ver]++
		}
	}
	tree.Codes = append(topCodes("function_code", fcs), topCodes("channel_code", ccs)...)
	tree.OperationCode = majority(opCodes)
	tree.Version = majority(vers)

	acc := map[string]*opRowAcc{}
	add := func(depth int, parentKey, kind, protocol, target, op string, s opSample) string {
		local := kind + ":" + target + "|" + op
		key := local
		if parentKey != "entry" {
			key = parentKey + ">" + local
		}
		a := acc[key]
		if a == nil {
			a = &opRowAcc{edge: OpCallEdge{
				Depth: depth, ParentKey: parentKey, Key: key,
				Kind: kind, Protocol: protocol, Target: target, Op: op,
			}}
			acc[key] = a
		}
		a.edge.Calls++
		if s.err {
			a.edge.Errors++
		}
		a.samples = append(a.samples, s)
		return key
	}

	var walk func(sid string, depth int, parentKey, svc, traceID string, guard int)
	walk = func(sid string, depth int, parentKey, svc, traceID string, guard int) {
		if guard > 256 {
			return
		}
		for _, c := range children[sid] {
			if _, isEntry := entrySet[c.sid]; isEntry {
				continue // kendi yürüyüşünde sayılır
			}
			switch {
			case c.svc != svc:
				// İstemci span'i olmadan doğrudan başka servis.
				if depth > opCardDepthCap {
					continue
				}
				k := add(depth, parentKey, "service", spanProtocol(*c), c.svc, serverOpName(*c),
					opSample{c.durMs, c.err, traceID})
				walk(c.sid, depth+1, k, c.svc, traceID, guard+1)
			case isBoundarySpan(*c):
				if wrapsSameServiceClient(c, children[c.sid]) {
					walk(c.sid, depth, parentKey, svc, traceID, guard+1)
					continue
				}
				if depth > opCardDepthCap {
					continue
				}
				cross := crossServiceChildren(c, children[c.sid])
				if len(cross) == 0 {
					kind, protocol, target, op := leafIdentity(*c)
					add(depth, parentKey, kind, protocol, target, op, opSample{c.durMs, c.err, traceID})
					continue
				}
				if c.msgSystem != "" {
					// Kuyruk: önce hedef satırı, tüketiciler onun altında.
					kind, protocol, target, op := leafIdentity(*c)
					qk := add(depth, parentKey, kind, protocol, target, op, opSample{c.durMs, c.err, traceID})
					for _, x := range cross {
						if depth+1 > opCardDepthCap {
							break
						}
						xk := add(depth+1, qk, "service", protocol, x.svc, serverOpName(*x),
							opSample{x.durMs, x.err, traceID})
						walk(x.sid, depth+2, xk, x.svc, traceID, guard+1)
					}
					continue
				}
				protocol := spanProtocol(*c)
				for _, x := range cross {
					k := add(depth, parentKey, "service", protocol, x.svc, serverOpName(*x),
						opSample{c.durMs, c.err || x.err, traceID})
					walk(x.sid, depth+1, k, x.svc, traceID, guard+1)
				}
			default:
				walk(c.sid, depth, parentKey, svc, traceID, guard+1) // geçiş
			}
		}
	}
	// Deterministik sıra: giriş span'leri kimliğe göre.
	entryIDs := make([]string, 0, len(entrySet))
	for sid := range entrySet {
		entryIDs = append(entryIDs, sid)
	}
	sort.Strings(entryIDs)
	for _, sid := range entryIDs {
		e := entrySet[sid]
		walk(sid, 1, "entry", service, e.traceID, 0)
	}

	tree.Children, tree.Others = finishOpRows(acc)
	return tree
}

// finishOpRows — SAF: birikimleri satıra çevirir, ≤opCardRowCap satır
// seçer (üstü seçilmeyen satır alınmaz) ve ağaç sırasında (üst, sonra
// çocukları çağrı sayısına göre) döker.
func finishOpRows(acc map[string]*opRowAcc) ([]OpCallEdge, int) {
	all := make([]*opRowAcc, 0, len(acc))
	for _, a := range acc {
		e := &a.edge
		if e.Calls > 0 {
			e.ErrPct = round2(float64(e.Errors) * 100 / float64(e.Calls))
		}
		durs := make([]float64, len(a.samples))
		for i, s := range a.samples {
			durs[i] = s.durMs
		}
		e.P50Ms = pctOf(durs, 0.50)
		e.P95Ms = pctOf(durs, 0.95)
		e.MaxMs = pctOf(durs, 1)
		e.SampleTraceIDs = pickSampleTraces(a.samples)
		all = append(all, a)
	}
	// Seçim: önce sığ, sonra çok çağrılan; üstü seçilmeyen alınmaz.
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i].edge, all[j].edge
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Calls != b.Calls {
			return a.Calls > b.Calls
		}
		return a.Key < b.Key
	})
	kept := map[string]bool{"entry": true}
	var picked []OpCallEdge
	others := 0
	for _, a := range all {
		if len(picked) >= opCardRowCap || !kept[a.edge.ParentKey] {
			others++
			continue
		}
		kept[a.edge.Key] = true
		picked = append(picked, a.edge)
	}
	// Ağaç sırası.
	byParent := map[string][]OpCallEdge{}
	for _, e := range picked {
		byParent[e.ParentKey] = append(byParent[e.ParentKey], e)
	}
	out := make([]OpCallEdge, 0, len(picked))
	var emit func(parent string)
	emit = func(parent string) {
		for _, e := range byParent[parent] { // byParent zaten (calls desc, key) sıralı
			out = append(out, e)
			emit(e.Key)
		}
	}
	emit("entry")
	return out, others
}

// pickSampleTraces — SAF: en yavaş, en yavaş hatalı, medyan; tekil, ≤3.
func pickSampleTraces(samples []opSample) []string {
	if len(samples) == 0 {
		return []string{}
	}
	s := append([]opSample(nil), samples...)
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].durMs != s[j].durMs {
			return s[i].durMs > s[j].durMs
		}
		return s[i].traceID < s[j].traceID
	})
	out := make([]string, 0, opCardSampleIDsCap)
	seen := map[string]bool{}
	push := func(id string) {
		if id != "" && !seen[id] && len(out) < opCardSampleIDsCap {
			seen[id] = true
			out = append(out, id)
		}
	}
	push(s[0].traceID)
	for _, x := range s {
		if x.err {
			push(x.traceID)
			break
		}
	}
	push(s[len(s)/2].traceID)
	return out
}

// topCodes — SAF: sayıma göre en çok opCardCodesPerKind kod.
func topCodes(kind string, m map[string]uint64) []OpBizCode {
	out := make([]OpBizCode, 0, len(m))
	for c, n := range m {
		out = append(out, OpBizCode{Kind: kind, Code: c, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	if len(out) > opCardCodesPerKind {
		out = out[:opCardCodesPerKind]
	}
	return out
}

// majority — SAF: en sık değer (eşitlikte sözlük sırası); "" sayılmaz.
func majority(m map[string]int) string {
	best, bestN := "", 0
	for k, n := range m {
		if k == "" {
			continue
		}
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// isBoundarySpan — SAF: servisin DIŞINA çıkan span (istemci / üretici /
// db / kuyruk).
func isBoundarySpan(w opWalkSpan) bool {
	return w.kind == "client" || w.kind == "producer" || w.dbSystem != "" || w.msgSystem != ""
}

// wrapsSameServiceClient — SAF: istemci span'i aynı servisin başka bir
// sınır span'ini sarıyor mu (ör. Spring istemci span'i + içte OkHttp).
func wrapsSameServiceClient(c *opWalkSpan, kids []*opWalkSpan) bool {
	for _, k := range kids {
		if k.svc == c.svc && isBoundarySpan(*k) {
			return true
		}
	}
	return false
}

// crossServiceChildren — SAF: sınır span'inin başka servisteki çocukları.
func crossServiceChildren(c *opWalkSpan, kids []*opWalkSpan) []*opWalkSpan {
	var out []*opWalkSpan
	for _, k := range kids {
		if k.svc != c.svc {
			out = append(out, k)
		}
	}
	return out
}

// spanProtocol — SAF: istemci span'inin protokolü.
func spanProtocol(w opWalkSpan) string {
	switch {
	case w.dbSystem != "":
		return "db"
	case strings.EqualFold(w.msgSystem, "kafka"):
		return "kafka"
	case w.msgSystem != "":
		return "messaging"
	case strings.EqualFold(w.rpcSystem, "grpc"):
		return "grpc"
	case w.httpMethod != "" || w.httpRoute != "":
		return "http"
	}
	return ""
}

// serverOpName — SAF: hedef servisin operasyonu: rota > rpc metodu > ad.
func serverOpName(w opWalkSpan) string {
	switch {
	case w.httpRoute != "":
		return strings.TrimSpace(w.httpMethod + " " + w.httpRoute)
	case w.rpcMethod != "":
		if w.rpcService != "" {
			return w.rpcService + "/" + w.rpcMethod
		}
		return w.rpcMethod
	}
	return opNameSafe(w.name)
}

// leafIdentity — SAF: yaprak sınır span'inin (tür, protokol, hedef, op).
// Hedef dili topology_edges_5m ile aynı: db:<sys>, queue:<sys>[:<dest>],
// ext:<host>.
func leafIdentity(w opWalkSpan) (kind, protocol, target, op string) {
	protocol = spanProtocol(w)
	switch {
	case w.dbSystem != "":
		op = strings.TrimSpace(strings.ToUpper(w.dbOp) + " " + w.dbTable)
		if op == "" {
			op = dbVerbOnly(w.name)
		}
		return "db", protocol, "db:" + strings.ToLower(w.dbSystem), op
	case w.msgSystem != "":
		target = "queue:" + strings.ToLower(w.msgSystem)
		if w.msgDest != "" {
			target += ":" + w.msgDest
		}
		return "messaging", protocol, target, opNameSafe(w.name)
	}
	host := w.peer
	if host == "" {
		host = "unknown"
	}
	switch {
	case w.rpcMethod != "":
		op = w.rpcMethod
		if w.rpcService != "" {
			op = w.rpcService + "/" + w.rpcMethod
		}
	case w.httpRoute != "":
		op = strings.TrimSpace(w.httpMethod + " " + w.httpRoute)
	default:
		op = opNameSafe(w.name)
	}
	return "external", protocol, "ext:" + host, op
}

// opNameSafeMax — span adı kırpma sınırı (rune).
const opNameSafeMax = 120

// opNameSafe — SAF: span adından DEĞER taşıyabilecek kısımları atar:
// sorgu dizesi (?…), şema+host (https://h/…) ve kırpma. İstemci span
// adına tam URL yazan enstrümantasyonlar var; http.url zaten okunmuyor,
// ad da onun kopyası olmasın.
func opNameSafe(name string) string {
	s := strings.TrimSpace(name)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+3:]
		prefix := ""
		if sp := strings.LastIndex(s[:i], " "); sp >= 0 {
			prefix = s[:sp+1] // "GET https://…" → "GET "
		}
		if j := strings.Index(rest, "/"); j >= 0 {
			s = prefix + rest[j:]
		} else {
			s = strings.TrimSpace(prefix)
		}
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > opNameSafeMax {
		r := []rune(s)
		s = string(r[:opNameSafeMax]) + "…"
	}
	return s
}

// dbVerbOnly — SAF: db.operation özniteliği yokken span adından yalnız
// işlem fiili. Semconv adı "SELECT orders" (iki kelime) ise aynen; daha
// uzunsa ad büyük olasılıkla SQL METNİDİR → yalnız ilk kelime (değer yok).
func dbVerbOnly(name string) string {
	f := strings.Fields(name)
	switch {
	case len(f) == 0:
		return ""
	case len(f) <= 2 && !strings.ContainsAny(name, "'\"=()"):
		return opNameSafe(strings.Join(f, " "))
	}
	return strings.ToUpper(opNameSafe(f[0]))
}
