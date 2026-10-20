package api

// metric_chart_validate.go — v0.10.1416 (CoSRE metrik grafiği R2; operatör:
// "VictoriaMetrics sorgulayıp grafik getirse"). Sohbete YAPIŞTIRILAN PromQL'in
// çalıştırılmadan önceki SAF doğrulayıcısı (AST yürüyüşü, internal/promql
// ayrıştırıcısı — yeni bağımlılık yok). Tablo testli
// (metric_chart_validate_test.go).
//
// Neden izin listesi: sorgu operatörden gelir ama VM'e Coremetry'nin kimliğiyle
// gider; sohbetin bütçesi tek sınırlı range sorgusudur (8 sn, ≤20 seri, ≤120
// nokta). Kural ihlali sorguyu ÇALIŞTIRMAZ; cevap gerekçeyi Türkçe söyler ve
// "Metrics'te aç" linkini (PromQL editörü, aynı pencere) yine verir — editör
// kendi tavanlarıyla her şeyi koşabilir.
//
// Kurallar: uzunluk ≤2048; ayrıştırılabilir; MetricsQL'e özgü yapı (WITH,
// keep_metric_names, rollup_*, aralıksız rate(x), [Ni]) çalıştırılmaz (yalnız
// link); alt sorgu yok; aralık [adım, 1 sa]; offset 0..7 gün; @ yok; ≤6 seçici,
// ≤10 farklı metrik adı; her seçicide sabit metrik adı ya da ≤10 sabitlik
// __name__ alternasyonu; etiket regex'i ≤128 karakter, çıplak .*/.+ değil,
// derlenebilir; fonksiyon / toplama izin listesi (topk/bottomk k ≤20); ikili
// işleçler serbest. Varlık kontrolü (her metrik adı VM'de var mı) okuma ister —
// demette (copilot_metric_chart_promql.go), bu dosya SAF kalır.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/promql"
)

// v0.10.1416 — doğrulayıcı tavanları.
const (
	promqlChartMaxLen       = 2048
	promqlChartMaxSelectors = 6
	promqlChartMaxNames     = 10
	promqlChartMaxAlt       = 10
	promqlChartMaxRegex     = 128
	promqlChartMaxRange     = time.Hour
	promqlChartMaxOffset    = 7 * 24 * time.Hour
	promqlChartMaxK         = 20
)

// Doğrulayıcı kural kimlikleri (testler ve kanıt metni okur).
const (
	pqRuleLen       = "len"
	pqRuleParse     = "parse"
	pqRuleMetricsQL = "metricsql"
	pqRuleSubquery  = "subquery"
	pqRuleRange     = "range"
	pqRuleOffset    = "offset"
	pqRuleAt        = "at"
	pqRuleSelectors = "selectors"
	pqRuleName      = "name"
	pqRuleRegex     = "regex"
	pqRuleFunc      = "func"
	pqRuleAgg       = "agg"
	pqRuleK         = "k"
	pqRuleMissing   = "metric_missing"
)

// promqlRangeFuncs — aralık vektörü alan izinli fonksiyonlar (tek argüman).
var promqlRangeFuncs = map[string]bool{
	"rate": true, "irate": true, "increase": true, "delta": true,
	"avg_over_time": true, "max_over_time": true, "min_over_time": true,
	"sum_over_time": true, "last_over_time": true,
}

// promqlVectorFuncs — anlık vektör alan izinli fonksiyonlar: ad → (en az, en
// çok) argüman. İkinci argüman (round/clamp_*) sayı sabiti olmalı.
var promqlVectorFuncs = map[string][2]int{
	"histogram_quantile": {2, 2},
	"abs":                {1, 1}, "ceil": {1, 1}, "floor": {1, 1},
	"round":     {1, 2},
	"clamp_min": {2, 2}, "clamp_max": {2, 2},
}

// promqlAggAllow — izinli toplamalar.
var promqlAggAllow = map[string]bool{
	"sum": true, "avg": true, "min": true, "max": true, "count": true,
	"topk": true, "bottomk": true, "quantile": true,
}

// promqlDenyReasons — AÇIKÇA reddedilen fonksiyon/toplamalar ve gerekçeleri.
var promqlDenyReasons = map[string]string{
	"label_replace":    "etiket yeniden yazma (label_replace/label_join) sohbette çalıştırılmaz",
	"label_join":       "etiket yeniden yazma (label_replace/label_join) sohbette çalıştırılmaz",
	"absent":           "absent/absent_over_time yokluk sorgusudur, grafiğe uygun değil",
	"absent_over_time": "absent/absent_over_time yokluk sorgusudur, grafiğe uygun değil",
	"sort":             "sıralama (sort/sort_desc) range grafiğinde anlamsız",
	"sort_desc":        "sıralama (sort/sort_desc) range grafiğinde anlamsız",
	"sort_by_label":    "sıralama (sort/sort_desc) range grafiğinde anlamsız",
	"vector":           "vector/scalar/time metrik okumaz — grafik için seçici gerekir",
	"scalar":           "vector/scalar/time metrik okumaz — grafik için seçici gerekir",
	"time":             "vector/scalar/time metrik okumaz — grafik için seçici gerekir",
	"predict_linear":   "tahmin fonksiyonları (predict_linear/holt_winters) sohbette çalıştırılmaz",
	"holt_winters":     "tahmin fonksiyonları (predict_linear/holt_winters) sohbette çalıştırılmaz",
	"count_values":     "count_values değer başına seri üretir (sınırsız kardinalite)",
	"group":            "group toplaması grafiğe değer taşımaz",
	"stddev":           "stddev/stdvar sohbetin izin listesinde değil",
	"stdvar":           "stddev/stdvar sohbetin izin listesinde değil",
	"stddev_over_time": "stddev/stdvar sohbetin izin listesinde değil",
	"stdvar_over_time": "stddev/stdvar sohbetin izin listesinde değil",
}

// promqlMetricsQLFuncs — yalnız MetricsQL'de olan (PromQL'de olmayan) yaygın
// fonksiyonlar; önekler promqlMetricsQLFuncPrefixes'te.
var promqlMetricsQLFuncs = map[string]bool{
	"histogram_quantiles": true, "histogram_share": true, "histogram_over_time": true,
	"label_set": true, "label_del": true, "label_keep": true, "label_copy": true, "label_move": true,
	"label_transform": true, "label_value": true, "label_match": true, "label_mismatch": true,
	"keep_last_value": true, "keep_next_value": true, "interpolate": true, "union": true, "alias": true,
	"limitk": true, "outliersk": true, "outliers_mad": true, "median": true, "mode": true, "any": true,
	"share": true, "zscore": true, "mad": true, "quantiles": true, "distinct": true,
	"distinct_over_time": true, "median_over_time": true, "mode_over_time": true,
	"quantiles_over_time": true, "tmax_over_time": true, "tmin_over_time": true,
	"tfirst_over_time": true, "tlast_over_time": true, "tlast_change_over_time": true,
	"increase_pure": true, "increase_prometheus": true, "delta_prometheus": true, "changes_prometheus": true,
	"rate_over_sum": true, "aggr_over_time": true, "count_eq_over_time": true, "count_gt_over_time": true,
	"count_le_over_time": true, "count_ne_over_time": true, "share_gt_over_time": true,
	"share_le_over_time": true, "share_eq_over_time": true, "sum2_over_time": true, "geomean_over_time": true,
	"lag": true, "lifetime": true, "ideriv": true, "integrate": true, "duration_over_time": true,
	"ascent_over_time": true, "descent_over_time": true, "increases_over_time": true,
	"decreases_over_time": true, "deriv_fast": true, "default_rollup": true, "ru": true, "ttf": true,
}

var promqlMetricsQLFuncPrefixes = []string{"rollup", "range_", "running_", "hoeffding_bound_"}

// promqlStrongMetricsQLRe — ayrıştırıcıdan ÖNCE metinde aranan, PromQL'de
// karşılığı olmayan MetricsQL yapıları: WITH şablonu, keep_metric_names,
// rollup_* çağrısı, adım katı aralık ([5i]).
var promqlStrongMetricsQLRe = regexp.MustCompile(`(?i)\bwith\s*\(|\bkeep_metric_names\b|\brollup[a-z_]*\s*\(|\[\s*\d+i\s*(:[^\]]*)?\]`)

// promqlWeakMetricsQLRe — MetricsQL'e özgü fonksiyon adı + "(" (ad kümesi
// promqlMetricsQLFuncs; düz İngilizce sözcükler — any/union/mode… — tek başına
// MetricsQL sayılmaz, yanında PromQL biçimi ister).
var promqlWeakMetricsQLRe = func() *regexp.Regexp {
	names := make([]string, 0, len(promqlMetricsQLFuncs))
	for n := range promqlMetricsQLFuncs {
		names = append(names, regexp.QuoteMeta(n))
	}
	sort.Strings(names)
	return regexp.MustCompile(`(?i)\b(` + strings.Join(names, "|") + `|(range_|running_|hoeffding_bound_)[a-z_]+)\s*\(`)
}()

// promqlMetricNameRe — tek bir sabit metrik adı (alternasyon üyesi).
var promqlMetricNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:]*$`)

// metricsQLMarker — SAF: metindeki MetricsQL'e özgü yapı ("" = yok). Güçlü
// işaretler tek başına; zayıf (fonksiyon adı) işaret çağıran isterse.
func metricsQLMarker(s string, weak bool) string {
	if m := promqlStrongMetricsQLRe.FindString(s); m != "" {
		return strings.TrimSpace(strings.TrimRight(m, "( "))
	}
	if weak {
		if m := promqlWeakMetricsQLRe.FindString(s); m != "" {
			return strings.TrimSpace(strings.TrimRight(m, "( "))
		}
	}
	return ""
}

func isMetricsQLFunc(name string) bool {
	if promqlMetricsQLFuncs[name] {
		return true
	}
	for _, p := range promqlMetricsQLFuncPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// promqlCheck — doğrulama sonucu. Rule == "" → çalıştırılabilir (varlık
// kontrolü demette ayrıca).
type promqlCheck struct {
	Query     string
	Expr      promql.Expr
	Rule      string
	Reason    string
	MetricsQL bool     // MetricsQL'e özgü: çalıştırılmaz, yalnız link
	Metrics   []string // farklı sabit metrik adları (ilk görülme sırası)
	NeedsWrap bool     // dış ifade toplama değil → topk(20, …) ile sarılır
	MinRange  time.Duration
	Selectors int
}

func (c promqlCheck) OK() bool { return c.Rule == "" }

// pqWalker — AST yürüyüşünün durumu.
type pqWalker struct {
	step     time.Duration
	chk      *promqlCheck
	seen     map[string]bool
	selCount int
}

func (w *pqWalker) fail(rule, format string, a ...any) bool {
	if w.chk.Rule == "" {
		w.chk.Rule, w.chk.Reason = rule, fmt.Sprintf(format, a...)
		if rule == pqRuleMetricsQL {
			w.chk.MetricsQL = true
		}
	}
	return false
}

func (w *pqWalker) addName(n string) {
	if !w.seen[n] {
		w.seen[n] = true
		w.chk.Metrics = append(w.chk.Metrics, n)
	}
}

// validateChartPromQL — SAF: yapıştırılan sorgu + grafik adımı (sn) → karar.
func validateChartPromQL(query string, stepS int) promqlCheck {
	q := strings.TrimSpace(query)
	chk := promqlCheck{Query: q}
	w := &pqWalker{step: time.Duration(stepS) * time.Second, chk: &chk, seen: map[string]bool{}}
	if q == "" {
		w.fail(pqRuleParse, "sorgu boş")
		return chk
	}
	if len(q) > promqlChartMaxLen {
		w.fail(pqRuleLen, "sorgu %d karakter (sohbet tavanı %d)", len(q), promqlChartMaxLen)
		return chk
	}
	if m := metricsQLMarker(q, false); m != "" {
		w.fail(pqRuleMetricsQL, "MetricsQL'e özgü yapı (%s) — sohbet yalnız PromQL alt kümesini çalıştırır", m)
		return chk
	}
	e, err := promql.Parse(q)
	if err != nil {
		if m := metricsQLMarker(q, true); m != "" {
			w.fail(pqRuleMetricsQL, "MetricsQL'e özgü yapı (%s) — sohbet yalnız PromQL alt kümesini çalıştırır", m)
			return chk
		}
		w.fail(pqRuleParse, "PromQL ayrıştırılamadı: %s", strings.TrimPrefix(err.Error(), "promql: "))
		return chk
	}
	chk.Expr = e
	w.walk(e, false)
	chk.Selectors = w.selCount
	if chk.Rule != "" {
		return chk
	}
	switch {
	case w.selCount == 0:
		w.fail(pqRuleSelectors, "sorguda metrik seçicisi yok (sabit ifade grafiğe değer taşımaz)")
	case w.selCount > promqlChartMaxSelectors:
		w.fail(pqRuleSelectors, "%d seçici var (sohbet tavanı %d)", w.selCount, promqlChartMaxSelectors)
	case len(chk.Metrics) > promqlChartMaxNames:
		w.fail(pqRuleSelectors, "%d farklı metrik adı var (sohbet tavanı %d)", len(chk.Metrics), promqlChartMaxNames)
	}
	if chk.Rule != "" {
		return chk
	}
	_, isAgg := unparen(e).(*promql.AggregateExpr)
	chk.NeedsWrap = !isAgg
	return chk
}

// unparen — SAF: dıştaki (…) katmanlarını soyar.
func unparen(e promql.Expr) promql.Expr {
	for {
		p, ok := e.(*promql.ParenExpr)
		if !ok {
			return e
		}
		e = p.Expr
	}
}

// walk — ilk ihlalde durur (false). inRange: düğüm bir aralık fonksiyonunun
// doğrudan argümanı (MatrixSelector yalnız orada geçerli).
func (w *pqWalker) walk(e promql.Expr, inRange bool) bool {
	switch n := e.(type) {
	case *promql.NumberLiteral:
		return true
	case *promql.StringLiteral:
		return w.fail(pqRuleFunc, "metin argümanı (%s) izinli fonksiyonların hiçbirinde geçmez", n.String())
	case *promql.ParenExpr:
		return w.walk(n.Expr, false)
	case *promql.UnaryExpr:
		return w.walk(n.Expr, false)
	case *promql.BinaryExpr:
		return w.walk(n.LHS, false) && w.walk(n.RHS, false)
	case *promql.SubqueryExpr:
		return w.fail(pqRuleSubquery, "alt sorgu (%s) sohbette çalıştırılmaz (maliyet katlanır)", "[aralık:adım]")
	case *promql.MatrixSelector:
		if !inRange {
			return w.fail(pqRuleRange, "aralık vektörü [%s] yalnız rate/increase/…_over_time içinde grafiğe dönüşür", fmtPromDur(n.Range))
		}
		if n.Range > promqlChartMaxRange {
			return w.fail(pqRuleRange, "aralık [%s] sohbet tavanı 1 saati aşıyor", fmtPromDur(n.Range))
		}
		if n.Range < w.step {
			return w.fail(pqRuleRange, "aralık [%s] grafik adımından (%s) kısa — noktalar arasında veri atlanır; aralığı en az [%s] yapın ya da pencereyi daraltın",
				fmtPromDur(n.Range), fmtPromDur(w.step), fmtPromDur(w.step))
		}
		if w.chk.MinRange == 0 || n.Range < w.chk.MinRange {
			w.chk.MinRange = n.Range
		}
		return w.selector(n.VectorSelector)
	case *promql.VectorSelector:
		return w.selector(n)
	case *promql.Call:
		return w.call(n)
	case *promql.AggregateExpr:
		return w.aggregate(n)
	}
	return w.fail(pqRuleParse, "tanınmayan ifade %T", e)
}

func (w *pqWalker) selector(v *promql.VectorSelector) bool {
	w.selCount++
	if v.At != nil || v.AtStart || v.AtEnd {
		return w.fail(pqRuleAt, "@ değiştiricisi sohbette çalıştırılmaz (grafik penceresi sabitlenemez)")
	}
	if v.Offset < 0 {
		return w.fail(pqRuleOffset, "negatif offset (geleceğe kaydırma) çalıştırılmaz")
	}
	if v.Offset > promqlChartMaxOffset {
		return w.fail(pqRuleOffset, "offset %s sohbet tavanı 7 günü aşıyor", fmtPromDur(v.Offset))
	}
	if v.Name != "" {
		w.addName(v.Name)
	}
	for _, m := range v.Matchers {
		if m.Name == "__name__" {
			if v.Name != "" || m.Type != promql.MatchRegexp {
				return w.fail(pqRuleName, "__name__%s%q — metrik adı sabit ad ya da sabit adların alternasyonu olmalı", m.Type.String(), m.Value)
			}
			alts, ok := promqlNameAlternation(m.Value)
			if !ok {
				return w.fail(pqRuleName, "__name__=~%q sabit adların alternasyonu değil (regex metrik adı çalıştırılmaz)", m.Value)
			}
			if len(alts) > promqlChartMaxAlt {
				return w.fail(pqRuleName, "__name__ alternasyonunda %d ad var (sohbet tavanı %d)", len(alts), promqlChartMaxAlt)
			}
			for _, a := range alts {
				w.addName(a)
			}
			continue
		}
		if m.Type == promql.MatchRegexp || m.Type == promql.MatchNotRegexp {
			if len(m.Value) > promqlChartMaxRegex {
				return w.fail(pqRuleRegex, "%s etiket regex'i %d karakter (sohbet tavanı %d)", m.Name, len(m.Value), promqlChartMaxRegex)
			}
			if promqlBareWildcard(m.Value) {
				return w.fail(pqRuleRegex, "%s%s%q süzgeç değil (çıplak .*/.+) — etiketi kaldırın ya da değer yazın", m.Name, m.Type.String(), m.Value)
			}
			if _, err := regexp.Compile("^(?:" + m.Value + ")$"); err != nil {
				return w.fail(pqRuleRegex, "%s etiket regex'i derlenemedi: %v", m.Name, err)
			}
		}
	}
	if v.Name == "" && !hasNameMatcher(v) {
		return w.fail(pqRuleName, "seçicide metrik adı yok (%s) — sabit metrik adı yazın", v.String())
	}
	return true
}

func hasNameMatcher(v *promql.VectorSelector) bool {
	for _, m := range v.Matchers {
		if m.Name == "__name__" {
			return true
		}
	}
	return false
}

// promqlNameAlternation — SAF: `a|b|c`, `(a|b)`, `^(a|b)$` → sabit adlar.
func promqlNameAlternation(v string) ([]string, bool) {
	s := strings.TrimSpace(v)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "^"), "$")
	if strings.HasPrefix(s, "(?:") && strings.HasSuffix(s, ")") {
		s = s[3 : len(s)-1]
	} else if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, "|")
	for _, p := range parts {
		if !promqlMetricNameRe.MatchString(p) || strings.Contains(p, ".") {
			return nil, false
		}
	}
	return parts, true
}

// promqlBareWildcard — SAF: `.*`, `.+`, `^.*$`, `(.*)`… süzgeç değildir.
func promqlBareWildcard(v string) bool {
	s := strings.TrimSpace(v)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "^"), "$")
	for strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = strings.TrimPrefix(s[1:len(s)-1], "?:")
	}
	return s == ".*" || s == ".+" || s == ""
}

func (w *pqWalker) call(c *promql.Call) bool {
	name := c.Func
	if r, ok := promqlDenyReasons[name]; ok {
		return w.fail(pqRuleFunc, "%s: %s", name, r)
	}
	if isMetricsQLFunc(name) {
		return w.fail(pqRuleMetricsQL, "MetricsQL'e özgü fonksiyon (%s) — sohbet yalnız PromQL alt kümesini çalıştırır", name)
	}
	if promqlRangeFuncs[name] {
		if len(c.Args) != 1 {
			return w.fail(pqRuleFunc, "%s tek argüman alır (%d verildi)", name, len(c.Args))
		}
		if _, ok := c.Args[0].(*promql.SubqueryExpr); ok {
			return w.walk(c.Args[0], false) // alt sorgu kuralı söylesin
		}
		if _, ok := c.Args[0].(*promql.VectorSelector); ok {
			return w.fail(pqRuleMetricsQL, "aralıksız %s(…) MetricsQL kısaltmasıdır — PromQL'de %s(metrik[5m]) yazılır", name, name)
		}
		if _, ok := c.Args[0].(*promql.MatrixSelector); !ok {
			return w.fail(pqRuleFunc, "%s argümanı bir aralık seçicisi olmalı (metrik{…}[5m])", name)
		}
		return w.walk(c.Args[0], true)
	}
	ar, ok := promqlVectorFuncs[name]
	if !ok {
		return w.fail(pqRuleFunc, "%s sohbetin fonksiyon izin listesinde değil", name)
	}
	if len(c.Args) < ar[0] || len(c.Args) > ar[1] {
		return w.fail(pqRuleFunc, "%s argüman sayısı %d (beklenen %d..%d)", name, len(c.Args), ar[0], ar[1])
	}
	switch name {
	case "histogram_quantile":
		if !numberIn(c.Args[0], 0, 1) {
			return w.fail(pqRuleFunc, "histogram_quantile ilk argümanı 0..1 arası sayı sabiti olmalı")
		}
		return w.walk(c.Args[1], false)
	case "round", "clamp_min", "clamp_max":
		if len(c.Args) == 2 {
			if _, ok := unparen(c.Args[1]).(*promql.NumberLiteral); !ok {
				return w.fail(pqRuleFunc, "%s ikinci argümanı sayı sabiti olmalı", name)
			}
		}
	}
	return w.walk(c.Args[0], false)
}

func (w *pqWalker) aggregate(a *promql.AggregateExpr) bool {
	if r, ok := promqlDenyReasons[a.Op]; ok {
		return w.fail(pqRuleAgg, "%s: %s", a.Op, r)
	}
	if !promqlAggAllow[a.Op] {
		return w.fail(pqRuleAgg, "%s toplaması sohbetin izin listesinde değil", a.Op)
	}
	switch a.Op {
	case "topk", "bottomk":
		n, ok := unparen(a.Param).(*promql.NumberLiteral)
		if a.Param == nil || !ok || n.Val != float64(int(n.Val)) || n.Val < 1 {
			return w.fail(pqRuleK, "%s k pozitif tam sayı sabiti olmalı", a.Op)
		}
		if n.Val > promqlChartMaxK {
			return w.fail(pqRuleK, "%s k=%d sohbet seri tavanını (%d) aşıyor", a.Op, int(n.Val), promqlChartMaxK)
		}
	case "quantile":
		if a.Param == nil || !numberIn(a.Param, 0, 1) {
			return w.fail(pqRuleAgg, "quantile φ 0..1 arası sayı sabiti olmalı")
		}
	}
	return w.walk(a.Expr, false)
}

func numberIn(e promql.Expr, lo, hi float64) bool {
	n, ok := unparen(e).(*promql.NumberLiteral)
	return ok && n.Val >= lo && n.Val <= hi
}

// fmtPromDur — SAF: süre → PromQL biçimi (5m, 1h30m); 0 → 0s.
func fmtPromDur(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	var b strings.Builder
	for _, u := range []struct {
		s string
		d time.Duration
	}{{"d", 24 * time.Hour}, {"h", time.Hour}, {"m", time.Minute}, {"s", time.Second}} {
		if d >= u.d {
			fmt.Fprintf(&b, "%d%s", d/u.d, u.s)
			d %= u.d
		}
	}
	if b.Len() == 0 {
		return fmt.Sprintf("%dms", d/time.Millisecond)
	}
	return b.String()
}

// ─── Yürütülecek ifade (SAF) ────────────────────────────────────────────

// promqlVisit — SAF: ağacın her düğümünü (önce ebeveyn) fn'e verir.
func promqlVisit(e promql.Expr, fn func(promql.Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch n := e.(type) {
	case *promql.MatrixSelector:
		promqlVisit(n.VectorSelector, fn)
	case *promql.ParenExpr:
		promqlVisit(n.Expr, fn)
	case *promql.UnaryExpr:
		promqlVisit(n.Expr, fn)
	case *promql.BinaryExpr:
		promqlVisit(n.LHS, fn)
		promqlVisit(n.RHS, fn)
	case *promql.Call:
		for _, a := range n.Args {
			promqlVisit(a, fn)
		}
	case *promql.AggregateExpr:
		promqlVisit(n.Expr, fn)
	case *promql.SubqueryExpr:
		promqlVisit(n.Expr, fn)
	}
}

// promqlInjectMatchers — SAF: her seçiciye ek eşleştirici(ler) (ortam
// süzgeci). AST yerinde değişir; çağıran String() ile yeniden yazar.
func promqlInjectMatchers(e promql.Expr, add []*promql.LabelMatcher) {
	promqlVisit(e, func(x promql.Expr) {
		if v, ok := x.(*promql.VectorSelector); ok {
			for _, m := range add {
				cp := *m
				v.Matchers = append(v.Matchers, &cp)
			}
		}
	})
}

// promqlHasLabelMatcher — SAF: herhangi bir seçici `label` üzerinde süzüyor mu.
func promqlHasLabelMatcher(e promql.Expr, label string) bool {
	found := false
	promqlVisit(e, func(x promql.Expr) {
		if v, ok := x.(*promql.VectorSelector); ok {
			for _, m := range v.Matchers {
				if m.Name == label {
					found = true
				}
			}
		}
	})
	return found
}

// promqlMatchersFrom — SAF: `label="v"` / `a=~"x|y", b="z"` metni → eşleştiriciler.
func promqlMatchersFrom(matcher string) ([]*promql.LabelMatcher, error) {
	e, err := promql.Parse("{" + matcher + "}")
	if err != nil {
		return nil, err
	}
	vs, ok := e.(*promql.VectorSelector)
	if !ok || vs.Name != "" || len(vs.Matchers) == 0 {
		return nil, fmt.Errorf("eşleştirici çözülemedi: %s", matcher)
	}
	return vs.Matchers, nil
}

// promqlExecQuery — SAF: doğrulanmış sorgu → VM'e gidecek TEK ifade. Ek
// eşleştirici yoksa operatörün metni AYNEN kullanılır (yeniden yazım yok); dış
// ifade toplama değilse topk(20, …) ile sarılır (D3: ≤20 seri).
func promqlExecQuery(chk promqlCheck, add []*promql.LabelMatcher) (string, error) {
	q := chk.Query
	if len(add) > 0 {
		e, err := promql.Parse(q) // taze ağaç: chk.Expr'e dokunulmaz
		if err != nil {
			return "", err
		}
		promqlInjectMatchers(e, add)
		q = e.String()
	}
	if chk.NeedsWrap {
		q = fmt.Sprintf("topk(%d, %s)", metricChartMaxSeries, q)
	}
	return q, nil
}

// promqlMinRange — SAF: sorgudaki en kısa aralık (yoksa 0; ayrıştırılamazsa -1).
func promqlMinRange(query string) time.Duration {
	e, err := promql.Parse(strings.TrimSpace(query))
	if err != nil {
		return -1
	}
	var min time.Duration
	promqlVisit(e, func(x promql.Expr) {
		if m, ok := x.(*promql.MatrixSelector); ok && (min == 0 || m.Range < min) {
			min = m.Range
		}
	})
	return min
}
