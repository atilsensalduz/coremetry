package api

// metric_panel_stats_test.go — v0.10.1435: panel açıklamasının saf ölçüm
// bloğu (metricPanelStatsTR) — brush içi/dışı Δ, ≥2σ değişim noktaları (≤3),
// seçili seri ↔ geri kalanlar, önceki pencere kıyası, eksik seçim notu.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func mpsSeries(name string, vals ...float64) mcptools.EvChartSeries {
	s := mcptools.EvChartSeries{Name: name}
	for i, v := range vals {
		s.Points = append(s.Points, [2]float64{float64(mpT0.Add(time.Duration(i) * time.Minute).UnixMilli()), v})
	}
	return s
}

func TestMetricPanelChanges(t *testing.T) {
	flat := []float64{10, 10, 10, 10, 10, 10, 10, 10}
	if ch := metricPanelChanges(mpsSeries("a", flat...).Points); len(ch) != 0 {
		t.Fatalf("düz seride değişim %v", ch)
	}
	step := mpsSeries("a", 10, 10, 11, 10, 10, 50, 50, 51, 50, 50, 10, 10)
	ch := metricPanelChanges(step.Points)
	if len(ch) != 2 || ch[0].Before != 10 || ch[0].After != 50 || ch[1].Before != 50 || ch[1].After != 10 {
		t.Fatalf("iki sıçrama bekleniyor: %+v", ch)
	}
	if ch[0].AtMs >= ch[1].AtMs {
		t.Fatal("zaman sırası")
	}
	var noisy []float64
	for i := 0; i < 40; i++ {
		v := 10.0
		if i%7 == 0 {
			v = 100 + float64(i)
		}
		noisy = append(noisy, v)
	}
	if got := metricPanelChanges(mpsSeries("n", noisy...).Points); len(got) > metricPanelChangeMax {
		t.Fatalf("tavan %d", len(got))
	}
	if metricPanelChanges(mpsSeries("k", 1, 2, 3).Points) != nil {
		t.Fatal("<4 nokta değişim üretmemeli")
	}
}

func TestMetricPanelStatsTR(t *testing.T) {
	a := mpsSeries("pod-a", 10, 10, 10, 10, 10, 10, 10, 50, 50, 10)
	b := mpsSeries("pod-b", 8, 8, 8, 8, 8, 8, 8, 8, 8, 8)
	bf, bt := mpT0.Add(7*time.Minute).UnixMilli(), mpT0.Add(8*time.Minute).UnixMilli()
	text, sum := metricPanelStatsTR(metricPanelStatsInput{Series: []mcptools.EvChartSeries{a, b}, Unit: "ms",
		BrushFromMs: bf, BrushToMs: bt, Selected: []string{"pod-a"}, Missing: []string{"pod-z"}, Loc: time.UTC})
	for _, want := range []string{
		"PANEL ÖLÇÜMÜ", "Seçili aralık (operatörün brush'ı): 10:07–10:08",
		"pod-a [SEÇİLİ]: pencere min 10 ms, max 50 ms (10:07)",
		"seçili aralıkta ort 50 ms, max 50 ms — dışarıda ort 10 ms (Δ +400%)",
		"değişim noktaları: 10:07 10 ms → 50 ms",
		"Seçili seri pod-a: ort 18 ms; diğer 1 serinin ortalaması 8 ms (Δ +125%)",
		"seçili seri pod-z bu sorgunun ilk 20 serisi dışında",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q yok:\n%s", want, text)
		}
	}
	if !strings.Contains(sum, "pod-a: seçili aralık ort 50 ms vs dışı 10 ms (+400%)") {
		t.Errorf("özet %q", sum)
	}
	// İnce adımlı brush serisi varsa içerisi ondan okunur.
	fine := mcptools.EvChartSeries{Name: "pod-a", Points: [][2]float64{{float64(bf), 70}, {float64(bt), 90}}}
	text, _ = metricPanelStatsTR(metricPanelStatsInput{Series: []mcptools.EvChartSeries{a}, BrushSeries: []mcptools.EvChartSeries{fine},
		Unit: "ms", BrushFromMs: bf, BrushToMs: bt, Loc: time.UTC})
	if !strings.Contains(text, "seçili aralıkta ort 80 ms, max 90 ms") {
		t.Errorf("ince brush:\n%s", text)
	}
	// Önceki pencere kıyası: bölmeden önceki noktalar önceki pencere.
	cmp := mpsSeries("x", 5, 5, 5, 5, 20, 20, 20, 20)
	text, sum = metricPanelStatsTR(metricPanelStatsInput{Series: []mcptools.EvChartSeries{cmp}, CmpSplitMs: mpT0.Add(4 * time.Minute).UnixMilli(), Loc: time.UTC})
	if !strings.Contains(text, "önceki pencere ort 5 → bu pencere 20 (Δ +300%)") || !strings.Contains(text, "pencere min 20") {
		t.Errorf("kıyas:\n%s", text)
	}
	if !strings.Contains(sum, "önceki pencere ort 5 → 20 (+300%)") {
		t.Errorf("kıyas özeti %q", sum)
	}
	// Boş girdi.
	if text, sum := metricPanelStatsTR(metricPanelStatsInput{}); !strings.Contains(text, "veri noktası yok") || sum != "veri noktası yok" {
		t.Errorf("boş: %q %q", text, sum)
	}
}

func TestMetricPanelDeltaPct(t *testing.T) {
	for _, c := range []struct {
		a, b float64
		want string
	}{{12, 10, "+20%"}, {5, 10, "-50%"}, {3, 0, "taban 0"}, {0, 0, "değişim yok"}, {-15, -10, "-50%"}} {
		if got := metricPanelDeltaPct(c.a, c.b); got != c.want {
			t.Errorf("%v/%v → %q, want %q", c.a, c.b, got, c.want)
		}
	}
}
