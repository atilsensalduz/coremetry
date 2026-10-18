package api

// metricsource_fake_test.go — v0.10.1361: metricSource sahte kaynağı ve
// seri fikstürleri. /api/endpoints/metric silinince (liste ve detay HTTP
// Metrics PromQL'inde) endpoints_metric_test.go'dan buraya taşındı;
// tüketiciler service_metric_red_test.go ve messaging_*_test.go.
//
// Sahte kaynak metricSource arayüzünü gömer: yalnız kullanılan yöntemler
// doldurulur, kullanılmayan bir yönteme dokunulursa nil-panic testi kırar
// — sessiz geçiş yok.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeEPSource struct {
	metricSource
	name    string
	unit    string
	keys    []string
	envOK   bool
	rateFn  func(f chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error)
	queryFn func(f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error)
	calls   []string
	filters [][]chstore.FilterExpr
	// v0.10.1350 — veri güdümlü durum yoklaması: rate sorgularının GroupBy'ı
	// ve MetricPresentKeys'in hiç sorulmadığının kanıtı.
	groupBys     [][]string
	presentCalls int
	queries      []chstore.MetricQueryFilter // v0.10.550 — QueryMetric süzgeçleri (Kafka testleri)
	mu           sync.Mutex                  // v0.10.550 — Kafka soruları paralel sorulur
}

func (f *fakeEPSource) Name() string                      { return f.name }
func (f *fakeEPSource) LatencyMetricName(n string) string { return strings.TrimSuffix(n, "_count") }

// v0.10.1358 — imza servis + pencere + üç hâlli PresentKeys.
func (f *fakeEPSource) MetricPresentKeys(context.Context, string, string, []string, time.Time, time.Time) chstore.PresentKeys {
	f.presentCalls++
	return chstore.PresentKeys{Keys: f.keys, State: chstore.PresentKeysKnown, Scope: chstore.PresentKeysScopeService}
}
func (f *fakeEPSource) MetricUnit(context.Context, string, string) string { return f.unit }
func (f *fakeEPSource) EnvFilterExpr(env string) (chstore.FilterExpr, bool) {
	if f.envOK {
		return chstore.FilterExpr{Key: "deployment.environment", Op: "=", Values: []string{env}}, true
	}
	return chstore.FilterExpr{}, false
}
func (f *fakeEPSource) QueryMetricRate(_ context.Context, q chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, "rate:"+mode)
	f.filters = append(f.filters, q.Filters)
	f.groupBys = append(f.groupBys, q.GroupBy)
	return f.rateFn(q, mode)
}
func (f *fakeEPSource) QueryMetricCountRate(_ context.Context, q chstore.MetricQueryFilter, mode string) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, "countrate:"+mode)
	f.filters = append(f.filters, q.Filters)
	f.groupBys = append(f.groupBys, q.GroupBy)
	return f.rateFn(q, mode)
}
func (f *fakeEPSource) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	f.calls = append(f.calls, "query:"+q.Aggregation+":"+q.Name)
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	return f.queryFn(q)
}

func epFixtureWindow() (time.Time, time.Time) {
	to := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	return to.Add(-time.Hour), to
}

// epConst — n nokta, kova BAŞLANGICI damgalı (from + i·step, i = 0..n−1).
// v0.10.504 (dış denetim A6): kaynak artık kova başlangıcını döner
// (vmetrics bucketStartNs); eski `i = 1..n` şekli VM'in kova-sonu
// damgasını taklit ediyordu ve reduce'taki `off--` telafisine yaslanıyordu.
func epConst(gk []string, from time.Time, step, n int, v float64) chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{GroupKey: gk}
	for i := 0; i < n; i++ {
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: from.Add(time.Duration(i*step) * time.Second).UnixNano(), Value: v})
	}
	return s
}

func hasStatusFilter(fs []chstore.FilterExpr) bool {
	for _, f := range fs {
		if strings.Contains(f.Key, "status_code") {
			return true
		}
	}
	return false
}

// statusGroupKey — GroupBy'ın son öğesi bir durum adayıysa o anahtar.
func statusGroupKey(groupBy []string) string {
	if n := len(groupBy); n > 0 {
		for _, k := range httpStatusKeyCandidates {
			if groupBy[n-1] == k {
				return k
			}
		}
	}
	return ""
}
