package vmetrics

// v0.10.1376 — span deploy_env ↔ VM ortam etiketi değeri (adlar SENTETİK).
// SÖZLEŞME: exact → ci → TEK TARAFLI tek önek (kalan ≥3); iki taraf birden
// soyulmaz; kazanan kademenin TÜM değerleri döner; EnvMatcher frontend
// promStr ile aynı kaçışı kullanır.

import (
	"reflect"
	"testing"
)

func TestMatchEnvValue_v0101376(t *testing.T) {
	cases := []struct {
		name string
		want string
		live []string
		tier string
		vals []string
	}{
		{"birebir", "eu-a", []string{"eu-a", "prod-eu-a"}, EnvTierExact, []string{"eu-a"}},
		{"span öneksiz, metrik önekli", "eu-a", []string{"prod-eu-a", "eu-b"}, EnvTierPrefix, []string{"prod-eu-a"}},
		{"span önekli, metrik öneksiz", "prod-tr-c", []string{"tr-c", "eu-a"}, EnvTierPrefix, []string{"tr-c"}},
		{"büyük/küçük harf", "EU-A", []string{"eu-a"}, EnvTierCI, []string{"eu-a"}},
		{"önek + harf", "EU-A", []string{"prod-eu-a"}, EnvTierPrefix, []string{"prod-eu-a"}},
		{"iki taraf soyulmaz", "prod-eu-a", []string{"dr-eu-a"}, EnvTierNone, nil},
		{"komşu küme", "eu-a", []string{"eu-b"}, EnvTierNone, nil},
		{"son ek eşleşmez", "eu-a", []string{"eu-a-svc"}, EnvTierNone, nil},
		{"kalan <3", "a", []string{"prod-a"}, EnvTierNone, nil},
		{"belirsiz: iki önek ikisi birden", "eu-a", []string{"prod-eu-a", "dr-eu-a", "eu-b"}, EnvTierPrefix, []string{"dr-eu-a", "prod-eu-a"}},
		{"canlı değer yok", "eu-a", nil, EnvTierNone, nil},
		{"boş istek", "", []string{"eu-a"}, EnvTierNone, nil},
		{"exact ci'yi ezer", "eu-a", []string{"EU-A", "eu-a"}, EnvTierExact, []string{"eu-a"}},
		{"ci öneki ezer", "eu-a", []string{"EU-A", "prod-eu-a"}, EnvTierCI, []string{"EU-A"}},
	}
	for _, c := range cases {
		got := MatchEnvValue(c.want, c.live)
		if got.Tier != c.tier || !reflect.DeepEqual(got.Values, c.vals) || got.Want != c.want {
			t.Errorf("%s: MatchEnvValue(%q, %v) = %+v, want tier %s values %v", c.name, c.want, c.live, got, c.tier, c.vals)
		}
	}
}

func TestMatchEnvValuesBounds_v0101376(t *testing.T) {
	got := MatchEnvValues([]string{"eu-a", "tr-c"}, []string{"prod-eu-a", "tr-c"})
	if len(got) != 2 || got[0].Tier != EnvTierPrefix || got[1].Tier != EnvTierExact {
		t.Fatalf("girdi sırası korunur: %+v", got)
	}
	many := []string{"a1x", "a2x", "a3x", "a4x", "a5x", "a6x", "a7x"}
	if got := MatchEnvValues(many, nil); len(got) != envMatchWantMax {
		t.Fatalf("≤%d istek: %d", envMatchWantMax, len(got))
	}
	live := make([]string, envMatchLiveMax+1)
	for i := range live {
		live[i] = "x-filler"
	}
	live[envMatchLiveMax] = "eu-a"
	if got := MatchEnvValue("eu-a", live); got.Tier != EnvTierNone {
		t.Fatalf("≤%d canlı değer okunur: %+v", envMatchLiveMax, got)
	}
}

func TestEnvMatcher_v0101376(t *testing.T) {
	for _, c := range []struct {
		vals []string
		want string
	}{
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"prod-eu-a"}, `deployment_environment_name="prod-eu-a"`},
		{[]string{"prod-eu-a", "dr-eu-a", "prod-eu-a"}, `deployment_environment_name=~"dr-eu-a|prod-eu-a"`},
		{[]string{"eu.a", "eu-b"}, `deployment_environment_name=~"eu-b|eu\\.a"`},
		{[]string{`q"x`}, `deployment_environment_name="q\"x"`},
	} {
		if got := EnvMatcher("deployment_environment_name", c.vals); got != c.want {
			t.Errorf("EnvMatcher(%v) = %s, want %s", c.vals, got, c.want)
		}
	}
	if got := PromStrTS(`a\b"c` + "\n"); got != `"a\\b\"c`+"\n"+`"` {
		t.Errorf("PromStrTS yalnız \\ ve \" kaçışlar: %s", got)
	}
}
