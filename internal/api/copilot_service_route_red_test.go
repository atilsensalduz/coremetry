package api

// v0.10.1481 — servis sağlığında sorumlu operasyonun VictoriaMetrics rota
// grafikleri (copilot_service_route_red.go): eşleme, sorgular, dürüst not,
// span grafiği hiçbir dalda yok.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestHTTPRouteFromOperation(t *testing.T) {
	cases := []struct {
		in, method, route string
		ok                bool
	}{
		{"GET /api/orders/{id}", "GET", "/api/orders/{id}", true},
		{"post /api/pay", "POST", "/api/pay", true},
		{"/api/health", "", "/api/health", true},
		{"GET", "", "", false},
		{"grpc.orders.Get", "", "", false},
		{"SELECT orders", "", "", false},
		{"GET /api/x?id=1", "", "", false},
		{"FETCH /api/x", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		m, r, ok := httpRouteFromOperation(c.in)
		if m != c.method || r != c.route || ok != c.ok {
			t.Errorf("%q → (%q,%q,%v) beklenen (%q,%q,%v)", c.in, m, r, ok, c.method, c.route, c.ok)
		}
	}
}

func TestSvcRouteREDQueries(t *testing.T) {
	qs := svcRouteREDQueries(redFamilies[0], "orders-api", "service_name", `deployment_environment_name="prod-eu-a"`, "http_response_status_code", "/api/orders/{id}", "GET", 60)
	if len(qs) != 3 {
		t.Fatalf("3 sorgu: %d", len(qs))
	}
	for i, k := range []string{"route_rate", "route_5xx", "route_latency"} {
		q := qs[i]
		if q.Kind != k || !strings.Contains(q.Query, `http_route="/api/orders/{id}"`) || !strings.Contains(q.Query, `http_request_method="GET"`) ||
			!strings.Contains(q.Query, `deployment_environment_name="prod-eu-a"`) || !strings.Contains(q.Title, "sorumlu operasyon GET /api/orders/{id}") {
			t.Errorf("sorgu %d: %+v", i, q)
		}
	}
	// Fiil etiketi yoksa yalnız rota süzgeci; durum etiketi yoksa 5xx yok.
	qs = svcRouteREDQueries(redFamilies[0], "orders-api", "service_name", "", "", "/api/orders", "", 60)
	if len(qs) != 2 || strings.Contains(qs[0].Query, "http_request_method") {
		t.Errorf("fiilsiz/durumsuz: %+v", qs)
	}
}

func TestSvcRouteREDSkipReason(t *testing.T) {
	ok := svcHTTPREDResult{UseVM: true, routeLabelOK: true, methodLabelOK: true}
	cases := []struct {
		name       string
		base       svcHTTPREDResult
		op, kind   string
		wantMethod string
		wantReason string // alt dizgi; "" = sorgulanır
	}{
		{"eşleşir", ok, "GET /api/orders", "SERVER", "GET", ""},
		{"fiil etiketi yok", svcHTTPREDResult{UseVM: true, routeLabelOK: true}, "GET /api/orders", "SERVER", "", ""},
		{"giden çağrı", ok, "GET /api/orders", "CLIENT", "", "giden çağrı"},
		{"rota değil", ok, "orders.process", "INTERNAL", "", "HTTP rotası değil"},
		{"rota etiketi yok", svcHTTPREDResult{UseVM: true}, "GET /api/orders", "SERVER", "", "http_route etiketini taşımıyor"},
		{"gRPC ailesi", svcHTTPREDResult{UseVM: true, Family: redKindGRPC, routeLabelOK: true}, "GET /api/orders", "SERVER", "", "gRPC"},
	}
	for _, c := range cases {
		m, _, r := svcRouteREDSkipReason(c.base, c.op, c.kind)
		if m != c.wantMethod || (c.wantReason == "") != (r == "") || !strings.Contains(r, c.wantReason) {
			t.Errorf("%s: method=%q reason=%q", c.name, m, r)
		}
	}
	if _, _, r := svcRouteREDSkipReason(svcHTTPREDResult{}, "GET /x", "SERVER"); r != "" {
		t.Errorf("servis RED'i çizilmediyse ayrıca not yok: %q", r)
	}
}

// TestReadServiceRouteRED — uçtan uca (sahte VM): servis RED'inin çözülmüş
// girdileriyle rota grafikleri; serisiz / hatalı / VM yok → grafik yok + not.
func TestReadServiceRouteRED(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)
	from := to.Add(-time.Hour)
	full := func() *redReader {
		return &redReader{
			fakeChartReader: fakeChartReader{labels: map[string][]string{mcHTTPMetric: {"http_response_status_code", "http_route", "http_request_method", "service_name"}}},
			rate:            []chstore.SpanMetricSeries{{Points: svcREDPts(from, 12, 13, 12, 40)}},
			fiveXX:          []chstore.SpanMetricSeries{{Points: svcREDPts(from, 0, 0, 1, 3)}},
			lat:             []chstore.SpanMetricSeries{{GroupKey: []string{`phi="0.95"`}, Points: svcREDPts(from, 0.1, 0.1, 0.2, 0.5)}},
		}
	}
	s := &Server{}
	rd := full()
	base := s.readServiceHTTPRED(context.Background(), rd, true, "orders-api", "", from, to, 3600)
	if !base.UseVM || !base.routeLabelOK || !base.methodLabelOK || base.svcLabel != "service_name" || base.statusLabel == "" {
		t.Fatalf("servis RED girdileri: %+v", base)
	}
	n0 := len(rd.qs)
	rr := s.readServiceRouteRED(context.Background(), rd, true, "orders-api", "GET /api/orders/{id}", "SERVER", base)
	if rr.Note != "" || len(rr.Charts) != 3 || len(rd.qs)-n0 != 3 {
		t.Fatalf("rota grafikleri: note=%q charts=%d q=%d", rr.Note, len(rr.Charts), len(rd.qs)-n0)
	}
	for _, c := range rr.Charts {
		if !strings.Contains(c.Query, `http_route="/api/orders/{id}"`) || c.Err != nil || len(c.Series) == 0 {
			t.Errorf("grafik %s: %+v", c.Kind, c)
		}
	}
	ev := &mcEvents{}
	text, drawn := s.emitServiceRouteRED(ev.emit(), "orders-api", 3600, base, rr, time.UTC)
	if !drawn || len(ev.blocks) != 3 || !strings.Contains(text, "SORUMLU OPERASYON GRAFİKLERİ") || strings.Contains(text, "```chart") {
		t.Errorf("emit: drawn=%v blocks=%d\n%s", drawn, len(ev.blocks), text)
	}
	for _, b := range ev.blocks {
		if p := b.Payload.(mcptools.EvChartPayload); !strings.Contains(p.Title, "VictoriaMetrics (örneklenmemiş)") {
			t.Errorf("başlık kaynağı söylemeli: %s", p.Title)
		}
	}

	// Rotanın serisi yok → grafik yok, not (5xx/gecikme tek başına çizilmez).
	rd2 := full()
	base2 := s.readServiceHTTPRED(context.Background(), rd2, true, "orders-api", "", from, to, 3600)
	rd2.rate = nil
	rr = s.readServiceRouteRED(context.Background(), rd2, true, "orders-api", "GET /api/none", "SERVER", base2)
	ev = &mcEvents{}
	text, drawn = s.emitServiceRouteRED(ev.emit(), "orders-api", 3600, base2, rr, time.UTC)
	if drawn || len(ev.blocks) != 0 || !strings.Contains(text, "serisi bu pencerede yok") || !strings.Contains(text, "span grafiği gösterilmez") {
		t.Errorf("serisiz: drawn=%v blocks=%d %q", drawn, len(ev.blocks), text)
	}
	// Okuma hatası ≠ yok.
	if ch, note := svcRouteREDFinish([]svcHTTPREDChart{{Err: errors.New("vm down")}}); ch != nil || !strings.Contains(note, "okunamadı ≠ yok") {
		t.Errorf("hata: %q", note)
	}
	// Rota değil → sorgu atılmaz.
	n0 = len(rd.qs)
	rr = s.readServiceRouteRED(context.Background(), rd, true, "orders-api", "orders.process", "INTERNAL", base)
	if len(rd.qs) != n0 || rr.Note == "" || len(rr.Charts) != 0 {
		t.Errorf("rota değil: %+v", rr)
	}
	// Servis RED'i çizilmediyse ne grafik ne not.
	rr = s.readServiceRouteRED(context.Background(), rd, true, "orders-api", "GET /api/orders", "SERVER", svcHTTPREDResult{Reason: "x"})
	if txt, d := s.emitServiceRouteRED((&mcEvents{}).emit(), "orders-api", 3600, svcHTTPREDResult{}, rr, time.UTC); txt != "" || d {
		t.Errorf("VM yok: %q", txt)
	}
}

// TestServiceHealthBundleRouteChartWiring — demet sorumlu operasyonu yalnız VM
// dalında ve taramanın operasyon grafiği düşürüldükten SONRA çizer.
func TestServiceHealthBundleRouteChartWiring(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func (s *Server) guidedServiceHealthBundle(")
	j := strings.Index(src[i:], "\n}\n")
	body := stripGoCommentsAPI(src[i : i+j])
	drop, vm, route := strings.Index(body, "sw.res.DropOperationCharts()"), strings.Index(body, "if httpRED.UseVM {"), strings.Index(body, "sw.res.ResponsibleOperation()")
	if drop < 0 || vm < 0 || route < vm || route < drop || !strings.Contains(body, "s.emitServiceRouteRED(") {
		t.Errorf("sorumlu operasyon grafiği VM dalında, düşürmeden sonra olmalı: drop=%d vm=%d route=%d", drop, vm, route)
	}
	if strings.Contains(body, "chartFence(") {
		t.Error("span çiti olmamalı")
	}
}
