package copilot

import (
	"context"
	"testing"
)

// v0.10.1302 — CoSRE: modele göre araç seti. Doğrulama kapalı kümeyi
// zorlar; ToolsetFor açık ayarı otomatik kurala yeğler ve sohbet model
// seçicisini (WithProfile) izler; sameProfile alan değişimini görür
// (30 s refresh değişen profili yeniden kurar).
func TestValidateProfileToolset(t *testing.T) {
	base := ModelProfile{ID: "p", Provider: ProviderOpenAI}
	for _, ts := range []string{"", "simple", "full"} {
		p := base
		p.Toolset = ts
		if err := ValidateProfile(p); err != nil {
			t.Errorf("toolset %q geçerli olmalı: %v", ts, err)
		}
	}
	p := base
	p.Toolset = "x"
	if err := ValidateProfile(p); err == nil {
		t.Error("toolset \"x\" reddedilmeli")
	}
}

func TestToolsetForExplicitBeatsAutoAndFollowsPicker(t *testing.T) {
	s := New("openai", "kd", "default-model")
	s.SetProfiles([]ModelProfile{
		{ID: "big", Provider: ProviderOpenAI, BaseURL: "http://big/v1", APIKey: "kb", Model: "gemma4-31b"},
		{ID: "small", Provider: ProviderOpenAI, BaseURL: "http://small/v1", APIKey: "ks", Model: "qwen3.5-2b"},
		{ID: "small-full", Provider: ProviderOpenAI, BaseURL: "http://small/v1", APIKey: "ks", Model: "qwen3.5-2b", Toolset: "full"},
		{ID: "big-simple", Provider: ProviderOpenAI, BaseURL: "http://big/v1", APIKey: "kb", Model: "gemma4-31b", Toolset: "simple"},
		{ID: "claude", Provider: ProviderAnthropic, APIKey: "ka", Model: "claude-sonnet-5"},
	}, "big", nil)
	bg := context.Background()
	cases := []struct {
		ctx  context.Context
		want string
	}{
		{bg, "full"},                              // varsayılan big: 31b → otomatik full
		{WithProfile(bg, "small"), "simple"},      // seçici: 2b → otomatik simple
		{WithProfile(bg, "small-full"), "full"},   // açık ayar otomatiği ezer
		{WithProfile(bg, "big-simple"), "simple"}, // açık ayar otomatiği ezer
		{WithProfile(bg, "claude"), "full"},       // boyutsuz ad → full
		{WithProfile(bg, "yok"), "full"},          // bilinmeyen id → varsayılan
	}
	for i, c := range cases {
		if got := s.ToolsetFor(c.ctx); got != c.want {
			t.Errorf("#%d: ToolsetFor = %q, want %q", i, got, c.want)
		}
	}
	var nilSvc *Service
	if got := nilSvc.ToolsetFor(bg); got != "full" {
		t.Errorf("nil servis full olmalı: %q", got)
	}
}

func TestSameProfileDetectsToolsetChange(t *testing.T) {
	a := ModelProfile{ID: "p", Provider: ProviderOpenAI, Model: "m"}
	b := a
	if !sameProfile(a, b) {
		t.Fatal("aynı profil farklı sayıldı")
	}
	b.Toolset = "simple"
	if sameProfile(a, b) {
		t.Fatal("toolset değişimi görülmedi")
	}
}
