package api

// copilot_service_topology_test.go — v0.10.1388: service_topology /
// trace_topology rotası (TR/EN, operasyon anması, hop/hatalı kipi, ask/kapsam,
// çift/periyot/metrik grafiği negatifleri, ön-getirme vetosu), bütçe, metin,
// trace grafı, temsilci trace seçimi, linkler, takip çiplerinin yönlenmesi,
// önbellek anahtarı (tüm girdiler), blok sıralayıcısı.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
)

var (
	topoServices = []string{"payment-service", "checkout-service", "ledger-service"}
	topoEnvs     = []string{"prod", "uat"}
)

func TestRouteTopologyAsk(t *testing.T) {
	const tid = "0af7651916cd43dd8448eb211c80319c"
	cases := []struct {
		q, ctxSvc string
		intent    guidedIntent
		svc, op   string
		hops      int
		eonly     bool
		askIntent guidedIntent
		traceID   string
	}{
		{q: "payment-service topolojisi", intent: guidedServiceTopology, svc: "payment-service", hops: 1},
		{q: "payment-service kime bağlı", intent: guidedServiceTopology, svc: "payment-service", hops: 1},
		{q: "payment-service'i kimler çağırıyor", intent: guidedServiceTopology, svc: "payment-service", hops: 1},
		{q: "payment-service upstream ve downstream servisleri", intent: guidedServiceTopology, svc: "payment-service", hops: 1},
		{q: "what depends on payment-service", intent: guidedServiceTopology, svc: "payment-service", hops: 1},
		{q: "show me the service map for checkout-service", intent: guidedServiceTopology, svc: "checkout-service", hops: 1},
		{q: "checkout-service bağımlılıkları neler", intent: guidedServiceTopology, svc: "checkout-service", hops: 1},
		{q: "payment-service topolojisini 2 adım göster", intent: guidedServiceTopology, svc: "payment-service", hops: 2},
		{q: "payment-service topolojisini iki hop genişlet", intent: guidedServiceTopology, svc: "payment-service", hops: 2},
		{q: "payment-service hatalı bağımlılıkları?", intent: guidedServiceTopology, svc: "payment-service", hops: 1, eonly: true},
		// Açık topoloji sözcüğü metrik grafiğini yener ("çiz" + "hata" 5xx şablonu değil).
		{q: "payment-service topolojisini çiz, hatalı olanlar", intent: guidedServiceTopology, svc: "payment-service", hops: 1, eonly: true},
		// Örnek trace kipi.
		{q: "payment-service örnek trace topolojisi", intent: guidedTraceTopology, svc: "payment-service", hops: 1},
		{q: "payment-service'e ait örnek bir trace'in topolojisini çiz", intent: guidedTraceTopology, svc: "payment-service", hops: 1},
		{q: "payment-service hatalı örnek trace topolojisi?", intent: guidedTraceTopology, svc: "payment-service", hops: 1, eonly: true},
		// Operasyon adı (servis isteğe bağlı).
		{q: "PAYMENT_CREATE operasyonunun topolojisi", intent: guidedTraceTopology, op: "PAYMENT_CREATE", hops: 1},
		{q: "PAYMENT_CREATE'in örnek trace topolojisi", intent: guidedTraceTopology, op: "PAYMENT_CREATE", hops: 1},
		{q: "POST /api/payments topolojisi", intent: guidedTraceTopology, op: "POST /api/payments", hops: 1},
		{q: "charge operasyonunun örnek trace topolojisi", intent: guidedTraceTopology, op: "charge", hops: 1},
		{q: "payment-service'in PAYMENT_CREATE operasyonu topolojisi", intent: guidedTraceTopology, svc: "payment-service", op: "PAYMENT_CREATE", hops: 1},
		// Açık trace kimliği.
		{q: tid + " topolojisi", intent: guidedTraceTopology, traceID: tid},
		// Servis yok → ekran servisi / "hangisi?".
		{q: "topoloji", ctxSvc: "checkout-service", intent: guidedServiceTopology, svc: "checkout-service", hops: 1},
		{q: "servis topolojisini göster", intent: guidedAskService, askIntent: guidedServiceTopology, hops: 1},
		{q: "örnek trace topolojisi", intent: guidedAskService, askIntent: guidedTraceTopology, hops: 1},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, topoServices, topoEnvs, nil, c.ctxSvc)
		if r.Intent != c.intent || r.Service != c.svc || r.TopoOperation != c.op || r.TopoErrorsOnly != c.eonly ||
			r.AskIntent != c.askIntent || r.TopoTraceID != c.traceID || (c.hops > 0 && r.TopoHops != c.hops) {
			t.Errorf("%q → %+v", c.q, r)
		}
		if !hasGuidedSignal(normalizeGuidedMsg(c.q)) {
			t.Errorf("%q: sıfır-maliyet kapısı geçmeli", c.q)
		}
		if !topologyVetoesOpPrefetch(c.q) {
			t.Errorf("%q: operasyon ön-getirmesi topoloji sorusunu sahiplenmemeli", c.q)
		}
	}
	// Negatifler — topoloji sözcüğü yok ya da kendi yolu kazanır.
	neg := []struct {
		q      string
		intent guidedIntent
	}{
		{"checkout-service'dan payment-service'e giden istekler", guidedPairRequests},
		{"payment-service JVM heap grafiği", guidedMetricChart},
		{"payment-service'i çağıranların 5xx grafiği", guidedMetricChart}, // zayıf sözcük grafikten sonra
		{"payment-service sağlığı nasıl", guidedServiceHealth},
		{"takımımın servisleri nasıl", guidedMyServices},
	}
	for _, c := range neg {
		if r := routeGuidedIntent(c.q, topoServices, topoEnvs, nil, ""); r.Intent != c.intent {
			t.Errorf("%q → %s, want %s (%+v)", c.q, r.Intent, c.intent, r)
		}
	}
	// Topoloji sözcüğü olmayan operasyon sorusu bugünkü ön-getirme yolunda.
	for _, q := range []string{"PAYMENT_CREATE operasyonuna bak", "POST /api/payments neden yavaş", "charge operasyonu hata veriyor mu"} {
		if topologyVetoesOpPrefetch(q) {
			t.Errorf("%q: veto edilmemeli (operasyon ön-getirmesi)", q)
		}
	}
	// Çok aday → ask_service; çip aynı niyete (ve kipe) yönlenir.
	multi := []string{"payment-service", "payment-worker", "checkout-service"}
	for _, q := range []string{"payment topolojisi", "payment örnek trace topolojisi", "payment topolojisini 2 adım göster"} {
		ask := routeGuidedIntent(q, multi, topoEnvs, nil, "")
		if ask.Intent != guidedAskService || len(ask.ServiceOptions) < 2 {
			t.Fatalf("%q çok aday: %+v", q, ask)
		}
		for _, chip := range guidedSuggestions(ask) {
			r := routeGuidedIntent(chip, multi, topoEnvs, nil, "")
			if r.Intent != ask.AskIntent || r.Service == "" || topoRouteHops(r) != topoRouteHops(ask) {
				t.Errorf("ask çipi %q → %+v (ask %+v)", chip, r, ask)
			}
		}
		sc := applyChatScopeRoute(&chatScope{Services: []string{"payment-service"}}, ask)
		if sc.Intent != ask.AskIntent || sc.Service != "payment-service" || sc.TopoHops != ask.TopoHops {
			t.Errorf("%q kapsam: %+v", q, sc)
		}
	}
	// Ekrandaki trace: "bu trace'in topolojisi" trace_by_id'ye değil trace_topology'ye.
	ask := routeGuidedIntent("bu trace'in topolojisi", topoServices, topoEnvs, nil, "")
	if r, ok := topologyInheritTrace(ask, tid); !ok || r.Intent != guidedTraceTopology || r.TopoTraceID != tid {
		t.Errorf("ekran trace devri: %+v → %+v ok=%v", ask, r, ok)
	}
	if _, ok := topologyInheritTrace(guidedRoute{Intent: guidedServiceHealth}, tid); ok {
		t.Error("topoloji dışı rota devralınmamalı")
	}
}

func TestTopoHopsFrom(t *testing.T) {
	cases := map[string]int{
		"payment-service topolojisi":                 1,
		"payment-service topolojisi 2 adım":          2,
		"payment-service topolojisi iki seviye":      2,
		"payment-service topolojisi 3 hop":           2, // sohbet tavanı 2
		"payment-service topolojisini genişlet":      2,
		"payment-service topolojisini 1 adım göster": 1,
		"son 2 saat payment-service topolojisi":      1, // "2 saat" adım değil
		"payment-service 2-hop topology":             2,
	}
	for q, want := range cases {
		if got := topoHopsFrom(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("%q → %d, want %d", q, got, want)
		}
	}
}

func TestTopologyOpMention(t *testing.T) {
	cases := map[string]string{
		"PAYMENT_CREATE operasyonunun topolojisi":     "PAYMENT_CREATE",
		"`PAYMENT_CREATE` operasyonu topolojisi":      "PAYMENT_CREATE",
		"topology of operation PAYMENT_CREATE":        "PAYMENT_CREATE",
		"charge operasyonunun örnek trace topolojisi": "charge",
		"POST /api/payments topolojisi":               "POST /api/payments",
		"https://api.example/api/orders topolojisi":   "/api/orders",
		"payment-service operasyonlarının topolojisi": "", // servis adı operasyon değil
		"payment-service topolojisi":                  "",
		"bu operasyonun topolojisi":                   "", // kısa / durak sözcük
		"PROD operasyonu topolojisi":                  "", // ortam adı
	}
	for q, want := range cases {
		if got := topologyOpMention(q, topoServices, []string{"prod", "uat"}); got != want {
			t.Errorf("%q → %q, want %q", q, got, want)
		}
	}
}

// topoGraph — test grafı: odak payment-service; upstream checkout-service,
// downstream ledger-service (hatalı), dış stripe, postgres; 2. adım ledger → db.
func topoGraph() ServiceGraphResponse {
	edges := []chstore.ServiceTopologyEdge{
		{ParentService: "checkout-service", ChildNode: "payment-service", NodeKind: "service", Protocol: "http", Calls: 1200, Errors: 5, ErrorRate: 0.42, P99Ms: 320},
		{ParentService: "payment-service", ChildNode: "ledger-service", NodeKind: "service", Protocol: "grpc", Calls: 900, Errors: 55, ErrorRate: 6.11, P99Ms: 1200},
		{ParentService: "payment-service", ChildNode: "ext:api.stripe.example", NodeKind: "external", Protocol: "http", Calls: 300, ErrorRate: 0, P99Ms: 800},
		{ParentService: "payment-service", ChildNode: "db:postgresql", NodeKind: "db", Protocol: "db", Calls: 5000, Errors: 0, P99Ms: 12},
		{ParentService: "ledger-service", ChildNode: "db:postgresql", NodeKind: "db", Protocol: "db", Calls: 700, Errors: 0, P99Ms: 9},
	}
	return buildServiceGraphWith(edges, "payment-service", "neighborhood", 2, nil, 60, nil)
}

func TestTopoChatBudget(t *testing.T) {
	var edges []chstore.ServiceTopologyEdge
	for i := 0; i < 20; i++ {
		edges = append(edges, chstore.ServiceTopologyEdge{ParentService: "payment-service", ChildNode: "svc-" + string(rune('a'+i)), NodeKind: "service", Calls: uint64(100 + i)})
		edges = append(edges, chstore.ServiceTopologyEdge{ParentService: "svc-" + string(rune('a'+i)), ChildNode: "leaf-" + string(rune('a'+i)), NodeKind: "service", Calls: uint64(10000 + i)})
	}
	g := buildServiceGraphWith(edges, "payment-service", "neighborhood", 2, nil, 60, nil)
	out, tr := topoChatBudget(g, "payment-service", false, topoChatMaxNodes, topoChatMaxEdges)
	if len(out.Nodes) > topoChatMaxNodes || len(out.Edges) > topoChatMaxEdges {
		t.Fatalf("bütçe aşıldı: %d düğüm / %d kenar", len(out.Nodes), len(out.Edges))
	}
	hasFocus := false
	for _, n := range out.Nodes {
		if n.ID == "payment-service" {
			hasFocus = true
		}
	}
	if !hasFocus {
		t.Fatal("odak düğümü bütçede düşmemeli")
	}
	// Doğrudan kenarlar (daha az çağrılı olsalar da) 2. adım kenarlarından önce.
	for _, e := range out.Edges {
		if e.Source != "payment-service" {
			t.Errorf("14 doğrudan komşu doluyken 2. adım kenarı girmemeli: %+v", e)
		}
	}
	if out.Edges[0].Target != "svc-t" { // en çok çağrılan doğrudan kenar önce
		t.Errorf("sıra: %+v", out.Edges[0])
	}
	if tr.Nodes != 41-15 || tr.Edges != 40-len(out.Edges) {
		t.Errorf("kesim sayıları: %+v (kenar %d)", tr, len(out.Edges))
	}
	// eonly: hatasız kenarlar düşer (kesim sayılmaz).
	eo, etr := topoChatBudget(topoGraph(), "payment-service", true, topoChatMaxNodes, topoChatMaxEdges)
	if len(eo.Edges) != 2 || etr.Edges != 0 {
		t.Errorf("eonly: %+v %+v", eo.Edges, etr)
	}
	// Kenarsız graf: düğüm yok, kesim yok.
	if e, etr := topoChatBudget(ServiceGraphResponse{}, "payment-service", false, 15, 30); len(e.Nodes) != 0 || etr != (topoTruncation{}) {
		t.Errorf("boş graf: %+v %+v", e, etr)
	}
}

func TestRenderTopologyEvidenceTR(t *testing.T) {
	g, tr := topoChatBudget(topoGraph(), "payment-service", false, topoChatMaxNodes, topoChatMaxEdges)
	text := renderTopologyEvidenceTR("payment-service", g, 2, 3600, "prod", false, tr, len(g.Edges))
	for _, want := range []string{
		"payment-service topolojisi (son 1sa, 2 hop · 4 komşu, 5 kenar)",
		"ÇAĞIRANLAR (upstream):\n- checkout-service → 1.200 çağrı · hata %0.42 · p99 320 ms (http)",
		"BAĞIMLILIKLAR (downstream):",
		"- ledger-service → 900 çağrı · hata %6.11 ⚠ · p99 1.20 s (grpc)",
		"- api.stripe.example (dış) → 300 çağrı",
		"- postgresql (veritabanı) → 5.000 çağrı",
		"2. ADIM:\n- ledger-service → postgresql (veritabanı) → 700 çağrı",
		"ortam (prod) topolojide süzgeç DEĞİL",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metinde yok: %q\n%s", want, text)
		}
	}
	empty := renderTopologyEvidenceTR("payment-service", ServiceGraphResponse{}, 1, 3600, "", false, topoTruncation{}, 0)
	if !strings.Contains(empty, "topoloji kenarı bulunamadı") || !strings.Contains(empty, "trace-context") {
		t.Errorf("boş metin: %s", empty)
	}
	noErr := renderTopologyEvidenceTR("payment-service", ServiceGraphResponse{}, 1, 3600, "", true, topoTruncation{}, 4)
	if !strings.Contains(noErr, "hatalı bağımlılık YOK: 4 kenarın hiçbirinde") {
		t.Errorf("eonly boş metin: %s", noErr)
	}
	cut := renderTopologyEvidenceTR("payment-service", g, 1, 3600, "", false, topoTruncation{Nodes: 3, Edges: 7}, 12)
	if !strings.Contains(cut, "+3 düğüm, +7 kenar Topology sekmesinde") {
		t.Errorf("bütçe notu: %s", cut)
	}
}

func topoSpan(id, parent, svc, kind string, startMs, durMs int64, attrs map[string]string) chstore.SpanRow {
	return chstore.SpanRow{TraceID: "t1", SpanID: id, ParentSpanID: parent, ServiceName: svc, Kind: kind, Name: svc + "-op",
		StartTime: startMs * 1e6, EndTime: (startMs + durMs) * 1e6, DurationMs: float64(durMs), Attributes: attrs}
}

func TestTraceServiceGraph(t *testing.T) {
	spans := []chstore.SpanRow{
		topoSpan("a", "", "checkout-service", "server", 0, 1000, map[string]string{"http.request.method": "POST"}),
		topoSpan("b", "a", "checkout-service", "client", 10, 900, map[string]string{"http.request.method": "POST"}),
		topoSpan("c", "b", "payment-service", "server", 20, 880, nil),
		topoSpan("d", "c", "payment-service", "client", 30, 40, map[string]string{"db.system": "postgresql", "db.name": "payments"}),
		topoSpan("e", "c", "payment-service", "client", 80, 300, map[string]string{"server.address": "api.stripe.example", "http.response.status_code": "502"}),
		topoSpan("f", "c", "payment-service", "client", 400, 100, map[string]string{"rpc.system": "grpc"}),
		topoSpan("g", "f", "ledger-service", "server", 410, 80, nil),
		topoSpan("h", "c", "payment-service", "client", 510, 100, map[string]string{"rpc.system": "grpc"}),
		topoSpan("i", "h", "ledger-service", "server", 520, 60, nil),
	}
	spans[6].StatusCode = "error"
	edges := traceTopologyEdges(spans)
	got := map[string]chstore.ServiceTopologyEdge{}
	for _, e := range edges {
		got[e.ParentService+">"+e.ChildNode] = e
	}
	if e := got["checkout-service>payment-service"]; e.Calls != 1 || e.Protocol != "http" {
		t.Errorf("checkout → payment: %+v", e)
	}
	if e := got["payment-service>ledger-service"]; e.Calls != 2 || e.Errors != 1 || e.Protocol != "grpc" || e.P99Ms != 80 || e.AvgMs != 70 {
		t.Errorf("payment → ledger: %+v", e)
	}
	if e := got["payment-service>db:postgresql"]; e.Calls != 1 || e.NodeKind != "db" {
		t.Errorf("payment → db: %+v", e)
	}
	if e := got["payment-service>ext:api.stripe.example"]; e.Calls != 1 || e.Errors != 1 || e.NodeKind != "external" {
		t.Errorf("payment → stripe (502 hata): %+v", e)
	}
	if len(edges) != 4 {
		t.Errorf("çapraz servis çocuğu olan CLIENT dış kenar üretmemeli: %+v", edges)
	}
	f := topoTraceFactsFrom(spans, "payment-service", traceSourceTempo, nil)
	if f.Graph.Scope != "trace" || f.Graph.Focus != "payment-service" || f.SpanCount != 9 || f.Services != 3 || f.DurationMs != 1000 {
		t.Errorf("künye: %+v", f)
	}
	text := renderTraceTopologyTR("0af7651916cd43dd8448eb211c80319c", "PAYMENT_CREATE", f, "Seçim: x.")
	for _, want := range []string{
		"Örnek trace (PAYMENT_CREATE): 0af7651916cd · checkout-service-op · 1.00 s · 9 span · 3 servis (kaynak: Tempo)",
		"- payment-service → ledger-service  2 çağrı · 1 hata ⚠ · 140 ms toplam (grpc)",
		"- payment-service → api.stripe.example (dış)  1 çağrı · 1 hata ⚠ · 300 ms toplam",
		"TEK trace",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("trace metninde yok: %q\n%s", want, text)
		}
	}
	// Odak trace'te yoksa kök servis.
	if f2 := topoTraceFactsFrom(spans, "orders-api", traceSourceCH, nil); f2.Graph.Focus != "checkout-service" {
		t.Errorf("odak yedeği: %q", f2.Graph.Focus)
	}
	single := renderTraceTopologyTR("abc", "", topoTraceFactsFrom(spans[:1], "", traceSourceCH, nil), "")
	if !strings.Contains(single, "tek serviste kalıyor") {
		t.Errorf("tek servis: %s", single)
	}
}

func TestPickTopologyTrace(t *testing.T) {
	rows := []chstore.TraceRow{
		{TraceID: "a", SpanCount: 50, DurationMs: 900, StartTime: 1},
		{TraceID: "b", SpanCount: 10, DurationMs: 800, StartTime: 2},
		{TraceID: "c", SpanCount: 30, DurationMs: 700, StartTime: 3},
		{TraceID: "d", SpanCount: 99, DurationMs: 990, StartTime: 4, HasError: true},
		{TraceID: "e", SpanCount: 20, DurationMs: 600, StartTime: 5, HasError: true},
	}
	if p, ok := pickTopologyTrace(rows, false); !ok || p.TraceID != "c" { // hatasızlar 10/30/50 → medyan 30
		t.Errorf("medyan: %+v", p)
	}
	if p, ok := pickTopologyTrace(rows, true); !ok || p.TraceID != "d" {
		t.Errorf("en yavaş hatalı: %+v", p)
	}
	allErr := []chstore.TraceRow{{TraceID: "x", SpanCount: 5, HasError: true}, {TraceID: "y", SpanCount: 7, HasError: true}}
	if p, ok := pickTopologyTrace(allErr, false); !ok || p.TraceID != "x" { // hepsi hatalı → hepsinden alt medyan
		t.Errorf("hepsi hatalı: %+v", p)
	}
	if _, ok := pickTopologyTrace(rows[:3], true); ok {
		t.Error("hatalı trace yokken eonly seçim yapmamalı")
	}
	if _, ok := pickTopologyTrace(nil, false); ok {
		t.Error("boş liste")
	}
	tie := []chstore.TraceRow{{TraceID: "late", SpanCount: 10, StartTime: 9}, {TraceID: "early", SpanCount: 10, StartTime: 1}}
	if p, _ := pickTopologyTrace(tie, false); p.TraceID != "early" {
		t.Errorf("eşitlikte en erken: %+v", p)
	}
}

func TestTopologyAnswerLinks(t *testing.T) {
	from, to := time.UnixMilli(1_790_000_000_000), time.UnixMilli(1_790_003_600_000)
	win := linkWindowBetween(from, to)
	r := guidedRoute{Intent: guidedServiceTopology, Service: "payment-service", TopoHops: 1}
	got := guidedAnswerLinks(r, win)
	want := []string{
		"/service?name=payment-service&tab=topology&hops=1&range=custom:1790000000000-1790003600000",
		"/service-map?focus=payment-service&hops=1&range=custom:1790000000000-1790003600000",
		"/service?name=payment-service&range=custom:1790000000000-1790003600000",
	}
	if len(got) != len(want) {
		t.Fatalf("linkler: %+v", got)
	}
	for i := range want {
		if got[i].Href != want[i] {
			t.Errorf("link %d: %q, want %q", i, got[i].Href, want[i])
		}
	}
	// 2 hop (varsayılan) URL'e yazılmaz; eonly=1.
	r2 := guidedAnswerLinkTargets(guidedRoute{Intent: guidedServiceTopology, Service: "payment-service", TopoHops: 2, TopoErrorsOnly: true})
	if r2[0].Href != "/service?name=payment-service&tab=topology&eonly=1" || r2[1].Href != "/service-map?focus=payment-service&eonly=1" {
		t.Errorf("2 hop + eonly: %+v", r2)
	}
	// Trace kipi: trace linki önce.
	tr := guidedAnswerLinkTargets(guidedRoute{Intent: guidedTraceTopology, Service: "payment-service", TopoTraceOut: "0af7651916cd43dd8448eb211c80319c"})
	if tr[0].Href != "/trace?id=0af7651916cd43dd8448eb211c80319c" || !strings.HasPrefix(tr[1].Href, "/service?name=payment-service&tab=topology") {
		t.Errorf("trace linkleri: %+v", tr)
	}
	if l := guidedAnswerLinkTargets(guidedRoute{Intent: guidedTraceTopology}); len(l) != 1 || l[0].Href != "/service-map" {
		t.Errorf("servissiz: %+v", l)
	}
}

func TestTopologyFollowups(t *testing.T) {
	r := guidedRoute{Intent: guidedServiceTopology, Service: "payment-service", Env: "prod", TopoHops: 1, TopoHasErrEdge: true, TopoNeighbours: []string{"ledger-service"}}
	chips := guidedFollowups(r, nil)
	kinds := map[string]chatFollowup{}
	for _, c := range chips {
		kinds[c.Kind] = c
	}
	for _, k := range []string{fuKindTopoHops, fuKindTopoTrace, fuKindTopoErrors, fuKindTopoNeighbour} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("çip türü yok: %s (%+v)", k, chips)
		}
	}
	if len(chips) > guidedFollowupMax {
		t.Errorf("tavan: %d", len(chips))
	}
	// Her çip kendi başına yönlenir.
	wantRoute := map[string]func(guidedRoute) bool{
		fuKindTopoHops:      func(g guidedRoute) bool { return g.Intent == guidedServiceTopology && g.TopoHops == 2 },
		fuKindTopoTrace:     func(g guidedRoute) bool { return g.Intent == guidedTraceTopology && g.Service == "payment-service" },
		fuKindTopoErrors:    func(g guidedRoute) bool { return g.Intent == guidedServiceTopology && g.TopoErrorsOnly },
		fuKindTopoNeighbour: func(g guidedRoute) bool { return g.Intent == guidedServiceHealth && g.Service == "ledger-service" },
	}
	for _, c := range chips {
		g := routeGuidedIntent(c.Text, topoServices, topoEnvs, nil, "")
		if ok := wantRoute[c.Kind]; ok == nil || !ok(g) {
			t.Errorf("%s çipi %q → %+v", c.Kind, c.Text, g)
		}
		if c.Label == "" || c.Scope == nil || c.Scope.Env != "prod" {
			t.Errorf("%s: etiket/kapsam %+v", c.Kind, c)
		}
	}
	// 2 hop → daralt; eonly ya da hatalı kenar yok → "hatalı bağımlılıklar" yok.
	r2 := guidedRoute{Intent: guidedServiceTopology, Service: "payment-service", TopoHops: 2, TopoErrorsOnly: true, TopoHasErrEdge: true}
	for _, c := range guidedFollowups(r2, nil) {
		if c.Kind == fuKindTopoErrors {
			t.Errorf("eonly'de hatalı bağımlılık çipi: %+v", c)
		}
		if c.Kind == fuKindTopoHops {
			if g := routeGuidedIntent(c.Text, topoServices, topoEnvs, nil, ""); g.TopoHops != 1 || c.Label != "1 adıma daralt" {
				t.Errorf("daralt çipi %q → %+v", c.Text, g)
			}
		}
	}
	// Trace kipi: servis topolojisi + bu trace'i açıkla.
	tr := guidedFollowups(guidedRoute{Intent: guidedTraceTopology, Service: "payment-service", TopoTraceOut: "0af7651916cd43dd8448eb211c80319c"}, nil)
	if len(tr) < 2 {
		t.Fatalf("trace çipleri: %+v", tr)
	}
	if g := routeGuidedIntent(tr[0].Text, topoServices, topoEnvs, nil, ""); g.Intent != guidedServiceTopology || g.Service != "payment-service" {
		t.Errorf("servis topolojisi çipi %q → %+v", tr[0].Text, g)
	}
	if g := routeGuidedIntent(tr[1].Text, topoServices, topoEnvs, nil, ""); g.Intent != guidedTraceByID || tr[1].Scope == nil || tr[1].Scope.Trace == "" {
		t.Errorf("açıkla çipi %q → %+v", tr[1].Text, g)
	}
	// Sorulmuş adım tekrar önerilmez.
	asked := []string{"payment-service örnek trace topolojisi?"}
	for _, c := range guidedFollowups(r, asked) {
		if c.Kind == fuKindTopoTrace {
			t.Errorf("sorulmuş adım tekrar: %+v", c)
		}
	}
	// Eski istemci düz çipleri de yönlenir.
	for _, s := range guidedSuggestions(r)[:1] {
		if g := routeGuidedIntent(s, topoServices, topoEnvs, nil, ""); g.Intent != guidedTraceTopology {
			t.Errorf("suggestion %q → %+v", s, g)
		}
	}
}

func TestOpPrefetchTopologyFollowupFact(t *testing.T) {
	p := &opPrefetch{Operation: "PAYMENT_CREATE"}
	facts := p.followupFacts()
	if len(facts) != 1 || facts[0].Kind != fuKindOpTopology || facts[0].Operation != "PAYMENT_CREATE" {
		t.Fatalf("olgu: %+v", facts)
	}
	c, ok := renderFollowup(facts[0])
	if !ok || c.Text != "PAYMENT_CREATE operasyonunun örnek trace topolojisi" {
		t.Fatalf("çip: %+v", c)
	}
	if g := routeGuidedIntent(c.Text, topoServices, topoEnvs, nil, ""); g.Intent != guidedTraceTopology || g.TopoOperation != "PAYMENT_CREATE" {
		t.Errorf("çip yönlenmesi: %+v", g)
	}
	// Endpoint hedefi: fiil + rota; yönlenince aynı operasyon.
	ep := (&opPrefetch{Operation: "POST /api/payments", Route: "/api/payments", Method: "POST"}).followupFacts()
	c2, _ := renderFollowup(ep[0])
	if c2.Text != "POST /api/payments örnek trace topolojisi" {
		t.Errorf("endpoint çipi: %+v", c2)
	}
	if g := routeGuidedIntent(c2.Text, topoServices, topoEnvs, nil, ""); g.Intent != guidedTraceTopology || g.TopoOperation != "POST /api/payments" {
		t.Errorf("endpoint çip yönlenmesi: %+v", g)
	}
	if f := (&opPrefetch{Operation: "x", searchText: "api payments"}).followupFacts(); len(f) != 0 {
		t.Errorf("yol-yalnız hedef çip üretmemeli: %+v", f)
	}
	var nilP *opPrefetch
	if nilP.followupFacts() != nil {
		t.Error("nil ön-getirme")
	}
	if _, ok := renderFollowup(followupFact{Kind: fuKindOpTopology, Operation: "bad\nop"}); ok {
		t.Error("bozuk operasyon adı çip olmamalı")
	}
}

func TestTopologyCacheKeysHashAllInputs(t *testing.T) {
	from, to := time.Unix(1_790_000_000, 0), time.Unix(1_790_003_600, 0)
	base := topologyCacheKey("payment-service", 1, false, from, to, "h1")
	variants := []string{
		topologyCacheKey("checkout-service", 1, false, from, to, "h1"),
		topologyCacheKey("payment-service", 2, false, from, to, "h1"),
		topologyCacheKey("payment-service", 1, true, from, to, "h1"),
		topologyCacheKey("payment-service", 1, false, from.Add(-10*time.Minute), to, "h1"),
		topologyCacheKey("payment-service", 1, false, from, to.Add(10*time.Minute), "h1"),
		topologyCacheKey("payment-service", 1, false, from, to, "h2"),
	}
	for i, v := range variants {
		if v == base {
			t.Errorf("girdi %d anahtara girmiyor", i)
		}
	}
	if topologyTraceCacheKey("abc", "payment-service") == topologyTraceCacheKey("abc", "ledger-service") ||
		topologyTraceCacheKey("ABC", "x") != topologyTraceCacheKey("abc", "x") {
		t.Error("trace anahtarı: odak girmeli, id harf duyarsız")
	}
}

func TestTopologyBlockSequenced(t *testing.T) {
	var kinds []string
	var seq blocks.Sequencer
	emit := withBlockSeq(func(k string, p any) {
		kinds = append(kinds, k)
		if m, ok := p.(map[string]any); ok && m["type"] != blocks.TypeTopology {
			t.Errorf("blok tipi: %v", m["type"])
		}
	}, &seq)
	emit(blocks.TypeTopology, topologyBlockPayload{Focus: "payment-service"})
	if len(kinds) != 1 || kinds[0] != "block" || seq.Count() != 1 {
		t.Errorf("topoloji bloğu sıralayıcıdan geçmeli: %v", kinds)
	}
}

func TestTopologyReadOnly(t *testing.T) {
	b, err := os.ReadFile("copilot_service_topology.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, bad := range []string{"s.audit(", "copilotStreamSurface(", "copilotExplain(", ".Insert", "Exec(", "s.copilot."} {
		if strings.Contains(src, bad) {
			t.Errorf("topoloji demeti salt-okur ve LLM'siz olmalı: %q", bad)
		}
	}
}

// TestOpPrefetchTopologyVetoFirst — v0.10.1388: veto ön-getirmenin İLK okumasından
// (endpoint anma / katalog) önce; kademe sırası değişmedi (TestChatTierOrderIsPinned).
func TestOpPrefetchTopologyVetoFirst(t *testing.T) {
	b, err := os.ReadFile("chat_operation_prefetch.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	i := strings.Index(src, "func (s *Server) chatOperationPrefetch(")
	if i < 0 {
		t.Fatal("chatOperationPrefetch yok")
	}
	body := src[i:]
	veto, read := strings.Index(body, "topologyVetoesOpPrefetch(raw)"), strings.Index(body, "s.resolveEndpointMention(")
	if veto < 0 || read < 0 || veto > read {
		t.Errorf("topoloji vetosu ilk okumadan önce olmalı (veto=%d okuma=%d)", veto, read)
	}
}
