package api

// copilot_metric_chart_promql.go — v0.10.1416 (CoSRE metrik grafiği R2;
// operatör: "VictoriaMetrics sorgulayıp grafik getirse"). Sohbete YAPIŞTIRILAN
// PromQL — çıplak ("sum(rate(x_total[5m])) çiz"), kod çitinde (```promql …```)
// ya da ters tırnakta — metric_chart rotasına ChartPromQL olarak iner; demet
// sorguyu doğrular (metric_chart_validate.go), her metrik adının VM'de var
// olduğunu önbellekli etiket okumasıyla sınar, TEK sınırlı range sorgusu koşar
// ve R1'in sohbet içi grafiğini (EvChartPayload + kanıt çipi + GRAFİK ÖZETİ)
// basar. Reddedilen sorgu ASLA çalışmaz: cevap gerekçeyi Türkçe söyler (anlatım
// çağrısı yok — DirectAnswer) ve "Metrics'te aç" linkini yine verir.
//
// Kararlar:
//   - Tanıma SAF (looksLikePromQL): ayrıştırıcıdan geçen VE PromQL biçimi
//     taşıyan (etiket eşleştiricisi, [aralık] ya da fonksiyon + alt çizgili ad)
//     parça; düzyazıdaki "rate" sözcüğü PromQL değildir. Çitli/ters tırnaklı
//     MetricsQL ya da ayrıştırılamayan PromQL biçimi de rotaya iner (dürüst ret).
//   - Pencere sorgunun DIŞINDAKİ metinden: açık pencere (5 dk..24 sa) yoksa 1 sa.
//   - Ortam (D8 genelleştirmesi): SORUDA anılan ortam sorgulanan metriğin ortam
//     etiketine çözülür (resolveMetricEnvScope) ve her seçiciye eklenir;
//     çözülemezse grafik YOK. Ekrandan devralınan ortam sorguya EKLENMEZ —
//     yapıştırılan sorgu olduğu gibi koşar ve kanıt bunu söyler.
//   - Dış ifade toplama değilse topk(20, …) (D3); ≤120 nokta; 8 sn; 60 sn önbellek.
//
// Salt-okur (TestPastedPromQLReadOnly): etiket okuması + tek range sorgusu.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/promql"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// v0.10.1416 — tanıma tavanları.
const (
	promqlDetectMaxLen    = 4096 // çitsiz metinde aranan en uzun mesaj
	promqlDetectLeadWords = 12   // sorgudan önce en çok bu kadar düzyazı sözcüğü
	promqlDetectTailWords = 10   // sorgudan sonra en çok bu kadar düzyazı sözcüğü
	promqlExistParallel   = 4
	promqlStepArgMax      = 200
)

// promqlTemplateID — önbellek anahtarının şablon bileşeni.
const promqlTemplateID = "promql"

// pastedPromQL — mesajdan çıkarılan sorgu.
type pastedPromQL struct {
	Query  string
	Prose  string // mesajın sorgu dışındaki metni (pencere / ortam bundan okunur)
	Fenced bool
	Extra  int // mesajdaki diğer aday sorgu sayısı (yalnız ilki çalışır)
}

var (
	promqlFenceRe  = regexp.MustCompile("(?s)```([^\n`]*)\n?(.*?)```")
	promqlInlineRe = regexp.MustCompile("`([^`\n]+)`")
	// promqlShapeRe — PromQL biçimi: etiket eşleştiricili seçici ya da [aralık].
	promqlShapeRe = regexp.MustCompile(`[A-Za-z_][\w.]*\s*\{\s*[A-Za-z_]\w*\s*(=~|!~|!=|=)\s*["'` + "`" + `]|\[\s*\d+(ms|s|m|h|d|w|y)\s*(:\s*(\d+(ms|s|m|h|d|w|y))?\s*)?\]`)
	promqlWordRe  = regexp.MustCompile(`\S+`)
	promqlLangRe  = regexp.MustCompile(`^[A-Za-z]{0,16}$`)
)

// promqlKnownFuncs — tanımada "fonksiyon çağrısı" sayılan adlar: izin
// listesindekiler, açıkça reddedilenler ve diğer PromQL fonksiyonları
// (doğrulayıcı reddeder ama sorgu olduğu bellidir).
var promqlKnownFuncs = func() map[string]bool {
	m := map[string]bool{}
	for k := range promqlRangeFuncs {
		m[k] = true
	}
	for k := range promqlVectorFuncs {
		m[k] = true
	}
	for k := range promqlDenyReasons {
		m[k] = true
	}
	for k := range promqlAggAllow {
		m[k] = true
	}
	for k := range promqlMetricsQLFuncs {
		m[k] = true
	}
	for _, k := range []string{"changes", "deriv", "resets", "exp", "ln", "log2", "log10", "sqrt", "timestamp",
		"count_over_time", "quantile_over_time", "present_over_time", "idelta", "clamp", "sgn", "minute", "hour",
		"day_of_week", "day_of_month", "days_in_month", "month", "year", "histogram_count", "histogram_sum",
		"histogram_fraction", "histogram_avg", "histogram_stddev", "histogram_stdvar", "mad_over_time"} {
		m[k] = true
	}
	return m
}()

// promqlStructural — SAF: ayrıştırılmış ifade PromQL biçimi taşıyor mu
// (düzyazıdaki tek sözcük / "rate(cpu)" PromQL sayılmaz).
func promqlStructural(e promql.Expr) bool {
	shaped, call, named := false, false, false
	promqlVisit(e, func(x promql.Expr) {
		switch n := x.(type) {
		case *promql.MatrixSelector:
			shaped = true
		case *promql.VectorSelector:
			if len(n.Matchers) > 0 {
				shaped = true
			}
			if strings.ContainsAny(n.Name, "_:") {
				named = true
			}
		case *promql.Call:
			if promqlKnownFuncs[n.Func] || isMetricsQLFunc(n.Func) {
				call = true
			}
		case *promql.AggregateExpr:
			call = true
		}
	})
	return shaped || (call && named)
}

// promqlCodeCandidate — SAF: çit / ters tırnak içeriği sorgu mu. Kod olduğu
// açık: ayrıştırılamasa da PromQL biçimi ya da MetricsQL işareti yeter.
func promqlCodeCandidate(content string) bool {
	c := strings.TrimSpace(content)
	if c == "" {
		return false
	}
	if e, err := promql.Parse(c); err == nil {
		return promqlStructural(e)
	}
	return metricsQLMarker(c, true) != "" || promqlShapeRe.MatchString(c)
}

// promqlPrecheck — SAF, ucuz: PromQL olabilecek karakter yoksa arama yok.
func promqlPrecheck(s string) bool {
	return strings.Contains(s, "`") || strings.ContainsAny(s, "{[") || (strings.Contains(s, "(") && strings.Contains(s, "_"))
}

// extractPastedPromQL — SAF: mesajdaki yapıştırılan sorgu. Sıra: kod çiti →
// ters tırnak → çitsiz MetricsQL (yalnız link için; işaret varsa önce) → çitsiz
// metin (sözcük sınırlarında, ayrıştırıcıdan geçen en erken başlayan en uzun parça).
func extractPastedPromQL(raw string) (pastedPromQL, bool) {
	if !promqlPrecheck(raw) {
		return pastedPromQL{}, false
	}
	var found []pastedPromQL
	var spans [][2]int
	for _, m := range promqlFenceRe.FindAllStringSubmatchIndex(raw, -1) {
		lang, body := raw[m[2]:m[3]], raw[m[4]:m[5]]
		content := body
		switch {
		case strings.TrimSpace(body) == "":
			content = lang
		case !promqlLangRe.MatchString(strings.TrimSpace(lang)):
			content = lang + "\n" + body
		}
		spans = append(spans, [2]int{m[0], m[1]})
		if promqlCodeCandidate(content) {
			found = append(found, pastedPromQL{Query: strings.TrimSpace(content), Fenced: true, Prose: cutSpan(raw, m[0], m[1])})
		}
	}
	for _, m := range promqlInlineRe.FindAllStringSubmatchIndex(raw, -1) {
		if insideSpans(m[0], spans) {
			continue
		}
		if content := raw[m[2]:m[3]]; promqlCodeCandidate(content) {
			found = append(found, pastedPromQL{Query: strings.TrimSpace(content), Fenced: true, Prose: cutSpan(raw, m[0], m[1])})
		}
	}
	if len(found) > 0 {
		p := found[0]
		p.Extra = len(found) - 1
		return p, true
	}
	if len(spans) > 0 || len(raw) > promqlDetectMaxLen {
		return pastedPromQL{}, false
	}
	// MetricsQL işareti varsa önce o: ayrıştırıcının kabul ettiği önek
	// ("rate(x[5m]) keep_metric_names" → "rate(x[5m])") sorgunun bir kısmını
	// sessizce düşürürdü.
	mq, ma, mb, mok := unfencedMetricsQL(raw)
	if mok {
		return pastedPromQL{Query: mq, Prose: cutSpan(raw, ma, mb)}, true
	}
	if q, a, b, ok := unfencedPromQL(raw); ok {
		return pastedPromQL{Query: q, Prose: cutSpan(raw, a, b)}, true
	}
	return pastedPromQL{}, false
}

// looksLikePromQL — SAF: mesaj yapıştırılmış bir PromQL/MetricsQL sorgusu taşıyor mu.
func looksLikePromQL(raw string) bool {
	_, ok := extractPastedPromQL(raw)
	return ok
}

func cutSpan(s string, a, b int) string {
	return strings.TrimSpace(s[:a] + " " + s[b:])
}

func insideSpans(i int, spans [][2]int) bool {
	for _, sp := range spans {
		if i >= sp[0] && i < sp[1] {
			return true
		}
	}
	return false
}

// promqlStartWord — sorgu bu sözcükle başlayabilir mi (ASCII ad/{/(/sayı/-).
func promqlStartWord(w string) bool {
	if w == "" {
		return false
	}
	c := w[0]
	return c == '_' || c == '{' || c == '(' || c == '-' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// promqlEndCuts — son sözcüğün olası bitişleri: sözcük sonu (sondaki .,;:!?
// atılır) ve son )/}/] karakterinden hemen sonrası ("…[5m]))'yi" → "…[5m]))").
func promqlEndCuts(raw string, a, b int) []int {
	out := []int{}
	e := b
	for e > a && strings.ContainsRune(".,;:!?", rune(raw[e-1])) {
		e--
	}
	if e > a {
		out = append(out, e)
	}
	if k := strings.LastIndexAny(raw[a:b], ")}]"); k >= 0 && a+k+1 != e {
		out = append(out, a+k+1)
	}
	return out
}

// unfencedPromQL — SAF: çitsiz metinde ayrıştırılan + biçimli parça.
func unfencedPromQL(raw string) (string, int, int, bool) {
	ws := promqlWordRe.FindAllStringIndex(raw, -1)
	n := len(ws)
	for si := 0; si < n && si < promqlDetectLeadWords; si++ {
		if !promqlStartWord(raw[ws[si][0]:ws[si][1]]) {
			continue
		}
		for ei := n - 1; ei >= si && ei >= n-promqlDetectTailWords; ei-- {
			for _, end := range promqlEndCuts(raw, ws[ei][0], ws[ei][1]) {
				if end <= ws[si][0] {
					continue
				}
				cand := raw[ws[si][0]:end]
				if !strings.ContainsAny(cand, "({[") {
					continue
				}
				if e, err := promql.Parse(cand); err == nil && promqlStructural(e) {
					return cand, ws[si][0], end, true
				}
			}
		}
	}
	return "", 0, 0, false
}

// promqlProseWord — SAF: düzyazı sözcüğü (ASCII dışı harf, ':' ile biten etiket
// ya da PromQL karakteri taşımayan, anahtar sözcük olmayan düz ad).
func promqlProseWord(w string, next string) bool {
	for _, r := range w {
		if r > 127 {
			return true
		}
	}
	if strings.HasSuffix(w, ":") {
		return true
	}
	if strings.ContainsAny(w, "(){}[]\"'`=~_") || strings.HasPrefix(next, "(") {
		return false
	}
	switch strings.ToLower(w) {
	case "by", "without", "on", "ignoring", "offset", "bool", "default", "if", "ifnot", "and", "or", "unless":
		return false
	}
	for _, r := range w {
		if r >= '0' && r <= '9' {
			return false
		}
	}
	return true
}

// unfencedMetricsQL — SAF: ayrıştırıcının reddettiği çitsiz MetricsQL
// (yalnız "Metrics'te aç" linki için); baş/son düzyazı sözcükleri atılır.
func unfencedMetricsQL(raw string) (string, int, int, bool) {
	if metricsQLMarker(raw, true) == "" {
		return "", 0, 0, false
	}
	ws := promqlWordRe.FindAllStringIndex(raw, -1)
	word := func(i int) string {
		if i < 0 || i >= len(ws) {
			return ""
		}
		return raw[ws[i][0]:ws[i][1]]
	}
	si, ei := 0, len(ws)-1
	for si <= ei && promqlProseWord(word(si), word(si+1)) {
		si++
	}
	for ei >= si && promqlProseWord(word(ei), "") {
		ei--
	}
	if si > ei {
		return "", 0, 0, false
	}
	a, b := ws[si][0], ws[ei][1]
	for b > a && strings.ContainsRune(".,;:!?", rune(raw[b-1])) {
		b--
	}
	cand := raw[a:b]
	if !balancedPromQL(cand) {
		return "", 0, 0, false
	}
	if metricsQLMarker(cand, false) == "" && !(metricsQLMarker(cand, true) != "" && promqlShapeRe.MatchString(cand)) {
		return "", 0, 0, false
	}
	return cand, a, b, true
}

// balancedPromQL — SAF: (), {}, [] dengeli mi (tırnak içi sayılmaz).
func balancedPromQL(s string) bool {
	var stack []byte
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote != '`' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '{', '[':
			stack = append(stack, c)
		case ')', '}', ']':
			open := map[byte]byte{')': '(', '}': '{', ']': '['}[c]
			if len(stack) == 0 || stack[len(stack)-1] != open {
				return false
			}
			stack = stack[:len(stack)-1]
		}
	}
	return quote == 0 && len(stack) == 0
}

// routePastedPromQL — SAF: yapıştırılan sorgu → metric_chart (ChartPromQL).
// Ortam yalnız sorgunun DIŞINDAKİ metinden çıkarılır (sorgudaki "prod" etiket
// değeri ortam anması değildir).
func routePastedPromQL(raw string, envs []string) (guidedRoute, bool) {
	p, ok := extractPastedPromQL(raw)
	if !ok {
		return guidedRoute{}, false
	}
	env := extractEnvEntity(normalizeGuidedMsg(p.Prose), envs)
	return guidedRoute{Intent: guidedMetricChart, ChartPromQL: p.Query, ChartPromQLExtra: p.Extra, Env: env, ChartEnvAsked: env != ""}, true
}

// pastedPromQLRangeS — SAF: pencere sorgunun DIŞINDAKİ metinden ("[1h]" ya
// da "jobs_per_hour" pencere sayılmaz); açık pencere yoksa 1 sa (D1).
func pastedPromQLRangeS(question string) (int64, bool) {
	text := question
	if p, ok := extractPastedPromQL(question); ok {
		text = p.Prose
	}
	if v, explicit := guidedRangeSExplicit(normalizeGuidedMsg(text)); explicit {
		return v, true
	}
	return metricChartDefaultRangeS, false
}

// promqlVetoesOpPrefetch — SAF: operasyon ön-getirme kademesi yapıştırılan
// sorguyu sahiplenmesin (http_route="/api/x" endpoint anması sanılırdı).
func promqlVetoesOpPrefetch(raw string) bool { return looksLikePromQL(raw) }

// ─── Takip / cevap metni (SAF) ─────────────────────────────────────────

// pastedPromQLFollowups — SAF: "Son 24 saate genişlet" (yalnız sorgu 24 sa
// adımıyla da geçerliyse — [5m] aralığı 12 dk adımda reddedilirdi) +
// "Metrics'te aç". Reddedilen sorguda pencere çipi yok.
func pastedPromQLFollowups(route guidedRoute, asked []string) []chatFollowup {
	var cands []chatFollowup
	if route.DirectAnswer == "" && route.ChartRangeS > 0 && route.ChartRangeS < 86400 {
		day := time.Duration(metricChartStepS(86400)) * time.Second
		if mr := promqlMinRange(route.ChartPromQL); mr == 0 || mr >= day {
			cands = append(cands, chatFollowup{Text: "Son 24 saate genişlet", Label: "Son 24 saate genişlet", Kind: fuKindMetricWindow})
		}
	}
	out := dedupFollowupsAsked(cands, asked, guidedFollowupMax)
	if href := strings.TrimSpace(route.ChartHref); href != "" && len(out) < guidedFollowupMax && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		out = append(out, chatFollowup{Text: "Metrics'te aç", Label: "Metrics'te aç", Kind: fuKindMetricLink, Href: href})
	}
	return out
}

// promqlDirectAnswerTR — SAF: ret / okuma hatası cevabı (anlatım çağrısı yok).
func promqlDirectAnswerTR(lead, reason string, metricsQL bool, href string) string {
	var b strings.Builder
	b.WriteString(lead + ": " + reason + ".")
	if metricsQL {
		b.WriteString(" Sohbet yalnız güvenli bir PromQL alt kümesini çalıştırır; MetricsQL sorgusunu PromQL editöründe VictoriaMetrics'e karşı koşabilirsiniz.")
	}
	if href != "" && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		b.WriteString("\n\n[Metrics'te aç](" + href + ") — sorgu PromQL editöründe aynı pencereyle açılır.")
	} else {
		b.WriteString("\n\n[Metrics kataloğu](/metrics) — sorgu link için çok uzun; editöre elle yapıştırın.")
	}
	return b.String()
}

// ─── Demet ──────────────────────────────────────────────────────────────

// metricChartRunPromQL — yapıştırılan sorgunun demeti (okuyucu dışarıdan: test dikişi).
func (s *Server) metricChartRunPromQL(ctx context.Context, emit func(string, any), route *guidedRoute, rd metricChartReader, isVM bool, from, to time.Time, rangeS int64, loc *time.Location) (string, string, error) {
	route.ChartRangeS, route.DirectAnswer = rangeS, ""
	q := strings.TrimSpace(route.ChartPromQL)
	argQ := q
	if len(argQ) > promqlStepArgMax {
		argQ = argQ[:promqlStepArgMax] + "…"
	}
	n := emitGuidedStep(emit, "metric_chart", withEnvArg(fmt.Sprintf(`{"promql":%s,"range_s":%d}`, jsonStr(argQ), rangeS), route.Env))
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(from))
	step := metricChartStepS(rangeS)
	href := mcptools.PromQLEditorHref(q, "vm", "", qFrom.UnixMilli(), qTo.UnixMilli())
	route.ChartHref = href
	head := fmt.Sprintf("METRİK GRAFİĞİ (yapıştırılan PromQL): son %s.\n", fmtAgoTR(rangeS))

	direct := func(lead, rule, reason string, metricsQL bool, srcTag string) (string, string, error) {
		text := head + fmt.Sprintf("Sorgu ÇALIŞTIRILMADI (kural %s): %s.\nPromQL: %s\n", rule, reason, q)
		route.DirectAnswer = promqlDirectAnswerTR(lead, reason, metricsQL, href)
		emitGuidedStepResult(emit, n, "metric_chart", text, nil)
		return text, "metric_chart (" + srcTag + ")", nil
	}
	const leadReject = "Yapıştırdığınız sorguyu sohbette çalıştırmadım"
	if !isVM || rd == nil {
		return direct(leadReject, "vm", "VictoriaMetrics yapılandırılmamış — yapıştırılan PromQL yalnız VictoriaMetrics'e karşı çalışır", false, "VM yok")
	}
	chk := validateChartPromQL(q, step)
	if !chk.OK() {
		return direct(leadReject, chk.Rule, chk.Reason, chk.MetricsQL, "PromQL reddedildi")
	}

	// Varlık: her metrik adı VM'de (son 24 sa etiket kümesi; 5 dk önbellek).
	sets, missing, readErr := s.promqlMetricLabelSets(ctx, rd, chk.Metrics, qTo)
	if readErr != nil {
		return direct("Sorguyu çalıştırmadım", "read", "VictoriaMetrics etiket okuması BAŞARISIZ (okunamadı ≠ yok): "+readErr.Error(), false, "VictoriaMetrics okunamadı")
	}
	if len(missing) > 0 {
		return direct(leadReject, pqRuleMissing, "VictoriaMetrics'te bulunamayan metrik (son 24 sa): "+strings.Join(missing, ", "), false, "metrik yok")
	}

	// Ortam (D8 genelleştirmesi): soruda anılan ortam sorgulanan metriğin etiketine.
	var notes []string
	var add []*promql.LabelMatcher
	envMatcher := ""
	if route.Env != "" {
		if !route.ChartEnvAsked {
			notes = append(notes, fmt.Sprintf("ekrandaki ortam %q süzgeci UYGULANMADI — yapıştırılan sorgu olduğu gibi koştu (tüm ortamlar, sorgunun kendi süzgeçleri)", route.Env))
		} else {
			sc, err := s.resolveMetricEnvScope(ctx, rd, chk.Metrics[0], map[string]string{vmetrics.RoleEnv: route.Env}, qTo)
			reason := ""
			switch {
			case err != nil:
				reason = "ortam etiketi okunamadı: " + err.Error()
			case !sc.Applied:
				reason = sc.Reason
			case promqlHasLabelMatcher(chk.Expr, sc.Label):
				notes = append(notes, fmt.Sprintf("sorgu %s etiketini zaten süzüyor — sorudaki ortam %q EKLENMEDİ", sc.Label, route.Env))
			default:
				for _, m := range chk.Metrics {
					if !sets[m][sc.Label] {
						reason = fmt.Sprintf("%s metriği %s etiketini taşımıyor", m, sc.Label)
						break
					}
				}
				if reason == "" {
					if add, err = promqlMatchersFrom(sc.Matcher); err != nil {
						reason = err.Error()
					} else {
						envMatcher = sc.Matcher
					}
				}
			}
			if reason != "" {
				return direct("Sorguyu çalıştırmadım", "env", fmt.Sprintf("ortam süzgeci %q uygulanamadı (%s) — tüm ortamların grafiği yanlış cevap olurdu", route.Env, reason), false, "ortam çözülemedi")
			}
		}
	}
	if route.ChartPromQLExtra > 0 {
		notes = append(notes, fmt.Sprintf("mesajda %d sorgu daha vardı; sohbet TEK sorgu çalıştırır — yalnız ilki çizildi", route.ChartPromQLExtra))
	}
	exec, err := promqlExecQuery(chk, add)
	if err != nil {
		return direct(leadReject, pqRuleParse, "sorgu yeniden yazılamadı: "+err.Error(), false, "PromQL reddedildi")
	}
	if exec != q {
		href = mcptools.PromQLEditorHref(exec, "vm", "", qFrom.UnixMilli(), qTo.UnixMilli())
		route.ChartHref = href
	}

	key := metricChartCacheKey(metricSourceVM, promqlTemplateID, exec, "", envMatcher, "", qFrom, qTo, step)
	var res metricChartCached
	if s.metricChartCacheGet(ctx, key, &res) {
		emitGuidedContextStep(emit, "önbellek: aynı PromQL grafiği (≤60 sn)")
	} else {
		rec := &vmetrics.QueryRecorder{}
		qctx, cancel := context.WithTimeout(vmetrics.WithQueryRecorder(ctx, rec), metricChartQueryTimeout)
		start := time.Now()
		raw, err := rd.QueryPromQLRange(qctx, exec, qFrom, qTo, step, metricChartMaxPoints)
		cancel()
		if err != nil {
			text := head + "PromQL: " + exec + "\nVictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): " + err.Error() + "\n"
			route.DirectAnswer = promqlDirectAnswerTR("VictoriaMetrics sorguyu çalıştıramadı", err.Error(), false, href)
			emitGuidedStepResult(emit, n, "metric_chart", text, err)
			return text, "VictoriaMetrics (okunamadı)", nil
		}
		res.Series, res.Truncated = metricChartSeries(raw, "", "sorgu")
		res.DurationMs = time.Since(start).Milliseconds()
		res.Query = exec
		if qs := rec.Queries(); len(qs) > 0 {
			res.Query = qs[len(qs)-1]
		}
		s.metricChartCacheSet(ctx, key, res, metricChartCacheTTL)
	}

	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "Kaynak: VictoriaMetrics, adım %ds, %d seri", step, len(res.Series))
	if res.Truncated {
		fmt.Fprintf(&b, " (tavan: ≤%d seri / ≤%d nokta uygulandı)", metricChartMaxSeries, metricChartMaxPoints)
	}
	b.WriteString(".\nPromQL (operatörün yapıştırdığı, doğrulandı): " + q + "\n")
	if exec != q {
		b.WriteString("VM'e giden ifade: " + exec + "\n")
	}
	if chk.NeedsWrap {
		fmt.Fprintf(&b, "Not: dış ifade toplama değil — topk(%d, …) ile sarıldı (seri tavanı).\n", metricChartMaxSeries)
	}
	if envMatcher != "" {
		b.WriteString("Ortam süzgeci sorgunun her seçicisine " + envMatcher + " olarak eklendi.\n")
	}
	for _, nt := range notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	b.WriteString("Birim bilinmiyor (yapıştırılan sorgu) — değerler ham sayı.\n")
	if len(res.Series) == 0 {
		b.WriteString("Bu pencerede SERİ YOK (metrikler son 24 sa'te var ama bu sorgu + pencerede veri dönmedi) — grafik YOK; sorguyu Metrics'te açıp pencereyi/süzgeci değiştirin.\n")
	} else {
		b.WriteString("Grafik cevabın altında kanıt çipinde çizildi (sunucu verisi; grafik bloğu YAZMA).\n")
		b.WriteString(metricChartStatsTR(res.Series, "", loc))
	}
	text := b.String()

	nSeries := len(res.Series)
	if res.Query == "" {
		res.Query = exec
	}
	ev := mcptools.ChatEvidence{Tool: "metric_chart", Source: "VictoriaMetrics", Query: res.Query, QueryKind: "promql",
		Window: mcptools.EvidenceWindow{FromMs: qFrom.UnixMilli(), ToMs: qTo.UnixMilli()}, Series: &nSeries,
		DurationMs: res.DurationMs, Href: href}
	if envMatcher != "" {
		ev.Env = route.Env
	}
	chart := &mcptools.EvChartPayload{Title: "Yapıştırılan PromQL", DeepLink: href,
		Window: mcptools.EvBlockWindow{FromNs: qFrom.UnixNano(), ToNs: qTo.UnixNano()}, Series: res.Series, Truncated: res.Truncated}
	emitGuidedEvidenceResult(emit, n, "metric_chart", text, ev, chart)
	return text, "VictoriaMetrics · metric_chart/promql", nil
}

// promqlMetricLabelSets — her metrik adının etiket kümesi (metricChartLabelSet:
// 2 sn, 5 dk önbellek; ≤4 paralel). Boş küme = metrik yok. İlk okuma hatası döner.
func (s *Server) promqlMetricLabelSets(ctx context.Context, rd metricChartReader, names []string, to time.Time) (map[string]map[string]bool, []string, error) {
	sets := make(map[string]map[string]bool, len(names))
	errs := make([]error, len(names))
	got := make([]map[string]bool, len(names))
	sem := make(chan struct{}, promqlExistParallel)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			got[i], errs[i] = s.metricChartLabelSet(ctx, rd, name, to)
		}(i, name)
	}
	wg.Wait()
	var missing []string
	for i, name := range names {
		if errs[i] != nil {
			return nil, nil, errs[i]
		}
		sets[name] = got[i]
		if len(got[i]) == 0 {
			missing = append(missing, name)
		}
	}
	return sets, missing, nil
}
