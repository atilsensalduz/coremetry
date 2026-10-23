package selfobs

// v0.10.1463 (D1, karar 7) — sre.investigation kökü HER ZAMAN örneklenir,
// geri kalan kökler eski oranla; çocuklar kökü izler.

import (
	"context"
	"reflect"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestParseAlwaysSample(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want []string
	}{
		{"", []string{"sre.investigation"}},
		{"  ", []string{"sre.investigation"}},
		{"-", nil},
		{"sre.investigation, ai.chat ,", []string{"sre.investigation", "ai.chat"}},
	} {
		if got := parseAlwaysSample(c.raw); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseAlwaysSample(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestRootSamplerTable(t *testing.T) {
	tid := trace.TraceID{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 1} // oran eşiğinin üstü
	for _, c := range []struct {
		name   string
		always []string
		ratio  float64
		span   string
		want   sdktrace.SamplingDecision
	}{
		{"always adı oran 0'da da örneklenir", []string{"sre.investigation"}, 0, "sre.investigation", sdktrace.RecordAndSample},
		{"başka kök oran 0 → düşer", []string{"sre.investigation"}, 0, "HTTP GET", sdktrace.Drop},
		{"başka kök oran 1 → örneklenir", []string{"sre.investigation"}, 1, "HTTP GET", sdktrace.RecordAndSample},
		{"liste boş → always adı da orana tabi", nil, 0, "sre.investigation", sdktrace.Drop},
		{"ad tam eşleşir, önek değil", []string{"sre.investigation"}, 0, "sre.investigation.x", sdktrace.Drop},
	} {
		s := newRootSampler(c.always, c.ratio)
		got := s.ShouldSample(sdktrace.SamplingParameters{ParentContext: context.Background(), TraceID: tid, Name: c.span})
		if got.Decision != c.want {
			t.Errorf("%s: karar %v, want %v", c.name, got.Decision, c.want)
		}
	}
}

// Uçtan uca: ParentBased sarmalı — kök always ise çocukları (sre.step,
// clickhouse.query) da örneklenir; oran 0'da başka kökün çocuğu düşer.
func TestSelfObsSamplerChildrenFollowRoot(t *testing.T) {
	t.Setenv(envAlwaysSample, "")
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithSampler(newSelfObsSampler(0)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	tr := tp.Tracer("t")
	ctx, root := tr.Start(context.Background(), "sre.investigation")
	_, child := tr.Start(ctx, "clickhouse.query")
	child.End()
	root.End()
	ctx2, other := tr.Start(context.Background(), "HTTP GET")
	_, oc := tr.Start(ctx2, "clickhouse.query")
	oc.End()
	other.End()
	names := map[string]int{}
	for _, s := range exp.GetSpans() {
		names[s.Name]++
	}
	if names["sre.investigation"] != 1 || names["clickhouse.query"] != 1 || names["HTTP GET"] != 0 {
		t.Fatalf("örneklenen span'lar: %v", names)
	}
}

func TestSampledTraceID(t *testing.T) {
	if SampledTraceID(trace.SpanContext{}) != "" {
		t.Fatal("geçersiz bağlam → boş")
	}
	tid := trace.TraceID{1, 2, 3}
	sid := trace.SpanID{4}
	unsampled := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid})
	if SampledTraceID(unsampled) != "" {
		t.Fatal("örneklenmemiş span'ın kimliği sorgulanamaz → boş")
	}
	sampled := unsampled.WithTraceFlags(trace.FlagsSampled)
	if got := SampledTraceID(sampled); got != tid.String() || len(got) != 32 {
		t.Fatalf("örneklenmiş → 32 hex trace id, got %q", got)
	}
}
