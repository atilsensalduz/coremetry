package vmetrics

// v0.10.1380 — EnvShortForm, CoSRE sohbetinin ortam anma çözücüsüyle
// (api/chat_env_mentions.go) PAYLAŞILAN tek önek kuralı: MatchEnvValue'nun
// prefix kademesiyle aynı sonucu vermeli (iki yüzey ayrışmasın).

import "testing"

func TestEnvShortForm(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"prod-eu-a", "eu-a", true},
		{"prod-Kxzone", "Kxzone", true},
		{"dr-eu-a", "eu-a", true},
		{"eu-a", "", false}, // kalan "a" <3
		{"prod", "", false}, // önek yok
		{"-eu-a", "", false},
	} {
		got, ok := EnvShortForm(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("EnvShortForm(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
		if c.ok && MatchEnvValue(got, []string{c.in}).Tier != EnvTierPrefix {
			t.Errorf("%q: kısa biçim metrik eşleştiricisinin prefix kademesinde tutmalı", c.in)
		}
	}
}
