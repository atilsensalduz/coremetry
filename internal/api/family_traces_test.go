package api

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// family_traces_test.go — v0.10.465 (CoSRE sohbet paritesi D2): "hatalı /
// yavaş trace'ler" + aile → trace LİSTESİ (aile sağlığı değil). Adlar SENTETİK.

func TestRouteFamilyTraces(t *testing.T) {
	cases := []struct {
		q      string
		intent guidedIntent
		fam    int
		errs   bool
	}{
		{"mobile bff son hatalı traceler", guidedFamilyTraces, 2, true},
		{"mobile bff'nin son 1 saatteki hatalı trace'lerini getir", guidedFamilyTraces, 2, true},
		{"mobile bff yavaş traceler", guidedFamilyTraces, 2, false},
		{"mobile bff en yavaş trace'ler", guidedFamilyTraces, 2, false},
		{"checkout-service ve payment-service hatalı trace'leri", guidedFamilyTraces, 2, true},
		// Tek servis + hatalı → hatalı liste (eskiden service_health'e çöküyordu).
		{"checkout hatalı traceler", guidedFamilyTraces, 1, true},
		// Tek servis + yalnız yavaş → mevcut rota AYNEN.
		{"checkout-service en yavaş trace'ler?", guidedSlowTraces, 0, false},
		// Trace kökü yok → aile sağlığı AYNEN.
		{"mobile bff hataları", guidedFamilyHealth, 2, false},
		// Kimse yok → bu kademe girmez.
		{"zzqx hatalı traceler", guidedProblems, 0, false},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, feServices, feEnvs, nil, "")
		if r.Intent != c.intent || len(r.Family) != c.fam || r.TraceErrorsOnly != c.errs {
			t.Errorf("%q → intent=%s fam=%v errs=%v; want %s/%d/%v", c.q, r.Intent, r.Family, r.TraceErrorsOnly, c.intent, c.fam, c.errs)
		}
	}
	// Sayfa bağlamı: adsız "hatalı traceler" o servisin hatalı listesi.
	r := routeGuidedIntent("hatalı traceler", feServices, feEnvs, nil, "payment-service")
	if r.Intent != guidedFamilyTraces || r.Service != "payment-service" || !r.TraceErrorsOnly {
		t.Errorf("bağlam: %+v", r)
	}
}

func TestFamilyTraceFilterAndHref(t *testing.T) {
	fam := []string{"mobile-commercial-bff-prod", "mobile-retail-bff-prod"}
	f := familyTraceFilter(fam, familyModeErrors, "prod", time.Now().Add(-time.Hour), time.Now())
	if f.Service != "" || len(f.Filters) != 1 || f.Filters[0].Op != "IN" || len(f.Filters[0].Values) != 2 || !f.HasError || f.Sort != "time" || f.Limit != 10 || f.Env != "prod" {
		t.Fatalf("aile süzgeci: %+v", f)
	}
	if err := chstore.ValidateFilters(f.Filters); err != nil {
		t.Fatalf("çip geçersiz: %v", err)
	}
	f1 := familyTraceFilter([]string{"checkout-service"}, familyModeSlow, "", time.Now().Add(-time.Hour), time.Now())
	if f1.Service != "checkout-service" || len(f1.Filters) != 0 || f1.HasError || f1.Sort != "duration" {
		t.Fatalf("tek servis süzgeci: %+v", f1)
	}
	h := familyTracesHref(fam, familyModeErrors)
	u, err := url.Parse(h)
	if err != nil || u.Path != "/traces" {
		t.Fatalf("href: %s %v", h, err)
	}
	q := u.Query()
	if q.Get("hasError") != "true" || q.Get("sort") != "time" || q.Get("order") != "desc" || !strings.Contains(q.Get("filters"), `"op":"IN"`) || !strings.Contains(q.Get("filters"), "mobile-retail-bff-prod") {
		t.Fatalf("href paramları: %s", h)
	}
	if h1 := familyTracesHref([]string{"checkout-service"}, familyModeSlow); !strings.Contains(h1, "service=checkout-service") || strings.Contains(h1, "hasError") || !strings.Contains(h1, "sort=duration") {
		t.Fatalf("tek servis href: %s", h1)
	}
}

func TestRenderFamilyTraces(t *testing.T) {
	rows := []chstore.TraceRow{{TraceID: "a1", RootName: "POST /pay", ServiceName: "mobile-retail-bff-prod", DurationMs: 812, SpanCount: 14, HasError: true, StartTime: 1_700_000_000_000_000_000}}
	txt := renderFamilyTracesEvidenceTR(rows, []string{"mobile-commercial-bff-prod", "mobile-retail-bff-prod"}, familyModeErrors, "", 3600)
	for _, want := range []string{"Hatalı trace'ler", "en yeniden eskiye", "812ms", "HATA", "trace=a1", "Kapsam 2 servis"} {
		if !strings.Contains(txt, want) {
			t.Errorf("%q yok:\n%s", want, txt)
		}
	}
	if e := renderFamilyTracesEvidenceTR(nil, []string{"x"}, familyModeSlow, "prod", 60); !strings.Contains(e, "En yavaş trace'ler") || !strings.Contains(e, "bulunamadı") || !strings.Contains(e, "ortam: prod") {
		t.Errorf("boş liste:\n%s", e)
	}
}

// Çipler ve linkler kendi rotalarına gider.
func TestFamilyTracesChipsAndLinks(t *testing.T) {
	r := routeGuidedIntent("mobile bff son hatalı traceler", feServices, feEnvs, nil, "")
	chips := guidedSuggestions(r)
	if len(chips) != 2 {
		t.Fatalf("çipler: %v", chips)
	}
	for _, c := range chips {
		if got := routeGuidedIntent(c, feServices, feEnvs, nil, ""); got.Intent != guidedLogErrors || got.Service == "" {
			t.Errorf("çip %q → %+v", c, got)
		}
	}
	links := guidedAnswerLinks(r, noLinkWindow())
	if len(links) != 3 || !strings.HasPrefix(links[0].Href, "/traces?") || !strings.Contains(links[0].Label, "hatalı") {
		t.Fatalf("linkler: %+v", links)
	}
}

// v0.10.1479 — exception sözcüğü trace'i niteliyor mu, yoksa soru exception
// gruplarının mı (trace yalnız örnek)?
func TestExceptionOwnsTraceAsk(t *testing.T) {
	for q, want := range map[string]bool{
		"orders-api exception grupları neler, örnek stack ve ilgili trace'i bul": true,
		"orders-api en sık exception hangisi, örnek trace bul":                   true,
		"orders-api istisnaları ve trace'leri":                                   true,
		"orders-api exception'lı trace'leri göster":                              false,
		"orders-api exceptionlı trace'ler":                                       false,
		"orders-api exception alan trace'leri bul":                               false,
		"orders-api hatalı trace'leri göster":                                    false,
	} {
		if got := exceptionOwnsTraceAsk(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("%q: got %v want %v", q, got, want)
		}
	}
}

// v0.10.1487 — düz trace listesi isteği nötr "son trace'ler" kipine girer;
// ek sözcük taşıyan sorular bugünkü yollarında kalır.
func TestPlainTraceListAsk(t *testing.T) {
	names, envs := []string{"orders-api"}, []string{"prod", "uat"}
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"orders-api trace'leri", true},
		{"orders-api trace'i bul", true},
		{"orders-api son trace'ler", true},
		{"orders-api trace'lerini göster", true},
		{"orders-api servisinin son 1 saatteki traceleri", true},
		{"orders-api prod son trace'leri getir", true},
		{"show recent traces for orders-api", true},
		{"orders-api nedir", false},
		{"orders-api trace sayfasını aç", false},
		{"orders-api trace'leri neden gelmiyor", false},
		{"orders-api trace sayısı", false},
		{"orders-api trace topolojisi", false},
		{"orders-api trace örnekleme oranı", false},
		{"orders-api tracing nasıl kurulur", false},
	} {
		if got := plainTraceListAsk(guidedTokens(normalizeGuidedMsg(c.q)), names, envs); got != c.want {
			t.Errorf("%q: got %v want %v", c.q, got, c.want)
		}
	}
}

func TestFamilyTracesRecentMode(t *testing.T) {
	services := []string{"orders-api", "billing-api"}
	r, ok := routeFamilyTraces("orders-api son trace'ler", guidedTokens("orders-api son trace'ler"), "orders-api", "", services, nil, "")
	if !ok || !r.TraceRecent || r.TraceErrorsOnly || r.Service != "orders-api" {
		t.Fatalf("nötr kip: ok=%v %+v", ok, r)
	}
	// Ekran servisi: servis adı yok, sayfa bağlamı kapsam.
	if r, ok := routeFamilyTraces("son trace'ler", guidedTokens("son trace'ler"), "", "", services, nil, "billing-api"); !ok || r.Service != "billing-api" || !r.TraceRecent {
		t.Errorf("ekran servisi: ok=%v %+v", ok, r)
	}
	// Kapsam yok → rota yok (bulanık aday üretimi nötr kipte kapalı).
	if _, ok := routeFamilyTraces("son trace'ler", guidedTokens("son trace'ler"), "", "", services, nil, ""); ok {
		t.Error("kapsamsız düz trace sorusu family_traces'e girmemeli")
	}
	f := familyTraceFilter([]string{"orders-api"}, familyModeRecent, "", time.Now().Add(-time.Hour), time.Now())
	if f.HasError || f.Sort != "time" || f.Order != "desc" || f.Limit != 10 || f.CountMode != "skip" || f.Service != "orders-api" {
		t.Errorf("son kip süzgeci (MV yolu: CountMode skip, LIMIT 10): %+v", f)
	}
	if h := familyTracesHref([]string{"orders-api"}, familyModeRecent); strings.Contains(h, "hasError") || !strings.Contains(h, "sort=time") {
		t.Errorf("son kip linki: %s", h)
	}
	// "sadece hatalı olanlar" takibi TraceErrorsOnly ekler: hata kipi kazanır.
	if m := familyTraceMode(guidedRoute{TraceRecent: true, TraceErrorsOnly: true}); m != familyModeErrors {
		t.Errorf("hata kipi önce olmalı: %s", m)
	}
	if e := renderFamilyTracesEvidenceTR(nil, []string{"orders-api"}, familyModeRecent, "", 3600); !strings.Contains(e, "Son trace'ler") {
		t.Errorf("son kip kanıtı: %s", e)
	}
	links := guidedAnswerLinks(guidedRoute{Intent: guidedFamilyTraces, Family: []string{"orders-api"}, Service: "orders-api", TraceRecent: true}, linkWindow{})
	if len(links) == 0 || links[0].Label != "Trace'ler (en yeni)" {
		t.Errorf("link: %+v", links)
	}
}
