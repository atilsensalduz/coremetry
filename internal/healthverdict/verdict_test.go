package healthverdict

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)
// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu

import (
	"strings"
	"testing"
)

// TestClassifyRED — satır hükmü servis hükmüyle AYNI eşikler (+ hacim tabanı).
func TestClassifyRED(t *testing.T) {
	base := RED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}
	for _, c := range []struct {
		name    string
		cur     RED
		hasBase bool
		level   string
		inWhy   string
	}{
		{"sağlıklı", RED{Spans: 900, ErrorRate: 0.6, P99Ms: 210}, true, LevelOK, ""},
		{"hata +1 puan uyarı", RED{Spans: 900, ErrorRate: 1.6, P99Ms: 210}, true, LevelWarn, "+1.1 puan"},
		{"hata +5 puan kritik", RED{Spans: 900, ErrorRate: 5.5, P99Ms: 210}, true, LevelCrit, "baseline %0.50"},
		{"mutlak %5 kritik", RED{Spans: 900, ErrorRate: 5, P99Ms: 210}, true, LevelCrit, "%5.00"},
		{"p99 +%25 uyarı", RED{Spans: 900, ErrorRate: 0.5, P99Ms: 250}, true, LevelWarn, "+%25"},
		{"p99 2× ve ≥200 kritik", RED{Spans: 900, ErrorRate: 0.5, P99Ms: 400}, true, LevelCrit, "2.0×"},
		{"hacim tabanı: 100 span puanlanmaz", RED{Spans: 100, ErrorRate: 50, P99Ms: 9000}, true, LevelOK, "düşük hacim"},
		{"101 span puanlanır", RED{Spans: 101, ErrorRate: 50, P99Ms: 210}, true, LevelCrit, ""},
		{"baseline yok: yalnız mutlak", RED{Spans: 900, ErrorRate: 3, P99Ms: 5000}, false, LevelOK, ""},
		{"baseline yok: ≥%5 kritik", RED{Spans: 900, ErrorRate: 7, P99Ms: 5000}, false, LevelCrit, "baseline yok"},
	} {
		l, why, _, _ := Row(c.cur, base, c.hasBase)
		if l != c.level || (c.inWhy != "" && !strings.Contains(strings.Join(why, ";"), c.inWhy)) {
			t.Errorf("%s: %s %v", c.name, l, why)
		}
	}
	// p99 2× ama < 200 ms → kritik değil, uyarı (servis hükmüyle aynı).
	if l, _, _, _ := Row(RED{Spans: 900, P99Ms: 150}, RED{Spans: 900, P99Ms: 60}, true); l != LevelWarn {
		t.Errorf("p99 150/60: %s", l)
	}
}

// TestRowMatchesServiceThresholds — aynı girdide satır seviyesi ↔ servis etiketi.
func TestRowMatchesServiceThresholds(t *testing.T) {
	base := RED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}
	for _, cur := range []RED{
		{Spans: 900, ErrorRate: 0.6, P99Ms: 210}, {Spans: 900, ErrorRate: 2.5, P99Ms: 210},
		{Spans: 900, ErrorRate: 6, P99Ms: 210}, {Spans: 900, ErrorRate: 0.5, P99Ms: 260},
		{Spans: 900, ErrorRate: 0.5, P99Ms: 500},
	} {
		sl, _, _ := Service(cur, base, nil, 0, 0)
		rl, _, _, _ := Row(cur, base, true)
		if LabelForLevel(rl) != sl {
			t.Errorf("%+v: servis %s ≠ satır %s", cur, sl, rl)
		}
	}
	// v0.10.1299 — gecikme kapıları iki yolda da aynı.
	for _, p := range [][2]RED{
		{{Spans: 900, ErrorRate: 0.5, P99Ms: 3}, {Spans: 1000, ErrorRate: 0.5, P99Ms: 2}},
		{{Spans: 250, ErrorRate: 0.5, P99Ms: 600}, {Spans: 250, ErrorRate: 0.5, P99Ms: 200}},
		{{Spans: 900, ErrorRate: 0.5, P99Ms: 130}, {Spans: 1000, ErrorRate: 0.5, P99Ms: 100}},
		{{Spans: 900, ErrorRate: 0.5, P99Ms: 300}, {Spans: 1000, ErrorRate: 0.5, P99Ms: 200}},
	} {
		sl, _, _ := Service(p[0], p[1], nil, 0, 0)
		rl, _, _, _ := Row(p[0], p[1], true)
		if LabelForLevel(rl) != sl {
			t.Errorf("%+v: servis %s ≠ satır %s", p, sl, rl)
		}
	}
}

func TestLabelRanks(t *testing.T) {
	if !(LabelRank(LabelCrit) > LabelRank(LabelWarn) && LabelRank(LabelWarn) > LabelRank(LabelHealthy) && LabelRank(LabelHealthy) > LabelRank(LabelNoData)) {
		t.Fatal("etiket sırası bozuk")
	}
	if !(LevelRank(LevelCrit) < LevelRank(LevelWarn) && LevelRank(LevelWarn) < LevelRank(LevelOK)) {
		t.Fatal("seviye sırası bozuk")
	}
}

// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
// LatencyLevel: hacim (≥300 iki pencere) + cari ≥100 ms + uyarı için fark ≥50 ms
// + önceki pencerenin kova oynaklığı (3 × 1.4826 × MAD, ≥6 dolu kova); kritik
// oynaklık istemez.
func TestLatencyLevel(t *testing.T) {
	n := func(ms float64) RED { return RED{Spans: 5000, P99Ms: ms} }
	noisy := []float64{100, 400, 120, 380, 110, 390}
	for _, c := range []struct {
		name      string
		cur, base RED
		buckets   []float64
		want      string
		inWhy     string
	}{
		{"2→3 ms (+%50) asla", n(3), n(2), nil, LevelOK, "100 ms altı"},
		{"oynak 2–7 ms serisi asla", n(7), n(2), []float64{2, 7, 3, 6, 2, 7, 4, 5}, LevelOK, "100 ms altı"},
		{"200→300 uyarı", n(300), n(200), nil, LevelWarn, "+%50"},
		{"200→500 kritik", n(500), n(200), nil, LevelCrit, "2.5×"},
		{"60→150 uyarı", n(150), n(60), nil, LevelWarn, "+%150"},
		{"80→130 uyarı", n(130), n(80), nil, LevelWarn, "+%62"},
		{"90→125 fark 35 ms < 50", n(125), n(90), nil, LevelOK, "fark 35 ms"},
		{"hacim: 250 span", RED{Spans: 250, P99Ms: 600}, RED{Spans: 5000, P99Ms: 200}, nil, LevelOK, "hacim < 300"},
		{"hacim: önceki pencere 250 span", RED{Spans: 5000, P99Ms: 600}, RED{Spans: 250, P99Ms: 200}, nil, LevelOK, "hacim < 300"},
		{"oynaklık uyarıyı keser", n(300), n(200), noisy, LevelOK, "kova oynaklığı"},
		{"MAD == 0 → kol atlanır", n(300), n(200), []float64{200, 200, 200, 200, 200, 200}, LevelWarn, "+%50"},
		{"< 6 dolu kova → kol atlanır", n(300), n(200), []float64{100, 400, 120, 0, 0}, LevelWarn, "+%50"},
		{"kritik oynaklık istemez", n(900), n(200), noisy, LevelCrit, "4.5×"},
		{"baseline yok", n(900), RED{}, nil, LevelOK, ""},
		{"+%10 sessiz (gerekçe yok)", n(220), n(200), nil, LevelOK, ""},
	} {
		lv, _, _, why := LatencyLevel(c.cur, c.base, c.buckets)
		if lv != c.want || (c.inWhy != "" && !strings.Contains(why, c.inWhy)) || (c.inWhy == "" && c.want == LevelOK && why != "") {
			t.Errorf("%s: %s %q", c.name, lv, why)
		}
	}
}

func TestDispersionBand(t *testing.T) {
	// medyan 250, sapmalar {150,140,130,130,140,150} → MAD 140 → 3×1.4826×140.
	if got := DispersionBand([]float64{100, 400, 120, 380, 110, 390}); got < 622 || got > 623 {
		t.Fatalf("band = %v", got)
	}
	if DispersionBand([]float64{1, 2, 3, 0, 0, 0, 0}) != 0 {
		t.Fatal("< 6 dolu kova 0 olmalı (boş kova sayılmaz)")
	}
}

func TestCorroborated(t *testing.T) {
	for _, c := range []struct {
		cur, prior uint64
		curLen     float64
		priorLen   float64
		want       bool
	}{
		{10, 0, 3600, 3600, true},
		{9, 0, 3600, 3600, false},  // < LatCorrobMinSlow
		{40, 20, 3600, 3600, true}, // 2×
		{39, 20, 3600, 3600, false},
		{40, 40, 3600, 7200, true}, // önceki pencere 2× uzun → normalize 20
	} {
		if got := Corroborated(c.cur, c.prior, c.curLen, c.priorLen); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if CorroborationThresholdMs(2) != LatMinP99Ms || CorroborationThresholdMs(400) != 500 {
		t.Fatal("doğrulama eşiği max(base×1.25, 100)")
	}
}

// TestRowSplitArms — hata ve gecikme kolları ayrı; gecikme kolu düşünce hata kolu kalır.
func TestRowSplitArms(t *testing.T) {
	v := RowSplit(RED{Spans: 900, ErrorRate: 3, P99Ms: 500}, RED{Spans: 1000, ErrorRate: 1, P99Ms: 200}, true, nil)
	if v.Level != LevelCrit || v.ErrLevel != LevelWarn || v.LatLevel != LevelCrit || v.P99Mult != 2.5 {
		t.Fatalf("%+v", v)
	}
	v.LatLevel, v.LatReason = LevelOK, "doğrulanmadı"
	v.Compose()
	if v.Level != LevelWarn || v.P99Mult != 0 || !strings.Contains(strings.Join(v.Reasons, ";"), "doğrulanmadı") {
		t.Fatalf("kol düşünce: %+v", v)
	}
	if !LatencyFloorsMet(150, 90) || LatencyFloorsMet(3, 2) || LatencyFloorsMet(130, 100) {
		t.Fatal("LatencyFloorsMet: ≥100 ms VE fark ≥50 ms")
	}
}
