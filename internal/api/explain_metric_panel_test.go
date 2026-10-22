package api

// explain_metric_panel_test.go — v0.10.1435 (Panelden "Bu grafiği açıkla").
// Sahte okuma yüzeyiyle (metricPanelSeam + fakeChartReader): güven kuralı
// (dashboard: kayıtlı panel + yalnız vars; uyuşmazlık = ret), PromQL
// doğrulayıcı reddi (sorgu ÇALIŞMAZ), yüksüz Explore niyeti, brush ölçümü +
// ince adımlı ikinci sorgu, seçili seri önde / top-20 dışı notu, 60 sn önbellek
// (ikinci tur sıfır sorgu + sıfır LLM), kanıt-önce (model düşer → blok +
// dürüst satır), takip çipleri, niyet normalizasyonu (8 KiB tavanı).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

type mpFake struct {
	mu          sync.Mutex
	dash        map[string]*chstore.Dashboard
	metric      []chstore.SpanMetricSeries
	span        []chstore.SpanMetricSeries
	vm          *fakeChartReader
	metricCalls []chstore.MetricQueryFilter
	spanCalls   []chstore.SpanMetricFilter
}

func (f *mpFake) GetDashboard(_ context.Context, id string) (*chstore.Dashboard, error) {
	return f.dash[id], nil
}
func (f *mpFake) MetricSourceName() string { return metricSourceCH }
func (f *mpFake) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metricCalls = append(f.metricCalls, q)
	return f.metric, nil
}
func (f *mpFake) QuerySpanMetric(_ context.Context, q chstore.SpanMetricFilter) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spanCalls = append(f.spanCalls, q)
	return f.span, nil
}
func (f *mpFake) PromQL() (metricChartReader, bool) {
	if f.vm == nil {
		return nil, false
	}
	return f.vm, true
}

var mpT0 = time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

// mpSeries — 120 noktalık seri; [spikeFrom, spikeTo) dakikalarında değer ×5.
func mpSeries(gk []string, base float64, spikeFrom, spikeTo int) chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{GroupKey: gk}
	for i := 0; i < 120; i++ {
		v := base
		if i >= spikeFrom && i < spikeTo {
			v = base * 5
		}
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: mpT0.Add(time.Duration(i) * 30 * time.Second).UnixNano(), Value: v})
	}
	return s
}

func mpServer(t *testing.T, p *defProvider, f *mpFake) *Server {
	t.Helper()
	metricPanelSeam = f
	t.Cleanup(func() { metricPanelSeam = nil })
	return defServer(t, p)
}

func mpSubject(loc string, brush bool, series ...string) string {
	s := aiSubject{Kind: explainAskMetricPanel, ID: loc, From: mpT0, To: mpT0.Add(time.Hour), Series: series}
	if brush {
		s.BrushFrom, s.BrushTo = mpT0.Add(20*time.Minute), mpT0.Add(25*time.Minute)
	}
	return formatAiSubject(s)
}

func mpDashboard(query string) *chstore.Dashboard {
	panels, _ := json.Marshal([]map[string]any{
		{"id": "md", "type": "markdown", "title": "Not", "config": map[string]any{"content": "x"}},
		{"id": "p1", "type": "promql", "title": "Checkout RPS", "config": map[string]any{"query": query, "unit": "rps"}},
	})
	return &chstore.Dashboard{ID: "dash01", Name: "Checkout", Panels: panels}
}

func mpVM(series ...chstore.SpanMetricSeries) *fakeChartReader {
	return &fakeChartReader{
		labels: map[string][]string{"http_server_requests_total": {"__name__", "service_name", "pod", "deployment_environment_name"}},
		series: series,
	}
}

func mpRun(t *testing.T, s *Server, ask chatExplainAsk) ([]xtEvent, map[string]any, bool) {
	t.Helper()
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-mp"})
	h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &ask, time.UTC)
	if !h {
		t.Fatalf("tur ele alınmadı: %s", xtKinds(evs))
	}
	return evs, xtAnswer(t, evs), ok
}

func mpBlock(t *testing.T, evs []xtEvent) metricPanelBlockPayload {
	t.Helper()
	for _, e := range evs {
		if e.kind == blocks.TypeMetricPanel {
			return e.data.(metricPanelBlockPayload)
		}
	}
	t.Fatalf("metric_panel bloğu yok: %s", xtKinds(evs))
	return metricPanelBlockPayload{}
}

const mpQuery = `sum by (pod) (rate(http_server_requests_total{service_name="${svc}"}[5m]))`

// Dashboard: kayıtlı panel + istemcinin vars'ı; çalışan sorgu sunucunun çözdüğü.
// İkinci tur: önbellek — sıfır VM sorgusu, sıfır LLM.
func TestMetricPanelDashboardTrustAndCache(t *testing.T) {
	p := newDefProvider(t, "Seçili aralıkta pod-a ", "5× sıçradı.")
	f := &mpFake{dash: map[string]*chstore.Dashboard{"dash01": mpDashboard(mpQuery)},
		vm: mpVM(mpSeries([]string{`pod="pod-a"`}, 10, 40, 50), mpSeries([]string{`pod="pod-b"`}, 9, 0, 0))}
	s := mpServer(t, p, f)
	resolved := `sum by (pod) (rate(http_server_requests_total{service_name="checkout"}[5m]))`
	ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("d.dash01.p1", true, "pod=pod-a"), Src: "button",
		Panel: &metricPanelAsk{Src: "promql", PromQL: resolved, Vars: map[string]string{"svc": "checkout"}, Title: "istemcinin başlığı yok sayılır"}}
	evs, ans, ok := mpRun(t, s, ask)
	if !ok || p.requests() != 1 {
		t.Fatalf("ok=%v calls=%d %s", ok, p.requests(), xtKinds(evs))
	}
	if got := xtKinds(evs); !strings.HasPrefix(got, "step,step-result,"+blocks.TypeMetricPanel+",") || !strings.HasSuffix(got, "answer") {
		t.Fatalf("olay sırası %s", got)
	}
	if len(f.vm.queries) == 0 || f.vm.queries[0] != resolved {
		t.Fatalf("VM'e giden sorgu sunucunun çözdüğü olmalı: %v", f.vm.queries)
	}
	b := mpBlock(t, evs)
	if b.Title != "Checkout RPS" || b.Unit != "rps" || len(b.Selected) != 1 || b.Selected[0] != "pod-a" || b.Chart.Series[0].Name != "pod-a" {
		t.Fatalf("blok %+v", b)
	}
	if len(b.Chart.Regions) != 1 || b.Chart.Regions[0].Label != "seçili" || b.BrushFromNs == 0 {
		t.Fatalf("brush bölgesi %+v", b.Chart.Regions)
	}
	meta, _ := ans["explain"].(chatExplainMeta)
	if meta.Renderer != explainRendererMetricPanel || meta.Surface != aisurface.ExplainMetricPanelChat || meta.Label != "Checkout RPS" || meta.Note == "" {
		t.Fatalf("künye %+v", meta)
	}
	_, user := p.lastMessages(t)
	for _, want := range []string{"Checkout RPS", resolved, "Operatörün seçtiği aralık", "pod-a [SEÇİLİ]", "seçili aralıkta ort", "değişim noktaları"} {
		if !strings.Contains(user, want) {
			t.Errorf("istemde %q yok:\n%s", want, user)
		}
	}
	if sys, _ := p.lastMessages(t); sys != copilot.SystemPromptServiceCharts() {
		t.Error("sistem istemi SystemPromptServiceCharts olmalı")
	}
	nq := len(f.vm.queries)
	evs2, ans2, ok2 := mpRun(t, s, ask)
	if !ok2 || p.requests() != 1 || len(f.vm.queries) != nq || ans2["cached"] != true {
		t.Fatalf("önbellek: calls=%d queries=%d→%d cached=%v %s", p.requests(), nq, len(f.vm.queries), ans2["cached"], xtKinds(evs2))
	}
}

// Dashboard: istemcinin çözdüğü sorgu kayıtlı panelle uyuşmuyor → LLM'siz ret, sorgu yok.
func TestMetricPanelDashboardVarMismatchRejects(t *testing.T) {
	p := newDefProvider(t, "x")
	f := &mpFake{dash: map[string]*chstore.Dashboard{"dash01": mpDashboard(mpQuery)}, vm: mpVM()}
	s := mpServer(t, p, f)
	ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("d.dash01.p1", false),
		Panel: &metricPanelAsk{Src: "promql", PromQL: `sum(rate(http_server_requests_total[5m]))`, Vars: map[string]string{"svc": "checkout"}}}
	_, ans, ok := mpRun(t, s, ask)
	if ok || p.requests() != 0 || len(f.vm.queries) != 0 {
		t.Fatalf("ret bekleniyor: ok=%v calls=%d queries=%v", ok, p.requests(), f.vm.queries)
	}
	if txt, _ := ans["text"].(string); !strings.Contains(txt, "kayıtlı panelle uyuşmuyor") {
		t.Fatalf("metin %q", txt)
	}
	// Bilinmeyen panel → bulunamadı; zaman serisi olmayan panel → dürüst ret.
	ask.Subject, ask.Panel = mpSubject("d.dash01.nope", false), nil
	if _, ans, _ := mpRun(t, s, ask); !strings.Contains(fmt.Sprint(ans["text"]), "bulunamadı") {
		t.Fatalf("bulunamadı %v", ans["text"])
	}
	ask.Subject = mpSubject("d.dash01.md", false)
	if _, ans, _ := mpRun(t, s, ask); !strings.Contains(fmt.Sprint(ans["text"]), "açıklanamaz") {
		t.Fatalf("markdown %v", ans["text"])
	}
}

// Explore / Metrics PromQL: doğrulayıcı reddi → sorgu ÇALIŞMAZ, LLM yok, link var.
func TestMetricPanelPromQLValidatorRejects(t *testing.T) {
	p := newDefProvider(t, "x")
	f := &mpFake{vm: mpVM(mpSeries([]string{`pod="a"`}, 1, 0, 0))}
	s := mpServer(t, p, f)
	for _, q := range []string{
		`label_replace(http_server_requests_total, "a", "b", "c", "d")`,
		`rate(http_server_requests_total[5m:1m])`,
		`rate(nonexistent_metric_total[5m])`,
	} {
		ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("m", false), Panel: &metricPanelAsk{Src: "promql", PromQL: q}}
		evs, ans, ok := mpRun(t, s, ask)
		if ok || p.requests() != 0 || len(f.vm.queries) != 0 {
			t.Fatalf("%s: ret bekleniyor ok=%v calls=%d queries=%v %s", q, ok, p.requests(), f.vm.queries, xtKinds(evs))
		}
		if txt, _ := ans["text"].(string); !strings.Contains(txt, "çalıştırmadım") || !strings.Contains(txt, "Metrics'te aç") {
			t.Fatalf("%s: metin %q", q, txt)
		}
	}
	// VM yok → PromQL çalışmaz, dürüst satır.
	f.vm = nil
	ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("m", false), Panel: &metricPanelAsk{Src: "promql", PromQL: `sum(rate(http_server_requests_total[5m]))`}}
	if _, ans, ok := mpRun(t, s, ask); ok || !strings.Contains(fmt.Sprint(ans["text"]), "yalnız VictoriaMetrics") {
		t.Fatalf("VM yok: %v %v", ok, ans["text"])
	}
}

// Yüksüz Explore niyeti (paylaşılan link) → LLM'siz dürüst cevap, sorgu yok.
func TestMetricPanelExploreWithoutPayload(t *testing.T) {
	p := newDefProvider(t, "x")
	f := &mpFake{}
	s := mpServer(t, p, f)
	_, ans, ok := mpRun(t, s, chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("x.A", false)})
	if ok || p.requests() != 0 || len(f.metricCalls)+len(f.spanCalls) != 0 {
		t.Fatalf("ok=%v calls=%d", ok, p.requests())
	}
	if !strings.Contains(fmt.Sprint(ans["text"]), "sorgusu bu istekte yok") {
		t.Fatalf("metin %v", ans["text"])
	}
}

// Builder / span: HTTP uçlarının filtre yapıları; seçili seri önde, top-20 dışı notu;
// brush 8 kovadan azsa ince adımlı ikinci sorgu.
func TestMetricPanelBuilderSelectedAndBrushQuery(t *testing.T) {
	p := newDefProvider(t, "ok")
	var many []chstore.SpanMetricSeries
	for i := 0; i < 25; i++ {
		many = append(many, mpSeries([]string{fmt.Sprintf("svc-%02d", i)}, float64(100-i), 0, 0))
	}
	f := &mpFake{metric: many}
	s := mpServer(t, p, f)
	sub := aiSubject{Kind: explainAskMetricPanel, ID: "x.B", From: mpT0, To: mpT0.Add(6 * time.Hour),
		BrushFrom: mpT0.Add(time.Hour), BrushTo: mpT0.Add(time.Hour + 10*time.Minute), Series: []string{"service=svc-24", "service=yok"}}
	ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: formatAiSubject(sub),
		Panel: &metricPanelAsk{Src: "builder", MetricName: "http.server.request.duration", Agg: "p95", GroupBy: "service.name",
			Filters: `[{"k":"env","op":"=","v":["stage"]}]`, Unit: "s", Title: "Explore · B"}}
	evs, _, ok := mpRun(t, s, ask)
	if !ok {
		t.Fatalf("ok=false %s", xtKinds(evs))
	}
	if len(f.metricCalls) != 2 {
		t.Fatalf("pencere + brush = 2 sorgu bekleniyor, %d", len(f.metricCalls))
	}
	w, br := f.metricCalls[0], f.metricCalls[1]
	if w.Name != "http.server.request.duration" || w.Aggregation != "p95" || len(w.GroupBy) != 1 || len(w.Filters) != 1 || w.StepSeconds != 180 || w.MaxDataPoints != 120 {
		t.Fatalf("pencere sorgusu %+v", w)
	}
	if !br.From.Equal(sub.BrushFrom) || br.StepSeconds != 15 {
		t.Fatalf("brush sorgusu %+v", br)
	}
	b := mpBlock(t, evs)
	if len(b.Chart.Series) != 20 || b.Chart.Series[0].Name != "svc-24" || len(b.Missing) != 1 || b.Missing[0] != "service=yok" || !b.Chart.Truncated {
		t.Fatalf("seçili önde / eksik: n=%d first=%q missing=%v", len(b.Chart.Series), b.Chart.Series[0].Name, b.Missing)
	}
	_, user := p.lastMessages(t)
	if !strings.Contains(user, "ilk 20 serisi dışında") || !strings.Contains(user, "Seçili seri svc-24") {
		t.Fatalf("istem:\n%s", user)
	}
}

// Kanıt-önce: model düşerse `error` değil — blok + dürüst satır + çipler.
func TestMetricPanelNarrationFailureKeepsEvidence(t *testing.T) {
	p := newDefProvider(t, "x")
	f := &mpFake{span: []chstore.SpanMetricSeries{mpSeries([]string{"GET /orders"}, 12, 30, 40)}}
	s := mpServer(t, p, f)
	p.srv.Close()
	ask := chatExplainAsk{Kind: explainAskMetricPanel, Subject: mpSubject("x.A", true),
		Panel: &metricPanelAsk{Src: "span", Agg: "p99", Field: "duration_ms", GroupBy: "name", DSL: `service.name = "orders-api"`}}
	evs, ans, ok := mpRun(t, s, ask)
	if ok || strings.Contains(xtKinds(evs), "error") {
		t.Fatalf("ok=%v olaylar %s", ok, xtKinds(evs))
	}
	if txt, _ := ans["text"].(string); !strings.HasPrefix(txt, "Anlatım üretilemedi") {
		t.Fatalf("dürüst satır %q", txt)
	}
	mpBlock(t, evs)
	if len(f.spanCalls) == 0 || len(f.spanCalls[0].Filters) != 1 || f.spanCalls[0].Aggregation != "p99" {
		t.Fatalf("span filtresi %+v", f.spanCalls)
	}
	fus, _ := ans["followups"].([]chatFollowup)
	if len(fus) == 0 {
		t.Fatal("kanıt-önce cevap da takip çiplerini taşımalı")
	}
}

// Takip çipleri: 4× genişlet + kıyas (kodek 8. parça) + pod kırılımı (yalnız
// etiket varsa ve doğrulayıcı izin verirse) + aralığın trace/log linkleri (≤4).
func TestMetricPanelFollowups(t *testing.T) {
	sub := aiSubject{Kind: explainAskMetricPanel, ID: "m", From: mpT0, To: mpT0.Add(time.Hour),
		BrushFrom: mpT0.Add(10 * time.Minute), BrushTo: mpT0.Add(20 * time.Minute), Panel: &metricPanelAsk{Src: "promql", PromQL: "x"}}
	r := metricPanelResolved{Query: metricPanelQuery{Src: "promql", PromQL: `rate(x_total{service_name="orders-api"}[5m])`}}
	fus := metricPanelFollowups(sub, r, map[string]bool{metricPanelByPod: true})
	if len(fus) != metricPanelChipsMax {
		t.Fatalf("çip sayısı %d", len(fus))
	}
	wide, _ := parseAiSubject(fus[0].ExplainAsk.Subject)
	if wide.To.Sub(wide.From) != 4*time.Hour || wide.BrushFrom.IsZero() || fus[0].ExplainAsk.Src != "chip" || fus[0].ExplainAsk.Panel == nil {
		t.Fatalf("genişlet %+v", wide)
	}
	cmp, _ := parseAiSubject(fus[1].ExplainAsk.Subject)
	if !cmp.Cmp || !strings.HasSuffix(fus[1].ExplainAsk.Subject, ":cmp") {
		t.Fatalf("kıyas %q", fus[1].ExplainAsk.Subject)
	}
	if fus[2].ExplainAsk == nil || fus[2].ExplainAsk.Panel.By != metricPanelByPod {
		t.Fatalf("pod çipi %+v", fus[2])
	}
	if fus[3].Href == "" || !strings.HasPrefix(fus[3].Href, "/traces?") || !strings.Contains(fus[3].Href, "service=orders-api") || !strings.Contains(fus[3].Href, "range=custom") {
		t.Fatalf("trace linki %+v", fus[3])
	}
	// Kırılım izni yoksa çip yok; brush yoksa link yok; kıyas turunda kıyas çipi yok.
	sub.BrushFrom, sub.BrushTo, sub.Cmp = time.Time{}, time.Time{}, true
	for _, c := range metricPanelFollowups(sub, r, nil) {
		if c.Kind == "metric_panel_by" || c.Href != "" || c.Kind == "metric_panel_compare" {
			t.Fatalf("beklenmeyen çip %+v", c)
		}
	}
}

// PromQL kırılım izni: pod etiketi metrikte VAR ve sarma doğrulayıcıdan geçer.
func TestMetricPanelByAllowed(t *testing.T) {
	s := &Server{}
	f := &mpFake{vm: mpVM()}
	r := metricPanelResolved{Query: metricPanelQuery{Src: "promql", PromQL: `sum(rate(http_server_requests_total[5m]))`}}
	ex, err := s.metricPanelPrepare(context.Background(), f, r, mpT0, mpT0.Add(time.Hour), 30)
	if err != nil || !ex.ByAllowed[metricPanelByPod] {
		t.Fatalf("pod izinli olmalı: %v %+v", err, ex.ByAllowed)
	}
	f.vm.labels["http_server_requests_total"] = []string{"__name__", "service_name"}
	f.vm.lm = vmetrics.LabelMap{}
	s2 := &Server{} // etiket önbelleği yok
	ex, _ = s2.metricPanelPrepare(context.Background(), f, r, mpT0, mpT0.Add(time.Hour), 30)
	if ex.ByAllowed[metricPanelByPod] {
		t.Fatal("pod etiketi yokken kırılım izni verildi")
	}
	r.By = metricPanelByPod
	if _, err := s2.metricPanelPrepare(context.Background(), f, r, mpT0, mpT0.Add(time.Hour), 30); err == nil {
		t.Fatal("izinsiz kırılım reddedilmeli")
	}
}

func TestNormalizeMetricPanelAsk(t *testing.T) {
	subj := mpSubject("x.A", false)
	big := &metricPanelAsk{Src: "promql", PromQL: strings.Repeat("a", metricPanelAskMaxBytes)}
	for _, c := range []struct {
		name string
		in   chatExplainAsk
		ok   bool
	}{
		{"yüksüz (arşiv / derin link)", chatExplainAsk{Kind: explainAskMetricPanel, Subject: subj}, true},
		{"yüklü", chatExplainAsk{Kind: explainAskMetricPanel, Subject: subj, Panel: &metricPanelAsk{Src: "span", Agg: "count"}}, true},
		{"8 KiB üstü", chatExplainAsk{Kind: explainAskMetricPanel, Subject: subj, Panel: big}, false},
		{"bilinmeyen kaynak", chatExplainAsk{Kind: explainAskMetricPanel, Subject: subj, Panel: &metricPanelAsk{Src: "sql"}}, false},
		{"bilinmeyen kırılım", chatExplainAsk{Kind: explainAskMetricPanel, Subject: subj, Panel: &metricPanelAsk{Src: "promql", By: "node"}}, false},
		{"kodeksiz", chatExplainAsk{Kind: explainAskMetricPanel, ID: "m"}, false},
		{"kimlik uyuşmaz", chatExplainAsk{Kind: explainAskMetricPanel, ID: "m", Subject: subj}, false},
	} {
		got, ok := normalizeExplainAsk(&c.in)
		if ok != c.ok {
			t.Errorf("%s: ok=%v", c.name, ok)
		}
		if ok && (got.ID != "x.A" || got.Subject != subj) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
	// Arşiv: künye kalır (renderer beyaz listede), yük künyede yok.
	out := sanitizeChatMessages([]aiChatMessage{{Role: "assistant", Text: "panel",
		Explain: &chatExplainMeta{Kind: explainAskMetricPanel, ID: "x.A", Subject: subj, Renderer: explainRendererMetricPanel, Note: "özet"}}})
	if len(out) != 1 || out[0].Explain == nil || out[0].Explain.Renderer != explainRendererMetricPanel || out[0].Explain.Note != "özet" {
		t.Fatalf("arşiv künyesi %+v", out)
	}
}

// withBlockSeq — metric_panel bloğu sohbetin TEK sıralayıcısından geçer.
func TestWithBlockSeqMetricPanel(t *testing.T) {
	var seq blocks.Sequencer
	var got []map[string]any
	emit := withBlockSeq(func(k string, v any) {
		if k == "block" {
			got = append(got, v.(map[string]any))
		}
	}, &seq)
	emit(blocks.TypeMetricPanel, metricPanelBlockPayload{Title: "p"})
	if len(got) != 1 || got[0]["type"] != "metric_panel" {
		t.Fatalf("blok %v", got)
	}
}

func TestMetricPanelSelectedMatches(t *testing.T) {
	for _, c := range []struct {
		gk  []string
		sel string
		ok  bool
	}{
		{[]string{`pod="pod-a"`}, "pod=pod-a", true},
		{[]string{`pod="pod-a"`}, `pod="pod-a"`, true},
		{[]string{"checkout", "GET"}, "checkout · GET", true},
		{[]string{"checkout", "GET"}, "A: service=checkout, method=GET", true},
		{[]string{"checkout", "GET"}, "checkout", false},
		{[]string{`pod="pod-a"`}, "pod=pod-b", false},
		{nil, "anything", false},
	} {
		if got := metricPanelSelectedMatches(c.gk, metricPanelSeriesName(c.gk, "f"), c.sel); got != c.ok {
			t.Errorf("%v ~ %q = %v", c.gk, c.sel, got)
		}
	}
}

func TestMetricPanelBrushStep(t *testing.T) {
	for _, c := range []struct {
		brush time.Duration
		step  int
		want  int
	}{
		{10 * time.Minute, 180, 15}, // 3 kova < 8 → ince
		{2 * time.Hour, 180, 0},     // 40 kova → yok
		{30 * time.Minute, 60, 0},   // 30 kova ≥ 8 → yok
		{5 * time.Minute, 15, 0},    // pencere adımı zaten en ince
		{6 * time.Hour, 3600, 360},  // 6 kova → 360 sn
	} {
		if got := metricPanelBrushStep(c.brush, c.step); got != c.want {
			t.Errorf("brush %v step %d → %d, want %d", c.brush, c.step, got, c.want)
		}
	}
}

func TestMetricPanelLoc(t *testing.T) {
	for s, ok := range map[string]bool{"m": true, "x.A": true, "x.D": true, "d.abc_1.p-2": true,
		"x.E": false, "x.a": false, "d.a": false, "d.a.b.c": false, "d..b": false, "q": false, "": false} {
		if _, got := parseMetricPanelLoc(s); got != ok {
			t.Errorf("%q → %v", s, got)
		}
	}
}
