package chstore

// v0.10.1315 — olay linki XSS: ListEvents okunan her satırın linkini
// safelink.Sanitize'dan geçirir (yazma kapısından önce saklanmış eski
// satırlar). Canlı CH yok — dosya-içerik ratchet'ı (argocd_settings_routes_test
// emsali): süzme ListEvents gövdesinden çıkarılırsa ya da `events` tablosunu
// okuyan ikinci bir sorgu eklenirse kırmızı.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/safelink"
)

func TestListEventsSanitizesLink(t *testing.T) {
	src, err := os.ReadFile("event.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	start := strings.Index(s, "func (s *Store) ListEvents(")
	if start < 0 {
		t.Fatal("ListEvents not found in event.go")
	}
	body := s[start:]
	if end := strings.Index(body[1:], "\nfunc "); end >= 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "e.Link = safelink.Sanitize(e.Link)") {
		t.Fatal("ListEvents must sanitize e.Link via safelink.Sanitize (v0.10.1315)")
	}
	if n := strings.Count(s, "FROM events"); n != 1 {
		t.Fatalf("event.go reads `events` in %d places; every reader must sanitize links", n)
	}
	// Süzgecin kendisi: eski bir javascript: satırı düşer, güvenli linkler kalır.
	for in, want := range map[string]string{
		"javascript:alert(1)":        "",
		"//evil.com":                 "",
		"https://ci.example/build/1": "https://ci.example/build/1",
		"/problems?problem=p1":       "/problems?problem=p1",
	} {
		if got := safelink.Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
