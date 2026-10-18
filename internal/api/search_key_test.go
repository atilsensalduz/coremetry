package api

// search_key_test.go — v0.10.1333. searchCacheKey cache_key_test.go
// kanonunu taşır (v0.5.187): her girdi anahtarda (ayrıklık), aynı girdi aynı
// anahtar (kararlılık), types KÜME olarak özetlenir (permütasyon değişmezliği,
// uzunluk-yalnız özet değil). Logs arka ucu anahtarda: Settings'ten ES↔CH
// geçişi diğerinin şeklini 15 sn servis edemesin.

import (
	"testing"
	"time"
)

func baseSearchParams() searchParams {
	to := time.Unix(1_800_000_000, 0)
	return searchParams{
		Q: "checkout", Kind: queryText,
		Types:   []string{"service", "endpoint", "problem", "exception", "trace", "log"},
		Service: "", Env: "", Sev: "", Status: "open", Sort: "relevance",
		From: to.Add(-time.Hour), To: to, Limit: 20,
	}
}

func TestSearchCacheKeyDistinctness(t *testing.T) {
	base := baseSearchParams()
	k0 := searchCacheKey(base, "clickhouse")
	mut := map[string]func(p *searchParams){
		"q":       func(p *searchParams) { p.Q = "checkouts" },
		"service": func(p *searchParams) { p.Service = "payments" },
		"env":     func(p *searchParams) { p.Env = "prod" },
		"sev":     func(p *searchParams) { p.Sev = "critical" },
		"status":  func(p *searchParams) { p.Status = "all" },
		"sort":    func(p *searchParams) { p.Sort = "newest" },
		"limit":   func(p *searchParams) { p.Limit = 5 },
		"from":    func(p *searchParams) { p.From = p.From.Add(-15 * time.Second) },
		"to":      func(p *searchParams) { p.To = p.To.Add(15 * time.Second) },
		"types":   func(p *searchParams) { p.Types = []string{"service"} },
		"types aynı uzunluk": func(p *searchParams) {
			p.Types = []string{"service", "endpoint", "problem", "exception", "trace", "trace"}
		},
	}
	seen := map[string]string{k0: "base"}
	for name, m := range mut {
		p := baseSearchParams()
		m(&p)
		k := searchCacheKey(p, "clickhouse")
		if prev, dup := seen[k]; dup {
			t.Errorf("%s anahtarı %s ile çakıştı: %s", name, prev, k)
		}
		seen[k] = name
	}
	// Ayraç birleşmesi: "a|b"+"c" ≠ "a"+"b|c" sınıfı.
	p1, p2 := baseSearchParams(), baseSearchParams()
	p1.Q, p1.Service = "ab", "c"
	p2.Q, p2.Service = "a", "bc"
	if searchCacheKey(p1, "clickhouse") == searchCacheKey(p2, "clickhouse") {
		t.Error("serbest metin alanları ayrı ayrı özetlenmeli (ayraç birleşmesi)")
	}
}

func TestSearchCacheKeyBackendInKey(t *testing.T) {
	p := baseSearchParams()
	if searchCacheKey(p, "clickhouse") == searchCacheKey(p, "elasticsearch") {
		t.Fatal("logs arka ucu anahtarda olmalı: ES↔CH geçişi diğerinin şeklini servis etmesin")
	}
	if searchCacheKey(p, "") == searchCacheKey(p, "clickhouse") {
		t.Fatal("arka ucu olmayan kurulum ayrı anahtar")
	}
}

func TestSearchCacheKeyStableAndPermutationInvariant(t *testing.T) {
	a, b := baseSearchParams(), baseSearchParams()
	a.Types = []string{"service", "log", "trace"}
	b.Types = []string{"trace", "service", "log"}
	if searchCacheKey(a, "clickhouse") != searchCacheKey(b, "clickhouse") {
		t.Fatal("types küme: sıra anahtarı değiştirmemeli")
	}
	k := searchCacheKey(a, "clickhouse")
	for i := 0; i < 10; i++ {
		if searchCacheKey(a, "clickhouse") != k {
			t.Fatal("anahtar kararsız")
		}
	}
}
