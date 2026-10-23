package selfobs

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
)

// v0.10.1470 — /ai "Trace" linki kapısı: yalnız kendi alıcısına (loopback)
// giden ya da açıkça "yerel" ilan edilmiş self-obs trace'i sorgulanabilir.
func TestTracesLocalTable(t *testing.T) {
	for _, tc := range []struct {
		endpoint, override string
		want               bool
	}{
		{"localhost:4317", "", true},
		{"LOCALHOST:4317", "", true},
		{"127.0.0.1:4317", "", true},
		{"127.0.0.2:4317", "", true},
		{"[::1]:4317", "", true},
		{"http://localhost:4317", "", true},
		{"localhost", "", true},
		{"coremetry-otelcol:4317", "", false},
		{"otel.example.test:4317", "", false},
		{"10.0.0.5:4317", "", false},
		{"", "", false},
		{"coremetry-otelcol:4317", "true", true},
		{"coremetry-otelcol:4317", " Yes ", true},
		{"localhost:4317", "false", false},
		{"localhost:4317", "off", false},
		{"localhost:4317", "maybe", true}, // tanınmayan → otomatik
	} {
		if got := tracesLocal(tc.endpoint, tc.override); got != tc.want {
			t.Errorf("tracesLocal(%q, %q) = %v, want %v", tc.endpoint, tc.override, got, tc.want)
		}
	}
}

// Init: self-obs KAPALIYKEN süreç geneli yayıcı boş kalır (eski davranış,
// TracesQueryable false); trace hattı kurulunca tracecontext + baggage.
func TestInitInstallsW3CPropagatorOnlyWhenEnabled(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	t.Cleanup(func() {
		otel.SetTextMapPropagator(prev)
		enabled, tracesQueryable, tpShutdown, mpShutdown = false, false, nil, nil
		tracerSink = noop.NewTracerProvider().Tracer("coremetry")
	})
	shutdown := func(f func(context.Context) error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = f(ctx)
	}

	t.Setenv(envEndpoint, "")
	shutdown(Init(context.Background(), "api", "test"))
	if f := otel.GetTextMapPropagator().Fields(); len(f) != 0 {
		t.Fatalf("self-obs kapalıyken yayıcı kuruldu: %v", f)
	}
	if TracesQueryable() {
		t.Fatal("self-obs kapalıyken TracesQueryable true")
	}

	t.Setenv(envEndpoint, "127.0.0.1:1") // dial tembel; dışarı çıkmaz
	t.Setenv(envTracesLocal, "")
	shutdown(Init(context.Background(), "all", "test"))
	f := otel.GetTextMapPropagator().Fields()
	for _, want := range []string{"traceparent", "tracestate", "baggage"} {
		if !slices.Contains(f, want) {
			t.Fatalf("yayıcı alanı %q yok: %v", want, f)
		}
	}
	if !TracesQueryable() {
		t.Fatal("loopback uç noktası sorgulanabilir sayılmalı")
	}
}
