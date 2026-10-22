package api

// metric_panel_stats.go — v0.10.1435 (Panelden "Bu grafiği açıkla"). Panel
// açıklamasının modelin okuduğu DETERMİNİSTİK ölçüm bloğu. SAF, tablo-testli
// (metric_panel_stats_test.go). Model noktaları yeniden saymaz; sayıları bu
// blok verir (metricChartStatsTR emsali, v0.10.1386).
//
// Bölümler:
//   - pencere: seri başına min / max (+saat) / ort / son (metricChartStats).
//   - seçili aralık (brush): içeride ort / max, dışarıda ort, Δ% (içerisi ince
//     adımlı ikinci sorgudan geliyorsa o noktalar).
//   - değişim noktaları: ardışık farkların ortalamasından ≥2σ sapan en büyük ≤3
//     sıçrama (saat + önce → sonra).
//   - seçili seri ↔ geri kalanlar: seçili serinin ortalaması diğerlerinin
//     ortalamalarının ortalamasına göre Δ%.
//   - önceki pencere (cmp): bölme anından önceki noktalar önceki pencere.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	// metricPanelStatsMaxSeries — ölçüm satırı yazılan en çok seri (seçili
	// seriler önce; gerisi "(+N seri daha)").
	metricPanelStatsMaxSeries = 8
	// metricPanelChangeSigma — değişim noktası eşiği (σ).
	metricPanelChangeSigma = 2.0
	// metricPanelChangeMax — seri başına en çok değişim noktası.
	metricPanelChangeMax = 3
)

// metricPanelStatsInput — ölçüm bloğunun girdisi (hepsi sunucu verisi).
type metricPanelStatsInput struct {
	Series      []mcptools.EvChartSeries // pencere (seçili seriler önde)
	BrushSeries []mcptools.EvChartSeries // brush'ın ince adımlı ikinci sorgusu (yoksa nil)
	Unit        string
	BrushFromMs int64 // 0 = brush yok
	BrushToMs   int64
	Selected    []string // grafikte BULUNAN seçili serilerin adları
	Missing     []string // seçili ama sonuçta (≤20 seri) olmayanlar
	CmpSplitMs  int64    // >0: bu andan önceki noktalar ÖNCEKİ pencere
	Loc         *time.Location
}

// metricPanelChange — tek değişim noktası.
type metricPanelChange struct {
	AtMs          int64
	Before, After float64
	Sigma         float64 // |fark - ort| / σ
}

// metricPanelChanges — SAF: ardışık farkların ortalamasından ≥2σ sapan en
// büyük ≤3 sıçrama (zamana göre sıralı). <4 nokta ya da σ=0 → yok.
func metricPanelChanges(pts [][2]float64) []metricPanelChange {
	if len(pts) < 4 {
		return nil
	}
	diffs := make([]float64, len(pts)-1)
	sum := 0.0
	for i := 1; i < len(pts); i++ {
		diffs[i-1] = pts[i][1] - pts[i-1][1]
		sum += diffs[i-1]
	}
	mean := sum / float64(len(diffs))
	v := 0.0
	for _, d := range diffs {
		v += (d - mean) * (d - mean)
	}
	sigma := math.Sqrt(v / float64(len(diffs)))
	if sigma == 0 || math.IsNaN(sigma) {
		return nil
	}
	var out []metricPanelChange
	for i, d := range diffs {
		if z := math.Abs(d-mean) / sigma; z >= metricPanelChangeSigma {
			out = append(out, metricPanelChange{AtMs: int64(pts[i+1][0]), Before: pts[i][1], After: pts[i+1][1], Sigma: z})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Sigma > out[b].Sigma })
	if len(out) > metricPanelChangeMax {
		out = out[:metricPanelChangeMax]
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].AtMs < out[b].AtMs })
	return out
}

// metricPanelMean — SAF: [lo, hi] ms aralığındaki (inside=true) ya da dışındaki
// noktaların ortalaması + tepe; nokta yoksa ok=false. lo=hi=0 → hepsi.
func metricPanelMean(pts [][2]float64, lo, hi int64, inside bool) (mean, peak float64, ok bool) {
	sum, n := 0.0, 0
	peak = math.Inf(-1)
	for _, p := range pts {
		t := int64(p[0])
		in := lo == 0 && hi == 0 || (t >= lo && t <= hi)
		if in != inside {
			continue
		}
		sum += p[1]
		n++
		if p[1] > peak {
			peak = p[1]
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return sum / float64(n), peak, true
}

// metricPanelDeltaPct — SAF: (a-b)/|b| yüzde metni; b=0 → "taban 0".
func metricPanelDeltaPct(a, b float64) string {
	if b == 0 {
		if a == 0 {
			return "değişim yok"
		}
		return "taban 0"
	}
	return fmt.Sprintf("%+.0f%%", (a-b)/math.Abs(b)*100)
}

// seriesByName — SAF: ada göre seri.
func seriesByName(ss []mcptools.EvChartSeries, name string) (mcptools.EvChartSeries, bool) {
	for _, s := range ss {
		if s.Name == name {
			return s, true
		}
	}
	return mcptools.EvChartSeries{}, false
}

// metricPanelStatsTR — SAF: modelin okuduğu "PANEL ÖLÇÜMÜ" bloğu + arşive
// giren kısa özet (künye notu; ≤200 rune'a çağıran kırpar).
func metricPanelStatsTR(in metricPanelStatsInput) (text, summary string) {
	loc := in.Loc
	if loc == nil {
		loc = time.UTC
	}
	hm := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("15:04") }
	f := func(v float64) string { return fmtMetricChartValue(v, in.Unit) }
	var b strings.Builder
	b.WriteString("PANEL ÖLÇÜMÜ (deterministik; model noktaları yeniden sayma):\n")
	if len(in.Series) == 0 {
		b.WriteString("- veri noktası yok\n")
		return b.String(), "veri noktası yok"
	}
	brush := in.BrushFromMs > 0 && in.BrushToMs > in.BrushFromMs
	if brush {
		fmt.Fprintf(&b, "Seçili aralık (operatörün brush'ı): %s–%s.\n", hm(in.BrushFromMs), hm(in.BrushToMs))
	}
	selected := map[string]bool{}
	for _, n := range in.Selected {
		selected[n] = true
	}
	var sums []string
	lines := 0
	for _, s := range in.Series {
		pts := s.Points
		if in.CmpSplitMs > 0 { // pencere istatistikleri yalnız GÜNCEL pencereden
			pts = metricPanelPointsFrom(s.Points, in.CmpSplitMs)
		}
		st, ok := metricChartStats(pts)
		if !ok {
			continue
		}
		if lines >= metricPanelStatsMaxSeries {
			fmt.Fprintf(&b, "- (+%d seri daha; grafikte)\n", len(in.Series)-lines)
			break
		}
		lines++
		tag := ""
		if selected[s.Name] {
			tag = " [SEÇİLİ]"
		}
		fmt.Fprintf(&b, "- %s%s: pencere min %s, max %s (%s), ort %s, son %s", s.Name, tag, f(st.Min), f(st.Max), hm(st.MaxAtMs), f(st.Avg), f(st.Last))
		if brush {
			src := pts
			if bs, ok := seriesByName(in.BrushSeries, s.Name); ok && len(bs.Points) > 0 {
				src = bs.Points
			}
			inMean, inPeak, okIn := metricPanelMean(src, in.BrushFromMs, in.BrushToMs, true)
			outMean, _, okOut := metricPanelMean(pts, in.BrushFromMs, in.BrushToMs, false)
			switch {
			case okIn && okOut:
				d := metricPanelDeltaPct(inMean, outMean)
				fmt.Fprintf(&b, "; seçili aralıkta ort %s, max %s — dışarıda ort %s (Δ %s)", f(inMean), f(inPeak), f(outMean), d)
				if len(sums) < 2 {
					sums = append(sums, fmt.Sprintf("%s: seçili aralık ort %s vs dışı %s (%s)", s.Name, f(inMean), f(outMean), d))
				}
			case okIn:
				fmt.Fprintf(&b, "; seçili aralıkta ort %s, max %s (dışında nokta yok)", f(inMean), f(inPeak))
			default:
				b.WriteString("; seçili aralıkta nokta yok")
			}
		}
		if in.CmpSplitMs > 0 {
			prev, _, okP := metricPanelMean(metricPanelPointsBefore(s.Points, in.CmpSplitMs), 0, 0, true)
			if okP {
				d := metricPanelDeltaPct(st.Avg, prev)
				fmt.Fprintf(&b, "; önceki pencere ort %s → bu pencere %s (Δ %s)", f(prev), f(st.Avg), d)
				if len(sums) < 2 {
					sums = append(sums, fmt.Sprintf("%s: önceki pencere ort %s → %s (%s)", s.Name, f(prev), f(st.Avg), d))
				}
			} else {
				b.WriteString("; önceki pencerede nokta yok")
			}
		}
		if ch := metricPanelChanges(pts); len(ch) > 0 {
			parts := make([]string, 0, len(ch))
			for _, c := range ch {
				parts = append(parts, fmt.Sprintf("%s %s → %s (%.1fσ)", hm(c.AtMs), f(c.Before), f(c.After), c.Sigma))
			}
			b.WriteString("; değişim noktaları: " + strings.Join(parts, ", "))
		}
		b.WriteString("\n")
		if len(sums) == 0 && lines == 1 && !brush && in.CmpSplitMs == 0 {
			sums = append(sums, fmt.Sprintf("%s: ort %s, max %s (%s)", s.Name, f(st.Avg), f(st.Max), hm(st.MaxAtMs)))
		}
	}
	if lines == 0 {
		b.WriteString("- veri noktası yok\n")
		return b.String(), "veri noktası yok"
	}
	// Seçili seri ↔ geri kalanlar.
	for _, name := range in.Selected {
		sel, ok := seriesByName(in.Series, name)
		if !ok {
			continue
		}
		selMean, _, ok := metricPanelMean(sel.Points, 0, 0, true)
		if !ok {
			continue
		}
		var others []float64
		for _, s := range in.Series {
			if selected[s.Name] {
				continue
			}
			if m, _, ok := metricPanelMean(s.Points, 0, 0, true); ok {
				others = append(others, m)
			}
		}
		if len(others) == 0 {
			fmt.Fprintf(&b, "Seçili seri %s grafikteki tek seri.\n", name)
			continue
		}
		o := 0.0
		for _, m := range others {
			o += m
		}
		o /= float64(len(others))
		fmt.Fprintf(&b, "Seçili seri %s: ort %s; diğer %d serinin ortalaması %s (Δ %s).\n", name, f(selMean), len(others), f(o), metricPanelDeltaPct(selMean, o))
	}
	if len(in.Missing) > 0 {
		fmt.Fprintf(&b, "Not: seçili seri %s bu sorgunun ilk %d serisi dışında kaldı — ölçülmedi.\n", strings.Join(in.Missing, ", "), metricChartMaxSeries)
	}
	return b.String(), strings.Join(sums, " · ")
}

// metricPanelPointsFrom — SAF: t ≥ split olan noktalar.
func metricPanelPointsFrom(pts [][2]float64, splitMs int64) [][2]float64 {
	out := make([][2]float64, 0, len(pts))
	for _, p := range pts {
		if int64(p[0]) >= splitMs {
			out = append(out, p)
		}
	}
	return out
}

// metricPanelPointsBefore — SAF: t < split olan noktalar.
func metricPanelPointsBefore(pts [][2]float64, splitMs int64) [][2]float64 {
	out := make([][2]float64, 0, len(pts))
	for _, p := range pts {
		if int64(p[0]) < splitMs {
			out = append(out, p)
		}
	}
	return out
}
