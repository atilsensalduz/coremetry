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
//
// v0.10.1402 (operatör: "Son deploy etkisini kaldırabiliriz") — deploys grubu
// kalktı (yalnız p1); anahtar v4. deploy_impact rotası sohbette durur.
//
// v0.10.1404 (operatör, prod ekranı: "P1 yerine en son exception'lara
// yönlendirsin.") — p1 grubu kalktı; yerinde exceptions grubu: en son AÇIK
// exception grupları (new/acknowledged/regressed), son görülme yeni önce, en
// çok 3, takım ya da filo; okuma 3 s'de düşerse grup yok. Öğe = açıklama
// niyeti (explainAsk {exception, <parmak izi>, starter}); "Tüm son
// exception'lar" çipinin iki cümlesi (FE capabilityHints) burada yönlenir.
// Anahtar v5. Sentetik adlar.
//
// v0.10.1515 (operatör 2026-10-24) — exceptions grubu kalktı (kartta takım
// seçici → Exceptions sayfası); grup üreticisi ve okuma testleri gitti, uç
// yalnız takımı döner. Anahtar v6. Yönlenme pini (aşağıda) aynen kalır: Takımım
// kartının "Exception'ları?" çipi aynı iki cümleyi gönderir.

func TestStartersKey(t *testing.T) {
	a := startersKey("u1", "")
	if !strings.HasPrefix(a, "copilot:starters:v6:") {
		t.Fatalf("v6 öneki (v0.10.1515 — exceptions grubu kalktı, v5 kayıtları okunmasın): %q", a)
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
// v0.10.1404 — P1 şablonu kalktı. Exception grubu çipi router'a GİTMEZ
// (açıklama niyeti — TestStarterExceptionChips normalizeExplainAsk'i pinler);
// "Tüm son exception'lar" çipinin iki cümlesi takım exception cevabına yönlenir.
// v0.10.1390 — başlangıç kartının arama kutuları: servis seçimi "<svc> sağlığı
// nasıl?" (service_health), operasyon seçimi "<OP> operasyonu neden hata
// veriyor?" — guided'dan ÖNCE katalog ön-getirmesinde birebir TEK eşleşme;
// hem Oracle kodu hem span operasyonu (aramanın döndüğü ad şekilleri).
func TestStarterQuestionsRoute(t *testing.T) {
	// v0.10.1404 — "Tüm son exception'lar" (capabilityHints allExceptionsQuestion):
	// takım biliniyorsa adı geçen takım, değilse iyelik.
	for _, c := range []struct {
		q      string
		intent guidedIntent
		team   string
	}{
		{"orders-team takımının exception'ları?", guidedTeamExceptions, "orders-team"},
		{"Takımımın exception'ları?", guidedMyExceptions, ""},
	} {
		// Adı geçen takım dalı takım kataloğuyla açılır — Takımım kartının
		// varsayılan (sunucu) takımla gönderdiği cümleyle AYNI koşul (copilot_team_card_test).
		if r := routeGuidedIntent(c.q, feServices, feEnvs, []string{"orders-team", "platform"}, ""); r.Intent != c.intent || r.Team != c.team {
			t.Errorf("tüm son exception'lar %q → %s/%q; want %s/%q", c.q, r.Intent, r.Team, c.intent, c.team)
		}
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
