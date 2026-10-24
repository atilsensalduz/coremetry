package evaluator

// v0.10.1489 — alarm kuralı ölçü kaynağı: eşdeğerlik (aynı seri → aynı
// ateşle/ateşleme kararı, kaynaktan bağımsız), kaynak karışmazlığı (VM
// seçilince span prefetch'i okunmaz; VM'in bir türü düşerse çift BÜTÜNÜYLE
// span), ihtiyaç toplama.

import (
	"context"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// evalFakeVM — tek HTTP ailesi, tek servis "svc"; tür sorgu metninden.
type evalFakeVM struct {
	count, errs, sum float64
	p99              float64
	failErr          bool
}

var evalPhiRe = regexp.MustCompile(`"phi", ([0-9., ]+), sum`)

func (f *evalFakeVM) Configured() bool                  { return true }
func (f *evalFakeVM) MetricLabelMap() vmetrics.LabelMap { return vmetrics.LabelMap{} }
func (f *evalFakeVM) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	if metric == "http.server.request.duration" {
		return []string{"service_name", "http_response_status_code", "le"}, false, nil
	}
	return nil, false, nil
}
func (f *evalFakeVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *evalFakeVM) QueryPromQLRange(context.Context, string, time.Time, time.Time, int, int) ([]chstore.SpanMetricSeries, error) {
	return nil, errors.New("kullanılmaz")
}
func (f *evalFakeVM) QueryPromQLInstant(_ context.Context, q string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	pt := func(v float64, extra ...string) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: append(extra, `service_name="svc"`), Points: []chstore.SpanMetricPoint{{Time: at.UnixNano(), Value: v}}}
	}
	switch {
	case strings.HasPrefix(q, "group by"):
		return []chstore.SpanMetricSeries{pt(1)}, nil
	case strings.Contains(q, "histogram_quantiles"):
		var out []chstore.SpanMetricSeries
		if m := evalPhiRe.FindStringSubmatch(q); m != nil {
			for _, phi := range strings.Split(m[1], ", ") {
				if phi == "0.99" {
					out = append(out, pt(f.p99, `phi="0.99"`))
				} else {
					out = append(out, pt(f.p99/2, `phi="`+phi+`"`))
				}
			}
		}
		return out, nil
	case strings.Contains(q, `=~"5.."`):
		if f.failErr {
			return nil, errors.New("vm 503")
		}
		return []chstore.SpanMetricSeries{pt(f.errs)}, nil
	case strings.Contains(q, "_sum"):
		return []chstore.SpanMetricSeries{pt(f.sum)}, nil
	}
	return []chstore.SpanMetricSeries{pt(f.count)}, nil
}

func vmTickFor(t *testing.T, f *evalFakeVM, rules []chstore.AlertRule) *anomaly.AlertVMTick {
	t.Helper()
	r := anomaly.NewAlertVMReader(func() chstore.AnomalySensitivityConfig { return chstore.DefaultAnomalySensitivity() })
	r.SetVM(f)
	return r.Prepare(context.Background(), collectAlertVMNeeds(rules), time.Now())
}

func spanPre(metric string, window int, val *float64, count uint64) *tickMeasures {
	k := measureKey{Metric: metric, WindowSec: window}
	pre := &tickMeasures{values: map[measureKey]map[string]float64{k: {}}, failed: map[measureKey]bool{},
		counts: map[int]map[string]uint64{window: {"svc": count}}, countsFailed: map[int]bool{}}
	if val != nil {
		pre.values[k]["svc"] = *val
	}
	return pre
}

func fp(v float64) *float64 { return &v }

// TestRuleMeasurementEquivalence — aynı pencere değeri + aynı istek sayısı →
// VM ve span yolu kapılara AYNI ölçüyü verir (aynı ateşle/ateşleme kararı).
func TestRuleMeasurementEquivalence(t *testing.T) {
	// VM: count 600 (5 dk), err 30, sum 60 s, p99 0.99 s → 2/s, 30, %5, 100 ms, 990 ms.
	f := &evalFakeVM{count: 600, errs: 30, sum: 60, p99: 0.99}
	cases := []struct {
		metric     string
		spanVal    *float64
		minSamples uint32
		threshold  float64
	}{
		{"request_rate", fp(2), 0, 1},
		{"error_count", fp(30), 0, 50},
		{"error_rate", fp(5), 100, 4},
		{"error_rate", fp(5), 1000, 4}, // taban altı (600 < 1000) iki yolda da
		{"avg_ms", fp(100), 0, 150},
		{"p99_ms", fp(990), 10, 500},
	}
	e := &Evaluator{}
	for _, c := range cases {
		r := chstore.AlertRule{ID: "r", Metric: c.metric, Comparator: ">", Threshold: c.threshold, WindowSec: 300, MinSamples: c.minSamples, Enabled: true}
		vt := vmTickFor(t, f, []chstore.AlertRule{r})
		vm := e.measureRule(context.Background(), r, "svc", spanPre(c.metric, 300, fp(-1), 0), vt)
		if !vm.src.VM {
			t.Fatalf("%s: VM seçilmeliydi: %+v", c.metric, vm.src)
		}
		span := e.spanRuleMeasurement(context.Background(), r, "svc", spanPre(c.metric, 300, c.spanVal, 600))
		if vm.belowFloor != span.belowFloor || vm.evaluate != span.evaluate || math.Abs(vm.value-span.value) > 1e-9 {
			t.Errorf("%s/min=%d: VM %+v ≠ span %+v", c.metric, c.minSamples, vm, span)
			continue
		}
		if vm.evaluate && compare(vm.value, r.Comparator, r.Threshold) != compare(span.value, r.Comparator, r.Threshold) {
			t.Errorf("%s: karar ayrıştı", c.metric)
		}
	}
	// Pencerede ölçü yok (oran) → iki yol da atlar; sayım → iki yol da 0.
	quiet := &evalFakeVM{count: 0}
	for metric, want := range map[string]bool{"error_rate": false, "request_rate": true} {
		r := chstore.AlertRule{ID: "q", Metric: metric, Comparator: "<", Threshold: 1, WindowSec: 300, Enabled: true}
		vm := e.measureRule(context.Background(), r, "svc", nil, vmTickFor(t, quiet, []chstore.AlertRule{r}))
		span := e.spanRuleMeasurement(context.Background(), r, "svc", spanPre(metric, 300, nil, 0))
		if vm.evaluate != want || span.evaluate != want || vm.value != span.value {
			t.Errorf("%s sessiz: VM %+v span %+v", metric, vm, span)
		}
	}
}

// TestRuleMeasurementNeverMixesSources — VM seçilince span prefetch'i (değer de
// sayım da) HİÇ okunmaz; VM'in gereken bir türü bu tikte düşerse çift
// BÜTÜNÜYLE span'den (değer + minSamples sayımı birlikte).
func TestRuleMeasurementNeverMixesSources(t *testing.T) {
	e := &Evaluator{}
	r := chstore.AlertRule{ID: "r", Metric: "error_rate", Comparator: ">", Threshold: 4, WindowSec: 300, MinSamples: 100, Enabled: true}
	// Span prefetch'i "okuma hatası" — okunsaydı ölçü atlanırdı.
	broken := &tickMeasures{values: map[measureKey]map[string]float64{}, failed: map[measureKey]bool{{Metric: "error_rate", WindowSec: 300}: true},
		counts: map[int]map[string]uint64{}, countsFailed: map[int]bool{300: true}}
	m := e.measureRule(context.Background(), r, "svc", broken, vmTickFor(t, &evalFakeVM{count: 600, errs: 60}, []chstore.AlertRule{r}))
	if !m.src.VM || !m.evaluate || m.value != 10 || m.belowFloor {
		t.Fatalf("VM ölçüsü span prefetch'inden bağımsız olmalı: %+v", m)
	}
	// Örneklenmiş span sayımı (30) tabanın altında, VM'in gerçek sayımı (600) üstünde:
	// VM seçildiyse taban VM sayımıyla değerlendirilir.
	sampled := spanPre("error_rate", 300, fp(40), 30)
	m = e.measureRule(context.Background(), r, "svc", sampled, vmTickFor(t, &evalFakeVM{count: 600, errs: 60}, []chstore.AlertRule{r}))
	if !m.src.VM || m.belowFloor || m.value != 10 {
		t.Fatalf("taban VM sayımından: %+v", m)
	}
	// VM hata türü düştü → değer de sayım da span'den (40 / 30 → taban altı).
	m = e.measureRule(context.Background(), r, "svc", sampled, vmTickFor(t, &evalFakeVM{count: 600, errs: 60, failErr: true}, []chstore.AlertRule{r}))
	if m.src.VM || !m.belowFloor || !strings.HasPrefix(m.src.Reason, "VictoriaMetrics okunamadı") {
		t.Fatalf("VM türü düşünce çift bütünüyle span: %+v", m)
	}
	// Okuyucu yok (nil tik) = bugünkü span yolu, gerekçeli.
	m = e.measureRule(context.Background(), r, "svc", spanPre("error_rate", 300, fp(40), 500), nil)
	if m.src.VM || m.value != 40 || m.src.Reason == "" {
		t.Fatalf("nil tik: %+v", m)
	}
}

func TestCollectAlertVMNeeds(t *testing.T) {
	rules := []chstore.AlertRule{
		{ID: "1", Metric: "error_rate", WindowSec: 300, Enabled: true},
		{ID: "2", Metric: "p99_ms", WindowSec: 600, Service: "a", Enabled: true},
		{ID: "3", Metric: "error_rate", WindowSec: 300, Enabled: false},
		{ID: "4", Metric: "log_query", WindowSec: 300, LogQuery: "x", Enabled: true},
		{ID: "5", Metric: "watcher", WindowSec: 300, WatcherJSON: "{}", Enabled: true},
		{ID: "6", Metric: "db_p99_ms", WindowSec: 300, Target: &chstore.RuleTarget{}, Enabled: true},
	}
	got := collectAlertVMNeeds(rules)
	if len(got) != 2 || got[0].Metric != "error_rate" || got[0].Services != nil || got[1].Services[0] != "a" {
		t.Fatalf("ihtiyaçlar: %+v", got)
	}
}

func TestProblemDescriptionCarriesSourceLine(t *testing.T) {
	r := chstore.AlertRule{Name: "Err", Metric: "error_rate", Comparator: ">", Threshold: 5, WindowSec: 300}
	vm := anomaly.WithAlertSourceEvidence(describeProblem(r, "svc", 7), anomaly.AlertSource{VM: true, Family: "HTTP"})
	if !strings.HasSuffix(vm, "kaynak: VictoriaMetrics (örneklenmemiş) · HTTP sunucu histogramı.") {
		t.Errorf("VM satırı: %q", vm)
	}
	// VM hiç yapılandırılmamış → gerekçe gizli (2026-10-24); başka gerekçe görünür.
	sp := anomaly.WithAlertSourceEvidence(describeProblem(r, "svc", 7), anomaly.SpanAlertSource("VictoriaMetrics yapılandırılmamış"))
	if !strings.HasSuffix(sp, "kaynak: span (örneklenmiş).") || strings.Contains(sp, "VM kullanılmadı") {
		t.Errorf("span satırı: %q", sp)
	}
	fail := anomaly.WithAlertSourceEvidence(describeProblem(r, "svc", 7), anomaly.SpanAlertSource("VictoriaMetrics okunamadı (okunamadı ≠ yok): timeout"))
	if !strings.HasSuffix(fail, "kaynak: span (örneklenmiş) — VM kullanılmadı: VictoriaMetrics okunamadı (okunamadı ≠ yok): timeout.") {
		t.Errorf("hata satırı: %q", fail)
	}
}
