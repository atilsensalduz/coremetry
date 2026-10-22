package api

// explain_metric_panel.go — v0.10.1435 (Panelden "Bu grafiği açıkla").
// Operatör programı madde 2: Metrics / Explore grafiklerinde ve dashboard
// panellerinde "✨ CoSRE'ye sor" — açıklama sohbetin bir turu (v0.10.1400/
// 1405/1407 kaydı: chat_explain_registry.go, tek case + tek kart), operatörün
// brush'ladığı aralık ve seçtiği seri bağlamda.
//
// ── ÖZNE BİR KONUMDUR, SORGU DEĞİL ──────────────────────────────────────
//
// `?ai=` öznesi 400 rune (chatExplainSubjectMax): ham PromQL adrese sığmaz,
// sığsa da adresten gelen sorguyu çalıştırmak "link ile sorgu koşturmak"
// olurdu. Kodek KONUM taşır — d.<pano>.<panel> | x.<harf> (Explore) | m
// (Metrics editörü) — + pencere + brush + ≤3 seçili seri (+ cmp). Sorgunun
// kendisi niyetin yapısal yükünde (chatExplainAsk.Panel, ≤8 KiB), ✨ tıklandığı
// anda sayfanın durumundan doldurulur.
//
// ── GÜVEN KURALI ────────────────────────────────────────────────────────
//
//   - Dashboard paneli (d.*): sunucu kayıtlı paneli KENDİSİ yükler; istemciden
//     YALNIZ `vars` kabul edilir. Kayıtlı yapılandırma substitutePanelVars ile
//     (promqlVars ikizi, altın testli) çözülür ve istemcinin çözdüğüyle
//     karşılaştırılır — uyuşmazlık = LLM'siz dürüst ret. Çalışan sorgu HER
//     ZAMAN sunucunun çözdüğüdür.
//   - Explore / Metrics PromQL: validateChartPromQL (v0.10.1416 izin listesi,
//     aralık ≥ adım) + her metrik adının VM'de varlığı + topk(20) sarması;
//     reddedilen sorgu çalışmaz.
//   - Builder / span: HTTP uçlarının KENDİ filtre yapıları (parseFilters →
//     chstore.MetricQueryFilter; parseFiltersAndDSL + parseFilterGroup →
//     chstore.SpanMetricFilter) — sohbet ham SQL görmez.
//
// ── BÜTÇE ───────────────────────────────────────────────────────────────
//
// Pencere üzerinde TEK sınırlı sorgu (≤120 nokta, ≤20 seri — seçili seriler
// önde, 8 sn), 60 sn önbellek (anahtar brush dahil TÜM girdiler). Brush
// pencere adımında 8 kovadan azsa brush üzerinde İKİNCİ sınırlı sorgu (adım
// ⌈brush/60⌉, ≥15 sn). "Önceki pencereyle karşılaştır" çipi ikinci sorgu
// açmaz: tek sorgu iki pencere boyunca koşar (adım iki katı).
//
// Kanıt-önce: model düşse de `metric_panel` bloğu (grafik + ölçüm) + dürüst
// satır cevaptır. İstem SystemPromptServiceCharts (yeni sabit YOK). Yüzey
// aisurface.ExplainMetricPanelChat. HTTP ucu yok, MCP aracı yok, api.go aynı.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

const (
	explainAskMetricPanel      = "metric_panel"
	explainRendererMetricPanel = "metric-panel"

	metricPanelSrcPromQL  = "promql"
	metricPanelSrcBuilder = "builder"
	metricPanelSrcSpan    = "span"

	// metricPanelAskMaxBytes — niyetin yapısal yükünün tavanı (JSON).
	metricPanelAskMaxBytes = 8 << 10
	// metricPanelMaxWindow — açıklanabilir en geniş pencere.
	metricPanelMaxWindow = 31 * 24 * time.Hour
	// metricPanelBrushMinBuckets — brush pencere adımında bundan az kova
	// kapsıyorsa ince adımlı ikinci sorgu.
	metricPanelBrushMinBuckets = 8
	metricPanelBrushMaxPoints  = 60
	metricPanelWidenFactor     = 4
	metricPanelChipsMax        = 4
	metricPanelTitleMax        = 120
	metricPanelVarsMax         = 50
	metricPanelNoteMax         = 200
	// metricPanelByPod / Env — çipin sunucu-uygulamalı kırılımı.
	metricPanelByPod = "pod"
	metricPanelByEnv = "env"
)

// metricPanelAsk — chatExplainAsk.Panel: panelin sorgusunun yapısal yükü
// (lib/types.ts MetricPanelAsk aynası). Alanlar HTTP uçlarının parametreleri.
type metricPanelAsk struct {
	Src         string            `json:"src"`                   // promql | builder | span
	PromQL      string            `json:"promql,omitempty"`      // promql
	MetricName  string            `json:"metricName,omitempty"`  // builder (?name=)
	Service     string            `json:"service,omitempty"`     // builder (?service=)
	Agg         string            `json:"agg,omitempty"`         // builder / span
	GroupBy     string            `json:"groupBy,omitempty"`     // virgüllü
	Filters     string            `json:"filters,omitempty"`     // JSON FilterExpr[]
	FilterGroup string            `json:"filterGroup,omitempty"` // span (Explore OR grubu)
	DSL         string            `json:"dsl,omitempty"`         // span
	Field       string            `json:"field,omitempty"`       // span
	Unit        string            `json:"unit,omitempty"`
	Title       string            `json:"title,omitempty"`
	Vars        map[string]string `json:"vars,omitempty"` // dashboard: GÜVENİLEN TEK istemci girdisi
	// By — takip çipinin kırılımı (pod | env); sunucu etiketi kendisi çözer
	// ve yalnız PromQL'de, doğrulayıcı izin verirse uygular.
	By string `json:"by,omitempty"`
}

// normalizeMetricPanelAsk — SAF: yükün sınırları. nil → (nil, true) (yük
// yok — arşiv / derin link meşru); bozuk / >8 KiB → ok=false (niyet reddi).
func normalizeMetricPanelAsk(p *metricPanelAsk) (*metricPanelAsk, bool) {
	if p == nil {
		return nil, true
	}
	if b, err := json.Marshal(p); err != nil || len(b) > metricPanelAskMaxBytes {
		return nil, false
	}
	switch p.Src {
	case metricPanelSrcPromQL, metricPanelSrcBuilder, metricPanelSrcSpan:
	default:
		return nil, false
	}
	switch p.By {
	case "", metricPanelByPod, metricPanelByEnv:
	default:
		return nil, false
	}
	if len(p.Vars) > metricPanelVarsMax {
		return nil, false
	}
	c := *p
	c.Title = clipRunes(strings.TrimSpace(p.Title), metricPanelTitleMax)
	if p.Vars != nil {
		c.Vars = make(map[string]string, len(p.Vars))
		for k, v := range p.Vars {
			c.Vars[k] = v
		}
	}
	return &c, true
}

// ── konum ────────────────────────────────────────────────────────────────

var metricPanelIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// metricPanelLoc — çözülmüş konum.
type metricPanelLoc struct {
	Kind   byte // 'd' dashboard | 'x' Explore | 'm' Metrics editörü
	Dash   string
	Panel  string
	Letter string
}

// parseMetricPanelLoc — SAF (aiSubject.ts parseMetricPanelLoc ikizi):
// `m` | `x.<A-D>` | `d.<pano>.<panel>` (kimlikler [A-Za-z0-9_-]{1,64}).
func parseMetricPanelLoc(s string) (metricPanelLoc, bool) {
	switch {
	case s == "m":
		return metricPanelLoc{Kind: 'm'}, true
	case strings.HasPrefix(s, "x."):
		l := s[2:]
		if len(l) == 1 && l[0] >= 'A' && l[0] <= 'D' {
			return metricPanelLoc{Kind: 'x', Letter: l}, true
		}
	case strings.HasPrefix(s, "d."):
		parts := strings.Split(s[2:], ".")
		if len(parts) == 2 && metricPanelIDRe.MatchString(parts[0]) && metricPanelIDRe.MatchString(parts[1]) {
			return metricPanelLoc{Kind: 'd', Dash: parts[0], Panel: parts[1]}, true
		}
	}
	return metricPanelLoc{}, false
}

// metricPanelLocTR — SAF: konumun okunur adı.
func metricPanelLocTR(l metricPanelLoc) string {
	switch l.Kind {
	case 'd':
		return "dashboard paneli"
	case 'x':
		return "Explore sorgu " + l.Letter
	}
	return "Metrics editörü"
}

// ── sorgu kimliği ────────────────────────────────────────────────────────

// metricPanelQuery — çalışacak sorgunun kimliği (birim hariç her alan
// karşılaştırmaya ve önbellek anahtarına girer).
type metricPanelQuery struct {
	Src, PromQL, MetricName, Service, Agg, GroupBy, Filters, FilterGroup, DSL, Field string
	Unit                                                                             string
}

func (q metricPanelQuery) identity() []string {
	return []string{q.Src, q.PromQL, q.MetricName, q.Service, q.Agg, q.GroupBy, q.Filters, q.FilterGroup, q.DSL, q.Field}
}

// sameQuery — SAF: iki çözümün sorgu alanları (birim/başlık hariç) bayt bayt aynı mı.
func (q metricPanelQuery) sameQuery(o metricPanelQuery) bool {
	a, b := q.identity(), o.identity()
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

func metricPanelQueryFromAsk(p *metricPanelAsk) metricPanelQuery {
	return metricPanelQuery{Src: p.Src, PromQL: p.PromQL, MetricName: p.MetricName, Service: p.Service, Agg: p.Agg,
		GroupBy: p.GroupBy, Filters: p.Filters, FilterGroup: p.FilterGroup, DSL: p.DSL, Field: p.Field, Unit: p.Unit}
}

// describe — SAF: modelin ve kartın okuduğu tek satır sorgu özeti.
func (q metricPanelQuery) describe() string {
	switch q.Src {
	case metricPanelSrcPromQL:
		return q.PromQL
	case metricPanelSrcBuilder:
		parts := []string{"metrik " + q.MetricName}
		if q.Agg != "" {
			parts = append(parts, "agg="+q.Agg)
		}
		if q.Service != "" {
			parts = append(parts, "servis="+q.Service)
		}
		if q.GroupBy != "" {
			parts = append(parts, "kırılım="+q.GroupBy)
		}
		if q.Filters != "" {
			parts = append(parts, "filtre="+q.Filters)
		}
		return strings.Join(parts, " · ")
	}
	parts := []string{"span " + q.Agg}
	if q.Field != "" {
		parts[0] += "(" + q.Field + ")"
	}
	if q.GroupBy != "" {
		parts = append(parts, "kırılım="+q.GroupBy)
	}
	if q.DSL != "" {
		parts = append(parts, "dsl="+strings.ReplaceAll(q.DSL, "\n", " AND "))
	}
	if q.Filters != "" {
		parts = append(parts, "filtre="+q.Filters)
	}
	if q.FilterGroup != "" {
		parts = append(parts, "grup="+q.FilterGroup)
	}
	return strings.Join(parts, " · ")
}

// ── okuma yüzeyi (test dikişi) ───────────────────────────────────────────

// metricPanelReads — kurucunun okuduğu dört şey; üretimde Server'dan.
type metricPanelReads interface {
	GetDashboard(ctx context.Context, id string) (*chstore.Dashboard, error)
	MetricSourceName() string
	QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error)
	QuerySpanMetric(ctx context.Context, f chstore.SpanMetricFilter) ([]chstore.SpanMetricSeries, error)
	PromQL() (metricChartReader, bool)
}

// metricPanelSeam — yalnız testler; üretimde nil.
var metricPanelSeam metricPanelReads

type metricPanelLive struct{ s *Server }

func (l metricPanelLive) GetDashboard(ctx context.Context, id string) (*chstore.Dashboard, error) {
	return l.s.store.GetDashboard(ctx, id)
}
func (l metricPanelLive) MetricSourceName() string { return l.s.metricSource().Name() }
func (l metricPanelLive) QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	series, _, err := queryMetricNoted(ctx, l.s.metricSource(), f)
	return series, err
}
func (l metricPanelLive) QuerySpanMetric(ctx context.Context, f chstore.SpanMetricFilter) ([]chstore.SpanMetricSeries, error) {
	series, _, _, _, err := l.s.store.QuerySpanMetricTopN(ctx, f)
	return series, err
}
func (l metricPanelLive) PromQL() (metricChartReader, bool) { return l.s.metricChartReaderFor() }

func (s *Server) metricPanelReads() metricPanelReads {
	if metricPanelSeam != nil {
		return metricPanelSeam
	}
	return metricPanelLive{s}
}

// ── çözüm ────────────────────────────────────────────────────────────────

// storedPanel — chstore.Dashboard.Panels öğesinin okunan alanları.
type storedPanel struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Title         string          `json:"title"`
	Config        json.RawMessage `json:"config"`
	RangeOverride json.RawMessage `json:"rangeOverride,omitempty"`
}

// metricPanelResolved — çözülmüş panel: sorgu + başlık + kırılım isteği.
type metricPanelResolved struct {
	Query metricPanelQuery
	Title string
	By    string
	Href  string // panelin kendi sayfası (dashboard)
}

// resolveMetricPanel — güven kuralı (dosya başı). Hata türleri: bulunamadı
// (explainNotFound) / geçersiz (explainBadArgs) / okuma hatası.
func resolveMetricPanel(ctx context.Context, rd metricPanelReads, loc metricPanelLoc, p *metricPanelAsk, from, to time.Time) (metricPanelResolved, error) {
	by := ""
	if p != nil {
		by = p.By
	}
	if loc.Kind != 'd' {
		if p == nil {
			return metricPanelResolved{}, explainBadArgs("panelin sorgusu bu istekte yok (paylaşılan bağlantı sorgu taşımaz) — grafiğin ⋯ menüsündeki ✨ CoSRE'ye sor ile yeniden sorun")
		}
		title := p.Title
		if title == "" {
			title = metricPanelLocTR(loc)
		}
		return metricPanelResolved{Query: metricPanelQueryFromAsk(p), Title: title, By: by}, nil
	}
	d, err := rd.GetDashboard(ctx, loc.Dash)
	if err != nil {
		return metricPanelResolved{}, err
	}
	if d == nil {
		return metricPanelResolved{}, explainNotFound("dashboard not found")
	}
	var panels []storedPanel
	if err := json.Unmarshal(d.Panels, &panels); err != nil {
		return metricPanelResolved{}, explainBadArgs("dashboard panelleri okunamadı")
	}
	var sp *storedPanel
	for i := range panels {
		if panels[i].ID == loc.Panel {
			sp = &panels[i]
			break
		}
	}
	if sp == nil {
		return metricPanelResolved{}, explainNotFound("panel not found")
	}
	vars := map[string]string{}
	if p != nil && p.Vars != nil {
		vars = p.Vars
	}
	q, ok := substitutePanelVars(sp.Type, sp.Config, vars)
	if !ok {
		return metricPanelResolved{}, explainBadArgs(fmt.Sprintf("%q türündeki panel açıklanamaz (yalnız zaman serisi panelleri: metric, spanmetric, promql)", sp.Type))
	}
	if p != nil && !q.sameQuery(metricPanelQueryFromAsk(p)) {
		return metricPanelResolved{}, explainBadArgs("ekrandaki panelin sorgusu kayıtlı panelle uyuşmuyor (pano sonradan değişmiş ya da kaydedilmemiş düzenleme olabilir) — sayfayı yenileyip yeniden sorun")
	}
	title := strings.TrimSpace(sp.Title)
	if title == "" {
		title = "Dashboard paneli"
	}
	href := "/dashboard?id=" + url.QueryEscape(loc.Dash) + "&range=" + url.QueryEscape(fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	return metricPanelResolved{Query: q, Title: clipRunes(title, metricPanelTitleMax), By: by, Href: href}, nil
}

// ── seri dönüşümü ─────────────────────────────────────────────────────────

// metricPanelGroupValues — SAF: GroupKey → değerler (`k="v"` → v; çıplak değer aynen).
func metricPanelGroupValues(gk []string) []string {
	out := make([]string, 0, len(gk))
	for _, kv := range gk {
		v := kv
		if _, after, ok := strings.Cut(kv, "="); ok {
			v = after
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// metricPanelSeriesName — SAF: serinin okunur adı (değerler " · " ile).
func metricPanelSeriesName(gk []string, fallback string) string {
	if vals := metricPanelGroupValues(gk); len(vals) > 0 {
		return strings.Join(vals, " · ")
	}
	return fallback
}

var metricPanelLabelSplitRe = regexp.MustCompile(`\s*(?:,|·|/|\|)\s*`)

// metricPanelSelectedMatches — SAF: istemcinin seri etiketi (Explore
// "pod=a, env=b", dashboard "a · b", Metrics "A: pod=a", PromQL `pod="a"`)
// bu serinin grup değerlerini KAPSIYOR mu. Grupsuz seri eşleşmez.
func metricPanelSelectedMatches(gk []string, name, sel string) bool {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return false
	}
	if sel == name && len(metricPanelGroupValues(gk)) > 0 {
		return true
	}
	vals := metricPanelGroupValues(gk)
	if len(vals) == 0 {
		return false
	}
	toks := map[string]bool{}
	for _, t := range metricPanelLabelSplitRe.Split(strings.Trim(sel, "{}"), -1) {
		if i := strings.LastIndex(t, ": "); i >= 0 {
			t = t[i+2:]
		}
		if _, after, ok := strings.Cut(t, "="); ok {
			t = after
		}
		t = strings.Trim(strings.TrimSpace(t), `"`)
		if t != "" {
			toks[t] = true
		}
	}
	for _, v := range vals {
		if !toks[v] {
			return false
		}
	}
	return true
}

// metricPanelSeries — SAF: ham seriler → grafik serileri. NaN/Inf atılır,
// nokta ≤maxPts (sondan); seçili seriler ÖNDE (seçim sırasıyla), gerisi tepe
// değere göre azalan; toplam ≤20. found = eşleşen serilerin SUNUCU adları,
// missing = eşleşmeyen seçimler.
func metricPanelSeries(in []chstore.SpanMetricSeries, unit, fallback string, selected []string, maxPts int) (out []mcptools.EvChartSeries, found, missing []string, truncated bool) {
	type cand struct {
		s    mcptools.EvChartSeries
		gk   []string
		peak float64
	}
	cands := make([]cand, 0, len(in))
	for _, s := range in {
		cs := mcptools.EvChartSeries{Name: metricPanelSeriesName(s.GroupKey, fallback), Unit: unit}
		mx := math.Inf(-1)
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			cs.Points = append(cs.Points, [2]float64{float64(p.Time / 1e6), p.Value})
			if p.Value > mx {
				mx = p.Value
			}
		}
		if len(cs.Points) == 0 {
			continue
		}
		if len(cs.Points) > maxPts {
			cs.Points, truncated = cs.Points[len(cs.Points)-maxPts:], true
		}
		cands = append(cands, cand{s: cs, gk: s.GroupKey, peak: mx})
	}
	used := make([]bool, len(cands))
	for _, sel := range selected {
		hit := -1
		for i, c := range cands {
			if !used[i] && metricPanelSelectedMatches(c.gk, c.s.Name, sel) {
				hit = i
				break
			}
		}
		if hit < 0 {
			missing = append(missing, sel)
			continue
		}
		used[hit] = true
		out = append(out, cands[hit].s)
		found = append(found, cands[hit].s.Name)
	}
	rest := make([]int, 0, len(cands))
	for i := range cands {
		if !used[i] {
			rest = append(rest, i)
		}
	}
	sort.SliceStable(rest, func(a, b int) bool {
		ca, cb := cands[rest[a]], cands[rest[b]]
		if ca.peak != cb.peak {
			return ca.peak > cb.peak
		}
		return ca.s.Name < cb.s.Name
	})
	for _, i := range rest {
		if len(out) >= metricChartMaxSeries {
			truncated = true
			break
		}
		out = append(out, cands[i].s)
	}
	return out, found, missing, truncated
}

// ── blok + kurucu ─────────────────────────────────────────────────────────

// metricPanelBlockPayload — `metric_panel` bloğunun gövdesi (blocks.TypeMetricPanel;
// FE MetricPanelCard). Model metninden BAĞIMSIZ.
type metricPanelBlockPayload struct {
	Title       string                  `json:"title"`
	Loc         string                  `json:"loc"`
	Src         string                  `json:"src"`
	Query       string                  `json:"query,omitempty"`
	Unit        string                  `json:"unit,omitempty"`
	FromNs      int64                   `json:"fromNs"`
	ToNs        int64                   `json:"toNs"`
	BrushFromNs int64                   `json:"brushFromNs,omitempty"`
	BrushToNs   int64                   `json:"brushToNs,omitempty"`
	Cmp         bool                    `json:"cmp,omitempty"`
	Selected    []string                `json:"selected,omitempty"`
	Missing     []string                `json:"missing,omitempty"`
	Chart       mcptools.EvChartPayload `json:"chart"`
	Stats       []string                `json:"stats"`
	Notes       []string                `json:"notes,omitempty"`
	Href        string                  `json:"href,omitempty"`
}

// metricPanelCached — 60 sn önbellek gövdesi.
type metricPanelCached struct {
	Series      []mcptools.EvChartSeries `json:"series"`
	BrushSeries []mcptools.EvChartSeries `json:"brushSeries,omitempty"`
	Found       []string                 `json:"found,omitempty"`
	Missing     []string                 `json:"missing,omitempty"`
	Truncated   bool                     `json:"truncated,omitempty"`
	Query       string                   `json:"query"`
}

// metricPanelCacheKey — SAF (CLAUDE.md "cache key hashes ALL inputs"):
// kaynak, konum, sorgu kimliği, çalışan ifade, kırılım, pencere, adım, brush,
// seçili seriler, kıyas — FNV.
func metricPanelCacheKey(srcName, loc string, q metricPanelQuery, exec, by string, from, to time.Time, step int, bFrom, bTo time.Time, selected []string, cmp bool) string {
	parts := append([]string{srcName, loc, exec, by}, q.identity()...)
	parts = append(parts, fmt.Sprint(from.UnixNano()), fmt.Sprint(to.UnixNano()), fmt.Sprint(step),
		fmt.Sprint(bFrom.UnixNano()), fmt.Sprint(bTo.UnixNano()), strings.Join(selected, "\x1f"), fmt.Sprint(cmp))
	return "cosre-mpanel:v1:" + fnvStr(parts...)
}

// metricPanelBrushStep — SAF: brush'ın ince adımı; brush pencere adımında
// ≥8 kova kapsıyorsa 0 (ikinci sorgu yok).
func metricPanelBrushStep(brush time.Duration, windowStep int) int {
	if brush <= 0 || windowStep <= 0 {
		return 0
	}
	if int(brush/time.Second)/windowStep >= metricPanelBrushMinBuckets {
		return 0
	}
	st := int(math.Ceil(brush.Seconds() / metricPanelBrushMaxPoints))
	if st < metricChartMinStepS {
		st = metricChartMinStepS
	}
	if st >= windowStep { // daha ince değil → anlamsız
		return 0
	}
	return st
}

// metricPanelReject — sorgu çalıştırılmadı: dürüst metin (LLM yok).
type metricPanelReject struct{ text string }

func (e *metricPanelReject) Error() string { return e.text }

// metricPanelExec — sorgunun çalışacak biçimi + koşucusu.
type metricPanelExec struct {
	Query string
	Href  string
	Run   func(ctx context.Context, from, to time.Time, step, maxPts int) ([]chstore.SpanMetricSeries, error)
	// ByAllowed — PromQL'de kırılım çipinin uygulanabildiği roller.
	ByAllowed map[string]bool
	Notes     []string
}

// metricPanelPrepare — güven kuralının yürütme yarısı: doğrulama + varlık +
// sarma (PromQL) ya da HTTP filtre yapıları (builder/span).
func (s *Server) metricPanelPrepare(ctx context.Context, rd metricPanelReads, r metricPanelResolved, from, to time.Time, step int) (metricPanelExec, error) {
	q := r.Query
	switch q.Src {
	case metricPanelSrcPromQL:
		raw := strings.TrimSpace(q.PromQL)
		href := mcptools.PromQLEditorHref(raw, "vm", "", from.UnixMilli(), to.UnixMilli())
		const lead = "Panelin sorgusunu sohbette çalıştırmadım"
		if raw == "" {
			return metricPanelExec{}, &metricPanelReject{"Panelde PromQL sorgusu yok — açıklanacak bir şey yok."}
		}
		vm, isVM := rd.PromQL()
		if !isVM || vm == nil {
			return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR(lead, "sohbet PromQL panellerini yalnız VictoriaMetrics metrik kaynağında çalıştırır (bu kurulumda yapılandırılmamış)", false, href)}
		}
		chk := validateChartPromQL(raw, step)
		if !chk.OK() {
			return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR(lead, chk.Reason, chk.MetricsQL, href)}
		}
		sets, missing, readErr := s.promqlMetricLabelSets(ctx, vm, chk.Metrics, to)
		if readErr != nil {
			return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR("Panelin sorgusunu çalıştırmadım", "VictoriaMetrics etiket okuması BAŞARISIZ (okunamadı ≠ yok): "+readErr.Error(), false, href)}
		}
		if len(missing) > 0 {
			return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR(lead, "VictoriaMetrics'te bulunamayan metrik (son 24 sa): "+strings.Join(missing, ", "), false, href)}
		}
		ex := metricPanelExec{ByAllowed: map[string]bool{}}
		labels := sets[chk.Metrics[0]]
		lm := vm.MetricLabelMap().Normalized()
		byLabel := map[string]string{}
		for role, by := range map[string]string{vmetrics.RolePod: metricPanelByPod, vmetrics.RoleEnv: metricPanelByEnv} {
			res := vmetrics.ResolveLabelRole(role, lm, labels, true)
			if !res.Applied() || !labels[res.Label] {
				continue
			}
			wrapped := fmt.Sprintf("sum by (%s) (%s)", res.Label, raw)
			if validateChartPromQL(wrapped, step).OK() && !promqlHasLabelMatcher(chk.Expr, res.Label) {
				ex.ByAllowed[by] = true
				byLabel[by] = res.Label
			}
		}
		exec, err := promqlExecQuery(chk, nil)
		if err != nil {
			return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR(lead, "sorgu yeniden yazılamadı: "+err.Error(), false, href)}
		}
		if r.By != "" {
			lbl, ok := byLabel[r.By]
			if !ok {
				return metricPanelExec{}, &metricPanelReject{promqlDirectAnswerTR(lead, fmt.Sprintf("%s kırılımı bu sorguya uygulanamıyor (etiket yok ya da doğrulayıcı izin vermiyor)", r.By), false, href)}
			}
			exec = fmt.Sprintf("sum by (%s) (%s)", lbl, raw)
			ex.Notes = append(ex.Notes, fmt.Sprintf("kırılım: sorgu sum by (%s) ile sarıldı (takip çipi)", lbl))
		} else if chk.NeedsWrap {
			ex.Notes = append(ex.Notes, fmt.Sprintf("dış ifade toplama değil — topk(%d, …) ile sarıldı (seri tavanı)", metricChartMaxSeries))
		}
		ex.Query = exec
		ex.Href = mcptools.PromQLEditorHref(exec, "vm", "", from.UnixMilli(), to.UnixMilli())
		ex.Run = func(ctx context.Context, f, t time.Time, st, mp int) ([]chstore.SpanMetricSeries, error) {
			return vm.QueryPromQLRange(ctx, exec, f, t, st, mp)
		}
		return ex, nil
	case metricPanelSrcBuilder:
		if strings.TrimSpace(q.MetricName) == "" {
			return metricPanelExec{}, explainBadArgs("panelde metrik adı yok")
		}
		filters, err := parseFilters(q.Filters)
		if err != nil {
			return metricPanelExec{}, explainBadArgs("panel filtresi geçersiz: " + err.Error())
		}
		ex := metricPanelExec{Query: q.describe()}
		if r.By != "" {
			ex.Notes = append(ex.Notes, "kırılım çipi yalnız PromQL panellerinde uygulanır — sorgu olduğu gibi koştu")
		}
		ex.Run = func(ctx context.Context, f, t time.Time, st, mp int) ([]chstore.SpanMetricSeries, error) {
			return rd.QueryMetric(ctx, chstore.MetricQueryFilter{Name: q.MetricName, Service: q.Service, Filters: filters,
				GroupBy: splitNonEmpty(q.GroupBy, ','), Aggregation: q.Agg, From: f, To: t, StepSeconds: st, MaxDataPoints: mp})
		}
		return ex, nil
	case metricPanelSrcSpan:
		filters, err := parseFiltersAndDSL(q.Filters, q.DSL)
		if err != nil {
			return metricPanelExec{}, explainBadArgs("panel filtresi / DSL geçersiz: " + err.Error())
		}
		root, err := parseFilterGroup(q.FilterGroup)
		if err != nil {
			return metricPanelExec{}, explainBadArgs("panel filtre grubu geçersiz: " + err.Error())
		}
		ex := metricPanelExec{Query: q.describe()}
		if r.By != "" {
			ex.Notes = append(ex.Notes, "kırılım çipi yalnız PromQL panellerinde uygulanır — sorgu olduğu gibi koştu")
		}
		ex.Run = func(ctx context.Context, f, t time.Time, st, _ int) ([]chstore.SpanMetricSeries, error) {
			return rd.QuerySpanMetric(ctx, chstore.SpanMetricFilter{Filters: filters, FilterRoot: root, Aggregation: q.Agg,
				Field: q.Field, GroupBy: splitNonEmpty(q.GroupBy, ','), From: f, To: t, StepSeconds: st})
		}
		return ex, nil
	}
	return metricPanelExec{}, explainBadArgs("bilinmeyen panel kaynağı")
}

// buildMetricPanelExplain — kayıt kurucusu (chat_explain_registry.go).
func (s *Server) buildMetricPanelExplain(ctx context.Context, subj aiSubject) (explainBuilt, error) {
	loc, ok := parseMetricPanelLoc(subj.ID)
	if !ok {
		return explainBuilt{}, explainBadArgs("panel konumu geçersiz")
	}
	window := subj.To.Sub(subj.From)
	if window <= 0 || window > metricPanelMaxWindow {
		return explainBuilt{}, explainBadArgs("pencere 31 günü aşıyor")
	}
	tz := subj.Loc
	if tz == nil {
		tz = time.UTC
	}
	rd := s.metricPanelReads()
	r, err := resolveMetricPanel(ctx, rd, loc, subj.Panel, subj.From, subj.To)
	if err != nil {
		return explainBuilt{}, err
	}
	// Sorgu penceresi: cmp → önceki eş pencere de AYNI sorguda (adım 2×).
	qFrom, qTo := subj.From, subj.To
	if subj.Cmp {
		qFrom = subj.From.Add(-window)
	}
	step := metricChartStepS(int64(qTo.Sub(qFrom) / time.Second))
	ex, err := s.metricPanelPrepare(ctx, rd, r, qFrom, qTo, step)
	unit := r.Query.Unit
	b := newExplainBuilt(copilot.SystemPromptServiceCharts(), "")
	b.Label, b.Renderer, b.Service = r.Title, explainRendererMetricPanel, metricPanelService(r.Query)
	if err != nil {
		var rej *metricPanelReject
		if errors.As(err, &rej) {
			b.Unexplained = rej.text
			return b, nil
		}
		return explainBuilt{}, err
	}
	srcName := rd.MetricSourceName()
	switch r.Query.Src {
	case metricPanelSrcPromQL:
		srcName = metricSourceVM
	case metricPanelSrcSpan:
		srcName = "spans"
	}
	hasBrush := !subj.BrushFrom.IsZero() && subj.BrushTo.After(subj.BrushFrom)
	brushStep := 0
	if hasBrush {
		brushStep = metricPanelBrushStep(subj.BrushTo.Sub(subj.BrushFrom), step)
	}
	key := metricPanelCacheKey(srcName, subj.ID, r.Query, ex.Query, r.By, qFrom, qTo, step, subj.BrushFrom, subj.BrushTo, subj.Series, subj.Cmp)
	var res metricPanelCached
	if !s.metricChartCacheGet(ctx, key, &res) {
		qctx, cancel := context.WithTimeout(ctx, metricChartQueryTimeout)
		raw, qerr := ex.Run(qctx, qFrom, qTo, step, metricChartMaxPoints)
		cancel()
		if qerr != nil {
			b.Unexplained = fmt.Sprintf("Panelin sorgusu okunamadı (okunamadı ≠ yok): %v. Sorgu: %s", qerr, ex.Query)
			return b, nil
		}
		res.Series, res.Found, res.Missing, res.Truncated = metricPanelSeries(raw, unit, r.Title, subj.Series, metricChartMaxPoints)
		if brushStep > 0 {
			bctx, cancel := context.WithTimeout(ctx, metricChartQueryTimeout)
			braw, berr := ex.Run(bctx, subj.BrushFrom, subj.BrushTo, brushStep, metricPanelBrushMaxPoints)
			cancel()
			if berr == nil {
				res.BrushSeries, _, _, _ = metricPanelSeries(braw, unit, r.Title, subj.Series, metricPanelBrushMaxPoints)
			}
		}
		res.Query = ex.Query
		s.metricChartCacheSet(ctx, key, res, metricChartCacheTTL)
	}

	cmpSplit := int64(0)
	if subj.Cmp {
		cmpSplit = subj.From.UnixMilli()
	}
	in := metricPanelStatsInput{Series: res.Series, BrushSeries: res.BrushSeries, Unit: unit, Selected: res.Found,
		Missing: res.Missing, CmpSplitMs: cmpSplit, Loc: tz}
	if hasBrush {
		in.BrushFromMs, in.BrushToMs = subj.BrushFrom.UnixMilli(), subj.BrushTo.UnixMilli()
	}
	stats, summary := metricPanelStatsTR(in)
	notes := append([]string(nil), ex.Notes...)
	if res.Truncated {
		notes = append(notes, fmt.Sprintf("tavan: ≤%d seri / ≤%d nokta uygulandı", metricChartMaxSeries, metricChartMaxPoints))
	}
	if hasBrush && brushStep > 0 && len(res.BrushSeries) == 0 {
		notes = append(notes, "seçili aralığın ince adımlı sorgusu okunamadı — seçili aralık ölçümü pencere adımından")
	}
	href := r.Href
	if href == "" {
		href = ex.Href
	}
	chart := mcptools.EvChartPayload{Title: r.Title, DeepLink: href,
		Window: mcptools.EvBlockWindow{FromNs: qFrom.UnixNano(), ToNs: qTo.UnixNano()}, Series: res.Series, Truncated: res.Truncated}
	if hasBrush {
		chart.Regions = []mcptools.EvChartRegion{{FromMs: subj.BrushFrom.UnixMilli(), ToMs: subj.BrushTo.UnixMilli(), Label: "seçili", Tone: "accent"}}
	}
	payload := metricPanelBlockPayload{Title: r.Title, Loc: subj.ID, Src: r.Query.Src, Query: res.Query, Unit: unit,
		FromNs: subj.From.UnixNano(), ToNs: subj.To.UnixNano(), Cmp: subj.Cmp, Selected: res.Found, Missing: res.Missing,
		Chart: chart, Stats: metricPanelStatLines(stats), Notes: notes, Href: href}
	if hasBrush {
		payload.BrushFromNs, payload.BrushToNs = subj.BrushFrom.UnixNano(), subj.BrushTo.UnixNano()
	}
	b.Blocks = []explainBlock{{Type: blocks.TypeMetricPanel, Payload: payload}}
	b.Note = clipRunes(summary, metricPanelNoteMax)
	if href != "" && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
		label := "Metrics'te aç"
		if loc.Kind == 'd' {
			label = "Panoyu aç"
		}
		b.Links = []guidedAnswerLink{{Label: label, Href: href}}
	}
	b.Followups = metricPanelFollowups(subj, r, ex.ByAllowed)
	if len(res.Series) == 0 {
		b.Unexplained = "Bu pencerede panelin sorgusu SERİ döndürmedi — anlatılacak ölçüm yok. Pencereyi genişletip yeniden sorun."
		return b, nil
	}
	b.User = metricPanelUserTR(r, loc, ex, subj, tz, notes, stats)
	b.CacheKey = explainCacheKey(b.System, b.User, "")
	return b, nil
}

// metricPanelStatLines — SAF: ölçüm bloğunun başlıksız satırları (kart).
func metricPanelStatLines(text string) []string {
	var out []string
	for i, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if i == 0 || strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, strings.TrimPrefix(l, "- "))
	}
	return out
}

// metricPanelUserTR — SAF: modelin kullanıcı istemi (tüm ölçüm metinde →
// önbellek anahtarı tüm sinyalleri hash'ler).
func metricPanelUserTR(r metricPanelResolved, loc metricPanelLoc, ex metricPanelExec, subj aiSubject, tz *time.Location, notes []string, stats string) string {
	hm := func(t time.Time) string { return t.In(tz).Format("2006-01-02 15:04") }
	var b strings.Builder
	b.WriteString("METRİK PANELİ — operatör grafikte \"Bu grafiği açıkla\" dedi.\n")
	fmt.Fprintf(&b, "Panel: %s (%s)\n", r.Title, metricPanelLocTR(loc))
	src := map[string]string{metricPanelSrcPromQL: "VictoriaMetrics PromQL", metricPanelSrcBuilder: "metrik deposu (builder)", metricPanelSrcSpan: "span metriği"}[r.Query.Src]
	fmt.Fprintf(&b, "Kaynak: %s\nSorgu: %s\n", src, ex.Query)
	if r.Query.Unit != "" {
		fmt.Fprintf(&b, "Birim: %s\n", r.Query.Unit)
	}
	fmt.Fprintf(&b, "Pencere: %s → %s (%s)\n", hm(subj.From), hm(subj.To), fmtAgoTR(int64(subj.To.Sub(subj.From)/time.Second)))
	if subj.Cmp {
		b.WriteString("Kıyas: aynı uzunluktaki ÖNCEKİ pencereyle (ölçümde \"önceki pencere\").\n")
	}
	if !subj.BrushFrom.IsZero() {
		fmt.Fprintf(&b, "Operatörün seçtiği aralık: %s → %s — açıklama ÖNCE bu aralığa odaklansın.\n", hm(subj.BrushFrom), hm(subj.BrushTo))
	}
	if len(subj.Series) > 0 {
		fmt.Fprintf(&b, "Operatörün seçtiği seri: %s — açıklama bu seriyi diğerleriyle kıyaslasın.\n", strings.Join(subj.Series, ", "))
	}
	for _, n := range notes {
		b.WriteString("Not: " + n + ".\n")
	}
	b.WriteString(stats)
	b.WriteString("Görev: bu panelde neyin, ne kadar ve NE ZAMAN değiştiğini anlat; seçili aralık / seçili seri verildiyse önce onları açıkla. Yalnız yukarıdaki sayıları kullan.\n")
	return b.String()
}

var metricPanelPromSvcRe = regexp.MustCompile(`service[._]name\s*=\s*"([^"]+)"`)

// metricPanelService — SAF: sorgunun TEK servisi (link çipleri için; yoksa "").
func metricPanelService(q metricPanelQuery) string {
	if strings.TrimSpace(q.Service) != "" && !strings.Contains(q.Service, "$") {
		return strings.TrimSpace(q.Service)
	}
	if q.Filters != "" {
		var fs []chstore.FilterExpr
		if json.Unmarshal([]byte(q.Filters), &fs) == nil {
			for _, f := range fs {
				if (f.Key == "service.name" || f.Key == "resource.service.name") && f.Op == "=" && len(f.Values) == 1 {
					return f.Values[0]
				}
			}
		}
	}
	for _, s := range []string{q.PromQL, q.DSL} {
		if m := metricPanelPromSvcRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// metricPanelFollowups — SAF: takip çipleri (≤4): pencereyi 4× genişlet,
// önceki pencereyle karşılaştır, pod/ortam kırılımı (yalnız izinliyse),
// seçili aralığın trace'leri / logları (uygulama-içi link).
func metricPanelFollowups(subj aiSubject, r metricPanelResolved, byAllowed map[string]bool) []chatFollowup {
	var out []chatFollowup
	ask := func(ns aiSubject, by string) *chatExplainAsk {
		var p *metricPanelAsk
		if subj.Panel != nil {
			c := *subj.Panel
			c.By = by
			p = &c
		}
		ns.Panel, ns.Loc = nil, nil
		return &chatExplainAsk{Kind: explainAskMetricPanel, ID: ns.ID, Subject: formatAiSubject(ns), Src: "chip", Panel: p}
	}
	window := subj.To.Sub(subj.From)
	if wide := window * metricPanelWidenFactor; wide <= metricPanelMaxWindow {
		ns := subj
		ns.From, ns.Cmp = subj.To.Add(-wide), false
		out = append(out, chatFollowup{Text: "Pencereyi 4× genişlet", Kind: "metric_panel_widen", ExplainAsk: ask(ns, r.By)})
	}
	if !subj.Cmp {
		ns := subj
		ns.Cmp = true
		out = append(out, chatFollowup{Text: "Önceki pencereyle karşılaştır", Kind: "metric_panel_compare", ExplainAsk: ask(ns, r.By)})
	}
	if r.By == "" && subj.Panel != nil {
		switch {
		case byAllowed[metricPanelByPod]:
			out = append(out, chatFollowup{Text: "Pod bazında göster", Kind: "metric_panel_by", ExplainAsk: ask(subj, metricPanelByPod)})
		case byAllowed[metricPanelByEnv]:
			out = append(out, chatFollowup{Text: "Ortam bazında göster", Kind: "metric_panel_by", ExplainAsk: ask(subj, metricPanelByEnv)})
		}
	}
	if !subj.BrushFrom.IsZero() {
		rng := fmt.Sprintf("custom:%d-%d", subj.BrushFrom.UnixMilli(), subj.BrushTo.UnixMilli())
		q := url.Values{"range": {rng}}
		if svc := metricPanelService(r.Query); svc != "" {
			q.Set("service", svc)
		}
		for _, l := range []struct{ text, path string }{{"Bu aralığın trace'leri", "/traces"}, {"Bu aralığın logları", "/logs"}} {
			href := l.path + "?" + q.Encode()
			if isCoremetryAppPath(l.path) && strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") {
				out = append(out, chatFollowup{Text: l.text, Kind: "metric_panel_link", Href: href})
			}
		}
	}
	if len(out) > metricPanelChipsMax {
		out = out[:metricPanelChipsMax]
	}
	return out
}
