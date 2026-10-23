package mcptools

// v0.10.1391 — operatör: "Prod sağlığı güzel ama VictoriaMetrics getirmedi, en
// azından HTTP metriclerimi getirebilirdi. Onun yerine gRPC getirdi. Aslında
// metrikler OTel collector'dan geliyor." Tarama grafiği servisin GİDEN gRPC
// istemci çağrısını (CLIENT) çizdi ve count paneli "p50/p99" başlığı taşıdı.
// Pinler: (1) giriş (SERVER/CONSUMER) operasyonu giden çağrıya tercih edilir;
// (2) başlık p50/p99 demez, örneklenmiş span olduğunu söyler; (3) VM grafiği
// çizildiğinde operasyon grafik blokları düşürülür.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSweepPickChartOps(t *testing.T) {
	const grpc, http, worker, consume = "inventory.StockService/Reserve", "POST /orders", "reindex-batch", "orders.created process"
	kinds := map[string][]string{
		grpc:    {"client"},
		http:    {"server"},
		worker:  {"internal"},
		consume: {"consumer"},
	}
	cases := []struct {
		name  string
		names []string
		kinds map[string][]string
		want  string
	}{
		{"operatör vakası: giden gRPC önce sıralı ama giriş varken çizilmez", []string{grpc, http}, kinds, http},
		{"yalnız giden çağrı bulgusu → yine çizilir (başka aday yok)", []string{grpc}, kinds, grpc},
		{"giriş önce, iç sonra, giden yok", []string{worker, grpc, consume}, kinds, consume + "|" + worker},
		{"tür okunamadı → eski sıra (tercih yok)", []string{grpc, http}, nil, grpc + "|" + http},
		{"tavan 3", []string{http, consume, worker, "GET /health/ready", grpc}, kinds, http + "|" + consume + "|" + worker},
		{"aday yok", nil, kinds, ""},
		{"hem client hem server görülen ad → giriş", []string{"orders.OrderService/Get", grpc}, map[string][]string{"orders.OrderService/Get": {"client", "server"}, grpc: {"client"}}, "orders.OrderService/Get"},
	}
	for _, c := range cases {
		if got := strings.Join(sweepPickChartOps(c.names, c.kinds), "|"); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if sweepOpKindTag([]string{"client"}) != "giden çağrı · CLIENT" || sweepOpKindTag([]string{"producer"}) != "giden çağrı · PRODUCER" ||
		sweepOpKindTag([]string{"server"}) != "" || sweepOpKindTag(nil) != "" {
		t.Error("tür eki yalnız giden çağrıda")
	}
	if sweepOpKindLabel([]string{"client", "server"}) != "SERVER" || sweepOpKindLabel([]string{"internal"}) != "INTERNAL" || sweepOpKindLabel(nil) != "" {
		t.Error("tür etiketi")
	}
}

// TestSweepOpChartTitleNoP50P99OnCount — arayüz birim başına panel çizer ve
// başlığa " · <birim>" ekler (EvidenceChips.tsx): count paneli artık "p50/p99"
// demez ve örneklenmiş olduğunu söyler.
func TestSweepOpChartTitleNoP50P99OnCount(t *testing.T) {
	base := sweepTo.Add(-time.Hour).Unix()
	pts := []chstore.OpLatencyPoint{{TS: base, Calls: 100, Errors: 2, P50Ms: 12, P99Ms: 40}, {TS: base + 120, Calls: 90, Errors: 0, P50Ms: 11, P99Ms: 35}}
	c, ok := sweepOpLatencyChart("inventory.StockService/Reserve", "giden çağrı · CLIENT", pts, 120, 30, nil, EvBlockWindow{})
	if !ok {
		t.Fatal("grafik yok")
	}
	if want := "inventory.StockService/Reserve (giden çağrı · CLIENT) · 2 dk kova · örneklenmiş span (spanmetrics_1m)"; c.Title != want {
		t.Fatalf("başlık\n got %q\nwant %q", c.Title, want)
	}
	units := map[string]bool{}
	for _, s := range c.Series {
		units[s.Unit] = true
	}
	if !units["ms"] || !units["count"] {
		t.Fatalf("iki birim (ms + count) bekleniyordu: %v", units)
	}
	for u := range units {
		panel := c.Title + " · " + u // EvidenceChart: groups.length > 1 → `${title} · ${unit}`
		if strings.Contains(panel, "p50/p99") || !strings.Contains(panel, "örneklenmiş") {
			t.Errorf("%s paneli başlığı %q", u, panel)
		}
	}
	sp, _ := sweepSparkChart("POST /orders", "", chstore.OperationSummary{P99Sparkline: []float64{3, 4}, ErrorsSparkline: []uint64{0, 1}}, sweepTo.Add(-time.Hour), sweepTo)
	if strings.Contains(sp.Title, "p50/p99") || !strings.Contains(sp.Title, "örneklenmiş span (operation_summary_5m)") {
		t.Errorf("sparkline başlığı %q", sp.Title)
	}
}

// TestSweepPrefersEntryOpChart — uçtan uca: giden gRPC (CLIENT) ve gelen HTTP
// (SERVER) iki bulgu; grafik yalnız SERVER'ınki, bulgu satırı türü taşır.
func TestSweepPrefersEntryOpChart(t *testing.T) {
	const grpc, http = "inventory.StockService/Reserve", "POST /orders"
	r := baseSweepReaders()
	r.servicePods, r.fnSpans, r.runtime = nil, nil, nil
	r.ops = func(context.Context, string, time.Time, time.Time) ([]chstore.OperationSummary, error) {
		return []chstore.OperationSummary{
			{Name: grpc, SpanCount: 4000, ErrorRate: 12, ErrorCount: 480, P99Ms: 90, HasPrior: true, PriorSpanCount: 4000, PriorErrorRate: 0.2, PriorP99Ms: 90},
			{Name: http, SpanCount: 3000, ErrorRate: 7, ErrorCount: 210, P99Ms: 120, HasPrior: true, PriorSpanCount: 3000, PriorErrorRate: 0.3, PriorP99Ms: 120},
		}, nil
	}
	var kindsAsked []string
	r.opKinds = func(_ context.Context, svc string, names []string, _, _ time.Time) (map[string][]string, error) {
		kindsAsked = append([]string(nil), names...)
		return map[string][]string{grpc: {"client"}, http: {"server"}}, nil
	}
	var charted []string
	r.opLatency = func(_ context.Context, _, op string, from, _ time.Time, _ int) ([]chstore.OpLatencyPoint, error) {
		return []chstore.OpLatencyPoint{{TS: from.Unix(), Calls: 10, P50Ms: 10, P99Ms: 30}}, nil
	}
	res, err := runServiceSweep(context.Background(), r, sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kindsAsked) != 2 {
		t.Fatalf("tür okuması iki adayı sormalı: %v", kindsAsked)
	}
	for _, b := range res.Data.Blocks {
		if c, ok := b.Payload.(EvChartPayload); ok && b.Hop == sweepHopOps {
			charted = append(charted, c.Title)
		}
	}
	if len(charted) != 1 || !strings.HasPrefix(charted[0], http+" · ") {
		t.Fatalf("yalnız giriş operasyonu çizilmeli: %v", charted)
	}
	kinds := map[string]string{}
	for _, f := range res.Findings {
		if f.Kind == sweepKindOperation {
			kinds[f.Name] = f.SpanKind
		}
	}
	if kinds[grpc] != "CLIENT" || kinds[http] != "SERVER" {
		t.Fatalf("bulgu türleri: %v", kinds)
	}

	// v0.10.1481 — sorumlu operasyon = çizilen giriş operasyonu (grafik düşse de).
	if n, k, ok := res.ResponsibleOperation(); !ok || n != http || k != "SERVER" {
		t.Fatalf("sorumlu operasyon: %q %q %v", n, k, ok)
	}
	// DropOperationCharts: ops grafiği düşer, tablo blokları kalır.
	before := len(res.Data.Blocks)
	shared := res.Data.Blocks
	if n := res.DropOperationCharts(); n != 1 || len(res.Data.Blocks) != before-1 {
		t.Fatalf("düşürülen %d, kalan %d/%d", n, len(res.Data.Blocks), before)
	}
	for _, b := range res.Data.Blocks {
		if b.Hop == sweepHopOps && b.Type == blocks.TypeChart {
			t.Fatal("ops grafiği kalmamalı")
		}
	}
	if len(shared) != before {
		t.Fatal("paylaşılan dilim yerinde değişmemeli")
	}
}

// TestSweepResponsibleOperation — v0.10.1481: grafik seçimiyle aynı kural
// (giriş önce; giden çağrı yalnız tek aday olduğunda; bulgu yoksa ok=false).
func TestSweepResponsibleOperation(t *testing.T) {
	op := func(name, kind string) SweepFinding {
		return SweepFinding{Kind: sweepKindOperation, Name: name, SpanKind: kind}
	}
	cases := []struct {
		name     string
		findings []SweepFinding
		want     string
		wantKind string
		ok       bool
	}{
		{"bulgu yok", nil, "", "", false},
		{"yalnız pod", []SweepFinding{{Kind: "pod", Name: "p-1"}}, "", "", false},
		{"giriş giden çağrıyı geçer", []SweepFinding{op("grpc.Call", "CLIENT"), op("GET /api/orders", "SERVER")}, "GET /api/orders", "SERVER", true},
		{"tür bilinmiyor: bulgu sırası", []SweepFinding{op("GET /a", ""), op("GET /b", "")}, "GET /a", "", true},
		{"yalnız giden çağrı", []SweepFinding{op("POST /pay", "CLIENT")}, "POST /pay", "CLIENT", true},
	}
	for _, c := range cases {
		n, k, ok := SweepResult{Findings: c.findings}.ResponsibleOperation()
		if n != c.want || k != c.wantKind || ok != c.ok {
			t.Errorf("%s: (%q,%q,%v) beklenen (%q,%q,%v)", c.name, n, k, ok, c.want, c.wantKind, c.ok)
		}
	}
}
