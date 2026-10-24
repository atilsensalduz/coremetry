package anomaly

// error_ramp.go — SÜREKLİ teknik hata yükselişi (operatör, prod 2026-10-24:
// "BFF'lerin metriklerinde http500 hataları arttı, sonra müdahale ettik düzeldi
// ama bunu bir anomali olarak algılamadı"). SAF + tablo testli.
//
// VAKA: mobil BFF gateway'inin 5xx hızı 06:00'da ~0.5–1/sn'den 10:00'da ~6/sn,
// 11:45'te ~8/sn'ye YAVAŞÇA çıktı; ekip 11:47'de müdahale etti. Ani-sapma kolu
// iki yerden kördü:
//  1. error_rate AbsFloor %1 (v0.9.180, "milyonlarca istekte 1-2-3 hata problem
//     olmasın"): yüksek hacimli gateway'de 8 hata/sn ≈ %0.5–0.8 — mutlak oran
//     tabanının altında, z ne olursa olsun açılmaz.
//  2. z ≥ CriticalZ (6) her dwell kovasında: gürültülü oranlı serviste rampa
//     6σ'ya ancak geç ulaşır.
//
// KURAL (ani-sapmanın YERİNE değil, YANINDA; aynı `anomaly:<svc>:error_rate`
// satırı — servis başına tek Problem, dedup aynı kimlikten): son W tamamlanmış
// kovanın HER BİRİNDE
//
//	oran ≥ max(RatioFactor × taban medyanı, taban p90'ı, MinRatioPct)
//	VE hata hızı (oran × istek/sn) ≥ MinErrorsPerSec
//
// Taban ani-sapmanın tabanıyla AYNI kaynak (mevsimsel 14 g ±15 dk, yoksa
// pencere öncesi ardışık 24 sa) — gün içi olağan oran şekli mevsimselde zaten
// var, sabah trafiğiyle orantılı artan hata SAYISI oranı oynatmaz.
//
// BİLİNÇLİ KARARLAR:
//   - Oran düz, hız yüksek (trafik 3× → hata 3×): AÇILMAZ. Bu teknik arıza
//     değil yük; yük sinyali request_rate (operatör kararıyla kapalı). Oran
//     kendi tabanının RatioFactor katına çıkmadıkça hız tek başına açmaz.
//   - p90 korkuluğu: sıçramalı geçmişte (taban oranı düzenli %0.1↔%0.5) 2×
//     medyan olağan dalgadır; eşik tabanın p90'ının altına inemez.
//   - Açık satır, en yeni kova hâlâ eşiğin üstündeyken KAPANMAZ (histerezis):
//     z bandına geri girmiş ama oran hâlâ 2× ise "toparlandı" demek yalan olur
//     ve 30 dk sonra yeni kimlikle yeniden açılırdı.
//   - 4xx GİRMEZ: VM hata kümesi `5..` (pinli), span ERROR = sunucu hatası.

import (
	"fmt"
	"math"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// errorRampMetric — kuralın tek metriği.
const errorRampMetric = "error_rate"

// Engelleyici gerekçeler (açıklama ucu + test).
const (
	rampBlockOff       = "kapalı (errorRamp.enabled=false)"
	rampBlockShort     = "seri pencere için kısa"
	rampBlockUnaligned = "hacim serisi hizalı değil"
	rampBlockBaseline  = "taban yetersiz"
	rampBlockRatio     = "oran eşiğin altında"
	rampBlockRate      = "hata hızı tabanın altında"
)

// errorRampResult — kuralın kararı + kanıt sayıları.
type errorRampResult struct {
	Evaluated bool    // kural koştu (açık + seri yeterli)
	Holds     bool    // pencerenin TAMAMI ihlalde → aç / tazele
	Latest    bool    // en yeni kova ihlalde → açık satırı kapatma
	Breaching int     // sondan geriye ardışık ihlal eden kova sayısı
	Window    int     // W
	Threshold float64 // oran eşiği (%)
	Median    float64 // taban medyanı (%)
	P90       float64 // taban p90'ı (%)
	CurRatio  float64 // en yeni kova oranı (%)
	CurEPS    float64 // en yeni kova hata/sn
	Blocker   string  // en yeni kovayı (ya da kuralı) durduran ilk kapı; "" = ihlal
}

// percentile — SAF: doğrusal olmayan (en yakın sıra) yüzdelik; boşta 0.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// errorRampThreshold — SAF: oran eşiği (%).
func errorRampThreshold(median, p90 float64, cfg chstore.ErrorRampConfig) float64 {
	return math.Max(math.Max(cfg.RatioFactor*median, p90), cfg.MinRatioPct)
}

// errorRampCheck — SAF. values = oran serisi (%), rates = aynı kovaların
// istek/sn'si (hizalı; padTrailingSilence ikisine de uygulanmış), baseline =
// pencere DIŞI taban örnekleri (çağıran seçer). cfg Normalize'dan geçmiş olmalı.
func errorRampCheck(values, rates, baseline []float64, cfg chstore.ErrorRampConfig) errorRampResult {
	cfg = chstore.NormalizeErrorRamp(cfg)
	w := cfg.WindowBuckets
	r := errorRampResult{Window: w}
	switch {
	case !cfg.On():
		r.Blocker = rampBlockOff
		return r
	case len(values) != len(rates):
		r.Blocker = rampBlockUnaligned
		return r
	case len(values) < w:
		r.Blocker = rampBlockShort
		return r
	case len(baseline) < minSamples:
		r.Blocker = rampBlockBaseline
		return r
	}
	r.Evaluated = true
	r.Median = medianOf(baseline)
	r.P90 = percentile(baseline, 0.90)
	r.Threshold = errorRampThreshold(r.Median, r.P90, cfg)
	n := len(values)
	r.CurRatio = values[n-1]
	r.CurEPS = values[n-1] / 100 * rates[n-1]
	breach := func(i int) string {
		if values[i] < r.Threshold {
			return rampBlockRatio
		}
		if values[i]/100*rates[i] < cfg.MinErrorsPerSec {
			return rampBlockRate
		}
		return ""
	}
	r.Blocker = breach(n - 1)
	r.Latest = r.Blocker == ""
	for i := n - 1; i >= 0 && breach(i) == ""; i-- {
		r.Breaching++
	}
	r.Holds = r.Breaching >= w
	return r
}

// errorRampBaseline — SAF: kuralın tabanı. Ani-sapmayla AYNI seçim
// (chooseBaseline), ama ardışık taban W kovalık pencerenin ÖNCESİNDEN kesilir
// (pencerenin kendisi kendi tabanına girmesin).
func errorRampBaseline(values, seasonal, rates []float64, w, seasonalMinSamples int) []float64 {
	if len(values) < w {
		return nil
	}
	split := len(values) - w
	r := rates
	if len(r) > split {
		r = r[:split]
	}
	return chooseBaseline(seasonal, trimTrailingSilent(values[:split], r), seasonalMinSamples)
}

// errorRampDescription — SAF: kuralın açtığı / tazelediği satırın açıklaması.
// "Teknik hata" adı operatörün BFF sınıflamasıyla aynı (5xx = teknik hata,
// 4xx = iş hatası — 4xx sayılmaz).
func errorRampDescription(service string, r errorRampResult) string {
	return fmt.Sprintf("Teknik hata (5xx) oranı sürekli yükseldi on %s — current %.2f%% vs baseline %.2f%% "+
		"(eşik %.2f%%, %.1f hata/sn, %d kova ≈ %d dk boyunca eşiğin üstünde).",
		service, r.CurRatio, r.Median, r.Threshold, r.CurEPS, r.Breaching, r.Breaching*bucketSeconds/60)
}
