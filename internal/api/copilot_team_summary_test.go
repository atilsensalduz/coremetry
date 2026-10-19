package api

// v0.10.1383 — servis cevabı sonrası yapısal takip adımları (guidedFollowups,
// copilot_followup.go) + takım özeti (copilot_team_summary.go). Saf; CH /
// model yok. Yönlenme sözleşmesi TestFollowupTemplatesRouteGuided'da
// (chat_followup_chips_test.go); burada seçim, dedup, render ve anahtar.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func fuTexts(cs []chatFollowup) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

func fuLabels(cs []chatFollowup) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Label)
	}
	return out
}

func TestGuidedFollowupsServiceSteps(t *testing.T) {
	svc := "checkout-service"
	// Servis sağlığı: operasyon + exception önce, tam rapor sonda; ≤4.
	got := guidedFollowups(guidedRoute{Intent: guidedServiceHealth, Service: svc, Env: "prod"}, []string{svc + " sağlığı nasıl?"})
	wantLabels := []string{"Operasyonları göster", "Exception'ları göster", "Yavaş trace'ler", "Tam rapor"}
	if strings.Join(fuLabels(got), "|") != strings.Join(wantLabels, "|") {
		t.Fatalf("etiketler %v, want %v", fuLabels(got), wantLabels)
	}
	if got[0].Text != svc+" en hatalı operasyonları?" || got[1].Text != svc+" teknik hataları?" || got[3].Text != serviceReportChip(svc) {
		t.Fatalf("metinler %v", fuTexts(got))
	}
	for _, c := range got {
		if c.Scope == nil || c.Scope.Env != "prod" || len(c.Scope.Services) != 1 || c.Scope.Services[0] != svc {
			t.Fatalf("kapsam servis+env olmalı: %+v", c.Scope)
		}
	}
	// Niyetin zaten cevapladığı adım önerilmez; boşalan yere sıradaki gelir.
	ops := guidedFollowups(guidedRoute{Intent: guidedTopErrorOps, Service: svc}, nil)
	if strings.Join(fuLabels(ops), "|") != "Exception'ları göster|Yavaş trace'ler|Son deploy'dan beri ne değişti?|Tam rapor" {
		t.Fatalf("operasyon cevabı sonrası %v", fuLabels(ops))
	}
	exc := guidedFollowups(guidedRoute{Intent: guidedServiceExceptions, Service: svc}, nil)
	for _, c := range exc {
		if c.Kind == fuKindExceptions {
			t.Fatalf("exception cevabı sonrası exception adımı: %v", fuLabels(exc))
		}
	}
	rep := guidedFollowups(guidedRoute{Intent: guidedServiceReport, Service: svc}, nil)
	if strings.Join(fuLabels(rep), "|") != "Yavaş trace'ler|Son deploy'dan beri ne değişti?" {
		t.Fatalf("rapor sonrası %v", fuLabels(rep))
	}
	// Sorulmuş adım (≥0.8 benzer) düşer, sıradaki aday dener.
	asked := []string{svc + " en hatalı operasyonları neler?"}
	dd := guidedFollowups(guidedRoute{Intent: guidedServiceHealth, Service: svc}, asked)
	if dd[0].Kind != fuKindExceptions || len(dd) != 4 || dd[2].Kind != fuKindDeploy {
		t.Fatalf("dedup sonrası %v", fuLabels(dd))
	}
}

func TestGuidedFollowupsNone(t *testing.T) {
	cases := map[string]guidedRoute{
		"konusuz":            {Intent: guidedServiceHealth},
		"filo problemleri":   {Intent: guidedProblems},
		"servis sorusu":      {Intent: guidedAskService, ServiceOptions: []string{"checkout-service", "orders-api"}, AskIntent: guidedServiceHealth},
		"takım sorusu":       {Intent: guidedMyServices, TeamOptions: []string{"platform"}},
		"aile":               {Intent: guidedFamilyHealth, Family: []string{"checkout-service", "orders-api"}},
		"geçersiz ad":        {Intent: guidedServiceHealth, Service: "bad name with spaces"},
		"takım çipsiz":       {Intent: guidedTeamServices, Team: "platform"},
		"servis netleştirme": {Intent: guidedServiceHealth, Service: "checkout-service", ServiceOptions: []string{"x"}},
	}
	for name, r := range cases {
		if got := guidedFollowups(r, nil); len(got) != 0 {
			t.Errorf("%s: adım beklenmiyordu: %v", name, fuTexts(got))
		}
	}
	// Takım rotası bundle'ın yazdığı çipleri taşır.
	tf := []chatFollowup{{Text: "checkout-service teknik hataları?", Label: "x", Kind: fuKindExceptions}}
	if got := guidedFollowups(guidedRoute{Intent: guidedTeamServices, Team: "platform", TeamFollowups: tf}, nil); len(got) != 1 {
		t.Fatalf("takım çipleri: %v", got)
	}
}

func TestWithGuidedFollowupsKeepsSuggestions(t *testing.T) {
	ans := withGuidedFollowups(map[string]any{"text": "x", "suggestions": []string{"a"}}, nil)
	if _, ok := ans["followups"]; ok {
		t.Fatal("çipsiz cevapta followups")
	}
	ans = withGuidedFollowups(map[string]any{"text": "x", "suggestions": []string{"a"}},
		[]chatFollowup{serviceStepFollowup(fuKindOperations, "checkout-service", "")})
	if s, _ := ans["suggestions"].([]string); len(s) != 1 || s[0] != "a" {
		t.Fatalf("suggestions eski istemci için AYNEN kalmalı: %v", ans["suggestions"])
	}
	raw, _ := json.Marshal(ans)
	if !strings.Contains(string(raw), `"label":"Operasyonları göster"`) {
		t.Fatalf("label tele girmeli: %s", raw)
	}
}

func TestRenderTeamSummaryOkunamadiVsYok(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	// Üç bölüm de okunamadı → üçü de OKUNAMADI, hiçbiri "yok".
	failed := renderTeamSummaryTR("platform", 4, teamSummary{}, 3600, "", now)
	if strings.Count(failed, ": OKUNAMADI") != 3 || strings.Contains(failed, ": yok") {
		t.Fatalf("okunamayan bölüm yok sayıldı:\n%s", failed)
	}
	// Üçü de okundu ve boş → üçü de "yok", hiçbiri OKUNAMADI.
	var empty teamSummary
	teamSummaryFromProblems(&empty, nil, false)
	teamSummaryFromDeploys(&empty, nil, []string{"checkout-service"}, false)
	teamSummaryFromExceptions(&empty, nil)
	e := renderTeamSummaryTR("platform", 4, empty, 3600, "", now)
	if strings.Contains(e, ": OKUNAMADI") || strings.Count(e, "yok.") != 3 {
		t.Fatalf("boş bölümler yok demeli:\n%s", e)
	}
	// Karışık: problemler okundu (P1 var), deploy düştü, exception okundu.
	var mix teamSummary
	teamSummaryFromProblems(&mix, []chstore.Problem{
		{ID: "p1", Service: "checkout-service", Priority: "P1", RuleName: "error rate spike"},
		{ID: "p2", Service: "checkout-service", Priority: "P1"},
		{ID: "p3", Service: "orders-api", Priority: "P2"},
		{ID: "p4", Service: "orders-api"},
	}, false)
	teamSummaryFromExceptions(&mix, []chstore.ExceptionGroup{{Service: "orders-api", Type: "TimeoutException", Occurrences: 42}})
	m := renderTeamSummaryTR("platform", 4, mix, 3600, "prod", now)
	for _, want := range []string{
		"Açık problemler: 4 (P1: 2, P2: 1, P3: 1)",
		"P1 servisleri: checkout-service (error rate spike).",
		"Son 24 saatte deploy: OKUNAMADI",
		"orders-api · TimeoutException · 42 oluşum",
		`"prod" ortam süzgeci YALNIZ problemlere`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("eksik %q:\n%s", want, m)
		}
	}
	// Tavana dayanan tarama "en az" der.
	var capped teamSummary
	teamSummaryFromProblems(&capped, []chstore.Problem{{Service: "orders-api", Priority: "P2"}}, true)
	if c := renderTeamSummaryTR("platform", 1, capped, 3600, "", now); !strings.Contains(c, "en az 1") {
		t.Fatalf("tavan söylenmeli:\n%s", c)
	}
	// Deploy satırı yaş + sürüm; takım dışı deploy düşer.
	var dep teamSummary
	teamSummaryFromDeploys(&dep, []chstore.RecentDeployEntry{
		{Service: "checkout-service", Version: "2.4.1", FirstSeenNs: now.Add(-3 * time.Hour).UnixNano()},
		{Service: "other-svc", Version: "9.9.9", FirstSeenNs: now.UnixNano()},
	}, []string{"checkout-service", "orders-api"}, false)
	if d := renderTeamSummaryTR("platform", 2, dep, 3600, "", now); !strings.Contains(d, "checkout-service → 2.4.1 (3sa önce)") || strings.Contains(d, "other-svc") {
		t.Fatalf("deploy satırı:\n%s", d)
	}
}

func TestTeamSummaryChipsAndRank(t *testing.T) {
	var sum teamSummary
	teamSummaryFromProblems(&sum, []chstore.Problem{{Service: "orders-api", Priority: "P1"}}, false)
	teamSummaryFromDeploys(&sum, []chstore.RecentDeployEntry{{Service: "orders-api", Version: "1.0.1"}}, []string{"checkout-service", "orders-api", "billing"}, false)
	teamSummaryFromExceptions(&sum, nil)
	base := []string{"billing", "checkout-service", "orders-api"}
	// rerank=false → base aynen (takım RED sırası).
	if got := teamSummaryRank(base, sum, false); strings.Join(got, ",") != "billing,checkout-service,orders-api" {
		t.Fatalf("rerank=false sırayı bozdu: %v", got)
	}
	// rerank=true → P1'li servis öne; eşitlikte base sırası.
	ranked := teamSummaryRank(base, sum, true)
	if strings.Join(ranked, ",") != "orders-api,billing,checkout-service" {
		t.Fatalf("rerank: %v", ranked)
	}
	chips := teamSummaryChips(ranked, sum, "prod", guidedFollowupMax)
	wantLabels := "orders-api detayı|orders-api exception'ları|orders-api deploy etkisi|billing detayı"
	if strings.Join(fuLabels(chips), "|") != wantLabels {
		t.Fatalf("çipler %v", fuLabels(chips))
	}
	if chips[0].Text != "orders-api servisini analiz et" || chips[2].Text != "orders-api son deploy etkisi?" || chips[0].Scope.Env != "prod" {
		t.Fatalf("çip metinleri %v", fuTexts(chips))
	}
	// Deploy yoksa / okunamadıysa deploy çipi yok, iki servis × iki çip.
	noDep := teamSummary{}
	c2 := teamSummaryChips([]string{"checkout-service", "orders-api", "billing"}, noDep, "", guidedFollowupMax)
	if strings.Join(fuTexts(c2), "|") != "checkout-service servisini analiz et|checkout-service teknik hataları?|orders-api servisini analiz et|orders-api teknik hataları?" {
		t.Fatalf("deploysuz çipler %v", fuTexts(c2))
	}
	if teamSummaryChips(nil, sum, "", 4) != nil {
		t.Fatal("servissiz çip")
	}
}

func TestTeamSummaryKeyHashesAllInputs(t *testing.T) {
	to := time.Date(2026, 10, 19, 12, 0, 30, 0, time.UTC)
	from := to.Add(-time.Hour)
	k := teamSummaryKey("platform", []string{"orders-api", "checkout-service"}, "", from, to)
	if !strings.HasPrefix(k, "copilot:teamsum:v1:") {
		t.Fatalf("önek: %s", k)
	}
	// Servis sırası anahtarı değiştirmez.
	if k != teamSummaryKey("platform", []string{"checkout-service", "orders-api"}, "", from, to) {
		t.Fatal("sıra anahtarı değiştirdi")
	}
	diff := map[string]string{
		"takım":     teamSummaryKey("payments-team", []string{"orders-api", "checkout-service"}, "", from, to),
		"servis":    teamSummaryKey("platform", []string{"orders-api", "checkout-svc"}, "", from, to),
		"aynı sayı": teamSummaryKey("platform", []string{"orders-api", "billing"}, "", from, to),
		"env":       teamSummaryKey("platform", []string{"orders-api", "checkout-service"}, "prod", from, to),
		"pencere":   teamSummaryKey("platform", []string{"orders-api", "checkout-service"}, "", to.Add(-6*time.Hour), to),
		"an":        teamSummaryKey("platform", []string{"orders-api", "checkout-service"}, "", from.Add(-time.Hour), to.Add(-time.Hour)),
		"birleşim":  teamSummaryKey("platform", []string{"orders-apicheckout-service"}, "", from, to),
	}
	for name, d := range diff {
		if d == k {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
}
