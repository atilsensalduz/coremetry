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

func TestStarterServiceChips(t *testing.T) {
	route := &chstore.EndpointRow{Service: "checkout-service", Path: "/api/checkout", Errors: 7}
	if got := starterServiceChips(nil, route, 3600, 3); len(got) != 0 {
		t.Fatalf("girdi yok → çip yok (yol çipi de yok): %+v", got)
	}
	rows := []chstore.ServiceSummary{
		{Name: "checkout-service", ErrorCount: 12, ErrorRate: 3.1},
		{Name: "orders-api", ErrorCount: 0},
		{Name: "oracle:eu-a-src", ErrorCount: 4},  // sentetik önek: atlanır
		{Name: "checkout-service", ErrorCount: 1}, // tekrar: atlanır
		{Name: "payment-service", ErrorCount: 2},
		{Name: "inventory", ErrorCount: 1}, // tavan dışı
	}
	got := starterServiceChips(rows, route, 3600, 3)
	if len(got) != 4 {
		t.Fatalf("3 servis + 1 yol çipi beklenir: %+v", got)
	}
	want := []struct{ kind, q string }{
		{"service_errors", "checkout-service son 1 saatte neden hata veriyor?"},
		{"service_health", "orders-api sağlığı nasıl?"},
		{"service_errors", "payment-service son 1 saatte neden hata veriyor?"},
		{"endpoint_errors", "/api/checkout hatalı trace'lerini getir"},
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Question != w.q {
			t.Errorf("[%d] = %s/%q; want %s/%q", i, got[i].Kind, got[i].Question, w.kind, w.q)
		}
	}
	for _, bad := range []string{"pkg.Svc/Method", "orders process", "/", ""} {
		g := starterServiceChips(rows[:1], &chstore.EndpointRow{Path: bad, Errors: 3}, 3600, 3)
		if len(g) != 1 {
			t.Errorf("uygunsuz yol %q endpoint çipi üretmemeli: %+v", bad, g)
		}
	}
	if g := starterServiceChips(rows[:1], &chstore.EndpointRow{Path: "/api/ok", Errors: 0}, 3600, 3); len(g) != 1 {
		t.Errorf("hatasız yol endpoint çipi üretmemeli: %+v", g)
	}
	if w := worstTeamService([]chstore.ServiceSummary{{Name: "a"}, {Name: "b", ErrorCount: 2}, {Name: "c", ErrorCount: 9}}); w == nil || w.Name != "b" {
		t.Fatalf("sıralı listede hata taşıyan İLK servis: %+v", w)
	}
	if worstTeamService([]chstore.ServiceSummary{{Name: "a"}}) != nil {
		t.Fatal("hatasız liste → nil")
	}
	eps := []chstore.EndpointRow{{Path: "orders process", Errors: 9}, {Path: "/api/x", Errors: 0}, {Path: "/api/y", Errors: 4}}
	if e := worstEndpoint(eps); e == nil || e.Path != "/api/y" {
		t.Fatalf("uygun ve hatalı ilk yol: %+v", e)
	}
}

func TestStarterWindowTR(t *testing.T) {
	for _, c := range []struct {
		s    int
		want string
	}{{3600, "son 1 saatte"}, {21600, "son 6 saatte"}, {86400, "son 24 saatte"}, {1800, "son 30 dakikada"}, {300, "son 5 dakikada"}} {
		if got := starterWindowTR(c.s); got != c.want {
			t.Errorf("%d → %q; want %q", c.s, got, c.want)
		}
	}
}

func TestStarterOperationChips(t *testing.T) {
	groups := []chstore.ExceptionGroup{
		{Fingerprint: "ora:0123456789abcdef", Message: "INSURANCE_DOC_PDF", Service: "policy-service"},
		{Fingerprint: "ora:1111111111111111", Message: "ABC", Service: "policy-service"},           // <4 karakter
		{Fingerprint: "ora:2222222222222222", Message: "two words", Service: "policy-service"},     // boşluk
		{Fingerprint: "ora:3333333333333333", Message: "INSURANCE_DOC_PDF", Service: "orders-api"}, // tekrar
		{Fingerprint: "a1b2", Type: "java.lang.NullPointerException", Service: "checkout-service"},
		{Fingerprint: "a1b3", Type: "java.io.IOException", Service: "checkout-service"}, // aynı servis
		{Fingerprint: "a1b4", Type: "X", Service: "oracle:eu-a-src"},                    // sentetik
		{Fingerprint: "a1b5", Type: "Y", Service: "orders-api"},
		{Fingerprint: "a1b6", Type: "Z", Service: "inventory"}, // tavan dışı
	}
	got := starterOperationChips(groups, 3)
	want := []struct{ kind, q string }{
		{"operation", "INSURANCE_DOC_PDF operasyonu neden hata veriyor?"},
		{"service_exceptions", "checkout-service teknik hataları?"},
		{"service_exceptions", "orders-api teknik hataları?"},
	}
	if len(got) != len(want) {
		t.Fatalf("üç çip beklenir: %+v", got)
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].Question != w.q {
			t.Errorf("[%d] = %s/%q; want %s/%q", i, got[i].Kind, got[i].Question, w.kind, w.q)
		}
	}
	if len(starterOperationChips(nil, 3)) != 0 {
		t.Fatal("grup yok → çip yok")
	}
}

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
	one := []copilotStarter{{Chip: "a", Question: "a sağlığı nasıl?", Kind: "service_health"}}
	gs := starterGroups(true, one, nil, []copilotStarter{}, one)
	if len(gs) != 2 || gs[0].Kind != "services" || gs[1].Kind != "deploys" || !gs[0].Fleet {
		t.Fatalf("boş grup yanıta girmez, sıra sabit: %+v", gs)
	}
	if len(starterGroups(false, nil, nil, nil, nil)) != 0 {
		t.Fatal("hepsi boş → grup yok")
	}
}

func TestStartersKey(t *testing.T) {
	a := startersKey("u1", 3600, "")
	if !strings.HasPrefix(a, "copilot:starters:v2:") {
		t.Fatalf("v2 öneki: %q", a)
	}
	seen := map[string]string{}
	for _, c := range []struct {
		u   string
		r   int
		env string
	}{{"u1", 3600, ""}, {"u1", 3600, "prod"}, {"u1", 3600, "uat"}, {"u2", 3600, "prod"}, {"u1", 7200, "prod"}} {
		k := startersKey(c.u, c.r, c.env)
		if prev, dup := seen[k]; dup {
			t.Fatalf("anahtar çakışması: %+v ile %s", c, prev)
		}
		seen[k] = c.u + "/" + c.env
	}
	if k1, k2 := startersKey("u1", 3600, "prod"), startersKey("u1", 3600, "prod"+""); k1 != k2 {
		t.Fatal("anahtar kararlı olmalı")
	}
}

// Üretilen cümleler router'da beklenen rotaya düşer. FE'deki statik çipler
// de burada pinli (copilotStarters.test.ts listeyi, bu test yönlenmeyi).
// v0.10.1382 — yeni grupların HER şablonu da (servis hata/sağlık, teknik
// hatalar, P1 kök neden, deploy etkisi, yerel servis çipi) LLM'siz yönlenir;
// operasyon cümlesi guided'dan ÖNCE katalog ön-getirmesinde birebir eşleşir.
func TestStarterQuestionsRoute(t *testing.T) {
	rows := []chstore.ServiceSummary{{Name: "checkout-service", ErrorCount: 12}, {Name: "payment-service"}}
	route := &chstore.EndpointRow{Path: "/api/checkout", Errors: 7}
	chips := starterServiceChips(rows, route, 3600, 3)
	chips = append(chips, starterServiceChips([]chstore.ServiceSummary{{Name: "inventory", ErrorCount: 1}}, nil, 21600, 3)...)
	chips = append(chips, starterOperationChips([]chstore.ExceptionGroup{{Fingerprint: "a1", Service: "checkout-service"}}, 3)...)
	chips = append(chips, starterP1Chips([]chstore.Problem{{ID: "p-1", Service: "payment-service", Priority: "P1"}}, 3)...)
	chips = append(chips, starterDeployChips([]chstore.RecentDeployEntry{{Service: "api-gateway", Version: "3.1"}}, nil, 2)...)
	want := map[string]struct {
		intent guidedIntent
		svc    string
	}{
		"service_errors":     {guidedRootCause, ""},
		"service_health":     {guidedServiceHealth, ""},
		"service_exceptions": {guidedServiceExceptions, "checkout-service"},
		"p1":                 {guidedRootCause, "payment-service"},
		"deploy":             {guidedDeployImpact, "api-gateway"},
	}
	kinds := map[string]bool{}
	for _, c := range chips {
		kinds[c.Kind] = true
		r := routeGuidedIntent(c.Question, feServices, feEnvs, nil, "")
		if c.Kind == "endpoint_errors" {
			if r.Intent != guidedEndpointTraces || r.SearchText != "/api/checkout" || !r.TraceErrorsOnly {
				t.Errorf("%q → %s/%q errs=%v; want endpoint_traces//api/checkout/true", c.Question, r.Intent, r.SearchText, r.TraceErrorsOnly)
			}
			continue
		}
		w := want[c.Kind]
		if r.Intent != w.intent {
			t.Errorf("%s %q → %s; want %s", c.Kind, c.Question, r.Intent, w.intent)
		}
		if w.svc != "" && r.Service != w.svc {
			t.Errorf("%s %q → servis %q; want %q", c.Kind, c.Question, r.Service, w.svc)
		}
		if r.Service == "" {
			t.Errorf("%s %q servis çözmedi", c.Kind, c.Question)
		}
	}
	for k := range want {
		if !kinds[k] {
			t.Errorf("tür %s test kümesinde yok", k)
		}
	}
	// P1 kapsamı: problem kimliği kapsam kademesinde "problem" planına gider.
	p1 := starterP1Chips([]chstore.Problem{{ID: "p-1", Service: "payment-service", Priority: "P1"}}, 3)[0]
	if pl := planChatScope("", *p1.Scope, p1.Question, p1.Question, feServices, feEnvs); pl.Kind != "problem" {
		t.Errorf("P1 kapsamı → %q; want problem", pl.Kind)
	}
	// Operasyon cümlesi: ön-getirme belirteçleri kodu taşır ve katalogla
	// BİREBİR tek operasyon eşleşir (çoklu seçim / bulanık değil).
	op := starterOperationChips([]chstore.ExceptionGroup{{Fingerprint: "ora:0123456789abcdef", Message: "INSURANCE_DOC_PDF"}}, 3)[0]
	toks := opTokens(op.Question)
	m := matchOperations(toks, []chstore.CatalogOperation{{Operation: "INSURANCE_DOC_PDF", CallCount: 9}, {Operation: "ORDERS_LIST_GET", CallCount: 40}})
	if m.Kind != "exact" || len(m.Ops) != 1 || m.Ops[0] != "INSURANCE_DOC_PDF" {
		t.Errorf("%q → belirteçler %v, eşleşme %+v; want exact INSURANCE_DOC_PDF", op.Question, toks, m)
	}
	// FE yerel servis çipi (capabilityHints localServiceChips) aynı cümleyi kurar.
	if r := routeGuidedIntent("inventory sağlığı nasıl?", feServices, feEnvs, nil, ""); r.Intent != guidedServiceHealth || r.Service != "inventory" {
		t.Errorf("yerel servis çipi → %s/%q", r.Intent, r.Service)
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
