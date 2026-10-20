package api

// copilot_team_card_test.go — v0.10.1402 (operatör: "Alttaki takımımın
// servisleri exceptionları yukarıda olsun. Takım seçilebilsin. Son deploy
// etkisini kaldırabiliriz." + "Takımı da seçilebilir yapabiliriz, oradan
// devam eder.")
//
// CoSRE başlangıcındaki Takımım kartının gönderdiği HER cümle LLM'siz,
// deterministik rotasına düşer (capabilityHints.ts teamCardQuestion /
// teamPickQuestion ile aynı kalıplar):
//   - takım seçili:  "<takım> takımının servisleri nasıl?"       → team_services
//                    "<takım> takımının exception'ları?"          → team_exceptions
//                    "<takım> takımının en hatalı servisleri?"    → team_top_errors (YENİ)
//   - takım seçilmemiş: "Takımımın …" → my_services / my_exceptions / my_top_errors
//   - seçicide takım seçimi = "<takım> takımının servisleri nasıl?" → team_services
//     (v0.10.1397 takım özeti: servis tablosu + son exception grupları + servis çipleri).
// Takım adı bir SERVİSLE aynı olsa da takım kelimesi takım dalını açar.

import "testing"

var (
	tcServices = []string{"orders-api", "checkout-service", "inventory"}
	tcEnvs     = []string{"prod", "uat"}
	tcTeams    = []string{"platform", "orders-api", "Payments-Core"}
)

func TestTeamCardQuestionsRoute(t *testing.T) {
	cases := []struct {
		q      string
		intent guidedIntent
		team   string
	}{
		// Takım seçili (seçicinin döndürdüğü katalog yazımı).
		{"platform takımının servisleri nasıl?", guidedTeamServices, "platform"},
		{"platform takımının exception'ları?", guidedTeamExceptions, "platform"},
		{"platform takımının en hatalı servisleri?", guidedTeamTopErrors, "platform"},
		{"Payments-Core takımının servisleri nasıl?", guidedTeamServices, "Payments-Core"},
		{"Payments-Core takımının exception'ları?", guidedTeamExceptions, "Payments-Core"},
		{"Payments-Core takımının en hatalı servisleri?", guidedTeamTopErrors, "Payments-Core"},
		// Takım adı servisle çakışıyor: takım kelimesi takım dalını açar.
		{"orders-api takımının servisleri nasıl?", guidedTeamServices, "orders-api"},
		{"orders-api takımının exception'ları?", guidedTeamExceptions, "orders-api"},
		{"orders-api takımının en hatalı servisleri?", guidedTeamTopErrors, "orders-api"},
		// Takım seçilmemiş → iyelik (kimlikten çözülür).
		{"Takımımın servisleri nasıl?", guidedMyServices, ""},
		{"Takımımın exception'ları?", guidedMyExceptions, ""},
		{"Takımımın en hatalı servisleri?", guidedMyTopErrors, ""},
		// Bozulmayan yollar: filo "en hatalı servisler", düz takım hataları.
		{"En hatalı servisler?", guidedTopErrorServices, ""},
		{"platform takımının hataları", guidedTeamServices, "platform"},
		{"platform", guidedTeamServices, "platform"},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, tcServices, tcEnvs, tcTeams, "")
		if r.Intent != c.intent || r.Team != c.team {
			t.Errorf("%q → %s team=%q; want %s team=%q", c.q, r.Intent, r.Team, c.intent, c.team)
		}
	}
	// Takım kataloğu yokken adı geçen takım dalı açılmaz (filo sıralaması).
	if r := routeGuidedIntent("platform takımının en hatalı servisleri?", tcServices, tcEnvs, nil, ""); r.Intent == guidedTeamTopErrors {
		t.Errorf("katalog yok → team_top_errors olmamalı: %+v", r)
	}
}

func TestTeamTopErrorsScopeAndFollowups(t *testing.T) {
	cases := []struct {
		name string
		in   guidedRoute
		sc   chatScope
		want guidedRoute
	}{
		{"@team kapsamı my_top_errors'ı adı geçen takıma çevirir",
			guidedRoute{Intent: guidedMyTopErrors, Env: "prod"}, chatScope{Team: "platform"},
			guidedRoute{Intent: guidedTeamTopErrors, Team: "platform", Env: "prod"}},
		{"kapsam takımı rotanın takımını ezer",
			guidedRoute{Intent: guidedTeamTopErrors, Team: "orders-api"}, chatScope{Team: "platform"},
			guidedRoute{Intent: guidedTeamTopErrors, Team: "platform"}},
		{"kapsamsız rota aynen",
			guidedRoute{Intent: guidedTeamTopErrors, Team: "platform"}, chatScope{},
			guidedRoute{Intent: guidedTeamTopErrors, Team: "platform"}},
	}
	for _, c := range cases {
		sc := c.sc
		got := applyChatScopeRoute(&sc, c.in)
		if got.Intent != c.want.Intent || got.Team != c.want.Team || got.Env != c.want.Env {
			t.Errorf("%s: %+v; want %+v", c.name, got, c.want)
		}
	}
	// Takip çipleri my_top_errors ile aynı ikinci adım: en hatalı servislerin operasyonları.
	got := guidedSuggestions(guidedRoute{Intent: guidedTeamTopErrors, Team: "platform", TeamServices: []string{"orders-api", "inventory"}})
	if len(got) != 2 || got[0] != "orders-api en hatalı operasyonları?" || got[1] != "inventory en hatalı operasyonları?" {
		t.Errorf("takip çipleri: %v", got)
	}
	// Veri yoksa aynı takımda kalır (iyelik cümlesine kaymaz).
	if got := guidedSuggestions(guidedRoute{Intent: guidedTeamTopErrors, Team: "platform"}); len(got) != 1 || got[0] != "platform takımının exception'ları?" {
		t.Errorf("boş takip: %v", got)
	}
}
