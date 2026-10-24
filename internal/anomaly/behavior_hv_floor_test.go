// behavior_hv_floor_test.go — v0.10.1521: davranış motorunun HACİM FARKINDA
// hata tabanı (v0.10.1518 sürekli teknik hata yükselişinin ikizi).
//
// Vaka: v0.10.1518'in BFF gateway'i — 1000 istek/sn'de 5xx oranı %0.1'den
// %0.5'e (5 hata/sn) kalıcı çıktı, kendi haftanın-saati kovasının 30σ üstü;
// davranış motoru iki %1 tabanı (minErrorRatePct + ani-sapmanın paylaşılan
// error_rate absFloor'u) yüzünden SUSTU. Yeni kapı: yukarı yönlü error_rate
// penceresinin HER dilimi ≥ highVolumeErrorsPerSec (1) hata/sn ise iki taban
// highVolumeMinErrorRatePct'e (%0.2) iner; diğer bütün kapılar aynen.
//
// Tablo saf (CH yok); her "açılmaz" vakası durduran kapıyı da çiviler.
package anomaly

import (
	"math"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// hvRows — `n` dilim, istek/sn rps, hata oranı pct (%), haftanın saati how.
func hvRows(how, n int, rps, pct float64) []behaviorRow {
	spans := uint64(math.Round(rps * 300))
	return rowsAt(how, n, spans, uint64(math.Round(float64(spans)*pct/100)), 0)
}

func TestBehaviorHighVolumeErrorFloor(t *testing.T) {
	sens := chstore.DefaultAnomalySensitivity() // error_rate absFloor %1, minErrorRatePct %1
	errPol := policyFor("error_rate", sens)

	// Taban %0.1, MAD 0.0089 → %0.5 = 0.6745 × 0.4 / 0.0089 ≈ 30.3σ.
	quiet := bucketFrom(10, spread(48, 0.1, 0.0089)...)
	// Olağan günlük yükseliş: gece kovası (3) %0.1, gündüz kovası (10) %0.5.
	daily := map[int]behaviorBucket{
		3:  {Values: spread(48, 0.1, 0.0089), Repeats: behaviorTestFullRepeats},
		10: {Values: spread(48, 0.5, 0.0089), Repeats: behaviorTestFullRepeats},
	}
	// İki pencerenin (rejim 6, mevsimsel son 3) ortak dilimi hız tabanının
	// altında (0.9 hata/sn): sürdürme her dilimde aranır.
	mixed := hvRows(10, 6, 1000, 0.5)
	mixed[4] = hvRows(10, 1, 180, 0.5)[0]
	mixed[4].Unix = mixed[3].Unix + 300
	// Kısa sıçrama: son 2 dilim %0.5, öncesi taban.
	short := append(hvRows(10, 4, 1000, 0.1), hvRows(10, 2, 1000, 0.5)...)
	for i := range short {
		short[i].Unix = int64(1_700_000_000 + i*300)
	}
	off := func(c *chstore.AnomalyBehaviorConfig) { c.HighVolumeErrorFloor = boolPtrT(false) }

	cases := []struct {
		name     string
		baseline map[int]behaviorBucket
		recent   []behaviorRow
		mut      func(*chstore.AnomalyBehaviorConfig)
		wantOK   bool
		wantWhy  string // açılmazsa gerekçede geçmesi gereken kapı
		minZ     float64
	}{
		{"yüksek hacim 1000 istek/sn, %0.1 → %0.5 (5 hata/sn, 30σ) → rejim ↑", quiet, hvRows(10, 6, 1000, 0.5), nil, true, "", 30},
		{"aynı vaka, hacim farkında taban kapalı → %1 tabanı susturur (eski davranış)", quiet, hvRows(10, 6, 1000, 0.5), off, false, behBlockAbsFloor, 0},
		{"düşük hacim 20 istek/sn, aynı oran (0.1 hata/sn) → aday YOK, %1 tabanı", quiet, hvRows(10, 6, 20, 0.5), nil, false, behBlockAbsFloor, 0},
		{"orta hacim 150 istek/sn (0.75 hata/sn) → aday YOK, %1 tabanı", quiet, hvRows(10, 6, 150, 0.5), nil, false, behBlockAbsFloor, 0},
		{"penceredeki TEK dilim hız tabanının altında → taban inmez", quiet, mixed, nil, false, behBlockAbsFloor, 0},
		{"olağan günlük yükseliş: gündüz kovası zaten %0.5 → aday YOK", daily, hvRows(10, 6, 1000, 0.5), nil, false, behBlockNoFire, 0},
		{"trafik 3× ama oran düz (%0.1, 3 hata/sn) → aday YOK", quiet, hvRows(10, 6, 3000, 0.1), nil, false, behBlockNoFire, 0},
		{"kısa sıçrama (2 dilim) → aday YOK", quiet, short, nil, false, behBlockNoFire, 0},
		{"yüksek hacim ama %0.15 < hacim tabanı %0.2 → aday YOK", bucketFrom(10, spread(48, 0.02, 0.002)...), hvRows(10, 6, 3000, 0.15), nil, false, behBlockAbsFloor, 0},
		{"yüksek hacim, hacim tabanı %0.6'ya çekilmiş → %0.5 açılmaz", quiet, hvRows(10, 6, 1000, 0.5),
			func(c *chstore.AnomalyBehaviorConfig) { c.HighVolumeMinErrorRatePct = 0.6 }, false, behBlockAbsFloor, 0},
		{"hız tabanı 10 hata/sn'ye çekilmiş → 5 hata/sn yetmez", quiet, hvRows(10, 6, 1000, 0.5),
			func(c *chstore.AnomalyBehaviorConfig) { c.HighVolumeErrorsPerSec = 10 }, false, behBlockAbsFloor, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := chstore.DefaultAnomalyBehavior()
			if tc.mut != nil {
				tc.mut(&cfg)
			}
			c, ok, why := evalBehaviorWhy("orders-api", "error_rate", tc.baseline, tc.recent, errPol, cfg)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (gerekçe %q, aday %+v)", ok, tc.wantOK, why, c)
			}
			if !ok {
				if !strings.Contains(why, tc.wantWhy) {
					t.Fatalf("gerekçe %q, %q içermeli", why, tc.wantWhy)
				}
				return
			}
			if why != "" || c.Direction != "up" || c.Signal != "regime" {
				t.Fatalf("aday %+v gerekçe %q", c, why)
			}
			if c.Z < tc.minZ {
				t.Fatalf("z = %.1f, ≥ %.0f bekleniyordu", c.Z, tc.minZ)
			}
		})
	}
}

// Hacim farkında taban yalnız error_rate'e ve yalnız yukarı yöne uygulanır.
func TestBehaviorHighVolumeErrorsScope(t *testing.T) {
	cfg := chstore.DefaultAnomalyBehavior()
	w := hvRows(10, 6, 1000, 0.5)
	if !behaviorHighVolumeErrors("error_rate", w, cfg) {
		t.Fatal("5 hata/sn pencere yüksek hacim sayılmalı")
	}
	for _, m := range []string{"p99_ms", "request_rate"} {
		if behaviorHighVolumeErrors(m, w, cfg) {
			t.Fatalf("%s için kapı uygulanmamalı", m)
		}
	}
	if behaviorHighVolumeErrors("error_rate", nil, cfg) {
		t.Fatal("boş pencere yüksek hacim değil")
	}
	// Düşüş: %2 → %0.5 yüksek hacimde de minErrorRatePct (%1) altına indiği
	// için aday değil (taban düşüş yönünde İNMEZ).
	pol := policyFor("error_rate", chstore.DefaultAnomalySensitivity())
	_, ok, why := evalBehaviorWhy("orders-api", "error_rate", bucketFrom(10, spread(48, 2, 0.02)...), w, pol, cfg)
	if ok || !strings.Contains(why, behBlockOwnFloor) {
		t.Fatalf("düşüş: ok=%v gerekçe %q", ok, why)
	}
}
