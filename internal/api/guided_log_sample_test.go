package api

// v0.10.1213 — log hataları cevabı son hata satırlarını şablona göre gruplar.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogBodyTemplate(t *testing.T) {
	a := logBodyTemplate("[Ljava.lang.StackTraceElement;@19e050d9")
	b := logBodyTemplate("[Ljava.lang.StackTraceElement;@79939c9d")
	if a != b {
		t.Fatalf("hex mask: %q vs %q", a, b)
	}
	c := logBodyTemplate(`req DEPM164060203 id 0ced1a34748c463bbd9e402f4009aca7 user "ali"`)
	d := logBodyTemplate(`req DEPM999 id 1e8cb56b748c463bbd9e402f4009aca7 user "veli"`)
	if c != d {
		t.Fatalf("num/hex/quote mask: %q vs %q", c, d)
	}
}

func TestGroupAndRenderLogSample(t *testing.T) {
	recs := []*logstore.LogRecord{
		{Timestamp: 3e9, Body: "GRPC_DISABLED: caller 12", ServiceName: "a", TraceID: "t1"},
		{Timestamp: 2e9, Body: "GRPC_DISABLED: caller 99", ServiceName: "b"},
		{Timestamp: 1e9, Body: "400 BAD_REQUEST", ServiceName: "a"},
		{Timestamp: 1e9, Body: "  "},
	}
	g, rest := groupLogSample(recs, 1)
	if len(g) != 1 || g[0].Count != 2 || rest != 1 || g[0].TraceID != "t1" || len(g[0].Services) != 2 {
		t.Fatalf("groups=%+v rest=%d", g, rest)
	}
	out := renderLogSampleTR(recs, false, "")
	if !strings.Contains(out, "×2 [a, b] GRPC_DISABLED: caller 12 (trace t1)") || !strings.Contains(out, "Son 4 hata satırı") {
		t.Fatal(out)
	}
	if !strings.Contains(renderLogSampleTR(nil, true, "svc"), "seviyesiz") {
		t.Fatal("fallback label")
	}
}

func TestSampleServicesAndTemplatesRender(t *testing.T) {
	recs := []*logstore.LogRecord{{ServiceName: "b"}, {ServiceName: "a"}, {ServiceName: "b"}, {ServiceName: "c"}}
	if got := strings.Join(sampleServices(recs, 2), ","); got != "b,a" {
		t.Fatalf("got %s", got)
	}
	if renderNewLogTemplatesTR(nil, []string{""}, 3600) != "" {
		t.Fatal("no service → empty")
	}
	out := renderNewLogTemplatesTR([]anomaly.LogTemplateEvidence{{Service: "a", TotalCount: 7, Template: "GRPC <*> off", Sample: "GRPC x off"}}, []string{"a"}, 3600)
	if !strings.Contains(out, "- [a] ×7 GRPC <*> off — örnek: GRPC x off") {
		t.Fatal(out)
	}
}
