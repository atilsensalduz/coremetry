package api

// v0.10.1482 — `/x/**` önek kipi: anma kuralı (sarmalayıcı ≠ joker), toplam
// cevabı (p95 satır başına), giden çağrı önek notu.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEndpointPrefixOf(t *testing.T) {
	cases := []struct {
		q, method, prefix string
		ok                bool
	}{
		{"/api/orders/** yavaş mı", "", "/api/orders", true},
		{"/api/orders/* yavaş mı?", "", "/api/orders", true},
		{"GET /api/orders/** neden yavaş", "GET", "/api/orders", true},
		{"orders-api /api/orders/**, yavaş mı", "", "/api/orders", true},
		{"**/api/orders/** yavaş mı", "", "", false}, // v0.10.1447 — sarmalayıcı, joker değil
		{"*/api/orders/* yavaş mı", "", "", false},
		{"_/api/orders/**_ yavaş mı", "", "", false},
		{"/api/orders/list yavaş mı", "", "", false}, // tam yol
		{"/api/x/.../y/** yavaş mı", "", "", false},  // elipsli önek bugünkü akışta
		{"/a/** yavaş mı", "", "", false},            // geçerli önek değil
		{"yavaş endpoint var mı", "", "", false},
	}
	for _, c := range cases {
		m, p, ok := endpointPrefixOf(c.q)
		if m != c.method || p != c.prefix || ok != c.ok {
			t.Errorf("%q → (%q,%q,%v) beklenen (%q,%q,%v)", c.q, m, p, ok, c.method, c.prefix, c.ok)
		}
	}
}

func TestRenderEndpointPrefixTR(t *testing.T) {
	rows := []chstore.RoutePrefixRow{
		{Service: "orders-api", Route: "/api/orders/{id}", Calls: 900, Errors: 9, AvgMs: 120, P95Ms: 400},
		{Service: "orders-api", Route: "/api/orders/{id}/items", Calls: 300, Errors: 30, AvgMs: 600, P95Ms: 1800},
	}
	res := chstore.RoutePrefixREDResult{Rows: rows,
		Cur:  chstore.RoutePrefixTotal{Calls: 1250, Errors: 40, AvgMs: 260, Routes: 14},
		Prev: chstore.RoutePrefixTotal{Calls: 1200, Errors: 12, AvgMs: 150, Routes: 13}}
	text := renderEndpointPrefixTR(endpointPrefixRender{Prefix: "/api/orders", Method: "GET", Service: "orders-api", RangeS: 3600, Res: res})
	for _, w := range []string{"/api/orders/** önek toplamı", "altındaki tüm HTTP rotaları", "14 rota", "1.250 çağrı", "önceki eşit pencere 1.200",
		"ort 260 ms (önceki 150 ms", "**ARTIŞ VAR**", "p95 yalnız rota başına (yüzdelikler toplanmaz)", "/api/orders/{id}/items 1.800 ms",
		"İlk 2 rota tabloda", "12 rota listelenmedi (toplamlara dahil)", "GET fiil süzgeci uygulanmadı"} {
		if !strings.Contains(text, w) {
			t.Errorf("cevap %q içermeli:\n%s", w, text)
		}
	}
	if strings.Contains(text, "p95 1") {
		t.Errorf("toplam p95 yazılmamalı:\n%s", text)
	}
	if got := endpointPrefixRows(append(rows, make([]chstore.RoutePrefixRow, 20)...)); len(got) != epPrefixListMax || got[1][3] != 10.0 {
		t.Errorf("tablo: %d satır, %v", len(got), got[1])
	}
}

func TestOutboundPrefixNote(t *testing.T) {
	in := outboundRender{Path: "/esb/external/**", Callers: []string{"orders-api"}, Peer: "esb.example.test", RangeS: 3600,
		Cur: outboundStat{Calls: 100, AvgMs: 80, Groups: 3}, PathGroups: 3}
	if txt := renderOutboundTR(in); !strings.Contains(txt, "Önek toplamı: /esb/external altındaki tüm yollar (3 yol)") || !strings.Contains(txt, "p95 yalnız yol başına") {
		t.Errorf("önek notu yok:\n%s", txt)
	}
	in.Path = "/esb/external"
	if txt := renderOutboundTR(in); strings.Contains(txt, "Önek toplamı") {
		t.Errorf("tam yolda önek notu olmamalı:\n%s", txt)
	}
}
