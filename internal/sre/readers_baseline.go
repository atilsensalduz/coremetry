package sre

// readers_baseline.go — v0.10.1494 (CoSRE 2d, plan §2.2–2.3): servis RED taban
// kıyası + en yakın deploy'un önce/sonra etkisi. Yalnız Context Pack açıkken
// planlanır (contextpack_settings.go); okuma, yazım yok.
//
// Kaynak sırası (tek koşuda TEK kaynak — iki pencere farklı kaynaktan
// kıyaslanmaz, v0.10.1478 "dört seri tek kaynak" emsali):
//  1. METRİK (örneklenmemiş): Service Overview metrik kipinin RED okuması
//     (mcptools.Deps.ServiceMetricRED — OTel HTTP/gRPC sunucu histogramı;
//     seam VictoriaMetrics ya da CH metric_points). Gateway tail-sampling
//     span'larda hata oranını yukarı çeker; metrik çekmez.
//  2. SPAN (örneklenmiş) yedeği: service_summary_5m (ServiceWindowRED; MV,
//     ortamlar birleşik). Neden düşüldüğü SourceNote'ta yazılır.
//
// Pencereler kodun: şimdi = araştırma çıpası [Anchor.From, Anchor.To);
// referanslar mcptools.ReferenceWindow (compare_periods ile TEK aritmetik):
// previous | week_before | day_before. Takvim sınıfı farklıysa (7 gün önce
// tatil) satır not taşır — kıyas değişmez, söylenir. Batch servis: rate farkı
// raporlanır ama hiçbir bulgu üretmez (operatör kuralı: batch yükü anomali
// değildir).
//
// Bulgu: hata oranı ≥ +%50 VE şimdi ≥ %1 (metrikte durum etiketi yoksa hiç);
// p99 ≥ +%50 VE ≥ +50 ms VE şimdi ≥ 100 ms. Skora GİRMEZ (Score bu parçayı
// okumaz; yeni kontrol ayrı /aiops kararı).

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Baseline kaynakları.
const (
	baselineSourceMetric = "metric"
	baselineSourceSpan   = "span"
)

const (
	baselineMDP         = 60
	baselineReadCap     = 3 * time.Second
	deployImpactWindow  = 10 * time.Minute
	deployImpactMinSide = 2 * time.Minute
	deployImpactLookbk  = 2 * time.Hour
)

// REDStat — tek pencerenin RED'i (istek/s, hata %, p99 ms).
type REDStat struct {
	Rate   float64 `json:"rate"`
	ErrPct float64 `json:"errPct"`
	P99Ms  float64 `json:"p99Ms"`
}

// BaselineRow — bir referans penceresi.
type BaselineRow struct {
	Ref          string   `json:"ref"` // previous | week_before | day_before
	From         int64    `json:"from"`
	To           int64    `json:"to"`
	NoData       bool     `json:"noData,omitempty"`
	Base         REDStat  `json:"base"`
	RateDeltaPct *float64 `json:"rateDeltaPct,omitempty"`
	ErrDeltaPct  *float64 `json:"errDeltaPct,omitempty"`
	P99DeltaPct  *float64 `json:"p99DeltaPct,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// DeployImpactFact — çıpaya en yakın deploy'un önce/sonra RED'i (baseline ile
// AYNI kaynak).
type DeployImpactFact struct {
	At           int64    `json:"at"`
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	WindowSec    int      `json:"windowSec"`
	BeforeOK     bool     `json:"beforeOk"`
	AfterOK      bool     `json:"afterOk"`
	Before       REDStat  `json:"before"`
	After        REDStat  `json:"after"`
	RateDeltaPct *float64 `json:"rateDeltaPct,omitempty"`
	ErrDeltaPct  *float64 `json:"errDeltaPct,omitempty"`
	P99DeltaPct  *float64 `json:"p99DeltaPct,omitempty"`
	Note         string   `json:"note,omitempty"`
}

// BaselineFacts — baseline adımının parçası.
type BaselineFacts struct {
	Source        string            `json:"source"`            // metric | span
	Backend       string            `json:"backend,omitempty"` // metrik seam: vm | ch
	SourceNote    string            `json:"sourceNote,omitempty"`
	CurOK         bool              `json:"curOk"`
	Cur           REDStat           `json:"cur"`
	ErrorsUnknown bool              `json:"errorsUnknown,omitempty"` // metrikte durum etiketi yok
	BatchService  bool              `json:"batchService,omitempty"`
	Rows          []BaselineRow     `json:"rows"`
	Impact        *DeployImpactFact `json:"impact,omitempty"`
}

// baselineRequest — adımın girdisi. Deploys dalga 2 başında deploy adımının
// gerçeklerinden dolar.
type baselineRequest struct {
	Service  string
	Env      string
	From, To time.Time
	Kinds    []string
	StartNs  int64
	Now      time.Time // planın "şimdi"si (tekrarda AsOf)
	Deploys  []DeployFact
}

// redRead — seçilmiş kaynağın pencere okuyucusu.
type redRead func(ctx context.Context, from, to time.Time) (REDStat, bool, error)

// finite — NaN / Inf olmayan.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// seriesPoints — SAF: ilk serinin sonlu noktaları (zaman → değer).
func seriesPoints(ss []chstore.SpanMetricSeries) map[int64]float64 {
	out := map[int64]float64{}
	if len(ss) == 0 {
		return out
	}
	for _, p := range ss[0].Points {
		if finite(p.Value) {
			out[p.Time] = p.Value
		}
	}
	return out
}

// redFromMetric — SAF: metrik RED serileri → pencere istatistiği. rate =
// ortalama istek/s; hata % ve p99 = rate ağırlıklı ortalama (ağırlık yoksa düz
// ortalama). Nokta yoksa ok=false.
func redFromMetric(res mcptools.MetricREDResult) (REDStat, bool) {
	rate := seriesPoints(res.Series["rate"])
	if len(rate) == 0 {
		return REDStat{}, false
	}
	var sum float64
	for _, v := range rate {
		sum += v
	}
	out := REDStat{Rate: round2(sum / float64(len(rate)))}
	wavg := func(m map[int64]float64) float64 {
		var num, den, plain float64
		for t, v := range m {
			plain += v
			if w, ok := rate[t]; ok && w > 0 {
				num += v * w
				den += w
			}
		}
		if den > 0 {
			return num / den
		}
		if len(m) > 0 {
			return plain / float64(len(m))
		}
		return 0
	}
	out.ErrPct = round2(wavg(seriesPoints(res.Series["error_rate"])))
	out.P99Ms = round2(wavg(seriesPoints(res.Series["p99"])))
	return out, true
}

// redFromSpan — SAF: service_summary_5m pencere satırı → istatistik.
func redFromSpan(r chstore.ServiceWindowRED, from, to time.Time) (REDStat, bool) {
	if r.Spans == 0 {
		return REDStat{}, false
	}
	sec := to.Sub(from).Seconds()
	if sec <= 0 {
		sec = 1
	}
	return REDStat{Rate: round2(float64(r.Spans) / sec), ErrPct: round2(float64(r.Errors) / float64(r.Spans) * 100), P99Ms: round2(r.P99Ms)}, true
}

// deltaPct — SAF: göreli değişim % (taban ≤ 0 → yok).
func deltaPct(cur, base float64) *float64 {
	if !(base > 0) || !finite(cur) {
		return nil
	}
	v := math.Round((cur-base)/base*1000) / 10
	return &v
}

// baselineSource — kaynak seçimi: metrik şimdiki pencerede veri döndürürse
// metrik, aksi hâlde span yedeği (neden SourceNote'ta).
func baselineSource(ctx context.Context, rd readers, req baselineRequest) (redRead, *BaselineFacts, error) {
	bf := &BaselineFacts{Rows: []BaselineRow{}}
	if rd.redMetric != nil {
		c, cancel := context.WithTimeout(ctx, baselineReadCap)
		res, err := rd.redMetric(c, req.Service, req.Env, req.From, req.To, baselineMDP)
		cancel()
		switch {
		case err != nil:
			bf.SourceNote = truncRunes("metrik okunamadı: "+err.Error(), 200)
		case !res.Found:
			bf.SourceNote = "servis için HTTP/gRPC sunucu metriği bulunamadı"
		default:
			if st, ok := redFromMetric(res); ok {
				bf.Source, bf.Backend, bf.CurOK, bf.Cur, bf.ErrorsUnknown = baselineSourceMetric, res.Source, true, st, res.ErrorsUnknown
				read := func(ctx context.Context, from, to time.Time) (REDStat, bool, error) {
					c, cancel := context.WithTimeout(ctx, baselineReadCap)
					defer cancel()
					r, err := rd.redMetric(c, req.Service, req.Env, from, to, baselineMDP)
					if err != nil {
						return REDStat{}, false, err
					}
					st, ok := redFromMetric(r)
					return st, ok, nil
				}
				return read, bf, nil
			}
			bf.SourceNote = "metrik bulundu ama pencerede nokta yok"
		}
	} else {
		bf.SourceNote = "metrik okuyucusu bağlı değil"
	}
	if rd.redSpan == nil {
		return nil, bf, fmt.Errorf("RED okuyucusu bağlı değil")
	}
	bf.Source = baselineSourceSpan
	if req.Env != "" {
		bf.SourceNote += " · span yedeği ortamları birleşik okur"
	}
	read := func(ctx context.Context, from, to time.Time) (REDStat, bool, error) {
		c, cancel := context.WithTimeout(ctx, baselineReadCap)
		defer cancel()
		r, err := rd.redSpan(c, req.Service, from, to)
		if err != nil {
			return REDStat{}, false, err
		}
		st, ok := redFromSpan(r, from, to)
		return st, ok, nil
	}
	st, ok, err := read(ctx, req.From, req.To)
	if err != nil {
		return nil, bf, err
	}
	bf.CurOK, bf.Cur = ok, st
	return read, bf, nil
}

// baselineRefLabelTR — referansın Türkçe adı.
func baselineRefLabelTR(ref string) string {
	switch ref {
	case BaselinePrevious:
		return "önceki pencere"
	case BaselineDayBefore:
		return "1 gün önce"
	case BaselineWeekBefore:
		return "7 gün önce"
	}
	return ref
}

// dayClassTR — takvim sınıfının Türkçe adı.
func dayClassTR(c string) string {
	switch c {
	case "weekday":
		return "hafta içi"
	case "saturday":
		return "cumartesi"
	case "sunday":
		return "pazar"
	case "holiday":
		return "tatil"
	case "half_day":
		return "yarım gün"
	case "month_end":
		return "ay sonu"
	}
	return c
}

// nearestDeploy — SAF: çıpaya en yakın deploy ([başlangıç − 2 sa, şimdi]).
func nearestDeploy(rows []DeployFact, startNs int64, now time.Time) *DeployFact {
	var best *DeployFact
	var bestD int64
	for i := range rows {
		r := rows[i]
		if r.At <= 0 || r.At < startNs-int64(deployImpactLookbk) || r.At > now.UnixNano() {
			continue
		}
		d := r.At - startNs
		if d < 0 {
			d = -d
		}
		if best == nil || d < bestD {
			best, bestD = &rows[i], d
		}
	}
	return best
}

// runBaseline — baseline adımı (adım goroutine'i; kayda dokunmaz).
func runBaseline(ctx context.Context, rd readers, req baselineRequest) stepOutcome {
	st := Step{Name: stepBaseline}
	read, bf, err := baselineSource(ctx, rd, req)
	if err != nil {
		st.State, st.Summary = StepError, truncRunes("baseline okunamadı: "+err.Error(), 400)
		return stepOutcome{step: st}
	}
	var sens chstore.AnomalySensitivityConfig
	if rd.sensitivity != nil {
		sens = rd.sensitivity()
	}
	bf.BatchService = sens.IsBatchService(req.Service)
	if !bf.CurOK {
		st.State, st.NoData = StepOK, true
		st.Summary = "veri yok: " + req.Service + " için çıpa penceresinde RED yok (" + sourceTR(bf) + ")"
		return stepOutcome{step: st, facts: &Facts{Baseline: bf}}
	}
	cur := mcptoolsWindow(req.From, req.To)
	failed := 0
	for _, kind := range req.Kinds {
		ref, err := mcptools.ReferenceWindow(kind, cur)
		if err != nil {
			continue
		}
		row := BaselineRow{Ref: kind, From: ref.From.UnixNano(), To: ref.To.UnixNano()}
		base, ok, err := read(ctx, ref.From, ref.To)
		switch {
		case err != nil:
			failed++
			row.NoData, row.Note = true, truncRunes("okunamadı: "+err.Error(), 160)
		case !ok:
			row.NoData, row.Note = true, "pencerede veri yok (saklama dışı olabilir)"
		default:
			row.Base = base
			row.RateDeltaPct, row.ErrDeltaPct, row.P99DeltaPct = deltaPct(bf.Cur.Rate, base.Rate), deltaPct(bf.Cur.ErrPct, base.ErrPct), deltaPct(bf.Cur.P99Ms, base.P99Ms)
			if a, b := anomaly.CalendarDayClass(req.From, sens.Calendar), anomaly.CalendarDayClass(ref.From, sens.Calendar); a != b {
				row.Note = "takvim: şimdi " + dayClassTR(a) + ", taban " + dayClassTR(b)
			}
		}
		bf.Rows = append(bf.Rows, row)
	}
	bf.Impact = deployImpact(ctx, read, req)
	st.State = StepOK
	if failed > 0 {
		st.State = StepPartial
	}
	st.Summary = truncRunes(baselineSummaryTR(bf), 600)
	o := stepOutcome{step: st, facts: &Facts{Baseline: bf}}
	if f := baselineFinding(req.Service, bf); f != nil {
		o.findings = []Finding{*f}
	}
	return o
}

func mcptoolsWindow(from, to time.Time) chstore.PeriodWindow {
	return chstore.PeriodWindow{From: from.UTC(), To: to.UTC()}
}

// deployImpact — çıpaya en yakın deploy'un önce/sonra RED'i (aynı kaynak).
func deployImpact(ctx context.Context, read redRead, req baselineRequest) *DeployImpactFact {
	d := nearestDeploy(req.Deploys, req.StartNs, req.Now)
	if d == nil {
		return nil
	}
	at := time.Unix(0, d.At)
	out := &DeployImpactFact{At: d.At, Name: d.Name, Version: d.Version, WindowSec: int(deployImpactWindow.Seconds())}
	afterTo := at.Add(deployImpactWindow)
	if afterTo.After(req.Now) {
		afterTo = req.Now
	}
	if afterTo.Sub(at) < deployImpactMinSide {
		out.Note = "deploy'dan sonra kıyas için yeterli süre yok"
		return out
	}
	var err error
	if out.Before, out.BeforeOK, err = read(ctx, at.Add(-deployImpactWindow), at); err != nil {
		out.Note = truncRunes("önce penceresi okunamadı: "+err.Error(), 160)
		return out
	}
	if out.After, out.AfterOK, err = read(ctx, at, afterTo); err != nil {
		out.Note = truncRunes("sonra penceresi okunamadı: "+err.Error(), 160)
		return out
	}
	if out.BeforeOK && out.AfterOK {
		out.RateDeltaPct = deltaPct(out.After.Rate, out.Before.Rate)
		out.ErrDeltaPct = deltaPct(out.After.ErrPct, out.Before.ErrPct)
		out.P99DeltaPct = deltaPct(out.After.P99Ms, out.Before.P99Ms)
	} else {
		out.Note = "önce ya da sonra penceresinde veri yok"
	}
	return out
}

// sourceTR — kaynağın tek satırlık beyanı.
func sourceTR(bf *BaselineFacts) string {
	if bf.Source == baselineSourceMetric {
		return "kaynak: metrik " + firstNonEmpty(bf.Backend, "?") + " — örneklenmemiş"
	}
	s := "kaynak: span — örneklenmiş"
	if bf.SourceNote != "" {
		s += "; " + bf.SourceNote
	}
	return s
}

func fmtDelta(d *float64) string {
	if d == nil {
		return "—"
	}
	return fmt.Sprintf("%+.0f%%", *d)
}

// baselineSummaryTR — SAF: adım özeti.
func baselineSummaryTR(bf *BaselineFacts) string {
	parts := []string{fmt.Sprintf("%s: şimdi %.4g r/s · hata %%%.2f · p99 %.0f ms", sourceTR(bf), bf.Cur.Rate, bf.Cur.ErrPct, bf.Cur.P99Ms)}
	for _, r := range bf.Rows {
		if r.NoData {
			parts = append(parts, baselineRefLabelTR(r.Ref)+": veri yok")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: rate %s hata %s p99 %s", baselineRefLabelTR(r.Ref), fmtDelta(r.RateDeltaPct), fmtDelta(r.ErrDeltaPct), fmtDelta(r.P99DeltaPct)))
	}
	return strings.Join(parts, " · ")
}

// baselineFinding — SAF: tabana göre kötüleşme bulgusu (yoksa nil). Rate
// farkı hiçbir zaman bulgu değildir (batch kuralı + yük ≠ arıza).
func baselineFinding(service string, bf *BaselineFacts) *Finding {
	if bf == nil || !bf.CurOK {
		return nil
	}
	var reasons []string
	errSeen, p99Seen := false, false
	for _, r := range bf.Rows {
		if r.NoData {
			continue
		}
		if !errSeen && !bf.ErrorsUnknown && r.ErrDeltaPct != nil && *r.ErrDeltaPct >= 50 && bf.Cur.ErrPct >= 1 {
			errSeen = true
			reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f (%s %%%.2f, %s)", bf.Cur.ErrPct, baselineRefLabelTR(r.Ref), r.Base.ErrPct, fmtDelta(r.ErrDeltaPct)))
		}
		if !p99Seen && r.P99DeltaPct != nil && *r.P99DeltaPct >= 50 && bf.Cur.P99Ms-r.Base.P99Ms >= 50 && bf.Cur.P99Ms >= 100 {
			p99Seen = true
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms (%s %.0f ms, %s)", bf.Cur.P99Ms, baselineRefLabelTR(r.Ref), r.Base.P99Ms, fmtDelta(r.P99DeltaPct)))
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	reasons = append(reasons, sourceTR(bf))
	return &Finding{Kind: "baseline", Name: service, Level: "warn", Reasons: reasons, Step: stepBaseline}
}
