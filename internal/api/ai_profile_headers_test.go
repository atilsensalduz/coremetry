package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.1238 — profilin ek LLM başlıkları: kırpma/boş ad atma, audit'e yalnız
// adlar (değer geçit jetonu olabilir), ValidateProfile ayrılmış adı reddeder.
func TestNormalizeProfileHeaders(t *testing.T) {
	got := normalizeProfileHeaders(map[string]string{" x-cluster ": " test-v2 ", "  ": "x"})
	if len(got) != 1 || got["x-cluster"] != "test-v2" {
		t.Fatalf("normalize: %v", got)
	}
	if normalizeProfileHeaders(map[string]string{"": "a"}) != nil {
		t.Fatal("boş sonuç nil olmalı")
	}
}

func TestProfileHeadersAudit(t *testing.T) {
	if profileHeadersAudit(nil) != "" {
		t.Fatal("başlıksız audit satırı değişmemeli")
	}
	if got := profileHeadersAudit(map[string]string{"x-z": "secret", "x-cluster": "test-v2"}); got != " headers=x-cluster,x-z" {
		t.Fatalf("audit: %q", got)
	}
}

func TestValidateProfileHeaders(t *testing.T) {
	p := copilot.ModelProfile{ID: "p1", Provider: copilot.ProviderOpenAI, Headers: map[string]string{"x-cluster": "test-v2"}}
	if err := copilot.ValidateProfile(p); err != nil {
		t.Fatalf("x-cluster reddedildi: %v", err)
	}
	p.Headers = map[string]string{"Authorization": "Bearer x"}
	if copilot.ValidateProfile(p) == nil {
		t.Fatal("Authorization ek başlık olarak kabul edildi")
	}
}
