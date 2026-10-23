package api

// copilot_outbound_call_route.go — v0.10.1455: outbound_call R2 (yolsuz
// hedef sözcüğü) — SAF router yarısı + canlı dış host kataloğu.
//
// Prod: "<servis> mainframe cevap sürelerimde artış var mı?" doküman/RAG
// kademesine düşüyordu (hiçbir guided sinyal yok); "esbprod'a giden çağrılar
// yavaşladı mı?" ise sunucu tarafı servis sağlığına gidiyordu (yanlış
// popülasyon: servisin GELEN istekleri, ESB'ye GİDEN çağrıları değil).
//
// Kural (plan-outbound-call.md §1.2): HEDEF (canlı dış host adı / ilk DNS
// etiketi, ya da sabit kök esb·osb·tibco·mainframe·anamakine·soap·wsdl, ya da
// "dış servis" / "giden çağrı" ikilisi) VE ÖLÇÜ ipucu (gecikme / hata / grafik
// / ortalama / artış / cevap süresi) birlikte → outbound_call. Router'da
// topolojiden ve metrik grafiğinden ÖNCE (copilot_guided.go): "ESB gecikme
// grafiği" sunucu p99 şablonuna, "dış bağımlılıklarım yavaş mı" topolojiye
// gitmesin. Oracle (hasDBSignal) db_health'te, kuyruk/log/pod/kaynak kendi
// yollarında kalır; yol anan mesaj R1'in (ön-getirme sondası) ya da
// endpoint'in. Host tek adaya inmezse tahmin yok: "hangi dış host?" çipleri.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// outboundHostEntry — canlı dış host kataloğunun satırı (GetExternalHosts:
// MV, enstrümante servisler SQL'de dışlanmış, çağrı sırasıyla).
type outboundHostEntry struct {
	Host    string   `json:"h"`
	Callers []string `json:"c,omitempty"` // ≤8 çağıran (groupUniqArray(8))
	Calls   uint64   `json:"n"`
}

const (
	outboundHostsCacheKey = "copilot:guided:exthosts"
	outboundHostsTTL      = 60 * time.Second
	outboundHostsLookback = 24 * time.Hour
	outboundHostsTimeout  = 3 * time.Second
	outboundOptionsMax    = 5
	outboundLabelMin      = 4 // ilk DNS etiketi eşleşmesinin taban uzunluğu
)

// outboundFixedStems — yolsuz hedef sabit kökleri (jeton öneki).
var outboundFixedStems = []string{"esb", "osb", "tibco", "mainframe", "anamakine", "soap", "wsdl"}

// outboundStemSearch — kökün host adında aranan biçimi (eşanlam).
var outboundStemSearch = map[string]string{"anamakine": "mainframe"}

// outboundLabelStop — ilk DNS etiketi olarak hedef sayılmayan genel sözcükler.
var outboundLabelStop = map[string]bool{
	"gateway": true, "service": true, "servis": true, "external": true, "internal": true,
	"local": true, "host": true, "localhost": true, "proxy": true, "server": true,
}

// outboundMeasureStems — ÖLÇÜ ipucu kökleri (hasLatencyAsk / hasErrorSignal /
// hasChartVerb'e ek): ortalama · artış · yükselme · cevap/yanıt süresi.
var outboundMeasureStems = []string{"ortalama", "avg", "average", "artış", "artis", "arttı", "artti", "artmış", "artmis",
	"yüksel", "yuksel", "cevap", "yanıt", "yanit", "response", "süre", "duration", "performan", "durum"}

// outboundMeasureCue — SAF: soru bir ÖLÇÜ istiyor mu.
func outboundMeasureCue(norm string, toks []string) bool {
	return hasLatencyAsk(norm) || hasErrorSignal(toks) || hasChartVerb(toks) || tokenHasPrefix(toks, outboundMeasureStems...)
}

// outboundExcluded — SAF: başka bir niyetin kesin şekli (DB / kuyruk / log /
// pod / kaynak) — hedef sözcüğü olsa bile o yolda kalır.
func outboundExcluded(toks []string) bool {
	return hasDBSignal(toks) || hasMessagingSignal(toks) || hasLogSignal(toks) || hasPodSignal(toks) || hasResourceSignal(toks)
}

// outboundTargetTerm — SAF: sabit kök ya da genel ikili. term = arama
// sözcüğü (sabit kökte jetonun kendisi: "esbprod"; ikilide ""), generic =
// "dış servis / giden çağrı" şekli. Canlı servis adı olan jeton hedef değil.
// İkisi birlikte dönebilir ("dış host esbprod… çağrı süreleri").
func outboundTargetTerm(toks []string, liveSvc map[string]bool) (term, stem string, generic bool) {
fixed:
	for _, t := range toks {
		if liveSvc[t] {
			continue
		}
		for _, s := range outboundFixedStems {
			if strings.HasPrefix(t, s) {
				term, stem = t, s
				break fixed
			}
		}
	}
	for i := 0; i+1 < len(toks) && !generic; i++ {
		a, b := toks[i], toks[i+1]
		switch {
		case (a == "dış" || a == "dis" || a == "harici" || a == "external") &&
			tokenHasPrefix([]string{b}, "servis", "service", "bağımlılı", "bagimlili", "dependenc", "çağrı", "cagri", "call", "api", "sistem", "system", "host", "entegrasyon", "integration"):
			generic = true
		case (a == "giden" || a == "outbound") && tokenHasPrefix([]string{b}, "çağrı", "cagri", "istek", "call", "request"):
			generic = true
		case (a == "üçüncü" || a == "ucuncu" || a == "3rd" || a == "third") && tokenHasPrefix([]string{b}, "parti", "party"):
			generic = true
		}
	}
	return term, stem, generic
}

// outboundHostLabel — SAF: host'un ilk DNS etiketi (küçük harf).
func outboundHostLabel(host string) string {
	h := strings.ToLower(host)
	if i := strings.IndexByte(h, '.'); i > 0 {
		return h[:i]
	}
	return h
}

// outboundLiveHostMatches — SAF: mesajda anılan canlı dış hostlar. Tam ad
// (sınırlı) önce; yoksa ilk DNS etiketi bir jetona BİREBİR eşitse (≥4
// karakter, ortam kademesi / canlı servis adı / genel sözcük / sayı değil).
func outboundLiveHostMatches(norm string, toks []string, hosts []outboundHostEntry, liveSvc map[string]bool) []outboundHostEntry {
	var full, byLabel []outboundHostEntry
	tokSet := map[string]bool{}
	for _, t := range toks {
		tokSet[t] = true
	}
	for _, h := range hosts {
		lh := strings.ToLower(strings.TrimSpace(h.Host))
		if lh == "" {
			continue
		}
		if strings.Contains(lh, ".") && indexBounded(norm, lh) >= 0 {
			full = append(full, h)
			continue
		}
		lb := outboundHostLabel(lh)
		if len(lb) < outboundLabelMin || envTierWords[lb] || liveSvc[lb] || outboundLabelStop[lb] || guidedStopwords[lb] ||
			strings.Trim(lb, "0123456789") == "" || !tokSet[lb] {
			continue
		}
		byLabel = append(byLabel, h)
	}
	if len(full) > 0 {
		return full
	}
	return byLabel
}

// outboundTermCandidates — SAF: sabit kökün aday hostları (jeton, yoksa kök
// arama biçimi, host adında geçer); genel ikilide tüm hostlar.
func outboundTermCandidates(term, stem string, generic bool, hosts []outboundHostEntry) []outboundHostEntry {
	if generic {
		return hosts
	}
	pick := func(needle string) []outboundHostEntry {
		var out []outboundHostEntry
		for _, h := range hosts {
			if needle != "" && strings.Contains(strings.ToLower(h.Host), needle) {
				out = append(out, h)
			}
		}
		return out
	}
	if out := pick(term); len(out) > 0 {
		return out
	}
	if s, ok := outboundStemSearch[stem]; ok {
		return pick(s)
	}
	return pick(stem)
}

// outboundByCaller — SAF: çağıranın listede olduğu hostlar (katalog ≤8 çağıran taşır).
func outboundByCaller(hosts []outboundHostEntry, caller string) []outboundHostEntry {
	var out []outboundHostEntry
	for _, h := range hosts {
		for _, c := range h.Callers {
			if c == caller {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

func outboundHostNames(hosts []outboundHostEntry, max int) []string {
	sorted := append([]outboundHostEntry(nil), hosts...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Calls > sorted[j].Calls })
	var out []string
	for _, h := range sorted {
		if len(out) == max {
			break
		}
		out = append(out, h.Host)
	}
	return out
}

func lowerSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[strings.ToLower(n)] = true
	}
	return m
}

// routeOutboundCall — SAF (v0.10.1455, R2): yolsuz hedef sözcüğü + ölçü
// ipucu → outbound_call. Host tek adaya inerse OutboundPeer; inmezse
// OutboundOptions (≤5, çağrı sırası) — cevap "hangi dış host?" çipleri sorar,
// tahmin etmez. Çağıran: mesaj servisi > "A'dan <host>'a" çiftinin kaynağı >
// ekran servisi; katalog satırının çağıranları (≤8) adayları daraltır.
func routeOutboundCall(raw, norm string, toks []string, svc, env string, services, envs []string, hosts []outboundHostEntry, ctxService string) (guidedRoute, bool) {
	if !outboundMeasureCue(norm, toks) || outboundExcluded(toks) {
		return guidedRoute{}, false
	}
	if _, _, ok := endpointMention(raw); ok { // yol anması R1 sondasının / endpoint'in
		return guidedRoute{}, false
	}
	liveSvc := lowerSet(services)
	matched := outboundLiveHostMatches(norm, toks, hosts, liveSvc)
	term, stem, generic := outboundTargetTerm(toks, liveSvc)
	caller := svc
	// Servis çıkarımı hedef jetonunun kendisinden geldiyse ("esb" → canlı
	// "esb-adapter", tam ad mesajda yok) o servis çağıran değil, hedeftir.
	if term != "" && caller != "" && strings.Contains(strings.ToLower(caller), term) && indexBounded(norm, strings.ToLower(caller)) < 0 {
		caller = ""
	}
	// "A'dan B'ye giden …" çifti: B canlı dış host ve A tek servis ise giden
	// çağrı (çağıran = A); aksi çift niyetinin (pair_requests / fan-out).
	if fromFrag, toFrag, ok := splitPairFragments(raw); ok {
		tn := normalizeGuidedMsg(toFrag)
		toHosts := outboundLiveHostMatches(tn, guidedTokens(tn), hosts, liveSvc)
		fromOpts := resolvePairSide(fromFrag, services, envs)
		if len(fromOpts) != 1 || len(toHosts) != 1 {
			return guidedRoute{}, false
		}
		caller, matched = fromOpts[0], toHosts
	}
	if len(matched) == 0 && term == "" && !generic {
		return guidedRoute{}, false
	}
	if caller == "" {
		caller = ctxService
	}
	r := guidedRoute{Intent: guidedOutboundCall, Service: caller, Env: env, OutboundTerm: term}
	if caller != "" {
		r.OutboundCallers = []string{caller}
	}
	cands := matched
	if len(cands) == 0 {
		cands = outboundTermCandidates(term, stem, generic && term == "", hosts)
	}
	if caller != "" && len(cands) > 1 {
		if mine := outboundByCaller(cands, caller); len(mine) > 0 {
			cands = mine
		}
	}
	if len(cands) == 1 {
		r.OutboundPeer = cands[0].Host
		return r, true
	}
	if len(cands) == 0 { // terim hiçbir canlı hostta geçmiyor: en yoğun hostlar (çağıranınki önce)
		r.OutboundMiss = term != ""
		cands = hosts
		if caller != "" {
			if mine := outboundByCaller(hosts, caller); len(mine) > 0 {
				cands = mine
			}
		}
	}
	r.OutboundOptions = outboundHostNames(cands, outboundOptionsMax)
	return r, true
}

// hasOutboundSignal — SAF: hasGuidedSignal'ın ucuz ön kapısı (katalog
// okumasından önce): ölçü ipucu + sabit kök / genel ikili / host şekilli jeton.
// "<svc> mainframe cevap sürelerimde artış var mı" hiçbir guided sinyal
// taşımıyor ve RAG'a düşüyordu.
func hasOutboundSignal(norm string, toks []string) bool {
	if !outboundMeasureCue(norm, toks) {
		return false
	}
	if term, _, generic := outboundTargetTerm(toks, nil); term != "" || generic {
		return true
	}
	for _, t := range toks {
		if i := strings.IndexByte(t, '.'); i >= 3 && i < len(t)-2 && strings.IndexFunc(t[i+1:], func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0 &&
			strings.IndexFunc(t[:i], func(r rune) bool { return r >= 'a' && r <= 'z' }) >= 0 {
			return true
		}
	}
	return false
}

// outboundVetoesOpPrefetch — SAF: operasyon ön-getirme kademesi bu mesajı
// sahiplenmesin mi. Yalnız GENEL ikili ("dış host <ad> çağrı süreleri" —
// "hangi dış host?" çipinin metni) + ölçü ipucu, yol anması yok: tam host
// adı identifierLike olduğu için katalogdaki "POST <host>" client operasyonuna
// bulanık eşleşip çipi ön-getirmeye kaptırırdı. Sabit kök (mainframe/esb)
// ön-getirmede kalır (plan §1.2: katalog operasyon adı daha özgül).
func outboundVetoesOpPrefetch(raw string) bool {
	norm := normalizeGuidedMsg(raw)
	toks := guidedTokens(norm)
	if !outboundMeasureCue(norm, toks) || outboundExcluded(toks) {
		return false
	}
	if _, _, ok := endpointMention(raw); ok {
		return false
	}
	_, _, generic := outboundTargetTerm(toks, nil)
	return generic
}

// guidedOutboundHosts — canlı dış host kataloğu (Redis 60 sn; guidedServiceNames
// ikizi). Yalnız ölçü ipucu taşıyan, başka niyetin kesin şekli olmayan
// mesajda okunur. Hata / store yok → nil (router yalnız sabit köklerle çalışır).
func (s *Server) guidedOutboundHosts(ctx context.Context, norm string) []outboundHostEntry {
	toks := guidedTokens(norm)
	if s == nil || !outboundMeasureCue(norm, toks) || outboundExcluded(toks) {
		return nil
	}
	if s.cache != nil {
		if b, ok, _ := s.cache.Get(ctx, outboundHostsCacheKey); ok && len(b) > 0 {
			var hs []outboundHostEntry
			if json.Unmarshal(b, &hs) == nil {
				return hs
			}
		}
	}
	if s.store == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, outboundHostsTimeout)
	defer cancel()
	now := time.Now()
	rows, err := s.store.GetExternalHosts(cctx, now.Add(-outboundHostsLookback), now)
	if err != nil {
		return nil
	}
	hs := make([]outboundHostEntry, 0, len(rows))
	for _, r := range rows {
		hs = append(hs, outboundHostEntry{Host: r.Host, Callers: r.CallerNames, Calls: r.Calls})
	}
	if s.cache != nil {
		if b, merr := json.Marshal(hs); merr == nil {
			_ = s.cache.Set(ctx, outboundHostsCacheKey, b, outboundHostsTTL)
		}
	}
	return hs
}
