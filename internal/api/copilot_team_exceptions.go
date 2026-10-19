package api

// copilot_team_exceptions.go — v0.10.1385 (operatör 2026-10-19: "Ekibimin
// teknik hataları var mı diye sorduğumda CoSRE arkada Exceptions açıp
// filtrelenebilir ve chatte en son exceptions errorleri getirebilir.")
//
// Takım-kapsamlı exception sorusu ("ekibimin teknik hataları var mı",
// "takımımın exception'ları", "platform takımının teknik hataları",
// "@team:platform teknik hataları") eskiden GetExceptions'ın ham span
// exception'larını tip×servis gruplayıp SAYIYA göre diziyordu: Exceptions
// sayfasının kutusu (exception_groups — span grupları + Oracle "teknik hata"
// grupları `ora:`, durum, son görülme, P1) hiç okunmuyordu, yani operatörün
// "teknik hata" dediği Oracle grupları cevapta YOKTU ve sayfaya köprü yoktu.
//
// Şimdi:
//   - okuma Exceptions kutusunun kendisi: ListExceptionGroups (paylaşılan dikiş
//     chat_exception_groups.go collectExceptionGroups) — takımın servisleri
//     `service IN (…)` (sayfanın owner/sre süzgeciyle aynı yol), AÇIK durumlar
//     (new/acknowledged/regressed), sayfanın varsayılan tabanı (çoklu-servis +
//     regressed muaf), EN SON GÖRÜLEN önce, en çok exGroupTeamRenderMax grup;
//     pencere varsayılanı son 24 saat (Oracle akışı durgun — operasyon yolunun
//     operatör onaylı geriye bakışıyla aynı), açık pencere kazanır;
//   - kanıt satırları deterministik (renderTeamExceptionsTR, tablo testli):
//     tür (teknik hata · Oracle / exception), servis, kod+operasyon ya da tip,
//     son görülme (göreli + saat), Oracle'da son 1 sa / önceki 1 sa, ömür boyu
//     toplam, öncelik, durum, grubun detay linki. OKUNAMADI ≠ yok;
//   - cevabın altına "Exceptions'ta aç (takım süzgeçli)" linki — sayfanın
//     GERÇEKTEN okuduğu param'larla (K4 ölü-param denetimi, kaynak-pin testi):
//     owner|sre=<takım>, env, s_exception-inbox-v2=lastSeen.desc ve soru
//     "teknik hata" dediyse + Oracle grubu bulunduysa ikinci bir oracle=only
//     linki; her href safelink'ten geçer;
//   - takip çipleri grup başına, LLM'siz yönlenen tam cümle: Oracle grubu →
//     "<OP> operasyonu neden hata veriyor?" (operasyon ön-getirmesi, v0.10.1382
//     başlangıç çipiyle aynı cümle), span grubu → "<svc> teknik hataları?"
//     (service_exceptions, v0.10.1383 takım özeti çipiyle aynı cümle).
//
// Eksen notu: Exceptions sayfasının takım süzgeci TEK eksenli (owner VE sre
// birlikte = kesişim); sohbetin "takımım"ı owner VEYA sre birleşimi. Takım
// yalnız bir eksende geçiyorsa link o ekseni taşır; ikisinde de geçiyorsa link
// owner eksenini taşır, kanıt bunu SÖYLER ve birleşim görünümü için Inbox
// (?team=, birleşim) linki de eklenir.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/safelink"
)

const (
	// teamExcDefaultRangeS — v0.10.1385: takım teknik hataları varsayılan
	// penceresi (24 sa); açık pencere kazanır.
	teamExcDefaultRangeS = 24 * 3600
	// teamExcChipMax — grup başına takip çipi tavanı (guidedFollowupMax).
	teamExcChipMax = guidedFollowupMax
	teamExcOpRunes = 80
	// fuKindOracleOperation — v0.10.1385: Oracle grubunun operasyon çipi.
	fuKindOracleOperation = "oracle_operation"
	// excPageSortParam / excPageSortLastSeen — Exceptions sayfasının sıralama
	// URL anahtarı (AnomaliesPage.tsx sortUrlKey) ve "son görülme, yeni önce"
	// değeri (lib/dataTable.ts formatSortParam "<id>.<dir>").
	excPageSortParam    = "s_exception-inbox-v2"
	excPageSortLastSeen = "lastSeen.desc"
)

// Takım ekseni — Exceptions sayfa linkinin hangi süzgeci taşıyacağı.
const (
	teamAxisOwner = "owner"
	teamAxisSRE   = "sre"
	teamAxisBoth  = "both" // owner linki + birleşim (Inbox) linki
)

// hasTeamErrorPresenceAsk — SAF: "takımımda / ekibimde hata var mı" şekli.
// TAKIM/EKİP kökü + "hata" kökü ("hatalı" DEĞİL — o en-hatalı-servis
// sıralaması / sağlık sorusu) + varlık sorusu ("var", "varmı", "mevcut") +
// problem kelimesi YOK. Bilerek DIŞARIDA: "takımımın hataları neler"
// (v0.9.650 — açık problemler) ve "servislerimde hata var mı" (v0.9.375 pini
// — my_problems); problem/sorun/alarm diyen soru da problem yolunda.
func hasTeamErrorPresenceAsk(toks []string) bool {
	if hasProblemSignal(toks) || !hasTeamWord(toks) {
		return false
	}
	hata, varQ := false, false
	for _, t := range toks {
		switch {
		case strings.HasPrefix(t, "hata") && !strings.HasPrefix(t, "hatal"):
			hata = true
		case t == "var" || t == "varmı" || t == "mevcut":
			varQ = true
		}
	}
	return hata && varQ
}

// hasTeamExceptionAsk — SAF: takım kapsamlı sorunun Exceptions kutusuna
// gitmesi gerekiyor mu — exception/istisna, "teknik hata" ya da varlık sorusu.
func hasTeamExceptionAsk(toks []string) bool {
	return hasExceptionWord(toks) || hasServiceExceptionSignal(toks) || hasTeamErrorPresenceAsk(toks)
}

// hasTechErrorWord — SAF: soru "teknik hata" diyor mu (Oracle süzgeçli ikinci
// link için; Exceptions sayfasında Oracle çipinin varsayılan adı "Teknik hata").
func hasTechErrorWord(toks []string) bool {
	return tokenHasPrefix(toks, "teknik") && tokenHasPrefix(toks, "hata")
}

// teamExceptionsAxis — SAF: takımın katalogda hangi eksende geçtiği
// (matchesTeamFilter ile — sayfanın servicesForTeam çözümüyle bit-bit aynı).
func teamExceptionsAxis(ta chstore.TeamAliases, mds map[string]chstore.ServiceMetadata, team string) string {
	owner, sre := 0, 0
	for _, md := range mds {
		if matchesTeamFilter(ta, md.OwnerTeam, md.SRETeam, team, "") && strings.TrimSpace(md.OwnerTeam) != "" {
			owner++
		}
		if matchesTeamFilter(ta, md.OwnerTeam, md.SRETeam, "", team) && strings.TrimSpace(md.SRETeam) != "" {
			sre++
		}
	}
	switch {
	case owner > 0 && sre > 0:
		return teamAxisBoth
	case sre > 0:
		return teamAxisSRE
	}
	return teamAxisOwner
}

// exceptionsPageTeamHref — SAF: Exceptions sayfasının takım süzgeçli, son
// görülmeye göre sıralı adresi. Param'lar sayfanın OKUDUKLARI (kaynak-pin
// testi AnomaliesPage.tsx'te arar): owner|sre, env (useUrlEnv), oracle
// (parseOracleFacet), s_exception-inbox-v2 (sortUrlKey). safelink'ten
// geçmeyen adres → ok=false (link yok; yanlış link linksizlikten kötü).
func exceptionsPageTeamHref(team, axis, env string, oracleOnly bool) (string, bool) {
	team = strings.TrimSpace(team)
	if team == "" {
		return "", false
	}
	q := url.Values{}
	key := teamAxisOwner
	if axis == teamAxisSRE {
		key = teamAxisSRE
	}
	q.Set(key, team)
	if e := strings.TrimSpace(env); e != "" {
		q.Set("env", e)
	}
	if oracleOnly {
		q.Set("oracle", "only")
	}
	q.Set(excPageSortParam, excPageSortLastSeen)
	return safelink.SafeLink("/problems?" + q.Encode())
}

// teamExceptionsLinks — SAF: takım exception cevabının derin linkleri.
// Takım çözülmediyse düz Exceptions sayfası (son görülme sıralı).
func teamExceptionsLinks(route guidedRoute) []guidedAnswerLink {
	team := strings.TrimSpace(route.Team)
	if team == "" {
		if h, ok := safelink.SafeLink("/problems?" + url.Values{excPageSortParam: {excPageSortLastSeen}}.Encode()); ok {
			return []guidedAnswerLink{{Label: "Exceptions", Href: h}}
		}
		return nil
	}
	var out []guidedAnswerLink
	if h, ok := exceptionsPageTeamHref(team, route.ExcAxis, route.Env, false); ok {
		out = append(out, guidedAnswerLink{Label: "Exceptions'ta aç (takım süzgeçli · " + team + ")", Href: h})
	}
	if route.TechErr && route.ExcOracle {
		if h, ok := exceptionsPageTeamHref(team, route.ExcAxis, route.Env, true); ok {
			out = append(out, guidedAnswerLink{Label: "Teknik hatalar (Oracle) · " + team, Href: h})
		}
	}
	if route.ExcAxis == teamAxisBoth {
		// Sayfa süzgeci tek eksenli; birleşim (owner VEYA sre) Inbox'ta.
		if h, ok := safelink.SafeLink("/inbox?kind=exception&team=" + url.QueryEscape(team)); ok {
			out = append(out, guidedAnswerLink{Label: "Inbox (owner+SRE) · " + team, Href: h})
		}
	}
	return out
}

// teamExceptionChips — SAF: grup başına takip çipi (liste sırası = en son
// görülen önce). Oracle grubu → operasyon cümlesi (kapsamsız: ön-getirme
// kodu katalogdan çözer), span grubu → servisin teknik hataları (servis +
// env kapsamlı). Uygunsuz kod/servis atlanır, aynı cümle bir kez.
func teamExceptionChips(groups []exGroupLine, env string, max int) []chatFollowup {
	var out []chatFollowup
	seen := map[string]bool{}
	for _, g := range groups {
		if len(out) >= max {
			break
		}
		var c chatFollowup
		if g.Oracle {
			op := strings.TrimSpace(g.Operation)
			if !starterOpCodeEligible(op) {
				continue
			}
			c = chatFollowup{Text: op + " operasyonu neden hata veriyor?",
				Label: starterTrunc(op, teamSumChipLabelRune) + " neden hata veriyor?", Kind: fuKindOracleOperation}
		} else {
			svc := strings.TrimSpace(g.Service)
			if !starterServiceEligible(svc) || !fuServiceRe.MatchString(svc) {
				continue
			}
			c = chatFollowup{Text: svc + " teknik hataları?", Label: starterTrunc(svc, teamSumChipLabelRune) + " teknik hataları",
				Kind: fuKindExceptions, Scope: &chatScope{Services: []string{svc}, Env: env}}
		}
		if seen[c.Text] {
			continue
		}
		seen[c.Text] = true
		out = append(out, c)
	}
	return out
}

// teamExcView — renderTeamExceptionsTR girdisi (saf).
type teamExcView struct {
	Team     string
	Services int    // okunan servis sayısı (ortam kesişiminden sonra)
	EnvNote  string // ortam daraltması notu ("" = yok)
	Floor    uint64 // uygulanan varsayılan taban (0 = yok)
	PageHref string // takım süzgeçli Exceptions linki ("" = yok)
	Axis     string
	Ev       exceptionGroupsEvidence
}

// renderTeamExceptionsTR — SAF (tablo testli): takım Exceptions kutusu kanıtı.
func renderTeamExceptionsTR(v teamExcView) string {
	var b strings.Builder
	win := "pencere"
	if v.Ev.LookbackS > 0 {
		win = "son " + fmtAgoTR(v.Ev.LookbackS)
	}
	fmt.Fprintf(&b, "Takımın teknik hataları — %s (%d servis), Exceptions kutusu: AÇIK gruplar (new/acknowledged/regressed), %s, EN SON GÖRÜLEN önce:\n",
		v.Team, v.Services, win)
	if v.EnvNote != "" {
		b.WriteString(v.EnvNote)
	}
	switch {
	case v.Ev.Err != "":
		fmt.Fprintf(&b, "Exceptions grupları OKUNAMADI (%s); grup olup olmadığı bilinmiyor — \"teknik hata yok\" deme.\n",
			clipRunes(oneLine(v.Ev.Err), 120))
	case len(v.Ev.Groups) == 0:
		fmt.Fprintf(&b, "Yok: %s içinde takımın servislerinde Exceptions'ta açık teknik hata / exception grubu bulunmadı", win)
		if v.Floor > 0 {
			fmt.Fprintf(&b, " (sayfanın varsayılan tabanıyla: <%d oluşumlu tek-servis gruplar sayılmadı)", v.Floor)
		}
		b.WriteString(".\n")
	default:
		for i, l := range v.Ev.Groups {
			b.WriteString(teamExcLineTR(i+1, l, v.Ev.at))
		}
		if v.Ev.More > 0 {
			more := fmt.Sprintf("%d", v.Ev.More)
			if v.Ev.MoreAtMin {
				more = "en az " + more
			}
			fmt.Fprintf(&b, "(+%s grup daha gösterilmedi — tümü Exceptions'ta)\n", more)
		}
		if v.Floor > 0 {
			fmt.Fprintf(&b, "Not: Exceptions sayfasının varsayılan tabanı uygulandı (<%d oluşumlu tek-servis gruplar gösterilmedi; çoklu-servis ve regressed muaf).\n", v.Floor)
		}
	}
	if v.PageHref != "" {
		fmt.Fprintf(&b, "Exceptions'ta aç (takım süzgeçli): %s\n", v.PageHref)
		if v.Axis == teamAxisBoth {
			b.WriteString("Not: sayfanın takım süzgeci tek eksenli — link ownerTeam eksenini açar; takımın SRE olarak geçtiği servisler Inbox (owner+SRE) linkinde.\n")
		}
	}
	if v.Ev.Err == "" && len(v.Ev.Groups) > 0 {
		b.WriteString("KURAL: cevaba \"evet, N açık grup var\" ya da \"yok\" ile başla; grupları bu SIRAYLA (en son görülen önce) say — " +
			"tür, kod/operasyon ya da tip, servis ve son görülme AYNEN; P1 varsa ayrıca vurgula. Listede olmayan grup, kod ya da sayı UYDURMA. " +
			"Son cümlede Exceptions'ta takım süzgeçli açabileceğini söyle (link cevabın altında).\n")
	}
	return b.String()
}

// teamExcLineTR — SAF: tek grup satırı (+ ilk iki grupta örnek mesaj).
func teamExcLineTR(n int, l exGroupLine, at time.Time) string {
	var b strings.Builder
	if l.Oracle {
		fmt.Fprintf(&b, "%d. [teknik hata · Oracle] %s · %s", n, clipRunes(l.Code, exGroupCodeRunes), clipRunes(l.Operation, teamExcOpRunes))
	} else {
		fmt.Fprintf(&b, "%d. [exception] %s", n, clipRunes(l.Type, exGroupOpRunes))
	}
	if len(l.Services) > 0 {
		sv := make([]string, len(l.Services))
		for j, s := range l.Services {
			sv[j] = clipRunes(s, exGroupSvcRunes)
		}
		fmt.Fprintf(&b, " · servis %s", strings.Join(sv, ", "))
		if l.ServiceCount > len(l.Services) {
			fmt.Fprintf(&b, " (+%d)", l.ServiceCount-len(l.Services))
		}
	}
	if l.LastSeen > 0 {
		ls := time.Unix(0, l.LastSeen).UTC()
		if !at.IsZero() {
			fmt.Fprintf(&b, " · son görülme %s önce (%s UTC)", fmtAgoTR(int64(at.Sub(ls)/time.Second)), ls.Format("01-02 15:04"))
		} else {
			fmt.Fprintf(&b, " · son görülme %s UTC", ls.Format("01-02 15:04"))
		}
	}
	if l.Oracle && l.HourKnown {
		fmt.Fprintf(&b, " · son 1 sa %s / önceki 1 sa %s", fmtThousands(l.LastHour), fmtThousands(l.PrevHour))
	}
	fmt.Fprintf(&b, " · toplam %s (grup ömrü)", fmtThousands(l.Occurrences))
	if l.Priority != "" {
		fmt.Fprintf(&b, " · %s", l.Priority)
	}
	fmt.Fprintf(&b, " · durum %s", l.State)
	if h, ok := safelink.SafeLink(l.Href); ok {
		fmt.Fprintf(&b, " · detay: %s", h)
	}
	b.WriteString("\n")
	switch {
	case l.Sample != "":
		fmt.Fprintf(&b, "   örnek mesaj: %q\n", l.Sample)
	case !l.Oracle && l.Message != "" && n <= exGroupSampleGroups:
		fmt.Fprintf(&b, "   mesaj: %q\n", clipRunes(oneLine(l.Message), exGroupSampleRunes))
	}
	return b.String()
}

// teamExcInput — teamExceptionsBlock girdisi.
type teamExcInput struct {
	Team     string   // katalog yazımı (link + kanıt)
	Services []string // takımın servisleri (tavanlı)
	Axis     string
}

// teamExceptionsBlock — iki takım yolunun (kimlik: my_exceptions, ad:
// team_exceptions) ORTAK okuması: ortam kesişimi → varsayılan taban →
// ListExceptionGroups (paylaşılan dikiş) → kanıt; rotaya link ekseni, Oracle
// bayrağı ve grup çiplerini yazar. Okuma hatası kanıtta OKUNAMADI olur
// (bundle düşmez — operatör "bilinmiyor"u görmeli, serbest döngüyü değil).
func (s *Server) teamExceptionsBlock(ctx context.Context, emit func(string, any), route *guidedRoute, in teamExcInput, from, to time.Time) string {
	svcs := in.Services
	envNote := ""
	if env := strings.TrimSpace(route.Env); env != "" {
		// Exceptions sayfasıyla AYNI kural: takım ∩ ortam üyeleri; harita
		// okunamazsa kısıtsız (sayfanın soft-fail duruşu) ve kanıt bunu söyler.
		if members, err := s.store.EnvMemberServices(ctx, env); err == nil {
			if members == nil {
				members = []string{}
			}
			svcs = intersectServices(svcs, members)
			envNote = fmt.Sprintf("Ortam: %s (takımın bu ortamdaki %d servisi).\n", env, len(svcs))
		} else {
			envNote = fmt.Sprintf("Not: %q ortam üyeleri okunamadı — ortam daraltması UYGULANMADI.\n", env)
		}
	}
	route.ExcAxis = in.Axis
	view := teamExcView{Team: in.Team, Services: len(svcs), EnvNote: envNote, Axis: in.Axis}
	if h, ok := exceptionsPageTeamHref(in.Team, in.Axis, route.Env, false); ok {
		view.PageHref = h
	}
	n := emitGuidedStep(emit, "list_exception_groups", fmt.Sprintf(`{"team":%s,"services":%d,"state":"open","sort":"lastSeen"}`, jsonStr(in.Team), len(svcs)))
	if len(svcs) == 0 {
		view.Ev = exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: int64(to.Sub(from) / time.Second), at: to}
		blk := renderTeamExceptionsTR(view)
		emitGuidedStepResult(emit, n, "list_exception_groups", blk, nil)
		return blk
	}
	floor := effectiveDefaultFloor(currentExceptionTriage())
	view.Floor = floor
	view.Ev = s.exceptionGroupsEvidence(ctx, exScope{
		Services: svcs, State: "open", From: from, To: to,
		MinOcc: floor, FloorExempt: s.exceptionSpread(ctx).ExemptBelow(floor), FloorExemptRegressed: true,
	})
	for _, g := range view.Ev.Groups {
		if g.Oracle {
			route.ExcOracle = true
			break
		}
	}
	route.TeamFollowups = teamExceptionChips(view.Ev.Groups, route.Env, teamExcChipMax)
	blk := renderTeamExceptionsTR(view)
	var stepErr error
	if view.Ev.Err != "" {
		stepErr = errors.New(view.Ev.Err)
	}
	emitGuidedStepResult(emit, n, "list_exception_groups", blk, stepErr)
	return blk
}

// guidedTeamExceptionsBundle — ADI GEÇEN takımın teknik hataları
// (team_exceptions; "platform takımının teknik hataları", "@team:platform
// teknik hataları"). Çözüm my_exceptions ile aynı katalog kuralı
// (mcptools.TeamServiceNames, alias farkındalıklı, owner VEYA sre).
func (s *Server) guidedTeamExceptionsBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time) (string, string, error) {
	team := strings.TrimSpace(route.Team)
	if team == "" {
		return "", "", errors.New("team_exceptions: takım adı boş")
	}
	nResolve := emitGuidedStep(emit, "resolve_team_services", `{"team":`+jsonStr(team)+`}`)
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", "", err)
		return "", "", err
	}
	ta := s.teamAliasesCtx(ctx)
	svcs := mcptools.TeamServiceNames(ta, mds, team)
	if len(svcs) == 0 {
		ev := fmt.Sprintf("%q takımı hiçbir serviste ownerTeam/sreTeam olarak geçmiyor (Service Catalog). "+
			"Kullanıcıya söyle: katalogda takım ataması yapılmalı ya da başka bir takım adı denemeli.\n", team)
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", ev, nil)
		return ev, fmt.Sprintf("servis kataloğu (takım: %s)", team), nil
	}
	trimmed := 0
	if len(svcs) > maxTeamServices {
		trimmed = len(svcs) - maxTeamServices
		svcs = svcs[:maxTeamServices]
	}
	display := mcptools.TeamDisplayName(ta, mds, team)
	route.Team = display
	route.TeamServices = svcs
	header := fmt.Sprintf("Takım: %s — %d servis (Service Catalog ownerTeam/sreTeam eşleşmesi).\n", display, len(svcs)+trimmed)
	if trimmed > 0 {
		header += fmt.Sprintf("Not: ilk %d servis okundu, %d servis dışarıda kaldı.\n", maxTeamServices, trimmed)
	}
	emitGuidedStepResult(emit, nResolve, "resolve_team_services", header+"Çözülen servisler:\n- "+strings.Join(svcs, "\n- ")+"\n", nil)
	blk := s.teamExceptionsBlock(ctx, emit, route, teamExcInput{Team: display, Services: svcs, Axis: teamExceptionsAxis(ta, mds, team)}, from, to)
	return header + blk, fmt.Sprintf("takım: %s (%d servis) — Exceptions kutusu (açık gruplar, en son görülen önce)", display, len(svcs)+trimmed), nil
}

// teamExceptionSuggestionTexts — SAF: grup çiplerinin tam cümleleri (eski
// istemcinin düz suggestions listesi için), en çok max.
func teamExceptionSuggestionTexts(chips []chatFollowup, max int) []string {
	var out []string
	for _, c := range chips {
		if len(out) >= max {
			break
		}
		out = append(out, c.Text)
	}
	return out
}
