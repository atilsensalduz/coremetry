package api

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// otelServerHandler — HTTP sunucusunun en dış katmanı: otelhttp SERVER span'ı.
//
// v0.10.1470 — api.go'daki listen'dan taşındı (test edilebilsin). Yayıcı
// belirtilmez: otelhttp süreç geneli otel.GetTextMapPropagator()'ı kullanır
// ve selfobs.Init trace hattını kurunca onu W3C tracecontext + baggage yapar
// (selfobs/propagation.go) — frontend'in `traceparent`'ı bu span'ın EBEVEYNİ
// olur. Self-obs kapalıyken yayıcı boş: başlık okunmaz (eski davranış).
// opts yalnız testte (tracer provider); üretim çağrısı boş.
func otelServerHandler(h http.Handler, opts ...otelhttp.Option) http.Handler {
	opts = append([]otelhttp.Option{
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			// "GET /api/traces/:id" — method + cardinality-collapsed
			// path. v0.6.46 — the raw r.URL.Path includes path params
			// (trace IDs, service names, problem IDs), so naming the
			// span after it would mint one distinct span name per ID.
			// The spans table's `name` column is LowCardinality(String)
			// — unbounded distinct names degrade it (CLAUDE.md anti-
			// pattern). collapseRoute() rewrites the volatile segments
			// to :id so the span name stays a bounded route template.
			return r.Method + " " + collapseRoute(r.URL.Path)
		}),
	}, opts...)
	return otelhttp.NewHandler(h, "coremetry-api", opts...)
}
