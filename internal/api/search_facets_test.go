package api

// search_facets_test.go — v0.10.1333. Facet sözleşmesi:
//   - service/env/sev Go'da, DÖNEN satırlar üstünde sayılır (≤120) ve
//     "within shown results" olarak işaretlenir.
//   - her facet sorgu + DİĞER aktif facet'lere göre sayılır; kendi seçimi
//     hariç tutulur (seçili servis facet'i diğer servisleri de gösterir).
//   - sev bir TABANDIR (critical ⊂ warning ⊂ info); boyutu olmayan satır o
//     facet'in süzgecine takılmaz (bilinmeyen ≠ uyuşmayan).
//   - type facet okuyucuların toplamından: count + truncated (trace hasMore
//     → truncated, "20+"); toplam = tür sayımlarının toplamı.

import (
	"testing"
)

func TestFacetCountsOwnFacetExcluded(t *testing.T) {
	rows := []SearchResult{
		{Type: "log", ID: "1", Service: "pay", Env: "prod", level: "critical"},
		{Type: "log", ID: "2", Service: "pay", Env: "uat", level: "info"},
		{Type: "log", ID: "3", Service: "cart", Env: "prod", level: "warning"},
		{Type: "service", ID: "4", Service: "cart"}, // env/sev boyutu yok
	}
	f := facetCounts(rows, facetSelection{Service: "pay", Env: "prod"})
	if f.Scope != searchScopeShown {
		t.Errorf("scope işaretli olmalı: %q", f.Scope)
	}
	// service facet: env=prod süzgeci (kendi seçimi pay HARİÇ) → pay 1, cart 2
	// (4. satırın env'i yok → süzgece takılmaz).
	if f.Service["pay"] != 1 || f.Service["cart"] != 2 {
		t.Errorf("service facet: %v", f.Service)
	}
	// env facet: service=pay süzgeci → prod 1, uat 1.
	if f.Env["prod"] != 1 || f.Env["uat"] != 1 || len(f.Env) != 2 {
		t.Errorf("env facet: %v", f.Env)
	}
	// sev facet: service=pay ∧ env=prod → yalnız 1. satır.
	if f.Sev["critical"] != 1 || len(f.Sev) != 1 {
		t.Errorf("sev facet: %v", f.Sev)
	}
}

func TestFacetCountsSevIsFloor(t *testing.T) {
	rows := []SearchResult{
		{Type: "log", ID: "1", Service: "a", level: "critical"},
		{Type: "log", ID: "2", Service: "b", level: "warning"},
		{Type: "log", ID: "3", Service: "c", level: "info"},
	}
	f := facetCounts(rows, facetSelection{Sev: "warning"})
	if f.Service["a"] != 1 || f.Service["b"] != 1 || f.Service["c"] != 0 {
		t.Errorf("sev=warning tabanı critical+warning geçirir: %v", f.Service)
	}
	// Kendi seçimi hariç: sev facet'i üç seviyeyi de sayar.
	if f.Sev["critical"] != 1 || f.Sev["warning"] != 1 || f.Sev["info"] != 1 {
		t.Errorf("sev facet kendi seçimini hariç tutmalı: %v", f.Sev)
	}
}

func TestAssembleSearchTypeFacetTotalsAndTruncation(t *testing.T) {
	p := baseSearchParams()
	p.Types = []string{"service", "trace", "log", "problem"}
	mk := func(typ string, n int) []SearchResult {
		out := make([]SearchResult, n)
		for i := range out {
			out[i] = SearchResult{Type: typ, ID: typ + string(rune('a'+i)), Title: "checkout"}
		}
		return out
	}
	res := map[string]sourceResult{
		"service": {Rows: mk("service", 1), Count: 1, Status: sourceStatus{Status: "ok"}},
		"trace":   {Rows: mk("trace", 20), Count: 20, Truncated: true, Status: sourceStatus{Status: "ok"}}, // hasMore
		"log":     {Rows: mk("log", 30), Count: 5000, Status: sourceStatus{Status: "ok"}},
		"problem": {Status: sourceStatus{Status: "timeout"}},
	}
	resp, cacheable := assembleSearchResponse(p, res)
	if !cacheable {
		t.Error("zaman aşımı kısmi cevabı önbelleğe yazılabilir olmalı")
	}
	if tf := resp.Facets.Type["trace"]; tf.Count != 20 || !tf.Truncated {
		t.Errorf("trace hasMore → 20+ (count 20, truncated): %+v", tf)
	}
	if tf := resp.Facets.Type["log"]; tf.Count != 5000 || !tf.Truncated {
		t.Errorf("log toplamı okuyucudan, gösterilenden fazla → truncated: %+v", tf)
	}
	if tf := resp.Facets.Type["service"]; tf.Count != 1 || tf.Truncated {
		t.Errorf("service: %+v", tf)
	}
	if tf := resp.Facets.Type["problem"]; tf.Count != 0 {
		t.Errorf("problem timeout → 0: %+v", tf)
	}
	if resp.Total != 1+20+5000 {
		t.Errorf("total tür sayımlarının toplamı: %d", resp.Total)
	}
	nLog := 0
	for _, r := range resp.Results {
		if r.Type == "log" {
			nLog++
		}
	}
	if nLog != 20 {
		t.Errorf("kaynak başına ≤20 satır: log %d", nLog)
	}
	if st := resp.Sources["log"]; !st.Truncated || st.Status != "ok" {
		t.Errorf("log kaynağı truncated işaretli olmalı: %+v", st)
	}
	if _, ok := resp.Sources["exception"]; ok {
		t.Error("seçilmeyen tür sources'ta görünmemeli")
	}
	if resp.Results == nil {
		t.Error("results null değil [] olmalı")
	}
}

func TestAssembleSearchUncacheableOnStoreError(t *testing.T) {
	p := baseSearchParams()
	p.Types = []string{"service", "problem"}
	res := map[string]sourceResult{
		"service": {Status: sourceStatus{Status: "ok"}},
		"problem": {Status: sourceStatus{Status: "unavailable", Reason: "query failed"}, storeErr: true},
	}
	if _, cacheable := assembleSearchResponse(p, res); cacheable {
		t.Fatal("zaman-aşımı-dışı store hatası önbelleğe yazılmamalı")
	}
}
