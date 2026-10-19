package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// copilot_starters_test.go — v0.10.702. İki sözleşme: (1) saf çip üretici
// hatasız/uygunsuz girdide çip üretmez; (2) üretilen HER cümle (veri
// çipleri + FE'deki statik çipler) guided router'da LLM'siz beklenen
// rotaya düşer — çip bir menü değil, çalışan bir soru.
//
// v0.10.1382 — gruplu başlangıç seçenekleri (services / operations / p1 /
// deploys): her grubun saf üreticisi, tavanları, boş grubun yanıta girmemesi,
// v2 cache anahtarının env'i özetlemesi ve her şablonun LLM'siz yönlenmesi
// (operasyon cümlesi: katalog ön-getirmesinin belirteç + birebir eşleşme şekli).
//
// v0.10.1390 — services / operations grupları kalktı (operatör: "filo geneli
// ve böyle birçok servis yazmasın, search kısmı çıksa yeter"); yerlerinde
// arama kutuları. Kutulardan gönderilen İKİ cümle ("<svc> sağlığı nasıl?",
// "<OP> operasyonu neden hata veriyor?") burada yönlenme pinini korur;
// anahtar v3 (pencere girdisi yok).

func TestStarterP1Chips(t *testing.T) {
	probs := []chstore.Problem{
		{ID: "p-1", Service: "checkout-service", RuleName: "error rate", Priority: "P1"},
		{ID: "p-2", Service: "orders-api", Priority: "P2"},          // P1 değil
		{ID: "p-3", Service: "checkout-service", Priority: "P1"},    // aynı servis
		{ID: "p-4", Service: "db:oracle@eu-a.prod", Priority: "P1"}, // db öznesi
		{ID: "", Service: "payment-service", Priority: "P1"},        // kimliksiz
		{ID: "p-6", Service: "payment-service", Priority: "P1"},
		{ID: "p-7", Service: "inventory", Priority: "P1"},
		{ID: "p-8", Service: "api-gateway", Priority: "P1"}, // tavan dışı
	}
	got := starterP1Chips(probs, 3)
	if len(got) != 3 {
		t.Fatalf("üç P1 çipi beklenir: %+v", got)
	}
	if got[0].Question != "checkout-service kök nedeni ne?" || got[0].Kind != "p1" ||
		got[0].Scope == nil || got[0].Scope.Problem != "p-1" || len(got[0].Scope.Services) != 0 {
		t.Errorf("ilk P1 çipi: %+v scope=%+v", got[0], got[0].Scope)
	}
	if !strings.Contains(got[0].Chip, "error rate") {
		t.Errorf("etiket kural adını taşır: %q", got[0].Chip)
	}
	if got[1].Scope.Problem != "p-6" || got[2].Scope.Problem != "p-7" {
		t.Errorf("sıra/atlama: %+v", got)
	}
	if len(starterP1Chips([]chstore.Problem{{ID: "x", Service: "a", Priority: ""}}, 3)) != 0 {
		t.Fatal("öncelik boş → P3 sayılır, çip yok")
	}
}

func TestStarterDeployChips(t *testing.T) {
	deps := []chstore.RecentDeployEntry{
		{Service: "checkout-service", Version: "1.4.2"},
		{Service: "checkout-service", Version: "1.4.1"},
		{Service: "orders-api", Version: "2.0.0"},
		{Service: "payment-service", Version: ""},
	}
	fleet := starterDeployChips(deps, nil, 2)
	if len(fleet) != 2 || fleet[0].Question != "checkout-service son deploy etkisi?" || fleet[1].Question != "orders-api son deploy etkisi?" {
		t.Fatalf("filo: servis başına bir çip, tavan 2: %+v", fleet)
	}
	if fleet[0].Chip != "checkout-service → 1.4.2" || fleet[0].Kind != "deploy" {
		t.Errorf("etiket: %+v", fleet[0])
	}
	team := starterDeployChips(deps, []string{"payment-service", "inventory"}, 2)
	if len(team) != 1 || team[0].Chip != "payment-service" {
		t.Fatalf("takım kesişimi: %+v", team)
	}
	if len(starterDeployChips(deps, []string{}, 2)) != 0 {
		t.Fatal("boş takım kümesi → çip yok (filo değil)")
	}
}

func TestStarterGroupsOmitEmpty(t *testing.T) {
	one := []copilotStarter{{Chip: "a", Question: "a son deploy etkisi?", Kind: "deploy"}}
	gs := starterGroups(true, []copilotStarter{}, one)
	if len(gs) != 1 || gs[0].Kind != "deploys" || !gs[0].Fleet {
		t.Fatalf("boş grup yanıta girmez: %+v", gs)
	}
	p1 := []copilotStarter{{Chip: "b", Question: "b kök nedeni ne?", Kind: "p1"}}
	if gs := starterGroups(false, p1, one); len(gs) != 2 || gs[0].Kind != "p1" || gs[1].Kind != "deploys" || gs[0].Fleet {
		t.Fatalf("sıra sabit p1 → deploys: %+v", gs)
	}
	if len(starterGroups(false, nil, nil)) != 0 {
		t.Fatal("hepsi boş → grup yok")
	}
}

func TestStartersKey(t *testing.T) {
	a := startersKey("u1", "")
	if !strings.HasPrefix(a, "copilot:starters:v3:") {
		t.Fatalf("v3 öneki (v0.10.1390 — gruplar değişti, v2 kayıtları okunmasın): %q", a)
	}
	seen := map[string]string{}
	for _, c := range []struct {
		u   string
		env string
	}{{"u1", ""}, {"u1", "prod"}, {"u1", "uat"}, {"u2", "prod"}, {"u2", ""}} {
		k := startersKey(c.u, c.env)
		if prev, dup := seen[k]; dup {
			t.Fatalf("anahtar çakışması: %+v ile %s", c, prev)
		}
		seen[k] = c.u + "/" + c.env
	}
	if k1, k2 := startersKey("u1", "prod"), startersKey("u1", "prod"+""); k1 != k2 {
		t.Fatal("anahtar kararlı olmalı")
	}
}

// Üretilen cümleler router'da beklenen rotaya düşer. FE'deki statik çipler
// de burada pinli (copilotStarters.test.ts listeyi, bu test yönlenmeyi).
// v0.10.1382 — P1 kök neden ve deploy etkisi şablonları LLM'siz yönlenir.
// v0.10.1390 — başlangıç kartının arama kutuları: servis seçimi "<svc> sağlığı
// nasıl?" (service_health), operasyon seçimi "<OP> operasyonu neden hata
// veriyor?" — guided'dan ÖNCE katalog ön-getirmesinde birebir TEK eşleşme;
// hem Oracle kodu hem span operasyonu (aramanın döndüğü ad şekilleri).
func TestStarterQuestionsRoute(t *testing.T) {
	chips := starterP1Chips([]chstore.Problem{{ID: "p-1", Service: "payment-service", Priority: "P1"}}, 3)
	chips = append(chips, starterDeployChips([]chstore.RecentDeployEntry{{Service: "api-gateway", Version: "3.1"}}, nil, 2)...)
	want := map[string]struct {
		intent guidedIntent
		svc    string
	}{
		"p1":     {guidedRootCause, "payment-service"},
		"deploy": {guidedDeployImpact, "api-gateway"},
	}
	kinds := map[string]bool{}
	for _, c := range chips {
		kinds[c.Kind] = true
		r := routeGuidedIntent(c.Question, feServices, feEnvs, nil, "")
		w := want[c.Kind]
		if r.Intent != w.intent || r.Service != w.svc {
			t.Errorf("%s %q → %s/%q; want %s/%q", c.Kind, c.Question, r.Intent, r.Service, w.intent, w.svc)
		}
	}
	for k := range want {
		if !kinds[k] {
			t.Errorf("tür %s test kümesinde yok", k)
		}
	}
	// P1 kapsamı: problem kimliği kapsam kademesinde "problem" planına gider.
	p1 := chips[0]
	if pl := planChatScope("", *p1.Scope, p1.Question, p1.Question, feServices, feEnvs); pl.Kind != "problem" {
		t.Errorf("P1 kapsamı → %q; want problem", pl.Kind)
	}
	// Servis arama kutusu (capabilityHints serviceHealthQuestion) aynı cümleyi kurar.
	for _, svc := range []string{"inventory", "checkout-service"} {
		q := svc + " sağlığı nasıl?"
		if r := routeGuidedIntent(q, feServices, feEnvs, nil, ""); r.Intent != guidedServiceHealth || r.Service != svc {
			t.Errorf("servis arama seçimi %q → %s/%q", q, r.Intent, r.Service)
		}
	}
	// Operasyon arama kutusu (capabilityHints operationErrorQuestion): aramanın
	// döndüğü her ad (opSearchRoutable) ön-getirme belirteçlerinde kendisi olarak
	// çıkar ve katalogla BİREBİR tek operasyon eşleşir (çoklu seçim / bulanık değil).
	cat := []chstore.CatalogOperation{
		{Operation: "INSURANCE_DOC_PDF", Source: "oracle", CallCount: 9},
		{Operation: "ORDERS_LIST_GET", Source: "oracle", CallCount: 40},
		{Operation: "OrderService.PlaceOrder", Source: "span", Service: "checkout-service", CallCount: 120},
		{Operation: "OrderService.PlaceOrderV2", Source: "span", Service: "checkout-service", CallCount: 80},
		{Operation: "payments/authorize", Source: "span", Service: "payment-service", CallCount: 30},
	}
	for _, op := range []string{"INSURANCE_DOC_PDF", "OrderService.PlaceOrder", "payments/authorize"} {
		if !opSearchRoutable(op) {
			t.Fatalf("%q aramada önerilebilmeli", op)
		}
		q := op + " operasyonu neden hata veriyor?"
		toks := opTokens(q)
		m := matchOperations(toks, cat)
		if m.Kind != "exact" || len(m.Ops) != 1 || m.Ops[0] != op {
			t.Errorf("%q → belirteçler %v, eşleşme %+v; want exact %s", q, toks, m, op)
		}
		if topologyVetoesOpPrefetch(q) {
			t.Errorf("%q ön-getirmeden vetolanmamalı", q)
		}
		if _, _, ok := endpointMention(q); ok {
			t.Errorf("%q endpoint anması sayılmamalı (ön-getirme belirteç yoluna düşer)", q)
		}
	}
	static := []struct {
		q      string
		intent guidedIntent
	}{
		{"Takımımın servisleri nasıl?", guidedMyServices},
		{"Takımımın açık problemleri?", guidedMyProblems},
		{"Takımımın exception'ları?", guidedMyExceptions},
		{"En yavaş trace'ler?", guidedSlowTraces},
		{"Son 1 saatteki log hataları?", guidedLogErrors},
	}
	for _, c := range static {
		if got := routeGuidedIntent(c.q, feServices, feEnvs, nil, ""); got.Intent != c.intent {
			t.Errorf("statik çip %q → %s, beklenen %s", c.q, got.Intent, c.intent)
		}
	}
}
