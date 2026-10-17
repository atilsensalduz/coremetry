package mcptools

// log_spike.go — v0.10.1284 (AI SRE Faz 1a): explain_log_spike — "log artışını
// ne açıklıyor".
//
// TEK çekirdek, üç tüketici (ReadServiceSweep deseni): RunLogSpike →
// (a) MCP / sohbet aracı explain_log_spike (JSON), (b) Logs sayfasının ✨
// yüzeyi (/api/copilot/explain-log-spike → Türkçe kanıt + anlatım), (c) Faz
// 1b orkestratörü (internal/sre, sohbet katmanı olmadan, süreç içi).
//
// Akış (bütçe 15 s; halka tavanları hopDeadline ile):
//
//	histogram  logstore.Histogram(severity bantları), ⌈pencere/60⌉ sn kova   5 s
//	           otomatik kip: yarım son kova atılır, boş kova 0; detectSpike
//	           (log_spike_score.go) tepeyi bulur. Verilen aralıkta tespit YOK,
//	           oran (artış hızı / taban hızı) < 1.2 → uyarı, kıyas yine yapılır.
//	taban      [başlangıç − B, başlangıç), B = clamp(3·L, 15 dk, 3 sa)
//	fields     logstore.FieldStatsMany artış (top10) + taban (top20), paralel  5 s
//	           (ES: pencere başına TEK _search; CH: alan başına FieldStats, aday
//	           kısaltılmış). Boyutlar: pod / cluster (alan eşlemesi rolleri),
//	           servis / namespace (yalnız servis boşken), logger, hata türü.
//	           Eşlenmemiş rol → state unmapped, okuma yok.
//	patterns   GroupBySignatureN artış + taban (500 satır örneklem, imleçsiz)    5 s
//	samples    SearchWithTimeout artış penceresi, ≤12 satır, SkipTotal         4 s
//
// Puanlama SAF (rankSpikeContributors): taban artış süresine ölçeklenir,
// "artışın %X'i Y kaynaklı". Okunamayan boyut state error/timeout/degraded
// (okunamadı ≠ yok) ve sonuç partial; kısmi sonuç önbelleğe GİRMEZ.
//
// ES istek sayısı (sıcak alan-eşleme önbelleğiyle): 1 histogram + 2 alan
// okuması + 2 desen örneklemi + 1 örnek satır = 6. Soğuk önbellekte + ≤2
// field_caps (rol eşlemesi + agg yazımı; 10 dk cache'li).
//
// ZARF (alan sırası = JSON sırası; sohbet sonucu 6000 rune'da keser, veri EN
// SONDA): tool, ok, partial, empty?, message?, error?, warnings, request,
// spike, contributors, contributorsHasMore?, dims, evidence, evidences, data.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// ExplainLogSpikeName — aracın adı (kanıt eşlemesi + sohbet evresi).
const ExplainLogSpikeName = "explain_log_spike"

const (
	logSpikeBudget        = 15 * time.Second
	logSpikeCapHist       = 5 * time.Second
	logSpikeCapFields     = 5 * time.Second
	logSpikeCapPatterns   = 5 * time.Second
	logSpikeCapSamples    = 4 * time.Second
	logSpikeSpikeTop      = 10
	logSpikeBaseTop       = 20
	logSpikePatternLimit  = 30
	logSpikePatternSample = 500
	logSpikeSampleRows    = 12
	logSpikeDataPatterns  = 8
	logSpikeHistBuckets   = 60
	logSpikeMaxGiven      = 6 * time.Hour
	logSpikeMinGiven      = time.Minute
	logSpikeMinWindow     = 15 * time.Minute
	logSpikeMaxWindow     = 24 * time.Hour
	logSpikeMaxAge        = 7 * 24 * time.Hour
	logSpikeTTLLong       = 5 * time.Minute
	logSpikeTTLShort      = 60 * time.Second
	logSpikeSettledAfter  = 10 * time.Minute
	logSpikeRoleCands     = 3
	logSpikeErrorFloor    = 17
	logSpikeSampleRunes   = 200
)

// Halka adları (kanıt + blok bağı).
const (
	logSpikeHopHistogram = "histogram"
	logSpikeHopFields    = "fields"
	logSpikeHopPatterns  = "patterns"
	logSpikeHopSamples   = "samples"
)

// Seviye kipi.
const (
	logSpikeLevelAll   = "all"
	logSpikeLevelError = "error"
)

var logSpikeWindowEnum = []string{"15m", "1h", "6h", "24h"}

var logSpikeLevelEnum = []string{logSpikeLevelAll, logSpikeLevelError}

var logSpikeLevelSynonyms = map[string]string{
	"any": "all", "hepsi": "all", "tümü": "all", "info": "all",
	"err": "error", "errors": "error", "hata": "error", "fatal": "error", "critical": "error",
}

// Aday alanlar (rolsüz boyutlar). İlki semconv / ECS; CH'de yalnız ilki okunur.
var (
	spikeLoggerFields    = []string{"log.logger", "logger.name", "logger_name"}
	spikeErrorTypeFields = []string{"exception.type", "error.type"}
)

// LogSpikeRequest — normalize argümanlar. SpikeFrom/SpikeTo ikisi birlikte
// (verilen aralık) ya da hiçbiri (otomatik tespit; From/To arama penceresi,
// sıfırsa ctx çıpası + Window).
type LogSpikeRequest struct {
	Service   string
	Env       string
	Query     string // /logs arama dili (ES query_string · CH derlenmiş)
	Level     string // all | error
	Window    string // 15m | 1h | 6h | 24h | custom (yankı)
	From, To  time.Time
	SpikeFrom time.Time
	SpikeTo   time.Time
}

// LogSpikeWindow — tespit edilen / verilen artış + taban.
type LogSpikeWindow struct {
	FromMs          int64   `json:"fromMs"`
	ToMs            int64   `json:"toMs"`
	Source          string  `json:"source"` // given | detected | level_shift
	Factor          float64 `json:"factor"` // artış hızı / taban hızı (taban 0 → 0)
	RatePerMinSpike float64 `json:"ratePerMinSpike"`
	RatePerMinBase  float64 `json:"ratePerMinBase"`
	BaseFromMs      int64   `json:"baseFromMs"`
	BaseToMs        int64   `json:"baseToMs"`
	SpikeCount      int64   `json:"spikeCount"`
	BaseCount       int64   `json:"baseCount"`
	BucketS         int     `json:"bucketS"`
}

// LogSpikeDim — boyutun durumu (okunamadı ≠ yok).
type LogSpikeDim struct {
	Dim   string `json:"dim"`
	Field string `json:"field,omitempty"`
	State string `json:"state"`
	Note  string `json:"note,omitempty"`
}

// logSpikePattern — veri: artış penceresinin desenleri (örneklem sayıları).
type logSpikePattern struct {
	Template   string `json:"template"`
	SpikeCount int64  `json:"spikeSampleCount"`
	BaseCount  int64  `json:"baseSampleCount"`
	New        bool   `json:"new,omitempty"`
	Sample     string `json:"sample,omitempty"`
}

type logSpikeData struct {
	// Histogram — [kova başı unix ms, toplam, ERROR].
	Histogram       [][3]int64        `json:"histogram"`
	HistogramFromMs int64             `json:"histogramFromMs,omitempty"`
	HistogramToMs   int64             `json:"histogramToMs,omitempty"`
	Samples         []qlRow           `json:"samples"`
	Patterns        []logSpikePattern `json:"patterns,omitempty"`
	Blocks          []pivotBlock      `json:"blocks"`
}

// LogSpikeResult — zarf; alan sırası = JSON sırası (veri EN SONDA).
type LogSpikeResult struct {
	Tool                string             `json:"tool"`
	OK                  bool               `json:"ok"`
	Partial             bool               `json:"partial"`
	Empty               bool               `json:"empty,omitempty"`
	Message             string             `json:"message,omitempty"`
	Error               *cosreError        `json:"error,omitempty"`
	Warnings            []string           `json:"warnings,omitempty"`
	Request             map[string]any     `json:"request"`
	Spike               *LogSpikeWindow    `json:"spike,omitempty"`
	Contributors        []SpikeContributor `json:"contributors"`
	ContributorsHasMore bool               `json:"contributorsHasMore,omitempty"`
	Dims                []LogSpikeDim      `json:"dims"`
	Evidence            *cosreEvidence     `json:"evidence,omitempty"`
	Evidences           []cosreHopEvidence `json:"evidences,omitempty"`
	Data                logSpikeData       `json:"data"`
}

// ── okuyucular (test dikişi) ────────────────────────────────────────────

type logSpikeReaders struct {
	backend    string
	source     string
	multi      bool // toplu alan okuması (FieldStatsMultier) var mı
	histogram  func(ctx context.Context, f logstore.Filter, bucketS int) ([]logstore.LogSeries, error)
	fieldStats func(ctx context.Context, f logstore.Filter, fields []string, limit int) (map[string]*logstore.FieldStatsResult, error)
	patterns   func(ctx context.Context, f logstore.Filter, limit, sample int) (*logstore.PatternsResult, error)
	search     func(ctx context.Context, f logstore.Filter, timeout time.Duration) (*logstore.Page, error)
	mapping    func(ctx context.Context) logstore.FieldMapping
	now        func() time.Time
}

// spikeNoCursorStore — desen örneklemesi TEK sayfa (500 satır) okur; imleç
// ilan etmek ES'te PIT açtırır (+1 istek, 2 dk segment tutma). Örneklem
// tek sayfaya sığdığı için imleç istenmez (Filter.WantCursor sözleşmesi).
type spikeNoCursorStore struct{ logstore.Store }

func (s spikeNoCursorStore) Search(ctx context.Context, f logstore.Filter) (*logstore.Page, error) {
	f.WantCursor = false
	return s.Store.Search(ctx, f)
}

func logSpikeReadersFrom(d Deps) logSpikeReaders {
	r := logSpikeReaders{now: wallNow, mapping: func(context.Context) logstore.FieldMapping {
		return logstore.FieldMapping{Roles: map[string]logstore.FieldResolution{}}
	}}
	st := d.LogStore
	if st == nil {
		return r
	}
	r.backend, r.source = st.Backend(), logsBackendSource(st)
	_, r.multi = logstore.Unwrap(st).(logstore.FieldStatsMultier)
	r.histogram = func(ctx context.Context, f logstore.Filter, bucketS int) ([]logstore.LogSeries, error) {
		return st.Histogram(ctx, f, bucketS, "severity")
	}
	r.fieldStats = func(ctx context.Context, f logstore.Filter, fields []string, limit int) (map[string]*logstore.FieldStatsResult, error) {
		return logstore.FieldStatsMany(ctx, st, f, fields, limit)
	}
	r.patterns = func(ctx context.Context, f logstore.Filter, limit, sample int) (*logstore.PatternsResult, error) {
		return logstore.GroupBySignatureN(ctx, spikeNoCursorStore{st}, f, limit, sample)
	}
	r.search = func(ctx context.Context, f logstore.Filter, timeout time.Duration) (*logstore.Page, error) {
		return logstore.SearchWithTimeout(ctx, st, f, timeout)
	}
	r.mapping = func(ctx context.Context) logstore.FieldMapping { return logFieldMapping(ctx, st, true) }
	return r
}

// ── normalizasyon ───────────────────────────────────────────────────────

func floorTime(t time.Time, step time.Duration) time.Time {
	if step <= 0 {
		return t
	}
	return time.Unix(0, t.UnixNano()/int64(step)*int64(step)).UTC()
}

func ceilTime(t time.Time, step time.Duration) time.Time {
	f := floorTime(t, step)
	if f.Equal(t) {
		return f
	}
	return f.Add(step)
}

// normalizeLogSpikeRequest — pencereleri 30 sn ızgarasına oturtur (önbellek
// anahtarı = girdiler, birebir), sınırları uygular. SAF (now verilir; ctx
// yalnız çıpa).
func normalizeLogSpikeRequest(ctx context.Context, req LogSpikeRequest, now time.Time) (LogSpikeRequest, []string, *cosreError) {
	var warnings []string
	req.Service, req.Env, req.Query = strings.TrimSpace(req.Service), strings.TrimSpace(req.Env), strings.TrimSpace(req.Query)
	if r := []rune(req.Query); len(r) > qlMaxTextRunes*2 {
		req.Query = string(r[:qlMaxTextRunes*2])
		warnings = append(warnings, "query 400 karakterde kesildi")
	}
	lvl, w := coerceEnum("level", req.Level, logSpikeLevelEnum, logSpikeLevelSynonyms, logSpikeLevelAll)
	if w != "" {
		warnings = append(warnings, w)
	}
	req.Level = lvl
	grid := time.Duration(spikeKeyBucket) * time.Second
	given := !req.SpikeFrom.IsZero() || !req.SpikeTo.IsZero()
	if given {
		if req.SpikeFrom.IsZero() || req.SpikeTo.IsZero() {
			return req, warnings, &cosreError{Code: cosreErrBadArgs, Message: "spike_start_iso ve spike_end_iso birlikte verilmeli (ya da hiçbiri: tepe otomatik bulunur)", Need: []string{"spike_start_iso", "spike_end_iso"}}
		}
		if !req.SpikeTo.After(req.SpikeFrom) {
			return req, warnings, &cosreError{Code: cosreErrBadArgs, Message: "spike_end_iso spike_start_iso'dan sonra olmalı"}
		}
		if !req.SpikeFrom.Before(now) {
			return req, warnings, &cosreError{Code: cosreErrBadArgs, Message: "artış aralığı gelecekte — spike_start_iso şimdiden önce olmalı"}
		}
		if now.Sub(req.SpikeFrom) > logSpikeMaxAge {
			return req, warnings, &cosreError{Code: cosreErrBadArgs, Message: "artış aralığı en fazla 7 gün geriye gidebilir"}
		}
		if req.SpikeTo.After(now) {
			req.SpikeTo = now
			warnings = append(warnings, "spike_end_iso şimdiden sonraydı; şimdiye kırpıldı")
		}
		if req.SpikeTo.Sub(req.SpikeFrom) > logSpikeMaxGiven {
			req.SpikeFrom = req.SpikeTo.Add(-logSpikeMaxGiven)
			warnings = append(warnings, "artış aralığı en fazla 6 saat; son 6 saati kıyaslandı")
		}
		if req.SpikeTo.Sub(req.SpikeFrom) < logSpikeMinGiven {
			req.SpikeFrom = req.SpikeTo.Add(-logSpikeMinGiven)
			warnings = append(warnings, "artış aralığı en az 1 dakika; bitişe göre 1 dakikaya genişletildi")
		}
		req.SpikeFrom = floorTime(req.SpikeFrom, grid)
		if c := ceilTime(req.SpikeTo, grid); !c.After(now) {
			req.SpikeTo = c
		}
		req.SpikeFrom, req.SpikeTo = req.SpikeFrom.UTC(), req.SpikeTo.UTC()
		req.Window, req.From, req.To = "", time.Time{}, time.Time{}
		return req, warnings, nil
	}
	if req.From.IsZero() || req.To.IsZero() {
		lbl, w := coerceEnum("window", req.Window, logSpikeWindowEnum, cosreWindowSynonyms, "1h")
		if w != "" {
			warnings = append(warnings, w)
		}
		req.Window = lbl
		req.From, req.To = rangeWindow(ctx, cosreWindowSeconds[lbl])
	} else if req.Window == "" {
		req.Window = "custom"
	}
	if req.To.After(now) {
		req.To = now
	}
	win := req.To.Sub(req.From)
	switch {
	case win < logSpikeMinWindow:
		win = logSpikeMinWindow
	case win > logSpikeMaxWindow:
		win = logSpikeMaxWindow
		warnings = append(warnings, "arama penceresi en fazla 24 saat; son 24 saat tarandı")
	}
	req.To = floorTime(req.To, grid).UTC()
	req.From = req.To.Add(-win).UTC()
	return req, warnings, nil
}

// ── boyut planı ─────────────────────────────────────────────────────────

type spikeDimPlan struct {
	dim   string
	cands []string // okunacak aday alanlar (sırayla; ilk dolu kazanır)
	state string   // ön-durum: "" = okunacak, unmapped / skipped
	note  string
}

// planSpikeDims — SAF: alan eşlemesi → boyut planı. multi=false (CH) iken
// aday listesi ilk adaya kısalır (her aday ayrı tarama).
func planSpikeDims(m logstore.FieldMapping, service, level string, multi bool) []spikeDimPlan {
	trim := func(cs []string) []string {
		if !multi && len(cs) > 1 {
			return cs[:1]
		}
		if len(cs) > logSpikeRoleCands {
			return cs[:logSpikeRoleCands]
		}
		return cs
	}
	role := func(dim, r string) spikeDimPlan {
		res := m.Role(r)
		p := spikeDimPlan{dim: dim}
		switch {
		case res.Source == logstore.FieldNone:
			p.state, p.note = hopUnmapped, r+" alanı log eşlemesinde yok — okunmadı"
		case res.Field == "":
			p.state, p.note = hopUnmapped, r+" alanı doğrulanamadı (eşleme okunamadı) — okunmadı"
		case len(res.Fields) > 1:
			p.cands = trim(append([]string(nil), res.Fields...))
		default:
			p.cands = []string{res.Field}
		}
		return p
	}
	plans := []spikeDimPlan{}
	if level == logSpikeLevelError {
		plans = append(plans, spikeDimPlan{dim: spikeDimSeverity, state: hopSkipped, note: "level=error: yalnız ERROR bandı kıyaslanıyor"})
	} else {
		plans = append(plans, spikeDimPlan{dim: spikeDimSeverity})
	}
	plans = append(plans, role(spikeDimPod, logstore.RolePod), role(spikeDimCluster, logstore.RoleCluster))
	if strings.TrimSpace(service) == "" {
		plans = append(plans, role(spikeDimService, logstore.RoleService), role(spikeDimNamespace, logstore.RoleNamespace))
	}
	plans = append(plans,
		spikeDimPlan{dim: spikeDimLogger, cands: trim(spikeLoggerFields)},
		spikeDimPlan{dim: spikeDimErrorType, cands: trim(spikeErrorTypeFields)},
		spikeDimPlan{dim: spikeDimPattern})
	return plans
}

// spikeFieldList — planın okunacak alanları (sıralı, tekrarsız).
func spikeFieldList(plans []spikeDimPlan) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range plans {
		if p.state != "" {
			continue
		}
		for _, c := range p.cands {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// ── ızgara ──────────────────────────────────────────────────────────────

// spikeGrid — SAF: seriler → kova ızgarası. starts unix ns; boş kova 0;
// keepPartial=false iken `to`yu aşan (yarım, süren) son kova düşer.
func spikeGrid(series []logstore.LogSeries, from, to time.Time, bucketS int, keepPartial bool) (starts, total, errs []int64, bands map[string][]int64) {
	if bucketS <= 0 {
		bucketS = 1
	}
	b := int64(bucketS) * int64(time.Second)
	start0 := from.UnixNano() / b * b
	end := to.UnixNano()
	for t := start0; t < end; t += b {
		if t+b > end && !keepPartial {
			break
		}
		starts = append(starts, t)
	}
	n := len(starts)
	total, errs = make([]int64, n), make([]int64, n)
	bands = map[string][]int64{}
	for _, s := range series {
		name := strings.ToUpper(strings.TrimSpace(s.Name))
		if _, ok := logSeverityBandOrder[name]; !ok {
			name = "OTHER"
		}
		arr := bands[name]
		if arr == nil {
			arr = make([]int64, n)
			bands[name] = arr
		}
		for _, p := range s.Points {
			if p.T < start0 || (p.T-start0)%b != 0 {
				continue
			}
			i := int((p.T - start0) / b)
			if i < 0 || i >= n {
				continue
			}
			arr[i] += p.V
			total[i] += p.V
			if name == "ERROR" {
				errs[i] += p.V
			}
		}
	}
	return starts, total, errs, bands
}

func sumRange(xs []int64, starts []int64, from, to time.Time) int64 {
	var s int64
	lo, hi := from.UnixNano(), to.UnixNano()
	for i, t := range starts {
		if t >= lo && t < hi {
			s += xs[i]
		}
	}
	return s
}

// ── koşucu ──────────────────────────────────────────────────────────────

// RunLogSpike — artışın açıklaması, sohbet katmanı olmadan (Faz 1b
// orkestratörü ve ✨ yüzeyi buradan çağırır). Deps.Memo varsa önbellekli
// (anahtar logspike:v1:…, kısmi sonuç saklanmaz). Hata yalnız çağıran iptal
// ettiğinde döner; her okuma arızası boyut / sonuç durumuna çevrilir.
func RunLogSpike(ctx context.Context, d Deps, req LogSpikeRequest) (LogSpikeResult, error) {
	res, body, err := logSpikeExec(ctx, d, req, nil)
	if err != nil || body == nil {
		return res, err
	}
	var out LogSpikeResult
	if err := json.Unmarshal(body, &out); err != nil {
		return LogSpikeResult{}, fmt.Errorf("log spike cache decode: %w", err)
	}
	return out, nil
}

// logSpikeExec — ortak yürütme: önbelleksiz → sonuç; önbellekli → gövde.
func logSpikeExec(ctx context.Context, d Deps, req LogSpikeRequest, warnings []string) (LogSpikeResult, []byte, error) {
	r := logSpikeReadersFrom(d)
	now := r.now().UTC()
	req, ws, cerr := normalizeLogSpikeRequest(ctx, req, now)
	warnings = append(warnings, ws...)
	if cerr != nil {
		res := newLogSpikeResult(req, warnings)
		res.Error = cerr
		return res, nil, nil
	}
	if r.histogram == nil {
		res := newLogSpikeResult(req, warnings)
		res.Error = &cosreError{Code: cosreErrNotConfigured, Message: "log arka ucu bağlı değil"}
		return res, nil, nil
	}
	m := r.mapping(ctx)
	plans := planSpikeDims(m, req.Service, req.Level, r.multi)
	if d.Memo == nil {
		res, err := runLogSpike(ctx, r, req, m, plans, warnings)
		return res, nil, err
	}
	from, to := req.From, req.To
	if !req.SpikeFrom.IsZero() {
		from, to = req.SpikeFrom, req.SpikeTo
	}
	ttl := logSpikeTTLShort
	if now.Sub(to) > logSpikeSettledAfter {
		ttl = logSpikeTTLLong // artış (en geç pencere sonu) 10 dk'dan eski: veri oturdu
	}
	key := logSpikeKey(req, from, to, r.backend, spikeFieldList(plans)) + ":w=" + fnvHex(warnings...)
	body, err := d.Memo(ctx, key, ttl, func(c context.Context) (any, bool, error) {
		res, err := runLogSpike(c, r, req, m, plans, warnings)
		if err != nil {
			return nil, false, err
		}
		return res, res.OK && !res.Partial && res.Error == nil, nil
	})
	return LogSpikeResult{}, body, err
}

func newLogSpikeResult(req LogSpikeRequest, warnings []string) LogSpikeResult {
	echo := map[string]any{"level": req.Level}
	if req.Service != "" {
		echo["service"] = req.Service
	}
	if req.Env != "" {
		echo["env"] = req.Env
	}
	if req.Query != "" {
		echo["query"] = req.Query
	}
	if !req.SpikeFrom.IsZero() {
		echo["spike_start_iso"] = req.SpikeFrom.UTC().Format(time.RFC3339)
		echo["spike_end_iso"] = req.SpikeTo.UTC().Format(time.RFC3339)
	} else if req.Window != "" {
		echo["window"] = req.Window
	}
	return LogSpikeResult{Tool: ExplainLogSpikeName, Warnings: warnings, Request: echo,
		Contributors: []SpikeContributor{}, Dims: []LogSpikeDim{},
		Data: logSpikeData{Histogram: [][3]int64{}, Samples: []qlRow{}, Blocks: []pivotBlock{}}}
}

// spikeJobOut — paralel okumanın çıktısı (kanal üzerinden; paylaşılan durum yok).
type spikeJobOut struct {
	name      string
	fields    map[string]*logstore.FieldStatsResult
	err       error
	pats      *logstore.PatternsResult
	basePats  *logstore.PatternsResult
	baseErr   error
	page      *logstore.Page
	ms        int64
	completed bool
}

func runLogSpike(ctx context.Context, r logSpikeReaders, req LogSpikeRequest, m logstore.FieldMapping, plans []spikeDimPlan, warnings []string) (LogSpikeResult, error) {
	t0 := wallNow()
	deadline := t0.Add(logSpikeBudget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	remaining := func() time.Duration { return time.Until(deadline) }
	res := newLogSpikeResult(req, warnings)
	now := r.now().UTC()
	given := !req.SpikeFrom.IsZero()
	errLevel := req.Level == logSpikeLevelError
	scope := firstNonEmpty(req.Service, "tüm servisler")

	// ── pencereler ─────────────────────────────────────────────────────
	var histFrom, histTo, sFrom, sTo, bFrom time.Time
	var bucketS int
	if given {
		L := req.SpikeTo.Sub(req.SpikeFrom)
		B := spikeBaseWindow(L)
		bucketS = int(math.Ceil((L + B).Seconds() / logSpikeHistBuckets))
		if bucketS < 1 {
			bucketS = 1
		}
		step := time.Duration(bucketS) * time.Second
		sFrom, sTo = floorTime(req.SpikeFrom, step), ceilTime(req.SpikeTo, step)
		if sTo.After(now) {
			sTo = now
		}
		B = time.Duration(math.Ceil(float64(B)/float64(step))) * step
		bFrom = sFrom.Add(-B)
		histFrom, histTo = bFrom, sTo
	} else {
		bucketS = int(math.Ceil(req.To.Sub(req.From).Seconds() / logSpikeHistBuckets))
		if bucketS < 1 {
			bucketS = 1
		}
		histFrom, histTo = floorTime(req.From, time.Duration(bucketS)*time.Second), req.To
	}
	step := time.Duration(bucketS) * time.Second
	logsReq := map[string]any{}
	if req.Service != "" {
		logsReq["service"] = req.Service
	}
	if req.Query != "" {
		logsReq["text"] = req.Query
	}
	if errLevel {
		logsReq["severity"] = qlSeverityError
	}
	baseFilter := logstore.Filter{Service: req.Service, Env: req.Env, Search: req.Query}

	// ── histogram ──────────────────────────────────────────────────────
	reportProgress(ctx, "Log", "log hacmi okunuyor: "+scope+" · "+windowTR(histFrom, histTo))
	dl := hopDeadline(remaining(), logSpikeCapHist)
	var series []logstore.LogSeries
	var herr error
	elapsed := stopwatch()
	if dl <= 0 {
		herr = context.DeadlineExceeded
	} else {
		hctx, cancel := context.WithTimeout(ctx, dl)
		hf := baseFilter
		hf.From, hf.To = histFrom, histTo
		series, herr = r.histogram(hctx, hf, bucketS)
		cancel()
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return LogSpikeResult{}, ctx.Err()
	}
	histEv := cosreHopEvidence{Hop: logSpikeHopHistogram, As: queryLogsName, Request: logsReq, cosreEvidence: cosreEvidence{
		Source: r.source, QueryKind: evKindSummary, Window: evWin(histFrom, histTo), Env: req.Env, DurationMs: elapsed(),
		Query: fmt.Sprintf("Histogram(severity bantları, kova %d sn) WHERE %s", bucketS, spikeFilterText(baseFilter))}}
	res.Evidences = append(res.Evidences, histEv)
	res.Evidence = &res.Evidences[0].cosreEvidence
	if herr != nil {
		res.Partial = true
		if req.Query != "" && errors.Is(herr, logstore.ErrBadQuery) {
			res.Error = &cosreError{Code: cosreErrBadArgs, Message: "query sözdizimi reddedildi: " + truncateRunes(herr.Error(), 200) + " — Lucene/query sözdizimini düzelt"}
			return res, nil
		}
		code := cosreClassify(herr)
		if errors.Is(herr, context.DeadlineExceeded) {
			code = cosreErrTimeout
		}
		res.Error = &cosreError{Code: code, Message: cosreErrMsg("log hacmi okunamadı (okunamadı ≠ log yok)", herr)}
		return res, nil
	}
	n := 0
	for _, s := range series {
		n += len(s.Points)
	}
	res.Evidences[0].Series = intPtr(len(series))
	res.Evidence = &res.Evidences[0].cosreEvidence
	starts, total, errs, bands := spikeGrid(series, histFrom, histTo, bucketS, given)
	res.Data.HistogramFromMs, res.Data.HistogramToMs = histFrom.UnixMilli(), histTo.UnixMilli()
	for i, t := range starts {
		res.Data.Histogram = append(res.Data.Histogram, [3]int64{t / 1e6, total[i], errs[i]})
	}
	sel := total
	if errLevel {
		sel = errs
	}

	// ── tespit ─────────────────────────────────────────────────────────
	source := spikeSourceGiven
	if !given {
		det := detectSpike(sel, bucketS)
		if !det.Found {
			res.OK, res.Empty = true, true
			what := "log"
			if errLevel {
				what = "ERROR log"
			}
			if n == 0 {
				res.Message = "veri yok: " + scope + " için " + windowTR(histFrom, histTo) + " penceresinde " + what + " bulunamadı (eşleşme yok — 'hata yok' kanıtı değil)"
			} else {
				res.Message = fmt.Sprintf("belirgin artış yok (tepe medyanın ×%s katı; eşik ×%s ve z ≥ %s) — %s penceresinde %s hacmi düz",
					fmtPctTR(det.PeakRatio), fmtPctTR(spikeMinFactor), fmtPctTR(spikeZHot), windowTR(histFrom, histTo), what)
			}
			res.Data.Blocks = logSpikeBlocks(req, res, starts, total, errs, step, time.Time{}, time.Time{}, time.Time{})
			return res, nil
		}
		sFrom = time.Unix(0, starts[det.Start]).UTC()
		sTo = time.Unix(0, starts[det.End]).UTC().Add(step)
		if det.LevelShift {
			source = spikeSourceLevelShift
			warnings = append(warnings, "artış pencerenin yarısından uzun — tek tepe değil SEVİYE DEĞİŞİMİ; taban artıştan öncesi")
		} else {
			source = spikeSourceDetected
		}
		B := spikeBaseWindow(sTo.Sub(sFrom))
		B = time.Duration(math.Ceil(float64(B)/float64(step))) * step
		bFrom = sFrom.Add(-B)
		if bFrom.Before(histFrom) {
			bFrom = histFrom
			if bFrom.Before(sFrom) {
				warnings = append(warnings, fmt.Sprintf("taban penceresi arama penceresinin başına kırpıldı: %s (istenen %s)", humanDur(sFrom.Sub(bFrom)), humanDur(B)))
			}
		}
	}
	res.Warnings = warnings
	Ls, Lb := sTo.Sub(sFrom), sFrom.Sub(bFrom)
	S, B := sumRange(sel, starts, sFrom, sTo), sumRange(sel, starts, bFrom, sFrom)
	win := &LogSpikeWindow{FromMs: sFrom.UnixMilli(), ToMs: sTo.UnixMilli(), Source: source, BaseFromMs: bFrom.UnixMilli(),
		BaseToMs: sFrom.UnixMilli(), SpikeCount: S, BaseCount: B, BucketS: bucketS}
	if Ls > 0 {
		win.RatePerMinSpike = round4(float64(S) / Ls.Minutes())
	}
	if Lb > 0 {
		win.RatePerMinBase = round4(float64(B) / Lb.Minutes())
	}
	if win.RatePerMinBase > 0 {
		win.Factor = round4(win.RatePerMinSpike / win.RatePerMinBase)
	}
	res.Spike = win
	if Lb <= 0 {
		res.OK = true
		res.Message = "artış arama penceresinin başında başlıyor — kıyaslanacak taban yok; daha geniş window ile tekrar dene"
		res.Data.Blocks = logSpikeBlocks(req, res, starts, total, errs, step, sFrom, sTo, bFrom)
		return res, nil
	}
	switch {
	case B == 0:
		res.Warnings = append(res.Warnings, "taban penceresinde log yok — oran tanımsız; her değer 'yeni' görünür")
	case given && win.Factor < spikeWeakFactor:
		res.Warnings = append(res.Warnings, fmt.Sprintf("seçilen aralıkta artış doğrulanamadı (×%s; taban hızına göre) — kıyas yine yapıldı", fmtPctTR(win.Factor)))
	}

	// ── evre 2: paralel okumalar ───────────────────────────────────────
	fields := spikeFieldList(plans)
	sevMin := uint8(0)
	if errLevel {
		sevMin = logSpikeErrorFloor
	}
	spikeF, baseF := baseFilter, baseFilter
	spikeF.From, spikeF.To, spikeF.SeverityMin = sFrom, sTo, sevMin
	baseF.From, baseF.To, baseF.SeverityMin = bFrom, sFrom, sevMin
	type job struct {
		name string
		cap  time.Duration
		run  func(context.Context, time.Duration) spikeJobOut
	}
	soft := func(c time.Duration) time.Duration { return c - time.Second }
	var jobs []job
	if len(fields) > 0 && r.fieldStats != nil {
		jobs = append(jobs,
			job{"fields_spike", logSpikeCapFields, func(c context.Context, cp time.Duration) spikeJobOut {
				f := spikeF
				f.SoftTimeout = soft(cp)
				m, err := r.fieldStats(c, f, fields, logSpikeSpikeTop)
				return spikeJobOut{fields: m, err: err}
			}},
			job{"fields_base", logSpikeCapFields, func(c context.Context, cp time.Duration) spikeJobOut {
				f := baseF
				f.SoftTimeout = soft(cp)
				m, err := r.fieldStats(c, f, fields, logSpikeBaseTop)
				return spikeJobOut{fields: m, err: err}
			}})
	}
	if r.patterns != nil {
		jobs = append(jobs, job{"patterns", logSpikeCapPatterns, func(c context.Context, cp time.Duration) spikeJobOut {
			type pr struct {
				p   *logstore.PatternsResult
				err error
			}
			bch := make(chan pr, 1)
			go func() {
				f := baseF
				f.SoftTimeout = soft(cp)
				p, err := r.patterns(c, f, logstore.PatternsMaxGroups, logSpikePatternSample)
				bch <- pr{p, err}
			}()
			f := spikeF
			f.SoftTimeout = soft(cp)
			p, err := r.patterns(c, f, logSpikePatternLimit, logSpikePatternSample)
			b := <-bch
			return spikeJobOut{pats: p, err: err, basePats: b.p, baseErr: b.err}
		}})
	}
	if r.search != nil {
		jobs = append(jobs, job{"samples", logSpikeCapSamples, func(c context.Context, cp time.Duration) spikeJobOut {
			f := spikeF
			f.Limit, f.SkipTotal, f.SoftTimeout = logSpikeSampleRows, true, soft(cp)
			p, err := r.search(c, f, cp)
			return spikeJobOut{page: p, err: err}
		}})
	}
	reportProgress(ctx, "Log", fmt.Sprintf("artış %s (×%s): pod · seviye · desen payları kıyaslanıyor", windowTR(sFrom, sTo), fmtPctTR(win.Factor)))
	outs := map[string]spikeJobOut{}
	ch := make(chan spikeJobOut, len(jobs))
	pending := 0
	for _, j := range jobs {
		dl := hopDeadline(remaining(), j.cap)
		if dl <= 0 {
			outs[j.name] = spikeJobOut{name: j.name, err: context.DeadlineExceeded}
			continue
		}
		pending++
		go func(j job, dl time.Duration) {
			jctx, cancel := context.WithTimeout(ctx, dl)
			defer cancel()
			el := stopwatch()
			o := j.run(jctx, dl)
			o.name, o.ms, o.completed = j.name, el(), true
			ch <- o
		}(j, dl)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
wait:
	for pending > 0 {
		select {
		case o := <-ch:
			outs[o.name] = o
			pending--
		case <-timer.C:
			break wait
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return LogSpikeResult{}, ctx.Err()
			}
			break wait
		}
	}
	for _, j := range jobs {
		if _, ok := outs[j.name]; !ok {
			outs[j.name] = spikeJobOut{name: j.name, err: context.DeadlineExceeded}
		}
	}

	// ── boyutlar ───────────────────────────────────────────────────────
	totals := spikeTotals{S: S, B: B, Ls: Ls, Lb: Lb}
	var inputs []spikeDimInput
	fs, fb := outs["fields_spike"], outs["fields_base"]
	pt := outs["patterns"]
	for _, p := range plans {
		dim := LogSpikeDim{Dim: p.dim, State: p.state, Note: p.note}
		if p.state != "" {
			res.Dims = append(res.Dims, dim)
			continue
		}
		switch p.dim {
		case spikeDimSeverity:
			dim.Field, dim.State = "severity", hopOK
			sp, bp := spikeBandStats(bands, starts, sFrom, sTo, bFrom)
			if sp.Total == 0 {
				dim.State, dim.Note = hopEmpty, "artış penceresinde log yok"
				break
			}
			inputs = append(inputs, spikeDimInput{Dim: p.dim, Field: "severity", Spike: sp, Base: bp})
		case spikeDimPattern:
			in, d := spikePatternInput(pt, totals, sFrom)
			dim = d
			if in != nil {
				inputs = append(inputs, *in)
			}
			if dim.State != hopOK && dim.State != hopEmpty {
				res.Partial = true
			}
		default:
			in, d := spikeFieldInput(p, fs, fb)
			dim = d
			if in != nil {
				inputs = append(inputs, *in)
			}
			if dim.State != hopOK && dim.State != hopEmpty && dim.State != hopUnmapped {
				res.Partial = true
			}
		}
		res.Dims = append(res.Dims, dim)
	}
	res.Contributors, res.ContributorsHasMore = rankSpikeContributors(inputs, totals, spikeContribMax)

	// ── kanıtlar ───────────────────────────────────────────────────────
	if len(fields) > 0 && r.fieldStats != nil {
		mode := "alan başına okuma"
		if r.multi {
			mode = "pencere başına TEK okuma"
		}
		rows := 0
		for _, d := range res.Dims {
			if d.State == hopOK && d.Dim != spikeDimSeverity && d.Dim != spikeDimPattern {
				rows++
			}
		}
		res.Evidences = append(res.Evidences, cosreHopEvidence{Hop: logSpikeHopFields, As: queryLogsName, Request: logsReq, cosreEvidence: cosreEvidence{
			Source: r.source, QueryKind: evKindSummary, Window: evWin(sFrom, sTo), Env: req.Env, DurationMs: maxI64(fs.ms, fb.ms), Rows: intPtr(rows),
			Query: fmt.Sprintf("FieldStatsMany(%s) artış top%d · taban [%s] top%d (%s) WHERE %s", strings.Join(fields, ", "), logSpikeSpikeTop,
				windowTR(bFrom, sFrom), logSpikeBaseTop, mode, spikeFilterText(spikeF))}})
	}
	if r.patterns != nil {
		pr := 0
		if pt.pats != nil {
			pr = len(pt.pats.Groups)
		}
		res.Evidences = append(res.Evidences, cosreHopEvidence{Hop: logSpikeHopPatterns, As: queryLogsName, Request: logsReq, cosreEvidence: cosreEvidence{
			Source: r.source, QueryKind: evKindSummary, Window: evWin(sFrom, sTo), Env: req.Env, DurationMs: pt.ms, Rows: intPtr(pr),
			Query: fmt.Sprintf("GroupBySignatureN(≤%d satır örneklem, en yeniden) artış + taban [%s] WHERE %s", logSpikePatternSample, windowTR(bFrom, sFrom), spikeFilterText(spikeF))}})
	}
	sm := outs["samples"]
	if r.search != nil {
		sf := spikeF
		sf.Limit = logSpikeSampleRows
		ev := cosreHopEvidence{Hop: logSpikeHopSamples, As: queryLogsName, Request: logsReq, cosreEvidence: cosreEvidence{
			Source: r.source, QueryKind: evKindSummary, Window: evWin(sFrom, sTo), Env: req.Env, DurationMs: sm.ms, Query: qlQueryText(r.backend, sf)}}
		if sm.err == nil && sm.page != nil {
			res.Data.Samples = spikeSampleRows(sm.page, m)
			ev.Rows = intPtr(len(res.Data.Samples))
			res.Warnings = append(res.Warnings, spikePageWarnings(sm.page)...)
		} else if sm.err != nil {
			res.Warnings = append(res.Warnings, cosreErrMsg("örnek satırlar okunamadı", sm.err))
		}
		res.Evidences = append(res.Evidences, ev)
	}
	if len(res.Evidences) > compositeEvidenceMax {
		res.Evidences = res.Evidences[:compositeEvidenceMax]
	}
	res.Evidence = &res.Evidences[0].cosreEvidence
	res.Data.Patterns = spikeDataPatterns(pt.pats)

	res.OK = true
	if len(res.Contributors) == 0 {
		res.Message = fmt.Sprintf("artış var (×%s) ama tek bir boyutta yoğunlaşmıyor (eşik: artışın ≥%%%d'u, ≥%d satır) — artış yayılmış ya da okunamayan boyutta",
			fmtPctTR(win.Factor), int(spikeMinShare*100), spikeMinSupport)
	}
	res.Data.Blocks = logSpikeBlocks(req, res, starts, total, errs, step, sFrom, sTo, bFrom)
	return res, nil
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func humanDur(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d sa", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%d dk", int(math.Round(d.Minutes())))
	}
	return fmt.Sprintf("%d sn", int(d.Seconds()))
}

// spikeFilterText — kanıt metni (deterministik; kimlik bilgisi yok).
func spikeFilterText(f logstore.Filter) string {
	var parts []string
	if f.Service != "" {
		parts = append(parts, "service = "+sqlQuote(f.Service))
	}
	if f.Env != "" {
		parts = append(parts, "env = "+sqlQuote(f.Env))
	}
	if f.Search != "" {
		parts = append(parts, "query: "+truncateRunes(f.Search, 120))
	}
	if f.SeverityMin > 0 {
		parts = append(parts, fmt.Sprintf("severity ≥ %d", f.SeverityMin))
	}
	if len(parts) == 0 {
		return "(süzgeç yok — tüm loglar)"
	}
	return strings.Join(parts, " AND ")
}

// spikeBandStats — SAF: severity bantlarından iki pencerenin "alan" sonucu.
func spikeBandStats(bands map[string][]int64, starts []int64, sFrom, sTo, bFrom time.Time) (*logstore.FieldStatsResult, *logstore.FieldStatsResult) {
	sp := &logstore.FieldStatsResult{Field: "severity", Values: []logstore.FieldValueCount{}}
	bp := &logstore.FieldStatsResult{Field: "severity", Values: []logstore.FieldValueCount{}}
	for _, name := range sortedKeys(bands) {
		s, b := sumRange(bands[name], starts, sFrom, sTo), sumRange(bands[name], starts, bFrom, sFrom)
		if s > 0 {
			sp.Values = append(sp.Values, logstore.FieldValueCount{Value: name, Count: s})
			sp.Total += s
		}
		if b > 0 {
			bp.Values = append(bp.Values, logstore.FieldValueCount{Value: name, Count: b})
			bp.Total += b
		}
	}
	sort.SliceStable(sp.Values, func(i, j int) bool { return sp.Values[i].Count > sp.Values[j].Count })
	sort.SliceStable(bp.Values, func(i, j int) bool { return bp.Values[i].Count > bp.Values[j].Count })
	return sp, bp
}

func spikeFailState(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded) || cosreClassify(err) == cosreErrTimeout:
		return hopTimeout
	case errors.Is(err, logstore.ErrBackendSlow):
		return hopDegraded
	}
	if logsDegradedState(sourcestate.FromError(logsSource, "", err).State) {
		return hopDegraded
	}
	return hopError
}

// spikeFieldInput — SAF: alan boyutunun durumu + puanlama girdisi.
func spikeFieldInput(p spikeDimPlan, fs, fb spikeJobOut) (*spikeDimInput, LogSpikeDim) {
	dim := LogSpikeDim{Dim: p.dim, Field: strings.Join(p.cands, "|")}
	if len(fs.fields) == 0 && fs.err != nil {
		dim.State, dim.Note = spikeFailState(fs.err), cosreErrMsg("artış penceresi okunamadı (okunamadı ≠ yok)", fs.err)
		return nil, dim
	}
	var chosen string
	for _, c := range p.cands {
		if r := fs.fields[c]; r != nil && len(r.Values) > 0 {
			chosen = c
			break
		}
	}
	if chosen == "" {
		missing := false
		for _, c := range p.cands {
			if fs.fields[c] == nil {
				missing = true
			}
		}
		if missing && fs.err != nil {
			dim.State, dim.Note = spikeFailState(fs.err), cosreErrMsg("alan okunamadı", fs.err)
			return nil, dim
		}
		dim.State, dim.Note = hopEmpty, "değer yok: artış penceresinde "+dim.Field+" taşıyan log yok"
		return nil, dim
	}
	dim.Field = chosen
	sp := fs.fields[chosen]
	bp := fb.fields[chosen]
	if bp == nil {
		if fb.err != nil {
			dim.State, dim.Note = spikeFailState(fb.err), cosreErrMsg("taban okunamadı — kıyas yok", fb.err)
		} else {
			dim.State, dim.Note = hopPartial, "taban sonucu yok — kıyas yok"
		}
		return nil, dim
	}
	dim.State = hopOK
	if sp.Partial || bp.Partial {
		dim.State, dim.Note = hopPartial, "arka uç kısmi cevap verdi (yumuşak zaman aşımı / shard) — paylar alt kümeden"
	}
	return &spikeDimInput{Dim: p.dim, Field: chosen, Spike: sp, Base: bp, BaseLimit: logSpikeBaseTop}, dim
}

// spikePatternInput — SAF: desen örneklemleri → ölçekli sayılar.
func spikePatternInput(pt spikeJobOut, t spikeTotals, sFrom time.Time) (*spikeDimInput, LogSpikeDim) {
	dim := LogSpikeDim{Dim: spikeDimPattern, Field: "body (NormalizeSignature)"}
	if pt.pats == nil {
		err := pt.err
		if err == nil {
			err = context.DeadlineExceeded
		}
		dim.State, dim.Note = spikeFailState(err), cosreErrMsg("desen örneklemi okunamadı", err)
		return nil, dim
	}
	if pt.basePats == nil {
		err := pt.baseErr
		if err == nil {
			err = errors.New("taban örneklemi yok")
		}
		dim.State, dim.Note = spikeFailState(err), cosreErrMsg("taban desen örneklemi okunamadı — kıyas yok", err)
		return nil, dim
	}
	cur := *pt.pats
	cur.Groups = append([]logstore.SignatureGroup(nil), pt.pats.Groups...)
	logstore.JoinPatternBaseline(&cur, pt.basePats)
	if cur.Sampled == 0 || len(cur.Groups) == 0 {
		dim.State, dim.Note = hopEmpty, "artış penceresinde örneklenen satır yok"
		return nil, dim
	}
	dim.State = hopOK
	notes := []string{fmt.Sprintf("örneklem: artış %d / taban %d satır; sayılar pencere toplamına ölçeklendi", cur.Sampled, pt.basePats.Sampled)}
	if cur.CoveredFromNs > 0 && cur.CoveredFromNs > sFrom.UnixNano() && cur.Sampled >= logSpikePatternSample {
		notes = append(notes, "örneklem en yeni satırlardan; artışın yalnız "+time.Unix(0, cur.CoveredFromNs).UTC().Format("15:04:05")+" sonrasını kapsıyor")
	}
	dim.Note = strings.Join(notes, "; ")
	sp := &logstore.FieldStatsResult{Field: "pattern", Total: t.S, Values: []logstore.FieldValueCount{}}
	raw := map[string]int64{}
	for _, g := range cur.Groups {
		v := int64(math.Round(float64(g.Count) * float64(t.S) / float64(cur.Sampled)))
		sp.Values = append(sp.Values, logstore.FieldValueCount{Value: g.Template, Count: v})
		raw[g.Template] = g.Count
	}
	bp := &logstore.FieldStatsResult{Field: "pattern", Total: t.B, Values: []logstore.FieldValueCount{}}
	if pt.basePats.Sampled > 0 {
		for _, g := range pt.basePats.Groups {
			v := int64(math.Round(float64(g.Count) * float64(t.B) / float64(pt.basePats.Sampled)))
			bp.Values = append(bp.Values, logstore.FieldValueCount{Value: g.Template, Count: v})
		}
	}
	in := &spikeDimInput{Dim: spikeDimPattern, Field: "pattern", Spike: sp, Base: bp, Raw: raw, RawMin: spikePatternMin}
	if pt.basePats.Distinct > len(pt.basePats.Groups) {
		in.BaseLimit = len(pt.basePats.Groups) // taban grup listesi kesik: yokluk "yeni" değil
	}
	return in, dim
}

func spikeDataPatterns(p *logstore.PatternsResult) []logSpikePattern {
	if p == nil {
		return nil
	}
	var out []logSpikePattern
	for _, g := range p.Groups {
		if len(out) >= logSpikeDataPatterns {
			break
		}
		out = append(out, logSpikePattern{Template: truncateRunes(g.Template, spikeValueRunes*2), SpikeCount: g.Count, BaseCount: g.PrevCount, New: g.New,
			Sample: promptfmt.FenceSafe(truncateRunes(strings.Join(strings.Fields(g.Sample), " "), logSpikeSampleRunes))})
	}
	return out
}

func spikeSampleRows(page *logstore.Page, m logstore.FieldMapping) []qlRow {
	out := []qlRow{}
	for _, rec := range page.Logs {
		if rec == nil || len(out) >= logSpikeSampleRows {
			continue
		}
		out = append(out, qlRow{TsMs: rec.Timestamp / 1e6, Severity: logSeverityLabel(rec), Service: rec.ServiceName,
			Pod: logstore.RoleValue(rec, logstore.RolePod, m), TraceID: rec.TraceID,
			Body: promptfmt.FenceSafe(truncateRunes(rec.Body, qlBodyMaxRunes))})
	}
	return out
}

// spikePageWarnings — örnek okumasının dürüstlük bayrakları (aynı süzgeç
// alan okumalarına da gitti; uygulanamayan süzgeç sayıları da etkiler).
func spikePageWarnings(p *logstore.Page) []string {
	var w []string
	if p.EnvUnapplied {
		w = append(w, "env süzgeci uygulanamadı (log arka ucunda ortam alanı yok) — sayılar tüm ortamların toplamı")
	}
	for _, f := range p.UnappliedFilters {
		if f == logstore.FilterSeverity {
			w = append(w, "seviye süzgeci uygulanamadı (sayısal seviye alanı yok) — alan payları tüm seviyelerden")
			continue
		}
		w = append(w, f+" süzgeci uygulanamadı")
	}
	return w
}

// ── bloklar ─────────────────────────────────────────────────────────────

// logSpikeBlocks — ≤3 blok: grafik (toplam + ERROR, taban/artış bölgeli),
// katkı tablosu, örnek satır tablosu. Ek sorgu yok.
func logSpikeBlocks(req LogSpikeRequest, res LogSpikeResult, starts, total, errs []int64, step time.Duration, sFrom, sTo, bFrom time.Time) []pivotBlock {
	out := []pivotBlock{}
	scope := firstNonEmpty(req.Service, "tüm servisler")
	if len(starts) > 0 {
		c := EvChartPayload{Title: scope + " · log hacmi", Series: []EvChartSeries{}}
		if len(starts) > 0 {
			c.Window = EvBlockWindow{FromNs: starts[0], ToNs: starts[len(starts)-1] + int64(step)}
		}
		lg := EvChartSeries{Name: "logs", Unit: "count", Points: [][2]float64{}}
		er := EvChartSeries{Name: "errors", Unit: "count", Points: [][2]float64{}}
		for i, t := range starts {
			lg.Points = append(lg.Points, [2]float64{float64(t / 1e6), float64(total[i])})
			er.Points = append(er.Points, [2]float64{float64(t / 1e6), float64(errs[i])})
		}
		c.Series = append(c.Series, sweepTrimSeries(lg), sweepTrimSeries(er))
		if !sFrom.IsZero() {
			if bFrom.Before(sFrom) {
				c.Regions = append(c.Regions, EvChartRegion{FromMs: bFrom.UnixMilli(), ToMs: sFrom.UnixMilli(), Label: "taban", Tone: "muted"})
			}
			c.Regions = append(c.Regions, EvChartRegion{FromMs: sFrom.UnixMilli(), ToMs: sTo.UnixMilli(), Label: "artış", Tone: "err"})
		}
		out = append(out, pivotBlock{Hop: logSpikeHopHistogram, Type: blocks.TypeChart, Payload: c})
	}
	if len(res.Contributors) > 0 {
		hop := logSpikeHopHistogram
		for _, ev := range res.Evidences {
			if ev.Hop == logSpikeHopFields {
				hop = logSpikeHopFields
				break
			}
			if ev.Hop == logSpikeHopPatterns {
				hop = logSpikeHopPatterns
			}
		}
		t := EvTablePayload{Title: scope + " · artışı ne açıklıyor", Columns: []EvTableColumn{
			{Key: "dim", Label: "Boyut", Kind: "text"}, {Key: "value", Label: "Değer", Kind: "text"},
			{Key: "share", Label: "Artıştaki pay %", Kind: "num"}, {Key: "spikePct", Label: "Artışta %", Kind: "num"},
			{Key: "basePct", Label: "Normalde %", Kind: "num"}, {Key: "reason", Label: "Gerekçe", Kind: "text"}}, Rows: [][]any{}}
		for _, c := range res.Contributors {
			var base any = c.BasePct
			switch {
			case c.New:
				base = "yeni"
			case c.BaseUnknown:
				base = "≤ " + fmtPctTR(c.BasePct)
			}
			t.Rows = append(t.Rows, []any{spikeDimTR(c.Dim), truncateRunes(c.Value, spikeValueRunes), c.SharePct, c.SpikePct, base, c.Reason})
		}
		out = append(out, pivotBlock{Hop: hop, Type: blocks.TypeTable, Payload: t})
	}
	if len(res.Data.Samples) > 0 {
		t := EvTablePayload{Title: scope + " · artıştan örnek satırlar", Columns: []EvTableColumn{
			{Key: "time", Label: "Zaman", Kind: "time"}, {Key: "severity", Label: "Seviye", Kind: "severity"},
			{Key: "source", Label: "Pod / servis", Kind: "text"}, {Key: "body", Label: "Mesaj", Kind: "text"},
			{Key: "traceId", Label: "Trace", Kind: "trace"}}, Rows: [][]any{}}
		for _, l := range res.Data.Samples {
			t.Rows = append(t.Rows, []any{l.TsMs, l.Severity, firstNonEmpty(l.Pod, l.Service), truncateRunes(l.Body, evBlockBodyRunes-1), l.TraceID})
		}
		out = append(out, pivotBlock{Hop: logSpikeHopSamples, Type: blocks.TypeTable, Payload: t})
	}
	return out
}

// ── araç ────────────────────────────────────────────────────────────────

// parseLogSpikeRequest — hoşgörülü argüman çözümü (cosreArgs). SAF.
func parseLogSpikeRequest(raw json.RawMessage) (LogSpikeRequest, []string, *cosreError) {
	a, warnings := parseCosreArgs(raw)
	req := LogSpikeRequest{
		Service: a.str("service", "service_name", "servis"),
		Env:     a.str("env", "environment", "ortam"),
		Query:   a.str("query", "q", "search", "text"),
		Level:   a.str("level", "severity", "seviye"),
		Window:  a.str("window", "range", "preset"),
	}
	parse := func(keys ...string) (time.Time, *cosreError) {
		s := a.str(keys...)
		if s == "" {
			return time.Time{}, nil
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, &cosreError{Code: cosreErrBadArgs, Message: fmt.Sprintf("%s RFC3339 değil (%q) — örn. 2026-10-17T14:05:00Z", keys[0], s)}
		}
		return t.UTC(), nil
	}
	var cerr *cosreError
	if req.SpikeFrom, cerr = parse("spike_start_iso", "spike_start", "from_iso"); cerr != nil {
		return req, warnings, cerr
	}
	if req.SpikeTo, cerr = parse("spike_end_iso", "spike_end", "to_iso"); cerr != nil {
		return req, warnings, cerr
	}
	return req, warnings, nil
}

func explainLogSpikeTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "explain_log_spike", // = ExplainLogSpikeName; literal: cost_claim_test tarayıcısı adı literalden okur
		ShortDescription: "Log ARTIŞINI açıklar: tepeyi bulur (ya da verilen aralık), önceki pencereyle pod/seviye/desen payını kıyaslar ('artışın %X'i Y kaynaklı').",
		MinRole:          "", // REST eşleri /api/logs/timeseries + /api/logs/fieldstats viewer'a açık
		Description: "Explain WHAT drove a log VOLUME SPIKE, in one deterministic call. Without spike_start_iso/spike_end_iso it auto-detects " +
			"the most pronounced burst in the search window (one severity-banded histogram, ~60 buckets; robust z-score vs the median, " +
			"≥1.5× the median and a Poisson floor — a flat series answers empty=true 'belirgin artış yok'); with them it compares exactly " +
			"that interval (≤6 h). The spike is compared with the PRIOR window [start − B, start), B = clamp(3×spike length, 15 min, 3 h), " +
			"scaled to the spike length, across dimensions: severity band, pod, cluster (log field-mapping roles; unmapped → state unmapped, " +
			"not read), service + namespace (only when service is empty), logger, error type and message PATTERN (body signature). " +
			"contributors (≤5, ≤2 per dimension) rank the values carrying the EXCESS: sharePct = share of the increase, spikePct vs basePct, " +
			"new (absent before) or baseUnknown (baseline top-20 full → 'normally ≤ x%'); each has a Turkish reason ('artışın %62'si pod X kaynaklı'). " +
			"level=error explains the ERROR band only. COST: 1 histogram + 2 field-stats reads + ≤2 sampled reads (≤500 lines each, newest first) " +
			"+ 1 read of ≤12 sample lines; cached 60 s (5 min once the spike is >10 min old). Every dimension reports state " +
			"ok|empty|partial|unmapped|error|timeout|degraded|skipped — 'could not read' is NOT 'not there'. Read-only. " +
			"Use for 'loglar neden arttı / bu log patlamasını ne açıklıyor'; for the raw volume curve use get_log_histogram, for the lines use query_logs.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":         map[string]any{"type": "string", "description": "Exact service name (list_services). Empty = the whole estate (then service and namespace are dimensions)."},
				"env":             map[string]any{"type": "string", "description": "Deployment environment (e.g. prod, uat, int, prep) via the log backend's env field."},
				"query":           map[string]any{"type": "string", "description": "Optional narrowing in the /logs search language (Elasticsearch query_string / ClickHouse compiled field syntax). Empty = all lines."},
				"level":           map[string]any{"type": "string", "enum": logSpikeLevelEnum, "description": "all = total volume (default); error = the ERROR band only."},
				"window":          map[string]any{"type": "string", "enum": logSpikeWindowEnum, "description": "Search window for auto-detection, ending at the chat anchor. Default 1h."},
				"spike_start_iso": map[string]any{"type": "string", "description": "Explicit spike start (RFC3339 UTC, e.g. 2026-10-17T14:05:00Z). Give BOTH or neither; ≤6 h, not in the future."},
				"spike_end_iso":   map[string]any{"type": "string", "description": "Explicit spike end (RFC3339 UTC). Give BOTH or neither."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			req, warnings, cerr := parseLogSpikeRequest(raw)
			if cerr != nil {
				res := newLogSpikeResult(req, warnings)
				res.Error = cerr
				return res, nil
			}
			res, body, err := logSpikeExec(ctx, d, req, warnings)
			if err != nil {
				return nil, err
			}
			if body != nil {
				return json.RawMessage(body), nil
			}
			return res, nil
		},
	}
}
