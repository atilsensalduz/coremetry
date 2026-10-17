package healthverdict

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)

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
}

func TestLabelRanks(t *testing.T) {
	if !(LabelRank(LabelCrit) > LabelRank(LabelWarn) && LabelRank(LabelWarn) > LabelRank(LabelHealthy) && LabelRank(LabelHealthy) > LabelRank(LabelNoData)) {
		t.Fatal("etiket sırası bozuk")
	}
	if !(LevelRank(LevelCrit) < LevelRank(LevelWarn) && LevelRank(LevelWarn) < LevelRank(LevelOK)) {
		t.Fatal("seviye sırası bozuk")
	}
}
