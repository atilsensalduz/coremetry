package chstore

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1491 — operasyon kartı trace yarısı (plan-operation-card §2.3.1 R3).
// foldOpCallTree SAF: ağaç kuralları (sınır geçişi = satır, iç span geçiş,
// derinlik ≤3, aynı hedef birleşir, hata %, örneklem p95, örnek trace
// seçimi, ≤25 satır + others, iş kodu kapısı) burada tabloyla pinli.
// Sorgu metinleri SAF kurucular: değer kolonu yok + maliyet sınırları.

// sp — test span'i kurucusu.
func ocSp(trace, sid, pid, svc, kind, name string, dur float64) opWalkSpan {
	return opWalkSpan{traceID: trace, sid: sid, pid: pid, svc: svc, kind: kind, name: name, durMs: dur}
}

func ocEntry(trace, sid string, dur float64, err bool) opEntryRow {
	return opEntryRow{traceID: trace, spanID: sid, name: "POST /orders/{id}/approve", kind: "server", durMs: dur, err: err}
}

// rowSig — satırın test için özeti: derinlik · tür · hedef · op · çağrı.
func rowSig(e OpCallEdge) string {
	return fmt.Sprintf("%d %s %s %s n=%d", e.Depth, e.Kind, e.Target, e.Op, e.Calls)
}

func rowSigs(rows []OpCallEdge) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = rowSig(r)
	}
	return out
}

// orderTrace — çok seviyeli örnek: giriş → iç span → HTTP istemci →
// payments-api sunucu → dış HTTP (bank-gw); giriş → Oracle SELECT;
// giriş → Kafka üretici (tüketici yok).
func orderTrace(tid string, payMs, dbMs float64, payErr bool) []opWalkSpan {
	e := ocSp(tid, tid+"-e", "", "orders-api", "server", "POST /orders/{id}/approve", 300)
	in := ocSp(tid, tid+"-in", tid+"-e", "orders-api", "internal", "OrderService.approve", 280)
	cl := ocSp(tid, tid+"-cl", tid+"-in", "orders-api", "client", "POST", payMs)
	cl.httpMethod = "POST"
	cl.peer = "payments-api"
	cl.err = payErr
	ps := ocSp(tid, tid+"-ps", tid+"-cl", "payments-api", "server", "POST /payments/authorize", payMs-2)
	ps.httpMethod, ps.httpRoute = "POST", "/payments/authorize"
	ext := ocSp(tid, tid+"-x", tid+"-ps", "payments-api", "client", "POST https://bank-gw.example.test/v1/auth?card=4111", 50)
	ext.httpMethod, ext.peer = "POST", "bank-gw.example.test"
	db := ocSp(tid, tid+"-db", tid+"-in", "orders-api", "client", "SELECT orders", dbMs)
	db.dbSystem, db.dbOp, db.dbTable = "oracle", "select", "orders"
	kp := ocSp(tid, tid+"-k", tid+"-e", "orders-api", "producer", "order-events publish", 3)
	kp.msgSystem, kp.msgDest = "kafka", "order-events"
	return []opWalkSpan{e, in, cl, ps, ext, db, kp}
}

func TestFoldOpCallTreeMultiLevel(t *testing.T) {
	spans := append(orderTrace("t1", 120, 10, false), orderTrace("t2", 80, 12, true)...)
	tree := foldOpCallTree("orders-api",
		[]opEntryRow{ocEntry("t1", "t1-e", 300, false), ocEntry("t2", "t2-e", 200, true)}, spans)

	want := []string{
		"1 db db:oracle SELECT orders n=2",
		"1 messaging queue:kafka:order-events order-events publish n=2",
		"1 service payments-api POST /payments/authorize n=2",
		"2 external ext:bank-gw.example.test POST /v1/auth n=2",
	}
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, want) {
		t.Fatalf("ağaç:\n got %q\nwant %q", got, want)
	}
	pay := tree.Children[2]
	if pay.Protocol != "http" || pay.Kind != "service" || pay.ParentKey != "entry" {
		t.Fatalf("payments satırı: %+v", pay)
	}
	// Süre ÇAĞIRANIN gördüğü (istemci span'i), hata istemci span'inden.
	if pay.MaxMs != 120 || pay.Errors != 1 || pay.ErrPct != 50 {
		t.Fatalf("payments sayıları: max=%v err=%d errPct=%v", pay.MaxMs, pay.Errors, pay.ErrPct)
	}
	ext := tree.Children[3]
	if ext.ParentKey != pay.Key || ext.Key == "ext" || !strings.HasPrefix(ext.Key, pay.Key+">") {
		t.Fatalf("derinlik-2 satırın üstü payments olmalı: %+v", ext)
	}
	if tree.Children[1].Protocol != "kafka" || tree.Children[0].Protocol != "db" {
		t.Fatalf("protokoller: %+v", tree.Children)
	}
	if tree.Entry.Calls != 2 || tree.Entry.Errors != 1 || tree.Entry.ErrPct != 50 || tree.Entry.MaxMs != 300 {
		t.Fatalf("giriş: %+v", tree.Entry)
	}
	if tree.Entry.Op != "POST /orders/{id}/approve" || tree.SampledTraces != 2 || tree.Others != 0 {
		t.Fatalf("giriş kimliği / örneklem: %+v", tree)
	}
}

// Aynı hedef aynı üst altında birleşir — trace içinde iki kez de, iki
// trace'te de; farklı üst altındaki aynı hedef AYRI satırdır.
func TestFoldOpCallTreeMergesSameTargets(t *testing.T) {
	e := ocSp("t", "e", "", "orders-api", "server", "GET /orders", 100)
	mk := func(sid, pid, svc string) opWalkSpan {
		w := ocSp("t", sid, pid, svc, "client", "SELECT orders", 5)
		w.dbSystem, w.dbOp, w.dbTable = "postgresql", "SELECT", "orders"
		return w
	}
	cl := ocSp("t", "cl", "e", "orders-api", "client", "GET", 40)
	cl.httpMethod = "GET"
	inv := ocSp("t", "inv", "cl", "inventory-api", "server", "GET /stock", 30)
	inv.httpMethod, inv.httpRoute = "GET", "/stock"
	spans := []opWalkSpan{e, mk("d1", "e", "orders-api"), mk("d2", "e", "orders-api"), cl, inv, mk("d3", "inv", "inventory-api")}
	tree := foldOpCallTree("orders-api", []opEntryRow{ocEntry("t", "e", 100, false)}, spans)
	want := []string{
		"1 db db:postgresql SELECT orders n=2",
		"1 service inventory-api GET /stock n=1",
		"2 db db:postgresql SELECT orders n=1",
	}
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, want) {
		t.Fatalf("birleşme:\n got %q\nwant %q", got, want)
	}
}

// Derinlik tavanı: A→B→C→D→E zincirinde yalnız 1..3.
func TestFoldOpCallTreeDepthCap(t *testing.T) {
	svcs := []string{"a", "b", "c", "d", "e"}
	var spans []opWalkSpan
	prev := ""
	for i, s := range svcs {
		kind := "server"
		sid := fmt.Sprintf("s%d", i)
		w := ocSp("t", sid, prev, s, kind, "GET /"+s, float64(100-i*10))
		w.httpMethod, w.httpRoute = "GET", "/"+s
		spans = append(spans, w)
		prev = sid
	}
	tree := foldOpCallTree("a", []opEntryRow{{traceID: "t", spanID: "s0", name: "GET /a", kind: "server", durMs: 100}}, spans)
	want := []string{
		"1 service b GET /b n=1",
		"2 service c GET /c n=1",
		"3 service d GET /d n=1",
	}
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, want) {
		t.Fatalf("derinlik tavanı:\n got %q\nwant %q", got, want)
	}
	for _, r := range tree.Children {
		if r.Depth > opCardDepthCap {
			t.Fatalf("derinlik %d > %d", r.Depth, opCardDepthCap)
		}
	}
}

// Hata % ve örneklem yüzdelikleri (yakın-sıra): 1..20 ms, 2 hatalı →
// %10, p50 = 10, p95 = 19, max = 20. Örnek trace: en yavaş, en yavaş
// hatalı, medyan — tekil, ≤3.
func TestFoldOpCallTreeErrPctAndP95(t *testing.T) {
	var spans []opWalkSpan
	var entries []opEntryRow
	for i := 1; i <= 20; i++ {
		tid := fmt.Sprintf("t%02d", i)
		spans = append(spans, ocSp(tid, tid+"e", "", "orders-api", "server", "GET /x", 100))
		d := ocSp(tid, tid+"d", tid+"e", "orders-api", "client", "SELECT orders", float64(i))
		d.dbSystem, d.dbOp = "oracle", "SELECT"
		d.err = i == 3 || i == 7
		spans = append(spans, d)
		entries = append(entries, opEntryRow{traceID: tid, spanID: tid + "e", name: "GET /x", kind: "server", durMs: 100})
	}
	tree := foldOpCallTree("orders-api", entries, spans)
	if len(tree.Children) != 1 {
		t.Fatalf("tek db satırı bekleniyordu: %q", rowSigs(tree.Children))
	}
	r := tree.Children[0]
	if r.Calls != 20 || r.Errors != 2 || r.ErrPct != 10 {
		t.Fatalf("hata: calls=%d errors=%d errPct=%v", r.Calls, r.Errors, r.ErrPct)
	}
	if r.P50Ms != 10 || r.P95Ms != 19 || r.MaxMs != 20 {
		t.Fatalf("yüzdelik: p50=%v p95=%v max=%v", r.P50Ms, r.P95Ms, r.MaxMs)
	}
	if want := []string{"t20", "t07", "t10"}; !reflect.DeepEqual(r.SampleTraceIDs, want) {
		t.Fatalf("örnek trace'ler: got %v want %v", r.SampleTraceIDs, want)
	}
	if r.Op != "SELECT" || r.Target != "db:oracle" {
		t.Fatalf("db kimliği: %+v", r)
	}
}

// ≤25 satır; kalan others. Üstü kesilen çocuk alınmaz.
func TestFoldOpCallTreeRowCapAndOthers(t *testing.T) {
	spans := []opWalkSpan{ocSp("t", "e", "", "orders-api", "server", "GET /x", 500)}
	for i := 0; i < 30; i++ {
		d := ocSp("t", fmt.Sprintf("d%02d", i), "e", "orders-api", "client", "q", 1)
		d.dbSystem = fmt.Sprintf("db%02d", i)
		d.dbOp = "SELECT"
		spans = append(spans, d)
	}
	tree := foldOpCallTree("orders-api", []opEntryRow{ocEntry("t", "e", 500, false)}, spans)
	if len(tree.Children) != opCardRowCap || tree.Others != 30-opCardRowCap {
		t.Fatalf("tavan: rows=%d others=%d", len(tree.Children), tree.Others)
	}
}

// Enstrümantasyon sarmalayıcısı: istemci span'i aynı servisin başka
// istemci span'ini sarıyorsa tek satır (çift sayım yok). İstemci span'i
// olmadan doğrudan başka servis de bir satırdır.
func TestFoldOpCallTreeWrapperAndDirectCross(t *testing.T) {
	e := ocSp("t", "e", "", "orders-api", "server", "GET /x", 100)
	outer := ocSp("t", "o", "e", "orders-api", "client", "RestTemplate.exchange", 60)
	inner := ocSp("t", "i", "o", "orders-api", "client", "GET", 58)
	inner.httpMethod, inner.peer = "GET", "catalog.example.test"
	direct := ocSp("t", "dx", "e", "audit-api", "consumer", "audit process", 5)
	direct.msgSystem = ""
	tree := foldOpCallTree("orders-api", []opEntryRow{ocEntry("t", "e", 100, false)},
		[]opWalkSpan{e, outer, inner, direct})
	want := []string{
		"1 external ext:catalog.example.test GET n=1",
		"1 service audit-api audit process n=1",
	}
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, want) {
		t.Fatalf("sarmalayıcı / doğrudan:\n got %q\nwant %q", got, want)
	}
}

// Aynı trace'te tekrarlanan rota: alt giriş kendi yürüyüşünde sayılır,
// üst girişin altında yeniden sayılmaz.
func TestFoldOpCallTreeNestedEntryNotDoubleCounted(t *testing.T) {
	e1 := ocSp("t", "e1", "", "orders-api", "server", "GET /x", 100)
	e2 := ocSp("t", "e2", "e1", "orders-api", "server", "GET /x", 50)
	d := ocSp("t", "d", "e2", "orders-api", "client", "SELECT t", 5)
	d.dbSystem, d.dbOp = "oracle", "SELECT"
	tree := foldOpCallTree("orders-api",
		[]opEntryRow{ocEntry("t", "e1", 100, false), ocEntry("t", "e2", 50, false)}, []opWalkSpan{e1, e2, d})
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, []string{"1 db db:oracle SELECT n=1"}) {
		t.Fatalf("iç içe giriş çift sayıldı: %q", got)
	}
	if tree.SampledTraces != 1 || tree.Entry.Calls != 2 {
		t.Fatalf("örneklem: traces=%d calls=%d", tree.SampledTraces, tree.Entry.Calls)
	}
}

// Kafka üretici + aynı trace'te tüketici: kuyruk satırı, tüketici servis
// onun altında.
func TestFoldOpCallTreeQueueConsumerUnderQueue(t *testing.T) {
	e := ocSp("t", "e", "", "orders-api", "server", "POST /orders", 100)
	p := ocSp("t", "p", "e", "orders-api", "producer", "order-events publish", 2)
	p.msgSystem, p.msgDest = "kafka", "order-events"
	c := ocSp("t", "c", "p", "billing-api", "consumer", "order-events process", 30)
	c.msgSystem, c.msgDest = "kafka", "order-events"
	tree := foldOpCallTree("orders-api", []opEntryRow{ocEntry("t", "e", 100, false)}, []opWalkSpan{e, p, c})
	want := []string{
		"1 messaging queue:kafka:order-events order-events publish n=1",
		"2 service billing-api order-events process n=1",
	}
	if got := rowSigs(tree.Children); !reflect.DeepEqual(got, want) {
		t.Fatalf("kuyruk:\n got %q\nwant %q", got, want)
	}
}

// İş kodları yalnız giriş span'lerinden; FUNCTION_CODE biçim kapısı
// (FunctionCodeShape) — kod+ID karışımı alınmaz. Op kodu / sürüm çoğunluk.
func TestFoldOpCallTreeCodesAndMajority(t *testing.T) {
	var spans []opWalkSpan
	var entries []opEntryRow
	for i, fc := range []string{"ORD0042", "ORD0042", "ord-77-x", "PAY0001"} {
		tid := fmt.Sprintf("t%d", i)
		w := ocSp(tid, tid+"e", "", "orders-api", "server", "GET /x", 10)
		w.fc, w.cc = fc, "MOBILE"
		w.opCode = "ORDER_APPROVE"
		w.ver = "1.42.0"
		if i == 3 {
			w.opCode, w.ver, w.cc = "OTHER", "1.41.0", "WEB"
		}
		spans = append(spans, w)
		entries = append(entries, opEntryRow{traceID: tid, spanID: tid + "e", name: "GET /x", kind: "server", durMs: 10})
	}
	// Giriş olmayan span'in kodu sayılmaz.
	x := ocSp("t0", "t0x", "t0e", "orders-api", "internal", "inner", 1)
	x.fc = "ZZZ9999"
	spans = append(spans, x)
	tree := foldOpCallTree("orders-api", entries, spans)
	want := []OpBizCode{
		{Kind: "function_code", Code: "ORD0042", Count: 2},
		{Kind: "function_code", Code: "PAY0001", Count: 1},
		{Kind: "channel_code", Code: "MOBILE", Count: 3},
		{Kind: "channel_code", Code: "WEB", Count: 1},
	}
	if !reflect.DeepEqual(tree.Codes, want) {
		t.Fatalf("kodlar:\n got %+v\nwant %+v", tree.Codes, want)
	}
	if tree.OperationCode != "ORDER_APPROVE" || tree.Version != "1.42.0" {
		t.Fatalf("çoğunluk: op=%q ver=%q", tree.OperationCode, tree.Version)
	}
}

// Boş örneklem: nil değil boş dilimler (FE .map()'liyor).
func TestFoldOpCallTreeEmpty(t *testing.T) {
	tree := foldOpCallTree("orders-api", nil, nil)
	if tree.Children == nil || tree.Codes == nil || tree.WindowEdges == nil {
		t.Fatalf("boş dilim yerine nil: %+v", tree)
	}
	if tree.Entry.Calls != 0 || tree.SampledTraces != 0 {
		t.Fatalf("boş giriş: %+v", tree.Entry)
	}
}

// Span adı DEĞER taşımasın: sorgu dizesi, şema+host, çok kelimeli SQL.
func TestOpNameSafeStripsValues(t *testing.T) {
	cases := map[string]string{
		"POST https://bank-gw.example.test/v1/auth?card=4111": "POST /v1/auth",
		"https://h.example.test":                              "",
		"GET /orders/42?token=abc#frag":                       "GET /orders/42",
		"order-events publish":                                "order-events publish",
	}
	for in, want := range cases {
		if got := opNameSafe(in); got != want {
			t.Errorf("opNameSafe(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("x", 300)
	if got := opNameSafe(long); len([]rune(got)) != opNameSafeMax+1 {
		t.Errorf("kırpma: %d rune", len([]rune(got)))
	}
	db := map[string]string{
		"SELECT orders":                               "SELECT orders",
		"select * from orders where id = 5":           "SELECT",
		"UPDATE accounts SET balance = 10 WHERE id=1": "UPDATE",
	}
	for in, want := range db {
		if got := dbVerbOnly(in); got != want {
			t.Errorf("dbVerbOnly(%q) = %q, want %q", in, got, want)
		}
	}
	// db span'inin satır op'u SQL metnini asla taşımaz.
	w := opWalkSpan{dbSystem: "oracle", name: "SELECT * FROM cards WHERE pan = '4111111111111111'"}
	if _, _, _, op := leafIdentity(w); strings.Contains(op, "4111") || strings.Contains(op, "cards") {
		t.Fatalf("db op SQL metni sızdırdı: %q", op)
	}
}

// Redaksiyon muhafızı: sorgu metinlerinde değer kolonu yok; attr_values
// yalnız adlandırılmış anahtarla (indexOf) okunur.
func TestOpCallTreeSQLCarriesNoPayloadColumns(t *testing.T) {
	wc := opCardWhere(OpCallTreeQuery{EndpointDetailQuery: EndpointDetailQuery{Service: "s", Path: "/p"}}, "cluster")
	texts := map[string]string{
		"sample": opCardSampleSQL(wc.sql(), "duration DESC", "x"),
		"walk":   opCardWalkSQL(3, "x"),
		"window": opCardWindowSQL,
		// v0.10.1509 — çağıran servisler (yalnız ad + sayı).
		"callers": opCardCallersSQL,
	}
	for name, sql := range texts {
		low := strings.ToLower(sql)
		for _, bad := range []string{"db_statement", "events", "http.url", "http.target", "url.full", "url.query", "status_msg", "payload", "body"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s SQL değer kolonu taşıyor: %q", name, bad)
			}
		}
		for i := 0; ; {
			j := strings.Index(sql[i:], "attr_values")
			if j < 0 {
				break
			}
			i += j
			if !strings.HasPrefix(sql[i:], "attr_values[indexOf(attr_keys, '") {
				t.Errorf("%s SQL ham attr_values okuyor: %q", name, sql[i:min(len(sql), i+40)])
			}
			i += len("attr_values")
		}
		if strings.Contains(sql, "res_values,") || strings.Contains(sql, "attr_values,") {
			t.Errorf("%s SQL ham dizi seçiyor", name)
		}
	}
}

// Maliyet sınırları: zaman sınırı + LIMIT + max_execution_time her geçişte;
// yer tutucu sayısı bağ argümanlarıyla tutarlı.
func TestOpCallTreeSQLIsBounded(t *testing.T) {
	q := OpCallTreeQuery{EndpointDetailQuery: EndpointDetailQuery{
		Service: "orders-api", Path: "/orders/:id", BySignature: true,
		From: time.Unix(0, 0), To: time.Unix(3600, 0), Env: "prod", Cluster: "c1",
	}}
	wc := opCardWhere(q, "cluster")
	sample := opCardSampleSQL(wc.sql(), endpointSampleOrder(""), "spill")
	for _, g := range []string{"time >= ?", "time <= ?", "service_name = ?", "LIMIT ?", "max_execution_time = 10", "ORDER BY duration DESC", "deploy_env = ?", "cluster = ?"} {
		if !strings.Contains(sample, g) {
			t.Errorf("örneklem sorgusu %q taşımıyor", g)
		}
	}
	if n := strings.Count(sample, "?"); n != len(wc.args)+1 {
		t.Errorf("örneklem: %d yer tutucu, %d argüman", n, len(wc.args)+1)
	}
	walk := opCardWalkSQL(5, "spill")
	for _, g := range []string{"AND time >= ? AND time <= ?", "LIMIT ?", "max_execution_time = 10", "trace_id IN (?,?,?,?,?)"} {
		if !strings.Contains(walk, g) {
			t.Errorf("yürüyüş sorgusu %q taşımıyor", g)
		}
	}
	if n := strings.Count(walk, "?"); n != 1+5+3 {
		t.Errorf("yürüyüş: %d yer tutucu, 9 bekleniyordu", n)
	}
	for _, g := range []string{"toDateTime(?, 'UTC')", "LIMIT 20", "max_execution_time = 5", "FINAL", "parent_op = ?"} {
		if !strings.Contains(opCardWindowSQL, g) {
			t.Errorf("pencere sorgusu %q taşımıyor", g)
		}
	}
	// v0.10.1509 — çağıran servisler: aynı MV sınırları.
	for _, g := range []string{"toDateTime(?, 'UTC')", "LIMIT 10", "max_execution_time = 5", "FINAL", "child_service = ? AND child_op = ?"} {
		if !strings.Contains(opCardCallersSQL, g) {
			t.Errorf("çağıran sorgusu %q taşımıyor", g)
		}
	}
	if n := strings.Count(opCardCallersSQL, "?"); n != 4 {
		t.Errorf("çağıran sorgusu: %d yer tutucu, 4 bekleniyordu", n)
	}
	// RPC girişi rota yerine span ADI.
	rpc := opCardWhere(OpCallTreeQuery{EndpointDetailQuery: EndpointDetailQuery{Service: "s", Path: "orders.Approve"}, Entry: EntryRPC}, "cluster")
	if !strings.Contains(rpc.sql(), "name = ?") || strings.Contains(rpc.sql(), "http_route") {
		t.Errorf("rpc kapsamı: %s", rpc.sql())
	}
	http := opCardWhere(OpCallTreeQuery{EndpointDetailQuery: EndpointDetailQuery{Service: "s", Path: "/p"}}, "cluster")
	if !strings.Contains(http.sql(), "http_route = ?") || !strings.Contains(http.sql(), "kind NOT IN ('client', 'producer')") {
		t.Errorf("http kapsamı: %s", http.sql())
	}
}
