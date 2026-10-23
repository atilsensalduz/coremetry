package api

// family_traces.go — v0.10.465 (CoSRE sohbet paritesi D2): "hatalı / yavaş
// trace'ler(i getir)" + AİLE (ya da açık 2+ ad, ya da tek servis).
//
// Ölçüm (v0.10.460 probu): "mobile bff son hatalı traceler" ve "mobile
// bff'nin son 1 saatteki hatalı trace'lerini getir" aile rotasına
// (family_health: yan yana RED) düşüyordu — operatör trace LİSTESİ istemişti;
// "mobile bff yavaş traceler" iki adayla ask_service oluyordu, oysa "mobile
// bff" ikisini birden kastediyor. "checkout hatalı traceler" ise
// service_health'e çöküyordu.
//
// Kural: mesaj TRACE kökü taşıyor + hata ya da yavaş sinyali → trace listesi;
// kapsam = açık 2+ ad > ad-parçası ailesi > açık tek servis > sayfa
// bağlamı. Trace kökü yoksa ("mobile bff hataları") aile sağlığı AYNEN
// kalır. Liste tek sorgu: service IN (...) çipi (FilterExpr, /traces'ın
// kendi `filters=` kodeği) + hasError; yavaş → duration desc, hatalı →
// zaman desc (en yeni hatalar). Link aynı görünümü açar.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const guidedFamilyTraces guidedIntent = "family_traces"

const familyTracesMaxServices = 40 // extractServiceFamily tavanıyla aynı

// exceptionTraceQualifiers — exception sözcüğünü trace'in SIFATI yapan
// ekler / komşular ("exception'lı trace", "exception alan trace").
var exceptionTraceQualifiers = map[string]bool{"lı": true, "li": true, "lu": true, "lü": true,
	"alan": true, "veren": true, "olan": true, "fırlatan": true, "firlatan": true, "atan": true, "with": true}

// exceptionOwnsTraceAsk — SAF (v0.10.1479): mesajda exception/istisna sözcüğü
// var ve hiçbiri trace'i niteleyen sıfat değil → soru exception gruplarının
// (trace ancak örneği). Nitelik: bitişik ek ("exceptionlı"), kesme sonrası ek
// ("exception'lı" → "exception","lı") ya da fiil komşusu ("exception alan").
func exceptionOwnsTraceAsk(toks []string) bool {
	found := false
	for i, t := range toks {
		var rest string
		switch {
		case strings.HasPrefix(t, "exception"):
			rest = strings.TrimPrefix(t, "exception")
		case strings.HasPrefix(t, "istisna"):
			rest = strings.TrimPrefix(t, "istisna")
		default:
			continue
		}
		if exceptionTraceQualifiers[rest] || (i+1 < len(toks) && exceptionTraceQualifiers[toks[i+1]]) {
			return false
		}
		found = true
	}
	return found
}

// plainTraceFiller — v0.10.1487: düz trace listesi sorusunda trace kökü ve
// kapsam adlarının YANINDA bulunabilecek sözcükler (kapalı liste). Başka her
// sözcük (neden, sayfa/aç, sayı, topoloji, log, deploy, nedir …) soruyu
// bugünkü yoluna bırakır — nötr kip yalnız "listeyi ver" isteğini alır.
var plainTraceFiller = map[string]bool{
	// trace kökünün kesme sonrası ekleri ("trace'leri" → "trace", "leri")
	"i": true, "ı": true, "in": true, "ini": true, "ler": true, "leri": true, "lerini": true, "lerine": true,
	"lerin": true, "e": true, "a": true, "de": true, "da": true, "den": true, "nin": true, "nın": true, "un": true, "ün": true,
	// fiiller
	"bul": true, "bulur": true, "bulsana": true, "göster": true, "goster": true, "gösterir": true, "göstersene": true,
	"getir": true, "getirir": true, "getirsene": true, "listele": true, "listeler": true, "ver": true, "verir": true,
	"bak": true, "bakalım": true, "gör": true, "görelim": true, "görebilir": true, "musun": true, "misin": true,
	"mısın": true, "müsün": true, "lütfen": true, "lutfen": true, "bana": true,
	// sıfat / kapsam
	"son": true, "en": true, "yeni": true, "güncel": true, "guncel": true, "örnek": true, "ornek": true,
	"birkaç": true, "birkac": true, "bir": true, "tüm": true, "tum": true, "bütün": true, "butun": true,
	"servis": true, "servisi": true, "servisin": true, "servisinin": true, "servisindeki": true, "servisine": true,
	"servisinde": true, "için": true, "icin": true, "ait": true, "olan": true,
	// pencere
	"saat": true, "saatte": true, "saatin": true, "saatteki": true, "saatlik": true, "dakika": true, "dakikada": true,
	"dakikadaki": true, "dakikalık": true, "bugün": true, "bugun": true, "bugünkü": true, "gün": true, "günün": true,
	// varlık sorusu
	"var": true, "mı": true, "mi": true, "neler": true, "listesi": true, "listesini": true,
	// EN
	"show": true, "find": true, "list": true, "get": true, "recent": true, "latest": true, "last": true,
	"of": true, "for": true, "the": true, "me": true,
}

// hasPlainTraceWords — SAF: trace kökü taşıyan bir jeton var (ucuz ön kapı;
// asıl karar plainTraceListAsk).
func hasPlainTraceWords(toks []string) bool {
	for _, t := range toks {
		if isTraceNounToken(t) {
			return true
		}
	}
	return false
}

// isTraceNounToken — "trace", "traces", "traceler", "trace'leri"nin kökü,
// "tracelerini" … (ek bitişik yazılmış hâller).
func isTraceNounToken(t string) bool {
	if !strings.HasPrefix(t, "trace") {
		return false
	}
	switch strings.TrimPrefix(t, "trace") {
	case "", "s", "i", "ı", "in", "ini", "ler", "leri", "lerini", "lerine", "lerin", "e", "de", "den":
		return true
	}
	return false
}

// plainTraceListAsk — SAF (v0.10.1487): mesaj trace isminden, kapsam
// adlarından (servis / aile üyesinin adı ya da ad parçası, ortam), sayılardan
// ve plainTraceFiller'dan ibaret. Kimlikli trace, neden, sayfa, topoloji,
// örnekleme, sayı vb. her ek sözcük düşürür (o sorular bugünkü yollarında).
func plainTraceListAsk(toks []string, names, envs []string) bool {
	noun := false
	for _, t := range toks {
		switch {
		case isTraceNounToken(t):
			noun = true
		case plainTraceFiller[t], isDigits(t):
		case nameOrEnvToken(t, names, envs):
		default:
			return false
		}
	}
	return noun
}

func isDigits(t string) bool {
	if t == "" {
		return false
	}
	for _, r := range t {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// nameOrEnvToken — jeton bir kapsam adının kendisi / bölümü ya da ortam adı.
func nameOrEnvToken(t string, names, envs []string) bool {
	for _, e := range envs {
		if strings.EqualFold(e, t) {
			return true
		}
	}
	for _, n := range names {
		ln := strings.ToLower(n)
		if ln == t || (len(t) >= 3 && segmentInName(ln, t)) {
			return true
		}
	}
	return false
}

func routeFamilyTraces(msg string, toks []string, svc, env string, services, envs []string, ctxService string) (guidedRoute, bool) {
	if !tokenHasPrefix(toks, "trace") {
		return guidedRoute{}, false
	}
	errSig, slowSig := hasErrorSignal(toks), hasSlowTraceSignal(msg)
	// v0.10.1487 — hata/yavaş sözcüğü yoksa yalnız DÜZ trace listesi isteği
	// ("<svc> trace'leri / trace'i bul / son trace'ler / trace'lerini göster")
	// nötr "son trace'ler" kipine girer; kapsam aşağıda açık servis / aile /
	// ekran servisi, sözcük kümesi plainTraceListAsk'ta kapalı liste.
	recent := !errSig && !slowSig
	if recent && !hasPlainTraceWords(toks) {
		return guidedRoute{}, false
	}
	// v0.10.1479 — "X exception grupları neler, örnek stack ve ilgili trace'i
	// bul": soru exception GRUPLARINI soruyor, trace yalnız örnek → servisli
	// service_exceptions dalına bırak. "exception'lı / exception alan trace'ler"
	// (exception trace'i niteliyor) burada kalır.
	if (svc != "" || ctxService != "") && exceptionOwnsTraceAsk(toks) {
		return guidedRoute{}, false
	}
	var fam []string
	switch {
	case svc != "":
		if multi := extractServiceEntities(msg, services); len(multi) >= 2 {
			fam = multi
		} else {
			fam = []string{svc}
		}
	default:
		fam = extractServiceFamily(msg, services, envs)
		if len(fam) == 0 {
			// Nötr kipte bulanık aday üretimi YOK: düz "son trace'ler" sözcükleri
			// bir servisin parçasına oturup soruyu sessizce daraltmasın.
			if opts := serviceCandidates(msg, services, envs, familyTracesMaxServices); len(opts) > 0 && !recent {
				fam = opts
			} else if ctxService != "" {
				fam = []string{ctxService}
			}
		}
	}
	if len(fam) == 0 {
		return guidedRoute{}, false
	}
	if recent && !plainTraceListAsk(toks, fam, envs) {
		return guidedRoute{}, false
	}
	// TEK servis + yalnız yavaş → mevcut slow_traces rotası AYNEN (kendi
	// bundle'ı, çipleri, linkleri; TestAskServiceChipsRoundTrip pini). Bu
	// kademe tek serviste yalnız HATALI liste için yenidir ("checkout hatalı
	// trace'ler" eskiden service_health'e çöküyordu).
	if len(fam) == 1 && !errSig && !recent {
		return guidedRoute{}, false
	}
	if len(fam) > familyTracesMaxServices {
		fam = fam[:familyTracesMaxServices]
	}
	r := guidedRoute{Intent: guidedFamilyTraces, Family: fam, Env: env, TraceErrorsOnly: errSig && !slowSig, TraceRecent: recent}
	if len(fam) == 1 {
		r.Service = fam[0]
	}
	return r, true
}

// familyTraceFilter — tek sorgu: service IN (...) çipi + hasError; yavaş →
// duration desc, hatalı → zaman desc (en yeni hatalar önce).
//
// v0.10.1487 — mode: familyTraceMode (hata > son > yavaş). "recent" = süzgeçsiz,
// zaman desc (servisin son trace'leri); aynı MV-öncelikli GetTraces yolu
// (trace_summary_5m, CountMode skip, LIMIT 10).
func familyTraceFilter(services []string, mode, env string, from, to time.Time) chstore.TraceFilter {
	f := chstore.TraceFilter{Env: env, From: from, To: to, Limit: 10, CountMode: "skip", HasError: mode == familyModeErrors, Sort: "duration", Order: "desc"}
	if mode != familyModeSlow {
		f.Sort = "time"
	}
	if len(services) == 1 {
		f.Service = services[0]
	} else {
		f.Filters = []chstore.FilterExpr{{Key: "service", Op: "IN", Values: services}}
	}
	return f
}

// familyTrace kipleri (v0.10.1487). Hata kipi önce: "sadece hatalı olanlar"
// takibi nötr rotaya TraceErrorsOnly ekler, TraceRecent kalsa da hata kazanır.
const (
	familyModeSlow   = "slow"
	familyModeErrors = "errors"
	familyModeRecent = "recent"
)

func familyTraceMode(r guidedRoute) string {
	switch {
	case r.TraceErrorsOnly:
		return familyModeErrors
	case r.TraceRecent:
		return familyModeRecent
	}
	return familyModeSlow
}

// familyTracesHref — /traces linki; aynı süzgeç (Traces.tsx `filters=` JSON
// kodeği + hasError + sort/order).
func familyTracesHref(services []string, mode string) string {
	q := url.Values{}
	if len(services) == 1 {
		q.Set("service", services[0])
	} else {
		b, _ := json.Marshal([]chstore.FilterExpr{{Key: "service", Op: "IN", Values: services}})
		q.Set("filters", string(b))
	}
	switch mode {
	case familyModeErrors:
		q.Set("hasError", "true")
		q.Set("sort", "time")
	case familyModeRecent:
		q.Set("sort", "time")
	default:
		q.Set("sort", "duration")
	}
	q.Set("order", "desc")
	return "/traces?" + q.Encode()
}

func familyScopeTR(services []string) string {
	if len(services) == 1 {
		return services[0]
	}
	if len(services) <= 3 {
		return strings.Join(services, ", ")
	}
	return fmt.Sprintf("%s, %s … (%d servis)", services[0], services[1], len(services))
}

func renderFamilyTracesEvidenceTR(rows []chstore.TraceRow, services []string, mode string, env string, rangeS int64) string {
	kind, order := "En yavaş trace'ler", "duration'a göre"
	switch mode {
	case familyModeErrors:
		kind, order = "Hatalı trace'ler", "en yeniden eskiye"
	case familyModeRecent:
		kind, order = "Son trace'ler", "en yeniden eskiye; hata/süre süzgeci yok"
	}
	scope := familyScopeTR(services)
	if env != "" {
		scope += ", ortam: " + env
	}
	var b strings.Builder
	if len(rows) == 0 {
		fmt.Fprintf(&b, "%s (son %s; kapsam: %s): bu pencerede trace bulunamadı.\n", kind, fmtAgoTR(rangeS), scope)
		return b.String()
	}
	fmt.Fprintf(&b, "%s (son %s; kapsam: %s; %s):\n", kind, fmtAgoTR(rangeS), scope, order)
	for _, r := range rows {
		flag := ""
		if r.HasError {
			flag = ", HATA"
		}
		fmt.Fprintf(&b, "- %s · %.0fms — %s / %s (%d span%s) trace=%s\n",
			time.Unix(0, r.StartTime).UTC().Format("15:04:05"), r.DurationMs, r.ServiceName, r.RootName, r.SpanCount, flag, r.TraceID)
	}
	if len(services) > 1 {
		fmt.Fprintf(&b, "Kapsam %d servis: %s\n", len(services), strings.Join(services, ", "))
	}
	return b.String()
}

func (s *Server) guidedFamilyTracesBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	mode := familyTraceMode(*route)
	n := emitGuidedStep(emit, "family_traces", withEnvArg(fmt.Sprintf(`{"services":%d,"mode":%q}`, len(route.Family), mode), route.Env))
	rows, _, _, err := s.store.GetTraces(ctx, familyTraceFilter(route.Family, mode, route.Env, from, to))
	if err != nil {
		emitGuidedStepResult(emit, n, "family_traces", "", err)
		return "", "", err
	}
	ev := renderFamilyTracesEvidenceTR(rows, route.Family, mode, route.Env, rangeS)
	emitGuidedStepResult(emit, n, "family_traces", ev, nil)
	src := fmt.Sprintf("%d servis kapsamlı trace listesi (son %s)", len(route.Family), fmtAgoTR(rangeS))
	switch mode {
	case familyModeErrors:
		src = fmt.Sprintf("%d servis kapsamlı hatalı trace listesi (son %s)", len(route.Family), fmtAgoTR(rangeS))
	case familyModeRecent:
		src = fmt.Sprintf("%d servis kapsamlı son trace listesi (son %s)", len(route.Family), fmtAgoTR(rangeS))
	}
	if route.Env != "" {
		src += ", ortam: " + route.Env
	}
	return ev, src, nil
}
