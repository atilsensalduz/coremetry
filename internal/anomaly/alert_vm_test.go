package anomaly

// v0.10.1489 — alarm kuralı VM kaynağı: metrik eşleme tablosu (her metrik ×
// HTTP / gRPC legacy / gRPC stable), yedek gerekçeleri, aile sırası (asla
// toplanmaz), sorgu sınırları (distinct pencere × aile × tür, tavan, servis
// süzgeci), geri çekilme, önizleme ↔ evaluator kaynak paritesi, taban.

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// ── sahte VM ────────────────────────────────────────────────────────────────

type alertFakeVM struct {
	mu       sync.Mutex
	labels   map[string][]string // probe → etiket adları
	members  map[string][]string // aile ID → servisler (son 1 sa)
	status   map[string][]string // aile ID → durum etiketli servisler
	val      func(fam string, k alertVMKind, svc, phi string, win int) (float64, bool)
	failKind map[alertVMKind]bool // bu türün değer sorgusu hata versin
	instant  []string
	ranges   []string
}

var (
	afWinRe = regexp.MustCompile(`\[(\d+)s\]`)
	afSvcRe = regexp.MustCompile(`service_name=(~?)"([^"]+)"`)
)

func afFamily(q string) string {
	switch {
	case strings.Contains(q, "rpc_server_duration_milliseconds"):
		return "grpc_legacy"
	case strings.Contains(q, "rpc_server_call_duration_seconds"):
		return "grpc_stable"
	}
	return "http"
}

func afKind(q string) alertVMKind {
	switch {
	case strings.Contains(q, "histogram_quantiles"):
		return akQuant
	case strings.Contains(q, `=~"5.."`) || strings.Contains(q, `=~"2|4|8`):
		return akErr
	case strings.Contains(q, "_sum"):
		return akSum
	}
	return akCount
}

func (f *alertFakeVM) Configured() bool                  { return true }
func (f *alertFakeVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *alertFakeVM) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	return f.labels[metric], false, nil
}
func (f *alertFakeVM) MetricLabelValuesIn(_ context.Context, _, _ string, _, _ time.Time, _ int) ([]string, bool, error) {
	return []string{"prod-eu-a"}, false, nil
}

func (f *alertFakeVM) svcAllowed(q, svc string) bool {
	m := afSvcRe.FindStringSubmatch(q)
	if m == nil {
		return true
	}
	if m[1] == "" {
		return m[2] == svc
	}
	for _, s := range strings.Split(m[2], "|") {
		if s == svc {
			return true
		}
	}
	return false
}

func (f *alertFakeVM) series(q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	fam := afFamily(q)
	if strings.HasPrefix(q, "group by") {
		src := f.members[fam]
		if strings.Contains(q, `!=""`) {
			src = f.status[fam]
		}
		var out []chstore.SpanMetricSeries
		for _, s := range src {
			out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + s + `"`}, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: 1}}})
		}
		return out, nil
	}
	k := afKind(q)
	if f.failKind[k] {
		return nil, errors.New("vm 503")
	}
	win := 0
	if m := afWinRe.FindStringSubmatch(q); m != nil {
		win, _ = strconv.Atoi(m[1])
	}
	var out []chstore.SpanMetricSeries
	for _, svc := range f.members[fam] {
		if !f.svcAllowed(q, svc) {
			continue
		}
		phis := []string{""}
		if k == akQuant {
			phis = alertVMPhis
			if strings.Contains(q, ", 1, sum") {
				phis = append(append([]string(nil), alertVMPhis...), "1")
			}
		}
		for _, phi := range phis {
			v, ok := f.val(fam, k, svc, phi, win)
			if !ok {
				continue
			}
			key := []string{`service_name="` + svc + `"`}
			if phi != "" {
				key = append([]string{`phi="` + phi + `"`}, key...)
			}
			out = append(out, chstore.SpanMetricSeries{GroupKey: key, Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: v}}})
		}
	}
	return out, nil
}

func (f *alertFakeVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	f.instant = append(f.instant, q)
	f.mu.Unlock()
	return f.series(q, at)
}

// QueryPromQLRange — her adımda aynı değer (sabit seri); nokta = kova başı.
func (f *alertFakeVM) QueryPromQLRange(_ context.Context, q string, from, to time.Time, step, _ int) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	f.ranges = append(f.ranges, q)
	f.mu.Unlock()
	one, err := f.series(q, from)
	if err != nil {
		return nil, err
	}
	for i := range one {
		v := one[i].Points[0].Value
		one[i].Points = nil
		for ts := from.Unix() + int64(step); ts <= to.Unix(); ts += int64(step) {
			one[i].Points = append(one[i].Points, chstore.SpanMetricPoint{Time: (ts - int64(step)) * int64(time.Second), Value: v})
		}
	}
	return one, nil
}

func (f *alertFakeVM) valueQueries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, q := range f.instant {
		if !strings.HasPrefix(q, "group by") {
			out = append(out, q)
		}
	}
	return out
}

// standardFake — HTTP: a, b (durum etiketli); gRPC legacy: g (+ a: iki ailede
// de görünür → HTTP kazanır, toplanmaz); gRPC stable: s. Sabit değerler.
func standardFake() *alertFakeVM {
	return &alertFakeVM{
		labels: map[string][]string{
			"http.server.request.duration": {"service_name", "http_response_status_code", "le"},
			"rpc.server.duration":          {"service_name", "rpc_grpc_status_code", "le"},
			"rpc.server.call.duration":     {"service_name", "le"}, // durum etiketi YOK
		},
		members: map[string][]string{"http": {"a", "b"}, "grpc_legacy": {"g", "a"}, "grpc_stable": {"s"}},
		status:  map[string][]string{"http": {"a", "b"}, "grpc_legacy": {"g", "a"}},
		val: func(fam string, k alertVMKind, svc, phi string, win int) (float64, bool) {
			base := map[string]float64{"http": 1, "grpc_legacy": 10, "grpc_stable": 100}[fam]
			switch k {
			case akCount:
				return 600 * base, true
			case akErr:
				return 30 * base, true
			case akSum:
				return 60 * base, true // s ya da ms (ailenin birimi)
			case akQuant:
				p, _ := strconv.ParseFloat(phi, 64)
				return p * base, true
			}
			return 0, false
		},
	}
}

func newTestAlertReader(vm REDVM, mode string) *AlertVMReader {
	r := NewAlertVMReader(func() chstore.AnomalySensitivityConfig {
		return chstore.AnomalySensitivityConfig{AlertSource: mode}
	})
	r.SetVM(vm)
	return r
}

var alertTestNow = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// ── metrik eşleme tablosu ──────────────────────────────────────────────────

func TestAlertVMMetricMappingTable(t *testing.T) {
	fake := standardFake()
	r := newTestAlertReader(fake, "auto")
	needs := []AlertVMNeed{}
	metrics := []string{"request_rate", "error_count", "error_rate", "avg_ms", "p50_ms", "p95_ms", "p99_ms"}
	for _, m := range metrics {
		needs = append(needs, AlertVMNeed{Metric: m, WindowSec: 300})
	}
	tick := r.Prepare(context.Background(), needs, alertTestNow)
	cases := []struct {
		svc, fam, metric string
		want             float64
	}{
		// HTTP (s): count 600, err 30, sum 60 s, quant phi·1 s
		{"b", "HTTP", "request_rate", 2},
		{"b", "HTTP", "error_count", 30},
		{"b", "HTTP", "error_rate", 5},
		{"b", "HTTP", "avg_ms", 100}, // 60 s / 600 · 1000
		{"b", "HTTP", "p50_ms", 500},
		{"b", "HTTP", "p95_ms", 950},
		{"b", "HTTP", "p99_ms", 990},
		// gRPC legacy (ms): count 6000, err 300, sum 600 ms, quant phi·10 ms
		{"g", "gRPC", "request_rate", 20},
		{"g", "gRPC", "error_count", 300},
		{"g", "gRPC", "error_rate", 5},
		{"g", "gRPC", "avg_ms", 0.1},
		{"g", "gRPC", "p99_ms", 9.9},
		// gRPC stable (s): count 60000, sum 6000 s, quant phi·100 s
		{"s", "gRPC", "request_rate", 200},
		{"s", "gRPC", "avg_ms", 100},
		{"s", "gRPC", "p95_ms", 95000},
	}
	for _, c := range cases {
		m := tick.Measure(c.svc, c.metric, 300)
		if !m.Source.VM || m.Source.Family != c.fam {
			t.Errorf("%s/%s: kaynak %+v, beklenen VM %s", c.svc, c.metric, m.Source, c.fam)
			continue
		}
		if !m.Present || math.Abs(m.Value-c.want) > 1e-9 {
			t.Errorf("%s/%s = %v (present %v), beklenen %v", c.svc, c.metric, m.Value, m.Present, c.want)
		}
	}
	// a hem HTTP'de hem gRPC legacy'de: HTTP kazanır, değerler TOPLANMAZ.
	if m := tick.Measure("a", "request_rate", 300); m.Source.Family != "HTTP" || m.Value != 2 {
		t.Errorf("aile sırası/toplanmama: %+v", m)
	}
	// minSamples sayımı = pencerede gerçek istek sayısı (aynı kaynak).
	if m := tick.Measure("g", "p99_ms", 300); m.Count != 6000 {
		t.Errorf("sayım: %d", m.Count)
	}
	// gRPC stable'da durum etiketi yok → hata metrikleri span, gerekçeli; hız VM.
	for _, metric := range []string{"error_rate", "error_count"} {
		if m := tick.Measure("s", metric, 300); m.Source.VM || m.Source.Reason != redReasonNoStatus {
			t.Errorf("s/%s: %+v", metric, m.Source)
		}
	}
}

func TestAlertVMExprShapes(t *testing.T) {
	for _, f := range vmREDFamilies {
		sc := vmQueryScope{SvcLabel: "service_name", StatusLabels: f.StatusLabels}
		q, _ := alertVMExpr(f, akQuant, sc, 600, "", nil)
		if !strings.HasPrefix(q, `histogram_quantiles("phi", 0.5, 0.95, 0.99, sum by (service_name, le) (increase(`) || !strings.Contains(q, "[600s]") {
			t.Errorf("%s yüzdelik: %s", f.ID, q)
		}
		q, _ = alertVMExpr(f, akErr, sc, 600, `service_name="x"`, nil)
		if !strings.Contains(q, `=~"`+f.ServerErr+`"`) || !strings.Contains(q, `service_name="x"`) {
			t.Errorf("%s hata: %s", f.ID, q)
		}
		if _, ok := alertVMExpr(f, akErr, vmQueryScope{SvcLabel: "service_name"}, 600, "", nil); ok {
			t.Errorf("%s: durum etiketi yokken hata ifadesi kurulmamalı", f.ID)
		}
	}
	// gRPC aileleri tam ad: ms ↔ s tek alternasyonda toplanmaz.
	q, _ := alertVMExpr(vmREDFamilies[1], akCount, vmQueryScope{SvcLabel: "service_name"}, 300, "", nil)
	if !strings.Contains(q, `{__name__="rpc_server_duration_milliseconds_count"`) {
		t.Errorf("legacy tam ad: %s", q)
	}
}

func TestAlertVMValueAbsentSemantics(t *testing.T) {
	// Pencerede istek yok: sayım türleri 0 (request_rate < kuralı sessizlikte
	// ateşler), oranlar ölçüsüz (span: NULL → atla), yüzdelik ölçüsüz.
	empty := alertVMSample{WindowSec: 300, Scale: 1000}
	for metric, want := range map[string]bool{"request_rate": true, "error_count": true, "error_rate": false, "avg_ms": false, "p99_ms": false} {
		_, ok := alertVMValue(metric, empty)
		if ok != want {
			t.Errorf("%s present=%v, beklenen %v", metric, ok, want)
		}
	}
	if v, _ := alertVMValue("error_rate", alertVMSample{Count: 10, HaveCount: true, Err: 50, HaveErr: true}); v != 100 {
		t.Errorf("oran %%100'de kelepçeli olmalı: %v", v)
	}
}

// ── yedek gerekçeleri ─────────────────────────────────────────────────────

func TestPickAlertSourceReasons(t *testing.T) {
	ok := alertPickInput{Configured: true, MetricSupported: true, WindowOK: true, FamilyFound: true, KindsOK: true}
	cases := []struct {
		name   string
		mut    func(*alertPickInput)
		vm     bool
		reason string
	}{
		{"vm", func(*alertPickInput) {}, true, ""},
		{"ayar span", func(i *alertPickInput) { i.SpanSetting = true }, false, alertReasonSpanSetting},
		{"vm yok", func(i *alertPickInput) { i.Configured = false }, false, redReasonNotConfigured},
		{"taşıma metriği", func(i *alertPickInput) { i.MetricSupported = false }, false, alertReasonMetric},
		{"kısa pencere", func(i *alertPickInput) { i.WindowOK = false }, false, alertReasonWindow},
		{"aile yok", func(i *alertPickInput) { i.FamilyFound = false }, false, redReasonNoService},
		{"aile gerekçesi", func(i *alertPickInput) { i.FamilyFound, i.FamilyReason = false, redReasonNoFamily }, false, redReasonNoFamily},
		{"tür", func(i *alertPickInput) { i.KindsOK, i.KindReason = false, redReasonNoStatus }, false, redReasonNoStatus},
	}
	for _, c := range cases {
		in := ok
		c.mut(&in)
		vm, reason := pickAlertSource(in)
		if vm != c.vm || reason != c.reason {
			t.Errorf("%s: (%v, %q), beklenen (%v, %q)", c.name, vm, reason, c.vm, c.reason)
		}
	}
}

func TestAlertVMFallbackReasonsEndToEnd(t *testing.T) {
	fake := standardFake()
	tick := newTestAlertReader(fake, "auto").Prepare(context.Background(), []AlertVMNeed{{Metric: "error_rate", WindowSec: 300}}, alertTestNow)
	cases := []struct {
		svc, metric string
		win         int
		reason      string
	}{
		{"yok", "error_rate", 300, redReasonNoService},
		{"b", "http_p99_ms", 300, alertReasonMetric},
		{"b", "error_rate", 60, alertReasonWindow},
		{"b", "p99_ms", 900, alertReasonBudget}, // bu tikte planlanmamış pencere/tür
	}
	for _, c := range cases {
		m := tick.Measure(c.svc, c.metric, c.win)
		if m.Source.VM || m.Source.Reason != c.reason {
			t.Errorf("%s/%s/%d: %+v, beklenen %q", c.svc, c.metric, c.win, m.Source, c.reason)
		}
		if ev := m.Source.Evidence(); !strings.HasPrefix(ev, "kaynak: span (örneklenmiş) — VM kullanılmadı: ") {
			t.Errorf("kanıt satırı: %q", ev)
		}
	}
	if ev := tick.Measure("b", "error_rate", 300).Source.Evidence(); ev != "kaynak: VictoriaMetrics (örneklenmemiş) · HTTP sunucu histogramı" {
		t.Errorf("VM kanıt satırı: %q", ev)
	}
	span := newTestAlertReader(fake, "span").Prepare(context.Background(), nil, alertTestNow)
	if m := span.Measure("b", "error_rate", 300); m.Source.VM || m.Source.Reason != alertReasonSpanSetting {
		t.Errorf("ayar span: %+v", m.Source)
	}
	var nilTick *AlertVMTick
	if m := nilTick.Measure("b", "error_rate", 300); m.Source.VM || m.Source.Reason != alertReasonNoTick {
		t.Errorf("nil tik: %+v", m.Source)
	}
}

// ── sorgu sınırları + geri çekilme ────────────────────────────────────────

func TestAlertVMQueryBounds(t *testing.T) {
	fake := standardFake()
	r := newTestAlertReader(fake, "auto")
	// Aynı (metrik, pencere) üzerinde 50 kural → sorgu sayısı kural sayısıyla büyümez.
	var needs []AlertVMNeed
	for i := 0; i < 50; i++ {
		needs = append(needs, AlertVMNeed{Metric: "error_rate", WindowSec: 300}, AlertVMNeed{Metric: "p99_ms", WindowSec: 600})
	}
	r.Prepare(context.Background(), needs, alertTestNow)
	got := len(fake.valueQueries())
	// 300 s: count+err × (http, legacy) + count × stable(err yok) = 5;
	// 600 s: count+quant × 3 aile = 6.
	if got != 11 {
		t.Fatalf("değer sorgusu %d, beklenen 11:\n%s", got, strings.Join(fake.valueQueries(), "\n"))
	}
	for _, q := range fake.valueQueries() {
		if strings.Contains(q, "service_name=") {
			t.Errorf("joker kural varken servis süzgeci olmamalı: %s", q)
		}
	}
	// Tüm kurallar servise sabit → servis süzgeci.
	fake2 := standardFake()
	newTestAlertReader(fake2, "auto").Prepare(context.Background(), []AlertVMNeed{{Metric: "request_rate", WindowSec: 300, Services: []string{"a"}}, {Metric: "request_rate", WindowSec: 300, Services: []string{"b"}}}, alertTestNow)
	for _, q := range fake2.valueQueries() {
		if !strings.Contains(q, `service_name=~"a|b"`) {
			t.Errorf("sabit servis süzgeci: %s", q)
		}
	}
	// Tavan: çok pencere → ≤ alertVMMaxQueriesPerTick, kalanlar bütçe gerekçesiyle span.
	fake3 := standardFake()
	var many []AlertVMNeed
	for w := 120; w < 120+60*20; w += 60 {
		many = append(many, AlertVMNeed{Metric: "p99_ms", WindowSec: w}, AlertVMNeed{Metric: "error_rate", WindowSec: w})
	}
	tick := newTestAlertReader(fake3, "auto").Prepare(context.Background(), many, alertTestNow)
	if n := len(fake3.valueQueries()); n != alertVMMaxQueriesPerTick {
		t.Fatalf("tavan: %d sorgu", n)
	}
	if tick.stats.Skipped == 0 {
		t.Fatal("bütçe dışı kalan sayılmalı")
	}
	if m := tick.Measure("b", "p99_ms", 120+60*19); m.Source.VM || m.Source.Reason != alertReasonBudget {
		t.Errorf("bütçe dışı: %+v", m.Source)
	}
}

func TestAlertVMBackoffFallsBackAndSkipsQueries(t *testing.T) {
	fake := standardFake()
	fake.failKind = map[alertVMKind]bool{akErr: true}
	r := newTestAlertReader(fake, "auto")
	needs := []AlertVMNeed{{Metric: "error_rate", WindowSec: 300}, {Metric: "request_rate", WindowSec: 300}}
	tick := r.Prepare(context.Background(), needs, alertTestNow)
	if m := tick.Measure("b", "error_rate", 300); m.Source.VM || !strings.HasPrefix(m.Source.Reason, redReasonVMError) {
		t.Fatalf("hata veren tür span'e düşmeli: %+v", m.Source)
	}
	if m := tick.Measure("b", "request_rate", 300); !m.Source.VM {
		t.Fatalf("hata vermeyen tür VM kalmalı: %+v", m.Source)
	}
	before := len(fake.valueQueries())
	tick2 := r.Prepare(context.Background(), needs, alertTestNow.Add(time.Minute))
	for _, q := range fake.valueQueries()[before:] {
		if afKind(q) == akErr {
			t.Fatalf("geri çekilmede tür yeniden sorgulanmamalı: %s", q)
		}
	}
	if m := tick2.Measure("b", "error_rate", 300); m.Source.VM || !strings.Contains(m.Source.Reason, "(err)") {
		t.Fatalf("geri çekilme gerekçesi: %+v", m.Source)
	}
	// Önizleme de aynı kararı verir (aynı okuyucu, aynı geri çekilme).
	r.clock = func() time.Time { return alertTestNow.Add(time.Minute) }
	if src := r.SourceFor(context.Background(), "b", "error_rate", 300); src != tick2.Measure("b", "error_rate", 300).Source {
		t.Fatalf("önizleme ≠ evaluator: %+v", src)
	}
}

// ── önizleme ↔ evaluator paritesi ─────────────────────────────────────────

func TestAlertPreviewEvaluatorSourceParity(t *testing.T) {
	fake := standardFake()
	r := newTestAlertReader(fake, "auto")
	r.clock = func() time.Time { return alertTestNow }
	metrics := []string{"request_rate", "error_count", "error_rate", "avg_ms", "p50_ms", "p95_ms", "p99_ms", "http_p99_ms"}
	var needs []AlertVMNeed
	for _, m := range metrics {
		needs = append(needs, AlertVMNeed{Metric: m, WindowSec: 300})
	}
	tick := r.Prepare(context.Background(), needs, alertTestNow)
	vnow := alertTestNow.Add(-vmIngestLag)
	from := vnow.Add(-time.Hour).Truncate(300 * time.Second)
	for _, svc := range []string{"a", "b", "g", "s", "yok"} {
		for _, metric := range metrics {
			live := tick.Measure(svc, metric, 300)
			if src := r.SourceFor(context.Background(), svc, metric, 300); src != live.Source {
				t.Errorf("%s/%s kaynak: önizleme %+v, evaluator %+v", svc, metric, src, live.Source)
			}
			pts, src, used := r.Series(context.Background(), svc, metric, 300, from, alertTestNow, 300)
			if used != live.Source.VM || src != live.Source {
				t.Errorf("%s/%s dizi kaynağı: %+v used=%v, evaluator %+v", svc, metric, src, used, live.Source)
				continue
			}
			if !used {
				continue
			}
			// Aynı seri → aynı değer (sabit seri: her nokta evaluator'ın değeri).
			if len(pts) == 0 {
				t.Errorf("%s/%s: nokta yok", svc, metric)
				continue
			}
			for _, p := range pts {
				if p.V == nil || math.Abs(*p.V-live.Value) > 1e-9 {
					t.Errorf("%s/%s nokta %v ≠ evaluator %v", svc, metric, p.V, live.Value)
					break
				}
				if p.T > vnow.UnixNano() {
					t.Errorf("VM gecikmesinin ötesi çizilmemeli: %d > %d", p.T, vnow.UnixNano())
				}
			}
		}
	}
}

func TestAlertVMBaselineSourceAndShape(t *testing.T) {
	fake := standardFake()
	r := newTestAlertReader(fake, "auto")
	r.clock = func() time.Time { return alertTestNow }
	b, src, used := r.Baseline(context.Background(), "b", "error_rate", 300)
	if !used || !src.VM || b == nil {
		t.Fatalf("VM tabanı: %+v used=%v", src, used)
	}
	if b.P50 != 5 || b.P99 != 5 || b.BucketSec != 300 || b.SampleCount != 2016 {
		t.Errorf("oran tabanı: %+v", b)
	}
	lb, _, used := r.Baseline(context.Background(), "b", "p99_ms", 300)
	if !used || lb.P50 != 500 || lb.P99 != 990 || lb.Max != 1000 || lb.Mean != 100 || lb.SampleCount != 600 || lb.BucketSec != 0 {
		t.Errorf("gecikme tabanı: %+v", lb)
	}
	if _, src, used := r.Baseline(context.Background(), "", "error_rate", 300); used || src.VM {
		t.Error("genel taban (servis yok) span olmalı")
	}
	if _, src, used := r.Baseline(context.Background(), "s", "error_rate", 300); used || src.Reason != redReasonNoStatus {
		t.Errorf("durum etiketsiz ailede hata tabanı span: %+v", src)
	}
}

func TestAlertVMQuantilesHelper(t *testing.T) {
	q := alertVMQuantiles([]float64{1, 2, 3, 4, 5}, 0.5, 0, 1)
	if q[0] != 3 || q[1] != 1 || q[2] != 5 {
		t.Errorf("yüzdelik: %v", q)
	}
	if q := alertVMQuantiles(nil, 0.5); q[0] != 0 {
		t.Errorf("boş: %v", q)
	}
}
