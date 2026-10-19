package vmetrics

// names_export_test.go — v0.10.1386: NameCandidates / NameSelector dış kapıları
// iç yazım kurallarının birebir aynısını döndürür (api metric_chart şablonları).

import (
	"reflect"
	"testing"
)

func TestNameCandidatesExport(t *testing.T) {
	const n = "http.server.request.duration"
	for _, c := range []struct {
		kind string
		want []string
	}{
		{"", nameSpellings(n, false)},
		{"gauge", nameSpellings(n, false)},
		{"counter", nameSpellings(n, true)},
		{"count", countNameCandidates(n)},
		{"sum", sumNameCandidates(n)},
		{"bucket", bucketNameCandidates(n)},
	} {
		if got := NameCandidates(c.kind, n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v, want %v", c.kind, got, c.want)
		}
	}
	// Çok ad → sıralı, tekil birleşim.
	got := NameCandidates("", "jvm.thread.count", "jvm.thread.count", "jvm_threads_live_threads")
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Fatalf("tekrar: %v", got)
		}
		seen[g] = true
	}
	if got[0] != "jvm.thread.count" || !seen["jvm_threads_live_threads"] {
		t.Errorf("sıra/birleşim: %v", got)
	}
	if NameSelector([]string{"x"}) != `__name__="x"` || NameSelector(nil) != `__name__=""` {
		t.Error("NameSelector nameMatcher ile aynı olmalı")
	}
}
