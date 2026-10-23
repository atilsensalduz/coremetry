package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// v0.10.1470 — frontend `traceparent`'ı backend trace'ine katılır: selfobs.Init
// süreç geneli yayıcıyı W3C yapınca otelServerHandler'ın SERVER span'ı gelen
// başlığın çocuğu olur. Yayıcı boşken (self-obs kapalı = eski davranış) başlık
// okunmaz, span kendi kökünü açar.
func TestOtelServerHandlerJoinsIncomingTraceparent(t *testing.T) {
	const (
		parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
		parentSpan  = "00f067aa0ba902b7"
	)
	prev := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	run := func(p propagation.TextMapPropagator) (tracetest.SpanStub, string) {
		otel.SetTextMapPropagator(p)
		exp := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		t.Cleanup(func() { _ = tp.Shutdown(t.Context()) })
		var inner string
		h := otelServerHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inner = trace.SpanContextFromContext(r.Context()).TraceID().String()
		}), otelhttp.WithTracerProvider(tp))
		req := httptest.NewRequest(http.MethodGet, "/api/traces/"+parentTrace, nil)
		req.Header.Set("traceparent", "00-"+parentTrace+"-"+parentSpan+"-01")
		req.Header.Set("baggage", "coremetry.page=trace")
		h.ServeHTTP(httptest.NewRecorder(), req)
		spans := exp.GetSpans()
		if len(spans) != 1 {
			t.Fatalf("1 SERVER span bekleniyordu, %d", len(spans))
		}
		return spans[0], inner
	}

	sp, inner := run(selfobs.Propagator())
	if got := sp.SpanContext.TraceID().String(); got != parentTrace || inner != parentTrace {
		t.Fatalf("SERVER span gelen trace'e katılmadı: span=%s handler=%s", got, inner)
	}
	if sp.Parent.SpanID().String() != parentSpan || !sp.Parent.IsRemote() {
		t.Fatalf("ebeveyn tarayıcı span'ı olmalı: %v", sp.Parent)
	}
	if sp.SpanKind != trace.SpanKindServer || sp.Name != "GET /api/traces/:id" {
		t.Fatalf("span adı/türü değişti: %s %v", sp.Name, sp.SpanKind)
	}

	sp, _ = run(propagation.NewCompositeTextMapPropagator())
	if sp.SpanContext.TraceID().String() == parentTrace || sp.Parent.IsValid() {
		t.Fatal("yayıcı boşken (self-obs kapalı) başlık okunmamalı")
	}

	// Üretim yolu bu sarmalayıcıdan geçer, kendi yayıcısını vermez.
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "handler := otelServerHandler(s.cors.middleware(") {
		t.Fatal("listen otelServerHandler'dan geçmiyor")
	}
}
