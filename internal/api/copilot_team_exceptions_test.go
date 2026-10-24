package api

// v0.10.1385 — operatör: "Ekibimin teknik hataları var mı diye sorduğumda
// CoSRE arkada Exceptions açıp filtrelenebilir ve chatte en son exceptions
// errorleri getirebilir."
//
// Pinler: (1) yönlendirme tablosu — iyelik ve adı geçen takım, @team kapsamı;
// my_problems / my_services / team_services yolları bozulmaz; (2) saf kanıt
// render'ı (Oracle + span grupları, boş, OKUNAMADI ≠ yok); (3) Exceptions
// sayfa link kurucusu (takım ekseni, env, oracle facet, safelink); (4) grup
// çipleri LLM'siz yönlenir; (5) okuma süzgeci (Services/open/taban/son görülme);
// (6) link param'ları sayfanın GERÇEKTEN okuduğu adlar (K4 kaynak pini).

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/safelink"
)

var (
	teServices = []string{"checkout-service", "orders-api", "inventory"}
	teEnvs     = []string{"prod", "uat"}
	teTeams    = []string{"platform", "payments-team"}
	teNow      = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
)

func TestTeamExceptionsRouting(t *testing.T) {
	cases := []struct {
		q       string
		intent  guidedIntent
		team    string
		techErr bool
	}{
		// İyelik → my_exceptions (kimlikten takım).
		{"Ekibimin teknik hataları var mı?", guidedMyExceptions, "", true},
		{"takımımın teknik hataları", guidedMyExceptions, "", true},
		{"ekibimin exception'ları", guidedMyExceptions, "", false},
		{"takımımda hata var mı", guidedMyExceptions, "", false},
		{"ekibimde hata var mı?", guidedMyExceptions, "", false},
		{"benim takımın teknik hataları neler", guidedMyExceptions, "", true},
		{"takımımın exception'ları neler", guidedMyExceptions, "", false},
		// Adı geçen takım → team_exceptions.
		{"platform takımının teknik hataları", guidedTeamExceptions, "platform", true},
		{"platform takımının exception'ları neler?", guidedTeamExceptions, "platform", false},
		{"platform ekibinin teknik hataları var mı", guidedTeamExceptions, "platform", true},
		// Bozulmayan yollar.
		{"takımımın hataları neler", guidedMyProblems, "", false},  // v0.9.650
		{"servislerimde hata var mı", guidedMyProblems, "", false}, // v0.9.375
		{"ekibimin problemleri var mı", guidedMyProblems, "", false},
		{"benim takımda sorun var mı", guidedMyProblems, "", false},
		{"takımımda hatalı servis var mı", guidedMyProblems, "", false},
		{"takımımın açık problemleri neler", guidedMyProblems, "", false},
		{"takımımın servisleri nasıl", guidedMyServices, "", false},
		{"takımımın en hatalı servisleri", guidedMyTopErrors, "", false},
		{"platform takımının servisleri nasıl", guidedTeamServices, "platform", false},
		{"platform takımının hataları", guidedTeamServices, "platform", false},
		{"platform", guidedTeamServices, "platform", false},
		{"checkout-service teknik hataları?", guidedServiceExceptions, "", false},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.q, teServices, teEnvs, teTeams, "")
		if r.Intent != c.intent || r.Team != c.team || (c.intent == guidedMyExceptions || c.intent == guidedTeamExceptions) && r.TechErr != c.techErr {
			t.Errorf("%q → %s team=%q techErr=%v; want %s team=%q techErr=%v", c.q, r.Intent, r.Team, r.TechErr, c.intent, c.team, c.techErr)
		}
	}
	// Ortam soruda geçiyorsa rotaya yazılır (link env'i taşır).
	if r := routeGuidedIntent("prod ortamında ekibimin teknik hataları var mı", teServices, teEnvs, teTeams, ""); r.Intent != guidedMyExceptions || r.Env != "prod" {
		t.Errorf("env: %+v", r)
	}
}

func TestTeamExceptionsScopeRouting(t *testing.T) {
	sc := chatScope{Team: "platform", Env: "prod"}
	routed, rest := cleanScopedQuestion("@team:platform teknik hataları")
	pl := planChatScope("", sc, routed, rest, teServices, teEnvs)
	if pl.Kind != "route" || pl.Route.Intent != guidedTeamExceptions || pl.Route.Team != "platform" || pl.Route.Env != "prod" || !pl.Route.TechErr {
		t.Fatalf("@team teknik hataları: %+v", pl)
	}
	routed, rest = cleanScopedQuestion("@team:platform exception'lar neler")
	if pl := planChatScope("", sc, routed, rest, teServices, teEnvs); pl.Route.Intent != guidedTeamExceptions || pl.Route.TechErr {
		t.Errorf("@team exception: %+v", pl)
	}
	// Metinde servis adı → servis yolu (plan boş, router devralır).
	routed, rest = cleanScopedQuestion("@team:platform checkout-service teknik hataları")
	if pl := planChatScope("", sc, routed, rest, teServices, teEnvs); pl.Kind != "" {
		t.Errorf("servis adı varken takım planı: %+v", pl)
	}
	// Yalnız anma → takım servisleri (bugünkü davranış).
	if pl := planChatScope("", sc, "", "", teServices, teEnvs); pl.Route.Intent != guidedTeamServices {
		t.Errorf("çıplak @team: %+v", pl)
	}
	// Kapsam, kimlik rotasını adı geçen takıma çevirir.
	r := applyChatScopeRoute(&chatScope{Team: "platform"}, guidedRoute{Intent: guidedMyExceptions, Env: "uat", TechErr: true})
	if r.Intent != guidedTeamExceptions || r.Team != "platform" || r.Env != "uat" || !r.TechErr {
		t.Errorf("applyChatScopeRoute: %+v", r)
	}
}

func teOraLine() exGroupLine {
	return exGroupLine{Fingerprint: "ora:abc123", Oracle: true, Code: "ORA-01555", Operation: "INSURANCE_DOC_PDF",
		Type: "ORA-01555", Message: "INSURANCE_DOC_PDF", Service: "checkout-service",
		Services: []string{"checkout-service", "orders-api"}, ServiceCount: 3,
		LastSeen: teNow.Add(-12 * time.Minute).UnixNano(), HourKnown: true, LastHour: 42, PrevHour: 10,
		Occurrences: 1204, Priority: "P1", State: "new", Href: "/problems?exc=ora%3Aabc123", Sample: "policy pdf timeout"}
}

func teSpanLine() exGroupLine {
	return exGroupLine{Fingerprint: "a1b2c3d4", Type: "java.lang.NullPointerException", Message: "order id null",
		Service: "orders-api", Services: []string{"orders-api"}, ServiceCount: 1,
		LastSeen: teNow.Add(-90 * time.Minute).UnixNano(), Occurrences: 37, Priority: "P3", State: "acknowledged",
		Href: "/problems?exc=a1b2c3d4"}
}

func TestRenderTeamExceptionsTR(t *testing.T) {
	href, _ := exceptionsPageTeamHref("platform", teamAxisOwner, "", false)
	base := teamExcView{Team: "platform", Services: 3, Floor: 5, PageHref: href, Axis: teamAxisOwner}
	head := "Takımın teknik hataları — platform (3 servis), Exceptions kutusu: AÇIK gruplar (new/acknowledged/regressed), son 1gün, EN SON GÖRÜLEN önce:\n"
	page := "Exceptions'ta aç (takım süzgeçli): /problems?owner=platform&s_exception-inbox-v2=lastSeen.desc\n"
	cases := []struct {
		name string
		ev   exceptionGroupsEvidence
		mut  func(*teamExcView)
		want string
	}{
		{"oracle + span, en son önce", exceptionGroupsEvidence{Groups: []exGroupLine{teOraLine(), teSpanLine()}, More: 3, LookbackS: 86400, at: teNow}, nil,
			head +
				"1. [teknik hata · Oracle] ORA-01555 · INSURANCE_DOC_PDF · servis checkout-service, orders-api (+1) · son görülme 12dk önce (10-19 09:48 UTC) · son 1 sa 42 / önceki 1 sa 10 · toplam 1,204 (grup ömrü) · P1 · durum new · detay: /problems?exc=ora%3Aabc123\n" +
				"   örnek mesaj: \"policy pdf timeout\"\n" +
				"2. [exception] java.lang.NullPointerException · servis orders-api · son görülme 1sa 30dk önce (10-19 08:30 UTC) · toplam 37 (grup ömrü) · P3 · durum acknowledged · detay: /problems?exc=a1b2c3d4\n" +
				"   mesaj: \"order id null\"\n" +
				"(+3 grup daha gösterilmedi — tümü Exceptions'ta)\n" +
				"Not: Exceptions sayfasının varsayılan tabanı uygulandı (<5 oluşumlu tek-servis gruplar gösterilmedi; çoklu-servis ve regressed muaf).\n" +
				page +
				"KURAL: cevaba \"evet, N açık grup var\" ya da \"yok\" ile başla; grupları bu SIRAYLA (en son görülen önce) say — tür, kod/operasyon ya da tip, servis ve son görülme AYNEN; P1 varsa ayrıca vurgula. Listede olmayan grup, kod ya da sayı UYDURMA. Son cümlede Exceptions'ta takım süzgeçli açabileceğini söyle (link cevabın altında).\n"},
		{"boş = yok", exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: 86400, at: teNow}, nil,
			head + "Yok: son 1gün içinde takımın servislerinde Exceptions'ta açık teknik hata / exception grubu bulunmadı (sayfanın varsayılan tabanıyla: <5 oluşumlu tek-servis gruplar sayılmadı).\n" + page},
		{"okuma hatası = OKUNAMADI", exceptionGroupsEvidence{Err: "code: 159, timeout\n exceeded", LookbackS: 86400, at: teNow}, nil,
			head + "Exceptions grupları OKUNAMADI (code: 159, timeout exceeded); grup olup olmadığı bilinmiyor — \"teknik hata yok\" deme.\n" + page},
		{"iki eksen + ortam notu", exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: 3600, at: teNow},
			func(v *teamExcView) {
				v.Axis, v.EnvNote, v.Floor = teamAxisBoth, "Ortam: prod (takımın bu ortamdaki 3 servisi).\n", 0
			},
			strings.Replace(head, "son 1gün", "son 1sa", 1) + "Ortam: prod (takımın bu ortamdaki 3 servisi).\n" +
				"Yok: son 1sa içinde takımın servislerinde Exceptions'ta açık teknik hata / exception grubu bulunmadı.\n" + page +
				"Not: sayfanın takım süzgeci tek eksenli — ilk link ownerTeam eksenini açar; takımın SRE olarak geçtiği servisler ikinci (SRE süzgeçli) linkte.\n"}, // v0.10.1397
	}
	for _, c := range cases {
		v := base
		v.Ev = c.ev
		if c.mut != nil {
			c.mut(&v)
		}
		if got := renderTeamExceptionsTR(v); got != c.want {
			t.Errorf("%s:\n got: %q\nwant: %q", c.name, got, c.want)
		}
	}
	// OKUNAMADI ile yok ASLA karışmaz.
	errOut := renderTeamExceptionsTR(teamExcView{Team: "x", Ev: exceptionGroupsEvidence{Err: "boom"}})
	if strings.Contains(errOut, "Yok:") || !strings.Contains(errOut, "OKUNAMADI") {
		t.Errorf("hata metni: %q", errOut)
	}
	// Güvensiz detay linki yazılmaz.
	bad := teSpanLine()
	bad.Href = "javascript:alert(1)"
	if out := teamExcLineTR(1, bad, teNow); strings.Contains(out, "javascript") {
		t.Errorf("güvensiz href yazıldı: %q", out)
	}
}

func TestExceptionsPageTeamHref(t *testing.T) {
	cases := []struct {
		team, axis, env string
		oracle          bool
		want            string
		ok              bool
	}{
		{"platform", teamAxisOwner, "", false, "/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc", true},
		{"platform", teamAxisBoth, "", false, "/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc", true},
		{"platform", "", "", false, "/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc", true},
		{"SRE Ops", teamAxisSRE, "prod", false, "/problems?env=prod&s_exception-inbox-v2=lastSeen.desc&sre=SRE+Ops", true},
		{"platform", teamAxisOwner, "prod", true, "/problems?env=prod&oracle=only&owner=platform&s_exception-inbox-v2=lastSeen.desc", true},
		{"a&b=c//x", teamAxisOwner, "", false, "/problems?owner=a%26b%3Dc%2F%2Fx&s_exception-inbox-v2=lastSeen.desc", true},
		{"  ", teamAxisOwner, "prod", false, "", false},
	}
	for _, c := range cases {
		got, ok := exceptionsPageTeamHref(c.team, c.axis, c.env, c.oracle)
		if got != c.want || ok != c.ok {
			t.Errorf("(%q,%q,%q,%v) = %q,%v; want %q,%v", c.team, c.axis, c.env, c.oracle, got, ok, c.want, c.ok)
		}
		if ok && safelink.Classify(got) != safelink.Internal {
			t.Errorf("%q safelink iç yolu değil", got)
		}
	}
}

func TestTeamExceptionsLinks(t *testing.T) {
	hrefs := func(ls []guidedAnswerLink) []string {
		var out []string
		for _, l := range ls {
			if safelink.Classify(l.Href) != safelink.Internal {
				t.Errorf("güvensiz link: %q", l.Href)
			}
			out = append(out, l.Href)
		}
		return out
	}
	cases := []struct {
		name  string
		route guidedRoute
		want  []string
	}{
		{"takımsız", guidedRoute{Intent: guidedMyExceptions}, []string{"/problems?s_exception-inbox-v2=lastSeen.desc"}},
		{"owner", guidedRoute{Intent: guidedMyExceptions, Team: "platform", ExcAxis: teamAxisOwner},
			[]string{"/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc"}},
		{"teknik hata ama Oracle grubu yok → tek link", guidedRoute{Intent: guidedTeamExceptions, Team: "platform", TechErr: true},
			[]string{"/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc"}},
		{"teknik hata + Oracle + env", guidedRoute{Intent: guidedTeamExceptions, Team: "platform", Env: "prod", TechErr: true, ExcOracle: true},
			[]string{"/problems?env=prod&owner=platform&s_exception-inbox-v2=lastSeen.desc", "/problems?env=prod&oracle=only&owner=platform&s_exception-inbox-v2=lastSeen.desc"}},
		{"sre ekseni", guidedRoute{Intent: guidedMyExceptions, Team: "payments-team", ExcAxis: teamAxisSRE},
			[]string{"/problems?s_exception-inbox-v2=lastSeen.desc&sre=payments-team"}},
		// v0.10.1397 — birleşimin öbür yarısı Inbox DEĞİL, aynı sayfanın sre ekseni.
		{"iki eksen → owner + SRE Exceptions linki", guidedRoute{Intent: guidedMyExceptions, Team: "platform", ExcAxis: teamAxisBoth},
			[]string{"/problems?owner=platform&s_exception-inbox-v2=lastSeen.desc", "/problems?s_exception-inbox-v2=lastSeen.desc&sre=platform"}},
	}
	for _, c := range cases {
		ls := guidedAnswerLinks(c.route, linkWindowBetween(teNow.Add(-time.Hour), teNow)) // /problems pencere okumaz
		if got := hrefs(ls); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v; want %v", c.name, got, c.want)
		}
	}
	if l := guidedAnswerLinks(guidedRoute{Intent: guidedTeamExceptions, Team: "platform"}, noLinkWindow()); l[0].Label != "Exceptions'ta aç (takım süzgeçli · platform)" {
		t.Errorf("etiket: %q", l[0].Label)
	}
}

func TestTeamExceptionChipsRouteLLMFree(t *testing.T) {
	ineligibleOra := teOraLine()
	ineligibleOra.Operation = "two words"
	synth := teSpanLine()
	synth.Service = "oracle:eu-a-src"
	dup := teSpanLine()
	dup.Fingerprint = "a1b2c3d5"
	inv := teSpanLine()
	inv.Service = "inventory"
	chips := teamExceptionChips([]exGroupLine{teOraLine(), ineligibleOra, teSpanLine(), dup, synth, inv}, "prod", teamExcChipMax)
	want := []string{"INSURANCE_DOC_PDF operasyonu neden hata veriyor?", "orders-api teknik hataları?", "inventory teknik hataları?"}
	var got []string
	for _, c := range chips {
		got = append(got, c.Text)
		if c.Label == "" || c.Action != nil || c.Href != "" {
			t.Errorf("etiketli metin çipi bekleniyordu: %+v", c)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("çipler %v; want %v", got, want)
	}
	if n := len(teamExceptionChips([]exGroupLine{teOraLine(), teSpanLine(), inv, func() exGroupLine { l := teSpanLine(); l.Service = "checkout-service"; return l }(), teOraLine()}, "", 2)); n != 2 {
		t.Errorf("tavan: %d", n)
	}
	for _, c := range chips {
		switch c.Kind {
		case fuKindOracleOperation:
			// Operasyon ön-getirmesi: belirteçler kodu taşır ve katalogla BİREBİR eşleşir.
			m := matchOperations(opTokens(c.Text), []chstore.CatalogOperation{{Operation: "INSURANCE_DOC_PDF", CallCount: 9}, {Operation: "ORDERS_LIST_GET", CallCount: 40}})
			if m.Kind != "exact" || len(m.Ops) != 1 || m.Ops[0] != "INSURANCE_DOC_PDF" || c.Scope != nil {
				t.Errorf("%q → %+v scope=%+v", c.Text, m, c.Scope)
			}
		case fuKindExceptions:
			svc := c.Scope.Services[0]
			if r := routeGuidedIntent(c.Text, teServices, teEnvs, teTeams, ""); r.Intent != guidedServiceExceptions || r.Service != svc {
				t.Errorf("%q → %s/%q", c.Text, r.Intent, r.Service)
			}
			if c.Scope.Env != "prod" {
				t.Errorf("kapsam env: %+v", c.Scope)
			}
			_, rest := cleanScopedQuestion(c.Text)
			if pl := planChatScope("", *c.Scope, c.Text, rest, teServices, teEnvs); pl.Kind != "" {
				t.Errorf("%q: kapsam planı router'ı atladı: %+v", c.Text, pl)
			}
		default:
			t.Errorf("pinsiz çip türü %q", c.Kind)
		}
	}
	// guidedFollowups iki takım exception rotasında bu çipleri taşır; sorulan elenir.
	for _, in := range []guidedIntent{guidedMyExceptions, guidedTeamExceptions} {
		fu := guidedFollowups(guidedRoute{Intent: in, TeamFollowups: chips}, []string{"orders-api teknik hataları?"})
		if len(fu) != 2 || fu[0].Text != want[0] || fu[1].Text != want[2] {
			t.Errorf("%s followups: %+v", in, fu)
		}
		for _, s := range guidedSuggestions(guidedRoute{Intent: in, Team: "platform", TeamFollowups: chips}) {
			if r := routeGuidedIntent(s, teServices, teEnvs, teTeams, ""); r.Intent == guidedNone && !strings.Contains(s, "operasyonu") {
				t.Errorf("%s öneri %q yönlenmiyor", in, s)
			}
		}
	}
}

func TestCollectTeamExceptionGroupsFilter(t *testing.T) {
	var rows []chstore.ExceptionGroup
	for i := 0; i < 14; i++ {
		rows = append(rows, chstore.ExceptionGroup{Fingerprint: "f" + string(rune('a'+i)) + "000000", Type: "java.io.IOException",
			Service: "orders-api", State: "new", Occurrences: 9, LastSeen: teNow.Add(-time.Duration(i) * time.Minute).UnixNano()})
	}
	fk := &fakeExGroups{rows: rows}
	svcs := []string{"checkout-service", "orders-api"}
	ev := collectExceptionGroups(context.Background(), fk, exScope{Services: svcs, State: "open", From: teNow.Add(-24 * time.Hour), To: teNow,
		MinOcc: 5, FloorExempt: []string{"fz"}, FloorExemptRegressed: true}, exGroupHooks{})
	f := fk.gotFilter
	if !reflect.DeepEqual(f.Services, svcs) || f.State != "open" || f.Sort != "lastSeen" || f.Dir != "desc" || f.MinOccurrences != 5 ||
		!reflect.DeepEqual(f.FloorExempt, []string{"fz"}) || !f.FloorExemptRegressed || f.ActiveFromNs != teNow.Add(-24*time.Hour).UnixNano() {
		t.Fatalf("süzgeç: %+v", f)
	}
	if ev.Err != "" || len(ev.Groups) != exGroupTeamRenderMax || ev.More != 4 || ev.MoreAtMin {
		t.Fatalf("takım tavanı: groups=%d more=%d atMin=%v err=%q", len(ev.Groups), ev.More, ev.MoreAtMin, ev.Err)
	}
	// Taban yoksa istisnalar da gitmez (yalnız tabanla anlamlı).
	fk2 := &fakeExGroups{}
	collectExceptionGroups(context.Background(), fk2, exScope{Services: svcs, FloorExempt: []string{"fz"}, FloorExemptRegressed: true}, exGroupHooks{})
	if fk2.gotFilter.FloorExempt != nil || fk2.gotFilter.FloorExemptRegressed {
		t.Errorf("tabansız istisna sızdı: %+v", fk2.gotFilter)
	}
	// Operasyon kapsamı (v0.10.1331) tavanı 5'te kalır.
	fk3 := &fakeExGroups{rows: rows}
	if ev := collectExceptionGroups(context.Background(), fk3, exScope{Messages: []string{"X_Y"}}, exGroupHooks{}); len(ev.Groups) != exGroupRenderMax {
		t.Errorf("operasyon tavanı: %d", len(ev.Groups))
	}
}

func TestTeamExceptionsAxis(t *testing.T) {
	mds := map[string]chstore.ServiceMetadata{
		"checkout-service": {OwnerTeam: "platform", SRETeam: "sre-core"},
		"orders-api":       {OwnerTeam: "payments-team", SRETeam: "platform"},
		"inventory":        {SRETeam: "sre-core"},
	}
	var ta chstore.TeamAliases
	for _, c := range []struct{ team, want string }{
		{"platform", teamAxisBoth}, {"payments-team", teamAxisOwner}, {"sre-core", teamAxisSRE}, {"nobody", teamAxisOwner},
	} {
		if got := teamExceptionsAxis(ta, mds, c.team); got != c.want {
			t.Errorf("%s → %s; want %s", c.team, got, c.want)
		}
	}
}

func TestTeamExceptionPresenceSignal(t *testing.T) {
	for q, want := range map[string]bool{
		"takımımda hata var mı":          true,
		"ekibimde hata varmı":            true,
		"takımımın hataları neler":       false, // varlık sorusu değil
		"servislerimde hata var mı":      false, // takım kökü yok (v0.9.375)
		"takımımda sorun var mı":         false, // problem kelimesi
		"takımımda hatalı servis var mı": false, // "hatalı" sıralama/sağlık
	} {
		if got := hasTeamErrorPresenceAsk(guidedTokens(normalizeGuidedMsg(q))); got != want {
			t.Errorf("%q → %v; want %v", q, got, want)
		}
	}
}

// K4 ölü-param denetimi (v0.9.1130 sınıfı): link param'ları Exceptions
// sayfasının GERÇEKTEN okuduğu adlar. Sayfa adı değiştirirse bu kırılır.
func TestExceptionsPageLinkParamsAreRead(t *testing.T) {
	page := readRepoFile(t, "../../frontend/src/features/anomalies/AnomaliesPage.tsx")
	for _, pin := range []string{
		"readExceptionsTeamFilter(searchParams)", "parseOracleFacet(searchParams.get('oracle'))",
		"sortUrlKey: '" + excPageSortParam + "'", "'lastSeen'", "useUrlEnv()",
	} {
		if !strings.Contains(page, pin) {
			t.Errorf("AnomaliesPage.tsx %q okumuyor — link ölü param taşır", pin)
		}
	}
	// v0.10.1515 — takım süzgeci okuyucusu lib/exceptionsTeamHref.ts'e taşındı
	// (CoSRE karşılamasındaki takım seçicisinin linkiyle ortak).
	teamRd := readRepoFile(t, "../../frontend/src/lib/exceptionsTeamHref.ts")
	for _, pin := range []string{"sp.get('owner')", "sp.get('sre')", "'" + excPageSortParam + "'", "'" + excPageSortLastSeen + "'"} {
		if !strings.Contains(teamRd, pin) {
			t.Errorf("exceptionsTeamHref.ts %q içermiyor — link ölü param taşır", pin)
		}
	}
	env := readRepoFile(t, "../../frontend/src/lib/useUrlEnv.ts")
	if !strings.Contains(env, "searchParams.get('env')") {
		t.Error("useUrlEnv env param'ını okumuyor")
	}
	oracle := readRepoFile(t, "../../frontend/src/features/anomalies/oracleGroup.ts")
	if !strings.Contains(oracle, "v === 'only'") {
		t.Error("parseOracleFacet 'only' değerini tanımıyor")
	}
	ds := readRepoFile(t, "../../frontend/src/lib/dataTable.ts")
	if !strings.Contains(ds, "`${s.id}.${s.dir}`") {
		t.Error("formatSortParam biçimi '<id>.<dir>' değil")
	}
	if v, _ := url.ParseQuery("x=" + excPageSortLastSeen); v.Get("x") != "lastSeen.desc" {
		t.Error("sıralama değeri")
	}
}
