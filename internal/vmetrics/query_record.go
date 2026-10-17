package vmetrics

// query_record.go — v0.10.1271 (CoSRE adım 2): çalışan MetricsQL'in kaydı.
//
// query_metrics'in kanıt çipi (evidence.query) VictoriaMetrics'e GERÇEKTEN
// giden ifadeyi göstermeli; ifadeler Overview metrik kipinin okuyucularında
// (QueryMetricRate / QueryMetricCountRate / QueryMetric) kuruluyor ve dışarı
// dönmüyor. Davranışı değiştirmeden okumak için context'e isteğe bağlı bir
// kaydedici takılır; query_range'e giden her ifade sırayla eklenir. Kaydedici
// yoksa hiçbir şey yapılmaz (sıfır maliyet). Yalnız sorgu METNİ kaydedilir —
// kimlik başlığı/token asla.

import (
	"context"
	"sync"
)

// QueryRecorder — bir okuma boyunca çalışan ifadeler.
type QueryRecorder struct {
	mu      sync.Mutex
	queries []string
}

// Queries — kaydedilen ifadelerin kopyası (çalışma sırasıyla).
func (r *QueryRecorder) Queries() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...)
}

type queryRecorderKey struct{}

// WithQueryRecorder — ctx'e kaydedici takar.
func WithQueryRecorder(ctx context.Context, r *QueryRecorder) context.Context {
	return context.WithValue(ctx, queryRecorderKey{}, r)
}

func recordQuery(ctx context.Context, q string) {
	r, _ := ctx.Value(queryRecorderKey{}).(*QueryRecorder)
	if r == nil {
		return
	}
	r.mu.Lock()
	r.queries = append(r.queries, q)
	r.mu.Unlock()
}
