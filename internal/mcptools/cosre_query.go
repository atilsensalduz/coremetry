package mcptools

// cosre_query.go — v0.10.1271 (CoSRE adım 2/8): query_metrics / query_traces /
// query_logs'un ORTAK katmanı.
//
// İlke: "sunucu deterministik toplar, model anlatır". Model PromQL/SQL/ES DSL
// YAZMAZ; küçük bir yapısal istek (enum'lar + birkaç düz alan) gönderir, sunucu
// doğrular, bozuk argümanı kurtarır (copilot_nl_query deseni: bilinmeyen enum →
// varsayılan + `warnings` notu), sorguyu SABİT şablonlardan kurar ve mevcut
// sınırlı okuyuculara verir. Hedef modeller küçük (qwen3.5-2b, gemma4-26b-a4b):
// şemalar dar, enum'lar serbest metinden önce.
//
// Tek zarf (cosreResult) üç araçta aynı: önce durum (ok/empty/error/warnings),
// sonra kanıt (evidence — adım 3'te çip olarak çizilir), en sonda veri. Sıra
// bilinçli: sohbet sonucu baştan keser (6000 rune) — durum asla kırpılmasın.
//
// Kanıt (evidence): {source, query, queryKind, window{fromMs,toMs}, env,
// cluster, rows|series, durationMs}. query ÇALIŞAN metindir: Thanos için
// cluster matcher'ı enjekte edilmiş PromQL (Route.ScopedQuery), ClickHouse
// pencere-RED için bind'leri yerine konmuş SQL. Okuyucunun içinde seçilen
// yol (MV / rollup / ham) dışarı açılmadığında queryKind="summary": okuyucu
// adı + uygulanan süzgeçler deterministik biçimde yazılır — uydurma SQL yok.
// Kimlik başlığı/token hiçbir zaman sorgu metnine girmez.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/metricsrc"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// Kanıt kaynak adları (çipte görünen).
const (
	evSourceThanos = "Thanos"
	evSourceVM     = "VictoriaMetrics"
	evSourceCH     = "ClickHouse"
	evSourceES     = "Elasticsearch"
)

// queryKind değerleri.
const (
	evKindPromQL  = "promql"
	evKindSQL     = "sql"
	evKindSummary = "summary"
)

// Yapısal hata kodları (model eylemi kodla seçer, düzyazıyla değil).
const (
	cosreErrBadArgs       = "bad_args"
	cosreErrNeedsCluster  = "needs_cluster"
	cosreErrNotConfigured = "not_configured"
	cosreErrBackend       = "backend_error"
	cosreErrTimeout       = "timeout"
)

// cosreBudget — tek okuma bütçesi (≤10 s; araç bütçesi 20 s).
const cosreBudget = 10 * time.Second

// MetricRouter — query_metrics'in metrik yönlendiricisi. *metricsrc.Resolver
// karşılar (api/mcp_deps.go doldurur); nil = Thanos/VM yok, yalnız CH.
type MetricRouter interface {
	Resolve(f metricsrc.Family, sc metricsrc.Scope) metricsrc.Route
	ThanosInstant(ctx context.Context, f metricsrc.Family, clusterRef, expr string) ([]thanos.Sample, metricsrc.Route, error)
	RuntimePods() chstore.RuntimePodReader
}

// EvidenceWindow — kanıt penceresi (unix ms).
type EvidenceWindow struct {
	FromMs int64 `json:"fromMs"`
	ToMs   int64 `json:"toMs"`
}

// cosreEvidence — adım 3'ün çipi.
type cosreEvidence struct {
	Source     string         `json:"source"`
	Query      string         `json:"query"`
	QueryKind  string         `json:"queryKind"`
	Window     EvidenceWindow `json:"window"`
	Env        string         `json:"env,omitempty"`
	Cluster    string         `json:"cluster,omitempty"`
	Rows       *int           `json:"rows,omitempty"`
	Series     *int           `json:"series,omitempty"`
	DurationMs int64          `json:"durationMs"`
}

// cosreError — yapısal hata (düzyazı değil).
type cosreError struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Need       []string `json:"need,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
}

// cosreResult — üç aracın ortak zarfı. Alan sırası = JSON sırası.
type cosreResult struct {
	Tool     string         `json:"tool"`
	OK       bool           `json:"ok"`
	Empty    bool           `json:"empty,omitempty"`
	Message  string         `json:"message,omitempty"`
	Error    *cosreError    `json:"error,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Request  map[string]any `json:"request"`
	Evidence *cosreEvidence `json:"evidence,omitempty"`
	// Evidences — v0.10.1275 (CoSRE adım 6): bileşik aracın hop başına
	// kanıtları; `evidence` hep listenin İLKİ (eski okuyucu tek çip görür).
	// Atomik araçlarda boş (omitempty — bayt-bayt eski zarf).
	Evidences []cosreHopEvidence `json:"evidences,omitempty"`
	Data      any                `json:"data,omitempty"`
}

// cosreHopEvidence — v0.10.1275: çok-kanıtlı zarfın tek kalemi. Hop =
// zincirdeki halka (trace|pods|jvm|logs|op_baseline…); As = href eşlemesinin
// anahtarı (EvidenceHref: query_traces|query_metrics|query_logs|trace|pod);
// Request = o eşlemenin istediği normalize argümanlar. Gömülü cosreEvidence
// JSON'da düz alanlar olarak yazılır.
type cosreHopEvidence struct {
	Hop     string         `json:"hop"`
	As      string         `json:"as"`
	Request map[string]any `json:"request,omitempty"`
	cosreEvidence
}

func cosreFail(tool string, req map[string]any, warnings []string, code, msg string) cosreResult {
	return cosreResult{Tool: tool, OK: false, Request: req, Warnings: warnings, Error: &cosreError{Code: code, Message: msg}}
}

func intPtr(n int) *int { return &n }

// stopwatch — kanıtın durationMs'i. wallNow (anchor.go) pencere KURMAZ;
// burada yalnız süre ölçer.
func stopwatch() func() int64 {
	t0 := wallNow()
	return func() int64 { return time.Since(t0).Milliseconds() }
}

// cosreClassify — okuma hatasının yapısal kodu.
func cosreClassify(err error) string {
	if err == nil {
		return ""
	}
	if strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return cosreErrTimeout
	}
	return cosreErrBackend
}

// cosreErrMsg — hata metnini modele sığacak boya indir.
func cosreErrMsg(prefix string, err error) string {
	return prefix + ": " + truncateRunes(err.Error(), 240)
}

// ── argüman kurtarma (SAF) ──────────────────────────────────────────────

// cosreArgs — ham JSON'un hoşgörülü görünümü: anahtarlar küçük harfe ve
// alt çizgisiz biçime indirilir (from_ms / fromMs / FromMS aynı), sayılar
// metin olarak da gelebilir.
type cosreArgs map[string]any

func normKey(k string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(k))
}

func parseCosreArgs(raw json.RawMessage) (cosreArgs, []string) {
	out := cosreArgs{}
	if len(raw) == 0 {
		return out, nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out, []string{"argümanlar JSON nesnesi değildi; varsayılanlar kullanıldı"}
	}
	for k, v := range m {
		out[normKey(k)] = v
	}
	return out, nil
}

// str — ilk dolu anahtarın metni (sayı da metne çevrilir).
func (a cosreArgs) str(keys ...string) string {
	for _, k := range keys {
		switch v := a[normKey(k)].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
	}
	return ""
}

// num — ilk sayısal anahtar; metin sayı da kabul edilir. ok=false: yok.
func (a cosreArgs) num(keys ...string) (float64, bool, string) {
	for _, k := range keys {
		switch v := a[normKey(k)].(type) {
		case float64:
			return v, true, ""
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				continue
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, false, fmt.Sprintf("%s sayı değil (%q) — yok sayıldı", k, s)
			}
			return f, true, ""
		}
	}
	return 0, false, ""
}

// coerceEnum — SAF: değer izinli kümede mi; değilse eşanlam haritası, o da
// yoksa varsayılan + uyarı. Boş değer uyarısız varsayılandır.
func coerceEnum(field, v string, allowed []string, synonyms map[string]string, def string) (string, string) {
	n := strings.ToLower(strings.TrimSpace(v))
	if n == "" {
		return def, ""
	}
	for _, a := range allowed {
		if n == a {
			return a, ""
		}
	}
	if s, ok := synonyms[n]; ok {
		return s, ""
	}
	return def, fmt.Sprintf("%s=%q bilinmiyor → %q kullanıldı (izinli: %s)", field, v, def, strings.Join(allowed, ", "))
}

// Pencere presetleri (enum) → saniye.
var cosreWindowEnum = []string{"15m", "1h", "6h", "24h", "7d"}

var cosreWindowSeconds = map[string]int{"15m": 900, "1h": 3600, "6h": 21600, "24h": 86400, "7d": 604800}

var cosreWindowSynonyms = map[string]string{
	"15min": "15m", "15dk": "15m", "60m": "1h", "1saat": "1h", "1hour": "1h", "6saat": "6h",
	"1d": "24h", "24saat": "24h", "1gün": "24h", "1day": "24h", "1w": "7d", "7gün": "7d", "1hafta": "7d",
}

// cosreWindow — SAF olmayan tek dal rangeWindow (çıpa). from_ms+to_ms ikisi
// birlikte gelirse onlar; saniye gibi görünen değer (< 1e12) ms'ye çevrilir;
// maxS aşılırsa pencere SONU korunarak kırpılır — hepsi uyarıyla.
func cosreWindow(ctx context.Context, a cosreArgs, maxS int, def string) (from, to time.Time, label string, warnings []string) {
	fromMs, okF, w1 := a.num("from_ms", "from")
	toMs, okT, w2 := a.num("to_ms", "to")
	for _, w := range []string{w1, w2} {
		if w != "" {
			warnings = append(warnings, w)
		}
	}
	if okF && okT {
		if toMs > 0 && toMs < 1e12 {
			fromMs, toMs = fromMs*1000, toMs*1000
			warnings = append(warnings, "from_ms/to_ms saniye gibiydi; milisaniyeye çevrildi")
		}
		if toMs > fromMs && fromMs > 0 {
			from, to = time.UnixMilli(int64(fromMs)).UTC(), time.UnixMilli(int64(toMs)).UTC()
			if to.Sub(from) > time.Duration(maxS)*time.Second {
				from = to.Add(-time.Duration(maxS) * time.Second)
				warnings = append(warnings, fmt.Sprintf("pencere en fazla %s; son kısmı okundu", humanSecs(maxS)))
			}
			return from, to, "custom", warnings
		}
		warnings = append(warnings, "from_ms/to_ms geçersiz (to > from olmalı) — window kullanıldı")
	} else if okF || okT {
		warnings = append(warnings, "from_ms ve to_ms birlikte verilmeli — window kullanıldı")
	}
	preset, w := coerceEnum("window", a.str("window", "range", "preset"), cosreWindowEnum, cosreWindowSynonyms, def)
	if w != "" {
		warnings = append(warnings, w)
	}
	secs := cosreWindowSeconds[preset]
	if secs > maxS {
		warnings = append(warnings, fmt.Sprintf("window=%s bu araç için fazla; %s'e kırpıldı", preset, humanSecs(maxS)))
		secs = maxS
		preset = humanSecs(maxS)
	}
	from, to = rangeWindow(ctx, secs)
	return from.UTC(), to.UTC(), preset, warnings
}

func humanSecs(s int) string {
	switch {
	case s%86400 == 0:
		if s == 86400 {
			return "24h"
		}
		return fmt.Sprintf("%dd", s/86400)
	case s%3600 == 0:
		return fmt.Sprintf("%dh", s/3600)
	default:
		return fmt.Sprintf("%dm", s/60)
	}
}

func evWin(from, to time.Time) EvidenceWindow {
	return EvidenceWindow{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()}
}

// windowTR — "veri yok" mesajı için pencere metni.
func windowTR(from, to time.Time) string {
	return from.UTC().Format("2006-01-02 15:04") + "–" + to.UTC().Format("15:04") + " UTC"
}

// scopeTR — "veri yok" mesajında NE arandığı.
func scopeTR(parts ...[2]string) string {
	var out []string
	for _, p := range parts {
		if p[1] != "" {
			out = append(out, p[0]+"="+p[1])
		}
	}
	return strings.Join(out, ", ")
}

// ── SQL kanıt metni (SAF) ───────────────────────────────────────────────

// inlineSQL — `?` bind'lerini sırayla okunur literal'lerle değiştirir (kanıt
// çipi için; YÜRÜTÜLMEZ). Metin içindeki `?` yalnız bind olarak kullanılır.
func inlineSQL(sql string, args ...any) string {
	var b strings.Builder
	i := 0
	for _, r := range sql {
		if r == '?' && i < len(args) {
			b.WriteString(sqlLiteral(args[i]))
			i++
			continue
		}
		b.WriteRune(r)
	}
	return collapseWS(b.String())
}

func sqlLiteral(v any) string {
	switch x := v.(type) {
	case string:
		return "'" + strings.ReplaceAll(strings.ReplaceAll(x, `\`, `\\`), "'", `\'`) + "'"
	case time.Time:
		return "'" + x.UTC().Format("2006-01-02 15:04:05") + "'"
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

func collapseWS(s string) string { return strings.Join(strings.Fields(s), " ") }

// sqlQuote — özet sorgu metninde değer çerçevesi.
func sqlQuote(s string) string { return sqlLiteral(s) }

// ── ortak okuma yardımcıları ────────────────────────────────────────────

// spanClusterFilter — cluster referansı (id/ad/span değeri) → spans `cluster`
// süzgeci. Kayıt yoksa ham değer süzülür + uyarı (Thanos'suz kurulumlarda span
// değeri doğrudan kullanılabilir).
func spanClusterFilter(d Deps, ref string) (chstore.FilterExpr, string, string) {
	ref = strings.TrimSpace(ref)
	if cs, ok := clustersFor(d, ref); ok && len(cs) == 1 && d.Clusters != nil {
		vals := cs[0].SpanValues
		if len(vals) == 0 {
			vals = []string{cs[0].Name}
		}
		if len(vals) == 1 {
			return chstore.FilterExpr{Key: "cluster", Op: "=", Values: vals}, cs[0].Name, ""
		}
		return chstore.FilterExpr{Key: "cluster", Op: "IN", Values: vals}, cs[0].Name, ""
	}
	return chstore.FilterExpr{Key: "cluster", Op: "=", Values: []string{ref}}, ref,
		fmt.Sprintf("cluster %q kayıtlı bir Remote Cluster değil; span değeri olarak süzüldü", ref)
}

// filtersSummary — özet sorgu metninin WHERE kısmı (deterministik sıra).
func filtersSummary(fs []chstore.FilterExpr) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		switch len(f.Values) {
		case 0:
			parts = append(parts, f.Key+" "+f.Op)
		case 1:
			parts = append(parts, f.Key+" "+f.Op+" "+sqlQuote(f.Values[0]))
		default:
			q := make([]string, len(f.Values))
			for i, v := range f.Values {
				q[i] = sqlQuote(v)
			}
			parts = append(parts, f.Key+" "+f.Op+" ("+strings.Join(q, ", ")+")")
		}
	}
	return strings.Join(parts, " AND ")
}

// round4 — değerleri modele kısa ver (4 anlamlı basamak ötesi gürültü).
func round4(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow(10, 4-math.Ceil(math.Log10(math.Abs(v))))
	if p < 1 {
		return math.Round(v)
	}
	return math.Round(v*p) / p
}

// sortedKeys — deterministik çıktı için.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// logsBackendSource — logstore arka uç adı → kanıt kaynağı.
func logsBackendSource(st logstore.Store) string {
	if st != nil && st.Backend() == "elasticsearch" {
		return evSourceES
	}
	return evSourceCH
}
