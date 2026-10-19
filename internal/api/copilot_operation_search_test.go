package api

import (
	"reflect"
	"strings"
	"testing"
)

// copilot_operation_search_test.go — v0.10.1390. CoSRE başlangıç kartının
// operasyon arama kutusu: kısa/uzun sorgu CH'ye gitmez, limit tavanı, anahtar
// tüm girdileri (q harf duyarsız + limit) taşır, ve aramanın önerdiği her ad
// ön-getirme belirteç kuralından TEK belirteç olarak geçer (yönlenme pini:
// TestStarterQuestionsRoute).

func TestOpSearchQuery(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{" a ", ""},
		{"ab", "ab"},
		{"  INSURANCE  ", "INSURANCE"},
		{strings.Repeat("x", 81), ""},
		{strings.Repeat("x", 80), strings.Repeat("x", 80)},
	} {
		if got := opSearchQuery(c.in); got != c.want {
			t.Errorf("opSearchQuery(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestOpSearchLimit(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int
	}{{"", 20}, {"0", 20}, {"-3", 20}, {"5", 5}, {"50", 50}, {"500", 50}, {"x", 20}} {
		if got := opSearchLimit(c.in); got != c.want {
			t.Errorf("opSearchLimit(%q) = %d; want %d", c.in, got, c.want)
		}
	}
}

func TestOpSearchKey(t *testing.T) {
	if !strings.HasPrefix(opSearchKey("ins", 20), "copilot:op-search:v1:") {
		t.Fatal("önek")
	}
	if opSearchKey("INS", 20) != opSearchKey("ins", 20) {
		t.Error("arama harf duyarsız → anahtar da")
	}
	seen := map[string]bool{}
	for _, c := range []struct {
		q string
		n int
	}{{"ins", 20}, {"ins", 10}, {"insu", 20}, {"pay", 20}} {
		k := opSearchKey(c.q, c.n)
		if seen[k] {
			t.Fatalf("anahtar çakışması: %+v", c)
		}
		seen[k] = true
	}
}

func TestOpSearchRoutable(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"INSURANCE_DOC_PDF", true},
		{"OrderService.PlaceOrder", true},
		{"payments/authorize", true},
		{"db:query", true},
		{"GET /api/orders", false}, // boşluk: iki belirteç
		{"/api/orders", false},     // uçtaki '/' kırpılır
		{"orders.", false},         // uçtaki '.' kırpılır
		{"ABC", false},             // <4 karakter
		{"POST", false},            // HTTP fiili belirteç değil
		{"Order#Place", false},     // ayraç karakteri: iki belirteç
		{"", false},
	} {
		if got := opSearchRoutable(c.name); got != c.want {
			t.Errorf("opSearchRoutable(%q) = %v; want %v", c.name, got, c.want)
		}
	}
}

func TestOpSearchNames(t *testing.T) {
	got := opSearchNames([]string{"INSURANCE_DOC_PDF", "/api/x", "INSURANCE_DOC_PDF", "OrderService.PlaceOrder", "INSURANCE_LIST"}, 2)
	want := []string{"INSURANCE_DOC_PDF", "OrderService.PlaceOrder"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sıra korunur, yönlenemeyen/tekrar atlanır, tavan: %v; want %v", got, want)
	}
	if got := opSearchNames(nil, 5); got == nil || len(got) != 0 {
		t.Fatalf("boş girdi → boş (nil değil) liste: %#v", got)
	}
}
