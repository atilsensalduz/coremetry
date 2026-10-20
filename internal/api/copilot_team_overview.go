package api

// copilot_team_overview.go — v0.10.1397, operatör (prod, 2026-10-19):
//
//	A. "Takım Exceptions dediğimde Problems'i açıyor, halbuki ben
//	   Exceptions/Errors sayfasını filtrelemesini ve otomatik getirmesini
//	   istiyorum."
//	B. "Mesela bir takım seçtim veya 'takımımın servisleri nasıl' dedim. Arkada
//	   chip'e basmadan otomatik o takımın servislerini listelesin ve spesifik
//	   olarak kontrol etmek istediğin bir servis var mı diye sorsun."
//
// Takım servis cevabı (team_services — "platform takımının servisleri nasıl?",
// "hangi takımdasın?" çipinden seçilen çıplak takım adı — ve my_services, ki
// artık aynı gövdeden geçiyor) model anlatımıydı: servisler ve RED yalnız
// kanıttaydı, anlatım iki servis seçip gerisini düşürüyordu; altındaki
// "<Takım> · Exceptions" çipi Inbox'ı (/inbox?kind=exception) açıyordu.
//
// Karar — SUNUCU EKİ (route.AnswerTail), anlatımın ARKASINA, "Kaynak:"
// künyesinden önce. Neden önek ya da ayrı blok değil:
//   - model ekleyemez/düşüremez: metni sunucu kurar, akış (delta) bittikten
//     sonra answer olayında anlatıma eklenir (answer olayı akışın yerine geçen
//     tek doğru kaynak, types.ts ChatStreamEvent);
//   - önek akışla çakışırdı: balon önce delta'ları (anlatım) çizer, answer
//     gelince metnin BAŞINA tablo eklenmesi okunan metni aşağı kaydırırdı; ek
//     yalnız uzatır;
//   - yeni blok türü FE değişikliği (chatBlocks/ChatBubble — başka işlerin
//     uçuştaki dosyaları) isterdi; sohbet markdown'u GFM tablo + kök-göreli
//     link zaten çiziyor (chatMarkdown.ts, chatLinks.ts "relative").
//
// Ek üç bölüm, hepsi deterministik ve saf render'dan (tablo testli):
//  1. servis tablosu — ad · hata oranı · hata · span · p99, hata oranı azalan
//     (ortak okuma mcptools.ReadTeamServicesRED sırası), en çok
//     teamOverviewListMax satır, "+N servis daha" dürüstlük satırı ve takım
//     süzgeçli Servisler linki (/services?ownerTeam|sreTeam=<takım> —
//     servicesUrlSpec.ts v0.10.1229'dan beri bu param'ları URL'den OKUYOR);
//  2. son exception grupları — v0.10.1385'in paylaşılan okuması
//     (readTeamExceptions: Exceptions kutusu, açık gruplar, sayfanın varsayılan
//     tabanı, EN SON GÖRÜLEN önce), en çok teamOverviewExcMax grup, pencere en
//     az 24 sa; OKUNAMADI ≠ yok; Exceptions/Errors sayfa linki (owner|sre);
//  3. kapanış sorusu "Özellikle kontrol etmek istediğin bir servis var mı?" —
//     altında listedeki ilk teamOverviewChipMax servis için birer çip (etiket
//     servis adı, metin "<svc> sağlığı nasıl?", router'da LLM'siz
//     service_health), ardından v0.10.1383 özet çipleri (detay/exception/deploy).
//
// Kanıta da kısa bir not girer: model tabloyu/listeyi tekrar yazmasın, soruyu
// sormasın (renderTeamServicesEvidenceTR KURAL'ı ile aynı söz).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/safelink"
)

const (
	// teamOverviewListMax — tabloda gösterilen servis satırı tavanı (kanıtın
	// satır tavanıyla aynı sayı: iki liste aynı "+N daha" sınırını söyler).
	teamOverviewListMax = teamServicesMaxRows
	// teamOverviewChipMax — "hangi servis?" çip sayısı.
	teamOverviewChipMax = 6
	// teamOverviewExcMax — son exception grupları bölümünde gösterilen grup.
	teamOverviewExcMax = 5
	// teamOverviewFollowupMax — takım servis cevabının çip tavanı: servis
	// çipleri + v0.10.1383 özet adımları (guidedFollowupMax).
	teamOverviewFollowupMax = teamOverviewChipMax + guidedFollowupMax
	// teamOverviewClosingQ — kapanış sorusu (operatörün cümlesi).
	teamOverviewClosingQ = "Özellikle kontrol etmek istediğin bir servis var mı?"
	// fuKindTeamService — "hangi servis?" çipi (servis sağlığına iner).
	fuKindTeamService = "team_service"
	// Servisler sayfasının takım süzgeci URL anahtarları
	// (frontend/src/lib/servicesUrlSpec.ts SERVICES_URL_SPEC).
	servicesPageOwnerParam = "ownerTeam"
	servicesPageSREParam   = "sreTeam"
	teamOverviewExcLineRun = 80
)

// teamOverviewInput — teamOverviewBlock girdisi (bundle'ın zaten okudukları).
type teamOverviewInput struct {
	Team     string                   // katalog yazımı
	Services []string                 // çözülen servisler (tavanlı)
	Trimmed  int                      // tavana takılıp okunmayan
	Rows     []chstore.ServiceSummary // RED, hata oranı azalan
	Names    []string                 // çip sırası (Rows boşsa alfabetik katalog)
}

// teamOverviewView — renderTeamOverviewTR girdisi (saf).
type teamOverviewView struct {
	Team         string
	Total        int // takımın servis sayısı (tavan dahil)
	ReadSvcs     int // MV'ye sorulan servis
	Trimmed      int
	Rows         []chstore.ServiceSummary
	Names        []string
	RangeS       int64
	Env          string
	ServicesHref string
	Exc          teamExcView
}

// teamOverviewBlock — takım servis cevabının sunucu eki: son exception
// gruplarını okur (paylaşılan okuma), ⚙ adımını emit eder, route.AnswerTail'i
// ve servis çiplerini yazar, modele kısa notu döndürür. teamSummaryBlock'tan
// SONRA çağrılmalı (onun çiplerinin önüne servis çiplerini ekler).
func (s *Server) teamOverviewBlock(ctx context.Context, emit func(string, any), route *guidedRoute, in teamOverviewInput, from, to time.Time, rangeS int64) string {
	team := strings.TrimSpace(in.Team)
	if team == "" || len(in.Services) == 0 {
		return ""
	}
	axis := s.teamAxisOf(ctx, team)
	route.ExcAxis = axis
	// Exceptions sayfası "en son görülen" kutusu: pencere en az 24 sa
	// (team_exceptions varsayılanı); sohbetin daha geniş penceresi kazanır.
	excRangeS := rangeS
	if excRangeS < teamExcDefaultRangeS {
		excRangeS = teamExcDefaultRangeS
	}
	n := emitGuidedStep(emit, "team_latest_exceptions", fmt.Sprintf(`{"team":%s,"services":%d,"state":"open","sort":"lastSeen","limit":%d}`,
		jsonStr(team), len(in.Services), teamOverviewExcMax))
	exc := s.readTeamExceptions(ctx, route.Env, teamExcInput{Team: team, Services: in.Services, Axis: axis},
		to.Add(-time.Duration(excRangeS)*time.Second), to, teamOverviewExcMax)
	var stepErr error
	if exc.Ev.Err != "" {
		stepErr = errors.New(exc.Ev.Err)
	}
	emitGuidedStepResult(emit, n, "team_latest_exceptions", renderTeamExceptionsTR(exc), stepErr)
	v := teamOverviewView{
		Team: team, Total: len(in.Services) + in.Trimmed, ReadSvcs: len(in.Services), Trimmed: in.Trimmed,
		Rows: in.Rows, Names: in.Names, RangeS: rangeS, Env: route.Env, Exc: exc,
	}
	v.ServicesHref, _ = teamServicesPageHref(team, axis)
	route.AnswerTail = renderTeamOverviewTR(v)
	route.TeamFollowups = append(teamServiceAskChips(in.Names, route.Env, teamOverviewChipMax), route.TeamFollowups...)
	return teamOverviewEvidenceTR(v)
}

// teamAxisOf — takımın katalogdaki ekseni (owner|sre|both); katalog okunamazsa
// owner (Exceptions linkinin v0.10.1385 varsayılanı).
func (s *Server) teamAxisOf(ctx context.Context, team string) string {
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		return teamAxisOwner
	}
	return teamExceptionsAxis(s.teamAliasesCtx(ctx), mds, team)
}

// teamServicesPageHref — SAF: Servisler sayfasının takım süzgeçli adresi.
// Param'lar sayfanın OKUDUKLARI (servicesUrlSpec.ts — kaynak-pin testi);
// sayfanın iki süzgeci AND olduğundan tek eksen: sre ekseninde sreTeam, aksi
// hâlde ownerTeam. safelink'ten geçmeyen adres → ok=false.
func teamServicesPageHref(team, axis string) (string, bool) {
	team = strings.TrimSpace(team)
	if team == "" {
		return "", false
	}
	key := servicesPageOwnerParam
	if axis == teamAxisSRE {
		key = servicesPageSREParam
	}
	return safelink.SafeLink("/services?" + url.Values{key: {team}}.Encode())
}

// teamServicesAnswerLinks — SAF: team_services / my_services cevabının derin
// linkleri: takım süzgeçli Servisler, en kötü servis, takımın Exceptions/Errors
// sayfası (teamExceptionsLinks — owner|sre, son görülme sıralı). Takım
// çözülmediyse düz Servisler.
func teamServicesAnswerLinks(route guidedRoute) []guidedAnswerLink {
	team := strings.TrimSpace(route.Team)
	var links []guidedAnswerLink
	if h, ok := teamServicesPageHref(team, route.ExcAxis); ok {
		links = append(links, guidedAnswerLink{Label: "Servisler · " + team, Href: h})
	} else {
		links = append(links, guidedAnswerLink{Label: "Servisler", Href: "/services"})
	}
	if len(route.TeamServices) > 0 {
		worst := route.TeamServices[0]
		links = append(links, guidedAnswerLink{Label: worst + " · Overview", Href: "/service?name=" + url.QueryEscape(worst)})
	}
	if team != "" {
		links = append(links, teamExceptionsLinks(route)...)
	}
	return links
}

// teamServiceAskChips — SAF: "hangi servis?" çipleri — ilk max uygun servis
// için etiket = servis adı, metin = "<svc> sağlığı nasıl?" (router'da LLM'siz
// service_health), kapsam servis + env. Uygunsuz ad (boşluk, sentetik önek,
// fuServiceRe dışı) atlanır; aynı ad bir kez.
func teamServiceAskChips(names []string, env string, max int) []chatFollowup {
	var out []chatFollowup
	seen := map[string]bool{}
	for _, n := range names {
		if len(out) >= max {
			break
		}
		svc := strings.TrimSpace(n)
		if seen[svc] || !starterServiceEligible(svc) || !fuServiceRe.MatchString(svc) {
			continue
		}
		seen[svc] = true
		out = append(out, chatFollowup{
			Text: svc + " sağlığı nasıl?", Label: starterTrunc(svc, teamSumChipLabelRune),
			Kind: fuKindTeamService, Scope: &chatScope{Services: []string{svc}, Env: env},
		})
	}
	return out
}

// mdCell — SAF: markdown tablo hücresi (boru ve satır sonu kaçışı).
func mdCell(s string) string {
	return strings.ReplaceAll(oneLine(s), "|", `\|`)
}

// renderTeamOverviewTR — SAF (tablo testli): sunucu eki. Başında "\n\n"
// (anlatımdan ayrılır); sonunda kapanış sorusu.
func renderTeamOverviewTR(v teamOverviewView) string {
	var b strings.Builder
	b.WriteString("\n\n")
	shown := 0
	if len(v.Rows) > 0 {
		fmt.Fprintf(&b, "**%s takımının servisleri** — %d servis, hata oranına göre (son %s):\n\n", mdCell(v.Team), v.Total, fmtAgoTR(v.RangeS))
		b.WriteString("| Servis | Hata oranı | Hata | Span | p99 |\n| --- | ---: | ---: | ---: | ---: |\n")
		for _, r := range v.Rows {
			if shown >= teamOverviewListMax {
				break
			}
			fmt.Fprintf(&b, "| %s | %%%.2f | %s | %s | %.0f ms |\n", mdCell(r.Name), r.ErrorRate, fmtThousands(r.ErrorCount), fmtThousands(r.SpanCount), r.P99Ms)
			shown++
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "**%s takımının servisleri** — %d servis; son %s içinde hiçbirinden span verisi yok (hata oranı okunamıyor):\n", mdCell(v.Team), v.Total, fmtAgoTR(v.RangeS))
		for _, n := range v.Names {
			if shown >= teamOverviewListMax {
				break
			}
			fmt.Fprintf(&b, "- %s\n", mdCell(n))
			shown++
		}
		b.WriteString("\n")
	}
	if more := v.Total - shown; more > 0 {
		var parts []string
		if len(v.Rows) > 0 {
			if lower := len(v.Rows) - shown; lower > 0 {
				parts = append(parts, fmt.Sprintf("%d tanesi daha düşük hata oranlı", lower))
			}
			if silent := v.ReadSvcs - len(v.Rows); silent > 0 {
				parts = append(parts, fmt.Sprintf("%d tanesi bu pencerede span üretmedi", silent))
			}
		}
		if v.Trimmed > 0 {
			parts = append(parts, fmt.Sprintf("%d tanesi okunmadı (tavan %d)", v.Trimmed, maxTeamServices))
		}
		fmt.Fprintf(&b, "+%d servis daha", more)
		if len(parts) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
		}
		b.WriteString(".")
		if v.ServicesHref != "" {
			fmt.Fprintf(&b, " Tümü: [Servisler · %s](%s)", mdCell(v.Team), v.ServicesHref)
		}
		b.WriteString("\n")
	} else if v.ServicesHref != "" {
		fmt.Fprintf(&b, "Servisler sayfasında: [Servisler · %s](%s)\n", mdCell(v.Team), v.ServicesHref)
	}
	if v.Env != "" && len(v.Rows) > 0 {
		fmt.Fprintf(&b, "_Not: hata oranları tüm ortamların toplamı; %q ortam süzgeci yalnız exception bölümüne uygulandı._\n", v.Env)
	}
	b.WriteString("\n")
	b.WriteString(renderTeamOverviewExcTR(v.Exc))
	b.WriteString("\n**" + teamOverviewClosingQ + "** Alttaki servis çiplerinden birini seç ya da servis adını yaz.")
	return b.String()
}

// renderTeamOverviewExcTR — SAF: son exception grupları bölümü. Üç hâl:
// gruplar / "yok" (okundu, boş) / "OKUNAMADI" (okuma düştü — sıfır DEĞİL).
func renderTeamOverviewExcTR(x teamExcView) string {
	var b strings.Builder
	win := "pencere"
	if x.Ev.LookbackS > 0 {
		win = "son " + fmtAgoTR(x.Ev.LookbackS)
	}
	b.WriteString("**Son exception grupları** — Exceptions/Errors, açık gruplar, " + win + ", en son görülen önce")
	if x.Ev.Err == "" && x.EnvNote != "" {
		b.WriteString(" · " + strings.TrimSuffix(strings.TrimSpace(x.EnvNote), "."))
	}
	b.WriteString(":\n")
	switch {
	case x.Ev.Err != "":
		fmt.Fprintf(&b, "OKUNAMADI (okuma başarısız: %s) — grup olup olmadığı bilinmiyor, \"yok\" sanma.\n", clipRunes(oneLine(x.Ev.Err), 120))
	case len(x.Ev.Groups) == 0:
		fmt.Fprintf(&b, "Yok — %s içinde takımın servislerinde açık exception grubu bulunmadı", win)
		if x.Floor > 0 {
			fmt.Fprintf(&b, " (sayfanın varsayılan tabanıyla: <%d oluşumlu tek-servis gruplar sayılmadı)", x.Floor)
		}
		b.WriteString(".\n")
	default:
		for i, l := range x.Ev.Groups {
			b.WriteString(teamOverviewExcLineTR(i+1, l, x.Ev.at))
		}
		if x.Ev.More > 0 {
			more := fmt.Sprintf("%d", x.Ev.More)
			if x.Ev.MoreAtMin {
				more = "en az " + more
			}
			fmt.Fprintf(&b, "(+%s grup daha — tümü Exceptions/Errors'ta)\n", more)
		}
	}
	if x.PageHref != "" {
		fmt.Fprintf(&b, "[Exceptions/Errors'ta aç · %s](%s)", mdCell(x.Team), x.PageHref)
		if x.Axis == teamAxisBoth {
			if h, ok := exceptionsPageTeamHref(x.Team, teamAxisSRE, "", false); ok && h != x.PageHref {
				fmt.Fprintf(&b, " · [SRE süzgeçli](%s)", h)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// teamOverviewExcLineTR — SAF: tek grup satırı (markdown, kısa).
func teamOverviewExcLineTR(n int, l exGroupLine, at time.Time) string {
	var b strings.Builder
	if l.Oracle {
		fmt.Fprintf(&b, "%d. [teknik hata · Oracle] `%s` · %s", n, mdCell(clipRunes(l.Code, exGroupCodeRunes)), mdCell(clipRunes(l.Operation, teamOverviewExcLineRun)))
	} else {
		fmt.Fprintf(&b, "%d. `%s`", n, mdCell(clipRunes(l.Type, exGroupOpRunes)))
	}
	if len(l.Services) > 0 {
		sv := make([]string, len(l.Services))
		for j, s := range l.Services {
			sv[j] = mdCell(clipRunes(s, exGroupSvcRunes))
		}
		fmt.Fprintf(&b, " · %s", strings.Join(sv, ", "))
		if l.ServiceCount > len(l.Services) {
			fmt.Fprintf(&b, " (+%d)", l.ServiceCount-len(l.Services))
		}
	}
	if l.LastSeen > 0 && !at.IsZero() {
		fmt.Fprintf(&b, " · son görülme %s önce", fmtAgoTR(int64(at.Sub(time.Unix(0, l.LastSeen))/time.Second)))
	}
	fmt.Fprintf(&b, " · %s oluşum", fmtThousands(l.Occurrences))
	if l.Priority != "" {
		fmt.Fprintf(&b, " · **%s**", l.Priority)
	}
	if h, ok := safelink.SafeLink(l.Href); ok {
		fmt.Fprintf(&b, " · [detay](%s)", h)
	}
	b.WriteString("\n")
	return b.String()
}

// teamOverviewEvidenceTR — SAF: modele giden kısa not (ek ne taşıyor; tekrar
// yazma). Exception bölümünün hâli de söylenir — anlatım "exception yok" diye
// OKUNAMADI'yı ezmesin.
func teamOverviewEvidenceTR(v teamOverviewView) string {
	shown := len(v.Rows)
	if shown == 0 {
		shown = len(v.Names)
	}
	if shown > teamOverviewListMax {
		shown = teamOverviewListMax
	}
	exc := fmt.Sprintf("%d grup", len(v.Exc.Ev.Groups))
	switch {
	case v.Exc.Ev.Err != "":
		exc = "OKUNAMADI"
	case len(v.Exc.Ev.Groups) == 0:
		exc = "yok"
	}
	return fmt.Sprintf("SUNUCU EKİ (cevabın ALTINA deterministik eklenir, sen YAZMA): %d servislik tablo (toplam %d, hata oranına göre), "+
		"son exception grupları (%s) ve kapanış sorusu %q. Tabloyu, exception listesini ve bu soruyu TEKRAR YAZMA; "+
		"exception bölümü OKUNAMADI ise \"exception yok\" DEME.\n", shown, v.Total, exc, teamOverviewClosingQ)
}
