package api

// v0.10.1397 — CoSRE takım akışı (copilot_team_overview.go). Operatör (prod):
//
//	A. "Takım Exceptions dediğimde Problems'i açıyor, halbuki ben
//	   Exceptions/Errors sayfasını filtrelemesini ve otomatik getirmesini
//	   istiyorum."
//	B. "Mesela bir takım seçtim veya 'takımımın servisleri nasıl' dedim. Arkada
//	   chip'e basmadan otomatik o takımın servislerini listelesin ve spesifik
//	   olarak kontrol etmek istediğin bir servis var mı diye sorsun."
//
// Saf; CH / model yok. Kilitlenenler: her takım cevabının Exceptions linki
// Exceptions/Errors sayfası (/problems?owner|sre=…&s_exception-inbox-v2=
// lastSeen.desc), Inbox köprüsü yok; sunucu eki deterministik servis
// tablosu + son exception grupları (OKUNAMADI ≠ yok) + kapanış sorusu; servis
// çipleri LLM'siz service_health'e, "hangi takım?" çipleri team_services'e
// yönlenir; my_services aynı gövdeden geçer ve ek cevap metnine girer.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

var toNow = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

// A — her takım cevabının Exceptions linki Exceptions/Errors sayfasına gider.
func TestTeamAnswersExceptionsLinkToExceptionsPage(t *testing.T) {
	intents := []guidedIntent{guidedTeamServices, guidedMyServices, guidedMyExceptions, guidedTeamExceptions}
	axes := map[string][]string{
		teamAxisOwner: {"/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc"},
		teamAxisSRE:   {"/problems?s_exception-inbox-v2=lastSeen.desc&sre=platform"},
		teamAxisBoth:  {"/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc", "/problems?s_exception-inbox-v2=lastSeen.desc&sre=platform"},
	}
	for _, in := range intents {
		for axis, want := range axes {
			links := guidedAnswerLinks(guidedRoute{Intent: in, Team: "platform", ExcAxis: axis,
				TeamServices: []string{"checkout-service"}}, linkWindowBetween(toNow.Add(-time.Hour), toNow))
			var exc []string
			for _, l := range links {
				if strings.Contains(l.Href, "/inbox") {
					t.Errorf("%s/%s: takım cevabında Inbox linki kaldı: %+v", in, axis, l)
				}
				if strings.HasPrefix(l.Href, "/problems?") {
					exc = append(exc, l.Href)
				}
			}
			if strings.Join(exc, " ") != strings.Join(want, " ") {
				t.Errorf("%s/%s: Exceptions linkleri %v; want %v", in, axis, exc, want)
			}
		}
	}
	// Kaynak-pin: Inbox köprüsü (eski inboxTeamExceptionsLink) geri gelmesin.
	for _, f := range []string{"copilot_followup.go", "copilot_team_exceptions.go", "copilot_team_overview.go"} {
		src := guidedSharedSource(t, f)
		if strings.Contains(src, "inbox?kind=exception&team") || strings.Contains(src, "inboxTeamExceptionsLink") {
			t.Errorf("%s: takım Exceptions linki Inbox'a gidiyor", f)
		}
	}
}

// Servisler linki takım ekseniyle süzgeçli; takımsız düz.
func TestTeamServicesPageHref(t *testing.T) {
	cases := []struct{ team, axis, want string }{
		{"platform", teamAxisOwner, "/services?ownerTeam=platform"},
		{"platform", teamAxisBoth, "/services?ownerTeam=platform"},
		{"payments-team", teamAxisSRE, "/services?sreTeam=payments-team"},
		{"SY Dijital", "", "/services?ownerTeam=SY+Dijital"},
	}
	for _, c := range cases {
		if got, ok := teamServicesPageHref(c.team, c.axis); !ok || got != c.want {
			t.Errorf("%s/%s: %q,%v; want %q", c.team, c.axis, got, ok, c.want)
		}
	}
	if _, ok := teamServicesPageHref("  ", teamAxisOwner); ok {
		t.Error("boş takımda link üretildi")
	}
	if l := teamServicesAnswerLinks(guidedRoute{Intent: guidedMyServices}); len(l) != 1 || l[0].Href != "/services" {
		t.Errorf("takımsız cevap düz Servisler linki almalı: %+v", l)
	}
}

func toRows(n int) []chstore.ServiceSummary {
	rows := make([]chstore.ServiceSummary, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, chstore.ServiceSummary{
			Name: string(rune('a'+i)) + "-svc", ErrorRate: float64(20 - i), ErrorCount: uint64(20 - i),
			SpanCount: 2857, P99Ms: 840,
		})
	}
	return rows
}

func toView(rows []chstore.ServiceSummary, ev exceptionGroupsEvidence) teamOverviewView {
	href, _ := exceptionsPageTeamHref("platform", teamAxisOwner, "", false)
	svcHref, _ := teamServicesPageHref("platform", teamAxisOwner)
	return teamOverviewView{
		Team: "platform", Total: 25, ReadSvcs: 22, Trimmed: 3, Rows: rows, RangeS: 1800, ServicesHref: svcHref,
		Exc: teamExcView{Team: "platform", Services: 22, PageHref: href, Axis: teamAxisOwner, Ev: ev},
	}
}

// B — deterministik servis tablosu + "+N daha" + Servisler linki + kapanış sorusu.
func TestRenderTeamOverviewServicesList(t *testing.T) {
	ev := exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: 86400, at: toNow}
	out := renderTeamOverviewTR(toView(toRows(20), ev))
	if !strings.HasPrefix(out, "\n\n**platform takımının servisleri** — 25 servis, hata oranına göre (son 30dk):") {
		t.Fatalf("başlık:\n%s", out)
	}
	if !strings.Contains(out, "| Servis | Hata oranı | Hata | Span | p99 |") {
		t.Errorf("tablo başlığı yok:\n%s", out)
	}
	if !strings.Contains(out, "| a-svc | %20.00 | 20 | 2,857 | 840 ms |") {
		t.Errorf("satır biçimi (ad · oran · hata · span · p99):\n%s", out)
	}
	if strings.Count(out, "-svc |") != teamOverviewListMax || strings.Contains(out, "| p-svc |") {
		t.Errorf("satır tavanı %d aşıldı/eksik:\n%s", teamOverviewListMax, out)
	}
	// 25 toplam − 15 gösterilen = 10: 5 daha düşük oranlı, 2 sessiz, 3 okunmadı.
	if !strings.Contains(out, "+10 servis daha (5 tanesi daha düşük hata oranlı, 2 tanesi bu pencerede span üretmedi, 3 tanesi okunmadı (tavan 100)). Tümü: [Servisler · platform](/services?ownerTeam=platform)") {
		t.Errorf("+N daha satırı / Servisler linki:\n%s", out)
	}
	if !strings.HasSuffix(out, "**"+teamOverviewClosingQ+"** Alttaki servis çiplerinden birini seç ya da servis adını yaz.") {
		t.Errorf("kapanış sorusu sonda değil:\n%s", out)
	}
	// Model ekinin ve exception bölümünün sırası: tablo → exception → soru.
	iTab, iExc, iQ := strings.Index(out, "| Servis |"), strings.Index(out, "**Son exception grupları**"), strings.Index(out, teamOverviewClosingQ)
	if !(iTab < iExc && iExc < iQ) {
		t.Errorf("bölüm sırası tablo→exception→soru değil (%d,%d,%d)", iTab, iExc, iQ)
	}

	// Pencerede veri yok: adlar yine listelenir (boş liste çıkmazdır).
	v := toView(nil, ev)
	v.Total, v.ReadSvcs, v.Trimmed, v.Names = 3, 3, 0, []string{"checkout-service", "orders-api", "payment-service"}
	empty := renderTeamOverviewTR(v)
	if !strings.Contains(empty, "hiçbirinden span verisi yok") || !strings.Contains(empty, "- orders-api\n") {
		t.Errorf("veri yokken ad listesi:\n%s", empty)
	}
	if strings.Contains(empty, "servis daha") || !strings.Contains(empty, "Servisler sayfasında: [Servisler · platform](/services?ownerTeam=platform)") {
		t.Errorf("tam liste + link:\n%s", empty)
	}
	if !strings.Contains(empty, teamOverviewClosingQ) {
		t.Errorf("veri yokken de soru sorulmalı:\n%s", empty)
	}
	// Boru içeren ad tabloyu bozmaz.
	if got := mdCell("a|b\nc"); got != `a\|b c` {
		t.Errorf("mdCell: %q", got)
	}
}

// A — son exception grupları: gruplar (en son görülen önce, detay linki) /
// "Yok" / "OKUNAMADI" hiç karışmaz; sayfa linki Exceptions/Errors.
func TestRenderTeamOverviewLatestExceptions(t *testing.T) {
	groups := exceptionGroupsEvidence{Groups: []exGroupLine{teOraLine(), teSpanLine()}, More: 3, LookbackS: 86400, at: teNow}
	out := renderTeamOverviewTR(toView(toRows(2), groups))
	for _, want := range []string{
		"**Son exception grupları** — Exceptions/Errors, açık gruplar, son 1gün, en son görülen önce:\n",
		"1. [teknik hata · Oracle] `ORA-01555` · INSURANCE_DOC_PDF · checkout-service, orders-api (+1) · son görülme 12dk önce · 1,204 oluşum · **P1** · [detay](/problems?exc=ora%3Aabc123)\n",
		"2. `java.lang.NullPointerException` · orders-api · son görülme 1sa 30dk önce · 37 oluşum · **P3** · [detay](/problems?exc=a1b2c3d4)\n",
		"(+3 grup daha — tümü Exceptions/Errors'ta)\n",
		"[Exceptions/Errors'ta aç · platform](/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("eksik %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "OKUNAMADI") || strings.Contains(out, "Yok —") {
		t.Errorf("gruplu bölümde OKUNAMADI/Yok:\n%s", out)
	}

	none := renderTeamOverviewTR(toView(toRows(2), exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: 86400, at: teNow}))
	if !strings.Contains(none, "Yok — son 1gün içinde takımın servislerinde açık exception grubu bulunmadı.") || strings.Contains(none, "OKUNAMADI") {
		t.Errorf("boş = yok:\n%s", none)
	}
	failed := renderTeamOverviewTR(toView(toRows(2), exceptionGroupsEvidence{Err: "code: 159, timeout\n exceeded", LookbackS: 86400, at: teNow}))
	if !strings.Contains(failed, "OKUNAMADI (okuma başarısız: code: 159, timeout exceeded)") || strings.Contains(failed, "Yok —") {
		t.Errorf("okuma hatası = OKUNAMADI:\n%s", failed)
	}
	// Model notu da üç hâli ayırır.
	for ev, want := range map[*exceptionGroupsEvidence]string{
		&groups:                   "(2 grup)",
		{Groups: []exGroupLine{}}: "(yok)",
		{Err: "x"}:                "(OKUNAMADI)",
	} {
		v := toView(toRows(2), *ev)
		if note := teamOverviewEvidenceTR(v); !strings.Contains(note, "son exception grupları "+want) || !strings.Contains(note, "TEKRAR YAZMA") {
			t.Errorf("model notu %q:\n%s", want, note)
		}
	}
	// İki eksenli takım: SRE yarısı aynı sayfanın sre ekseni.
	v := toView(toRows(1), groups)
	v.Exc.Axis = teamAxisBoth
	if both := renderTeamOverviewTR(v); !strings.Contains(both, "· [SRE süzgeçli](/problems?s_exception-inbox-v2=lastSeen.desc&sre=platform)") {
		t.Errorf("iki eksen SRE linki:\n%s", both)
	}
}

// B — "hangi servis?" çipleri: etiket servis adı, metin "<svc> sağlığı nasıl?",
// router'da LLM'siz service_health; uygunsuz adlar atlanır, tavan 6.
func TestTeamServiceAskChipsRouteServiceHealth(t *testing.T) {
	names := []string{"checkout-service", "oracle:eu-src", "bad name", "payment-service", "checkout-service",
		"mobile-bff", "ledger-service", "auth-service", "mobile-bff-uat", "orders-api"}
	chips := teamServiceAskChips(names, "prod", teamOverviewChipMax)
	want := []string{"checkout-service", "payment-service", "mobile-bff", "ledger-service", "auth-service", "mobile-bff-uat"}
	if len(chips) != len(want) {
		t.Fatalf("çipler %v; want %v", fuLabels(chips), want)
	}
	for i, c := range chips {
		if c.Label != want[i] || c.Text != want[i]+" sağlığı nasıl?" || c.Kind != fuKindTeamService {
			t.Errorf("çip %d: %+v", i, c)
		}
		if c.Scope == nil || c.Scope.Env != "prod" || len(c.Scope.Services) != 1 || c.Scope.Services[0] != want[i] {
			t.Errorf("kapsam servis+env olmalı: %+v", c.Scope)
		}
		r := routeGuidedIntent(c.Text, guidedTestServices, guidedTestEnvs, teamTestTeams, "")
		if r.Intent != guidedServiceHealth || r.Service != want[i] {
			t.Errorf("çip %q LLM'siz service_health'e yönlenmiyor: %s/%s", c.Text, r.Intent, r.Service)
		}
	}
	// Servis çipleri + özet adımları birlikte taşınır (tavan 6+4); exception
	// rotalarının tavanı değişmedi.
	summary := []chatFollowup{
		{Text: serviceReportChip("checkout-service"), Label: "checkout-service detayı", Kind: fuKindReport},
		{Text: "checkout-service teknik hataları?", Label: "checkout-service exception'ları", Kind: fuKindExceptions},
		{Text: serviceReportChip("payment-service"), Label: "payment-service detayı", Kind: fuKindReport},
		{Text: "payment-service teknik hataları?", Label: "payment-service exception'ları", Kind: fuKindExceptions},
	}
	all := append(append([]chatFollowup(nil), chips...), summary...)
	for _, in := range []guidedIntent{guidedTeamServices, guidedMyServices} {
		if got := guidedFollowups(guidedRoute{Intent: in, Team: "platform", TeamFollowups: all}, nil); len(got) != teamOverviewFollowupMax {
			t.Errorf("%s: %d çip; want %d (%v)", in, len(got), teamOverviewFollowupMax, fuLabels(got))
		}
	}
	if got := guidedFollowups(guidedRoute{Intent: guidedTeamExceptions, Team: "platform", TeamFollowups: all}, nil); len(got) != guidedFollowupMax {
		t.Errorf("team_exceptions tavanı değişmemeli: %d", len(got))
	}
}

// B — "Hangi takımdasın?" çipi doğrudan team_services cevabına iner (çıplak ad,
// LLM'siz router), servisle aynı adlı takım dahil.
func TestAskTeamChipRoutesToTeamServices(t *testing.T) {
	entries := []mcptools.TeamCatalogueEntry{
		{Team: "platform", Services: 9, Owner: 9},
		{Team: "payments-team", Services: 7, SRE: 7},
		{Team: "payment-service", Services: 2, Owner: 2},
	}
	teams := []string{"platform", "payments-team", "payment-service"}
	opts, _, _, _ := teamAskOptions(entries, guidedTeamAskMax)
	if len(opts) != 3 {
		t.Fatalf("seçenekler: %v", opts)
	}
	sugg := guidedSuggestions(guidedRoute{Intent: guidedMyServices, TeamOptions: opts})
	for _, q := range sugg {
		r := routeGuidedIntent(q, guidedTestServices, guidedTestEnvs, teams, "")
		if r.Intent != guidedTeamServices || r.Team != q {
			t.Errorf("takım çipi %q → %s/%q; team_services bekleniyordu", q, r.Intent, r.Team)
		}
		if !mayNameTeam(normalizeGuidedMsg(q)) && !hasGuidedSignal(normalizeGuidedMsg(q)) {
			t.Errorf("takım çipi %q hızlı-çıkışa takılır", q)
		}
	}
	if a := askTeamAnswerTR("", entries, guidedTeamAskMax); !strings.Contains(a, "Hangi takımdasın?") {
		t.Errorf("takım sorusu: %s", a)
	}
}

// Kaynak-pin: ek anlatımın arkasına, künyeden önce girer; my_services aynı
// gövdeden geçer; team_services gövdesi eki kurar.
func TestTeamOverviewWiring(t *testing.T) {
	src := guidedSharedSource(t, "copilot_guided.go")
	if !strings.Contains(src, `strings.TrimSpace(raw) + route.AnswerTail + "\n\nKaynak: "`) {
		t.Error("route.AnswerTail anlatım ile Kaynak künyesi arasına girmiyor — ek cevaba ulaşmaz")
	}
	body := func(sig string) string {
		i := strings.Index(src, sig)
		if i < 0 {
			t.Fatalf("%s bulunamadı", sig)
		}
		b := src[i:]
		if j := strings.Index(b[1:], "\nfunc "); j >= 0 {
			b = b[:j]
		}
		return b
	}
	if !strings.Contains(body("func (s *Server) guidedTeamServicesBundle("), "s.teamOverviewBlock(") {
		t.Error("team_services gövdesi sunucu ekini kurmuyor")
	}
	my := body("func (s *Server) guidedMyTeamBundle(")
	if !strings.Contains(my, "s.guidedTeamServicesBundle(ctx, emit, route, from, to, rangeS)") || strings.Contains(my, "guidedFamilyHealthBundle(") {
		t.Error("my_services adı geçen takımın gövdesinden geçmiyor — iki takım cevabı ayrışır")
	}
}
