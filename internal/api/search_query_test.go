package api

// search_query_test.go — v0.10.1333 (birleşik arama, GET /api/search).
//
// SÖZLEŞME (saf parseSearchParams / classifyQuery / capWindow):
//   - q kırpılır, en az 2 karakter; kapalı sözlükler (types, sev, status,
//     sort) bilinmeyen değerde 400 — sessizce düşürmek cevabı değiştirirdi.
//   - types boş → altı tür, KANONİK sırada (service → … → log); tekrar ve
//     sıra anahtarı/fan-out'u değiştirmez.
//   - limit ≤ 20 (kaynak başına); ≤0 → 20.
//   - canlı pencere 15 sn basamağa oturur (snapLogsWindow emsali), uzunluk
//     korunur; mutlak pencere aynen; pencere 7 günle kırpılır.
//   - 32-hex → trace id (küçük harf); paletin IDENTITY_RE'si + rakam →
//     kimlik (harf korunur); gerisi serbest metin.

import (
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestParseSearchParamsWhitelists(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name    string
		qs      string
		wantErr bool
		check   func(t *testing.T, p searchParams)
	}{
		{"q zorunlu", "q=", true, nil},
		{"q tek karakter", "q=a", true, nil},
		{"q kırpılır, 2 karakter yeter", "q=%20ab%20", false, func(t *testing.T, p searchParams) {
			if p.Q != "ab" {
				t.Errorf("q kırpılmalı: %q", p.Q)
			}
		}},
		{"types boş → altı tür kanonik sırada", "q=ab", false, func(t *testing.T, p searchParams) {
			if len(p.Types) != 6 || p.Types[0] != "service" || p.Types[5] != "log" {
				t.Errorf("types: %v", p.Types)
			}
		}},
		{"types kanonik sıraya dizilir, tekrar düşer", "q=ab&types=log,service,log", false, func(t *testing.T, p searchParams) {
			if len(p.Types) != 2 || p.Types[0] != "service" || p.Types[1] != "log" {
				t.Errorf("types: %v", p.Types)
			}
			if p.wants("trace") || !p.wants("log") {
				t.Errorf("wants yanlış: %v", p.Types)
			}
		}},
		{"bilinmeyen tür 400", "q=ab&types=service,spans", true, nil},
		{"sev sözlüğü", "q=ab&sev=warning", false, func(t *testing.T, p searchParams) {
			if p.Sev != "warning" {
				t.Errorf("sev: %q", p.Sev)
			}
		}},
		{"bilinmeyen sev 400", "q=ab&sev=P1", true, nil},
		{"status varsayılan open", "q=ab", false, func(t *testing.T, p searchParams) {
			if p.Status != "open" {
				t.Errorf("status: %q", p.Status)
			}
		}},
		{"status all", "q=ab&status=all", false, func(t *testing.T, p searchParams) {
			if p.Status != "all" {
				t.Errorf("status: %q", p.Status)
			}
		}},
		{"bilinmeyen status 400", "q=ab&status=resolved", true, nil},
		{"sort varsayılan relevance", "q=ab", false, func(t *testing.T, p searchParams) {
			if p.Sort != "relevance" {
				t.Errorf("sort: %q", p.Sort)
			}
		}},
		{"bilinmeyen sort 400", "q=ab&sort=random", true, nil},
		{"limit 20 tavanı", "q=ab&limit=500", false, func(t *testing.T, p searchParams) {
			if p.Limit != 20 {
				t.Errorf("limit: %d", p.Limit)
			}
		}},
		{"limit küçültülebilir", "q=ab&limit=5", false, func(t *testing.T, p searchParams) {
			if p.Limit != 5 {
				t.Errorf("limit: %d", p.Limit)
			}
		}},
		{"limit sıfır → 20", "q=ab&limit=0", false, func(t *testing.T, p searchParams) {
			if p.Limit != 20 {
				t.Errorf("limit: %d", p.Limit)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, _ := url.ParseQuery(c.qs)
			p, err := parseSearchParams(v, now)
			if c.wantErr {
				if err == nil {
					t.Fatalf("hata bekleniyordu: %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("beklenmeyen hata: %v", err)
			}
			if c.check != nil {
				c.check(t, p)
			}
		})
	}
}

func TestClassifyQuery(t *testing.T) {
	cases := []struct {
		in   string
		want searchQueryKind
	}{
		{"0FCD70A94BA1F695EA079750E71A7C10", queryTraceID},
		{"0fcd70a94ba1f695ea079750e71a7c10", queryTraceID},
		{"0fcd70a94ba1f695", queryIdentity},                 // 16-hex: trace id DEĞİL (32 şart), kimlik adayı
		{"060209Dr8G0037156551", queryIdentity},             // function_id gibi
		{"order-12345678", queryIdentity},                   // ≥8, rakamlı, sınıf içi
		{"checkout-service", queryText},                     // rakamsız → kimlik değil
		{"POST /api/pay", queryText},                        // boşluk
		{"abc123", queryText},                               // <8
		{"pay*ment12345", queryText},                        // joker
		{"0fcd70a94ba1f695ea079750e71a7c1z", queryIdentity}, // 32 ama hex değil
	}
	for _, c := range cases {
		if got := classifyQuery(c.in); got != c.want {
			t.Errorf("classifyQuery(%q) = %q, beklenen %q", c.in, got, c.want)
		}
	}
}

func TestParseSearchParamsTraceIDLowercased(t *testing.T) {
	v := url.Values{"q": {"0FCD70A94BA1F695EA079750E71A7C10"}}
	p, err := parseSearchParams(v, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != queryTraceID || p.Q != "0fcd70a94ba1f695ea079750e71a7c10" {
		t.Fatalf("trace id küçük harfe inmeli: %q %q", p.Kind, p.Q)
	}
	v2 := url.Values{"q": {"060209Dr8G0037156551"}}
	p2, _ := parseSearchParams(v2, time.Now())
	if p2.Q != "060209Dr8G0037156551" {
		t.Fatalf("kimlik değeri harf korunur (v0.10.674): %q", p2.Q)
	}
}

func TestParseSearchParamsWindowSnapAndCaps(t *testing.T) {
	now := time.Unix(1_800_000_007, 0) // 15 sn ızgarasının 7 sn ilerisi
	ns := func(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

	// Varsayılan: son 1 sa, sağ uç 15 sn basamağa oturur.
	p, err := parseSearchParams(url.Values{"q": {"ab"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.To.UnixNano()%int64(15*time.Second) != 0 || p.To.After(now) {
		t.Errorf("canlı pencere 15 sn'ye oturmalı: to=%v", p.To)
	}
	if p.To.Sub(p.From) != time.Hour {
		t.Errorf("varsayılan pencere 1 sa: %v", p.To.Sub(p.From))
	}

	// Canlı mutlak-olmayan pencere iki istekte aynı anahtarı üretir.
	p2, _ := parseSearchParams(url.Values{"q": {"ab"}}, now.Add(3*time.Second))
	if !p2.To.Equal(p.To) {
		t.Errorf("aynı basamakta iki istek aynı pencere: %v vs %v", p.To, p2.To)
	}

	// Mutlak (geçmiş) pencere aynen.
	from, to := now.Add(-48*time.Hour), now.Add(-47*time.Hour)
	p3, _ := parseSearchParams(url.Values{"q": {"ab"}, "from": {ns(from)}, "to": {ns(to)}}, now)
	if !p3.From.Equal(from) || !p3.To.Equal(to) {
		t.Errorf("mutlak pencere aynen kalmalı: %v→%v", p3.From, p3.To)
	}

	// 30 günlük pencere 7 güne kırpılır (sağ uç korunur).
	p4, _ := parseSearchParams(url.Values{"q": {"ab"}, "from": {ns(to.Add(-30 * 24 * time.Hour))}, "to": {ns(to)}}, now)
	if p4.To.Sub(p4.From) != searchMaxWindow || !p4.To.Equal(to) {
		t.Errorf("pencere %v'ye kırpılmalı: %v", searchMaxWindow, p4.To.Sub(p4.From))
	}

	// Ters pencere → varsayılan.
	p5, _ := parseSearchParams(url.Values{"q": {"ab"}, "from": {ns(to)}, "to": {ns(from)}}, now)
	if p5.To.Sub(p5.From) != time.Hour {
		t.Errorf("ters pencere varsayılana dönmeli: %v", p5.To.Sub(p5.From))
	}
}

func TestCapWindow(t *testing.T) {
	to := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name       string
		span, max  time.Duration
		wantSpan   time.Duration
		wantCapped bool
	}{
		{"altında", 30 * time.Minute, time.Hour, 30 * time.Minute, false},
		{"tam", time.Hour, time.Hour, time.Hour, false},
		{"üstünde → sağ uç korunur", 6 * time.Hour, time.Hour, time.Hour, true},
		{"log 24 sa tavanı", 72 * time.Hour, 24 * time.Hour, 24 * time.Hour, true},
	}
	for _, c := range cases {
		f, tt, capped := capWindow(to.Add(-c.span), to, c.max)
		if !tt.Equal(to) || tt.Sub(f) != c.wantSpan || capped != c.wantCapped {
			t.Errorf("%s: %v→%v capped=%v", c.name, f, tt, capped)
		}
	}
}
