// Package healthverdict — v0.10.1276 (CoSRE adım 7): servis / operasyon /
// fonksiyon sağlık hükmünün SAF eşikleri. Tek kaynak: api'nin servis sağlık
// özeti (service_health_verdict.go, guided "HÜKÜM" satırı) ve mcptools'un
// service_health_sweep taraması AYNI sayılarla karar verir — mcptools api'yi
// import edemediği için eşikler buraya taşındı (pivot_trace.go'daki ayna
// sabitler de buradan okur).
//
// Hüküm MODELDEN değil, sabit eşiklerden: küçük model "sorunlu/sağlıklı"
// uydurmasın, kanıtta sayılarıyla gelsin (operatör isteği 2026-10-15).
// Paket I/O yapmaz; yalnız standart kütüphane.
package healthverdict

import (
	"fmt"
	"strings"
)

// Hüküm eşikleri (pencere vs baseline; ErrorRate yüzde).
const (
	ErrWarnPt   = 1.0  // hata oranı ≥ +1 puan → Dikkat
	ErrCritPt   = 5.0  // ≥ +5 puan → Sorunlu
	ErrCritAbs  = 5.0  // mutlak ≥ %5 → Sorunlu
	P99WarnRel  = 0.25 // p99 ≥ +%25 → Dikkat
	P99CritMult = 2.0  // p99 ≥ 2× (ve ≥ 200 ms) → Sorunlu
	P99CritMin  = 200.0
)

// VolumeFloor — operasyon / fonksiyon satırının puanlanması için gereken en
// az span sayısı (pencerede > VolumeFloor). Emsal: chstore/correlate.go
// changeDirection'ın hacim tabanı. Altındaki satır taranır ama hüküm almaz
// (birkaç çağrının %50 hatası gürültüdür).
const VolumeFloor = 100

// Etiketler (servis hükmü).
const (
	LabelHealthy = "Sağlıklı"
	LabelWarn    = "Dikkat"
	LabelCrit    = "Sorunlu"
	LabelNoData  = "Veri yok"
)

// Seviyeler (satır hükmü; FE `level` sütunu).
const (
	LevelCrit = "crit"
	LevelWarn = "warn"
	LevelOK   = "ok"
)

// RED — hükmün okuduğu üç sayı.
type RED struct {
	Spans     uint64
	ErrorRate float64 // yüzde
	P99Ms     float64
}

// Service — SAF: servis sağlık hükmü (etiket, gerekçeler, önerilen adım).
// api.serviceHealthVerdict'in taşınmış gövdesi; çıktı bayt-bayt aynı
// (api/service_health_verdict_golden_test.go).
func Service(cur, base RED, curErr error, openProblems, errTraces int) (label string, reasons []string, next string) {
	if curErr != nil || cur.Spans == 0 {
		return LabelNoData, []string{"bu pencerede span verisi okunamadı"}, "Zaman aralığını genişletin ya da servis adını kontrol edin."
	}
	crit, warn := false, false
	errUp := cur.ErrorRate - base.ErrorRate
	latUp := false
	switch {
	case errUp >= ErrCritPt || cur.ErrorRate >= ErrCritAbs:
		crit = true
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f (baseline %%%.2f)", cur.ErrorRate, base.ErrorRate))
	case errUp >= ErrWarnPt:
		warn = true
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, baseline'dan +%.1f puan", cur.ErrorRate, errUp))
	}
	if base.P99Ms > 0 {
		rel := (cur.P99Ms - base.P99Ms) / base.P99Ms
		switch {
		case cur.P99Ms >= base.P99Ms*P99CritMult && cur.P99Ms >= P99CritMin:
			crit, latUp = true, true
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms (baseline %.0f ms, %.1f×)", cur.P99Ms, base.P99Ms, cur.P99Ms/base.P99Ms))
		case rel >= P99WarnRel:
			warn, latUp = true, true
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms, baseline'dan +%%%.0f", cur.P99Ms, rel*100))
		}
	}
	if openProblems > 0 {
		warn = true
		reasons = append(reasons, fmt.Sprintf("%d açık problem", openProblems))
	}
	switch {
	case crit:
		label = LabelCrit
	case warn:
		label = LabelWarn
	default:
		label = LabelHealthy
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, p99 %.0f ms — baseline içinde", cur.ErrorRate, cur.P99Ms))
	}
	switch {
	case errUp >= ErrWarnPt && errTraces > 0:
		next = "Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın."
	case latUp:
		next = "\"Overview\" bağlantısında en yavaş operasyona bakın; trace'te zamanın gittiği span'i bulun."
	case openProblems > 0:
		next = "Açık problemi açıp kök neden panelini inceleyin."
	case label == LabelHealthy:
		next = "Şu an aksiyon gerekmiyor."
	default:
		next = "Hatalı trace'ler ve error loglarına bakın."
	}
	return label, reasons, next
}

// RenderTR — SAF: kanıtın İLK satırı (model bunu anlatır).
func RenderTR(label string, reasons []string, next string) string {
	return fmt.Sprintf("HÜKÜM (deterministik eşik, model değil): %s — %s.\nÖNERİLEN ADIM: %s\n", label, strings.Join(reasons, "; "), next)
}

// Row — SAF: TEK operasyon / fonksiyon satırının hükmü, servis hükmüyle AYNI
// eşiklerle. hasBase=false (önceki pencerede satır yok ya da baseline
// okunamadı) → yalnız mutlak hata eşiği (≥ ErrCritAbs → crit); delta ve p99
// kıyası yapılmaz (baseline'ı 0 saymak her yeni satırı uyarıya çevirirdi).
// Hacim tabanı: cur.Spans ≤ VolumeFloor → ok + "düşük hacim" gerekçesi.
// Dönen errDelta / p99Mult puanlamanın etki çarpanıdır (0 = yok).
func Row(cur, base RED, hasBase bool) (level string, reasons []string, errDelta, p99Mult float64) {
	if cur.Spans <= VolumeFloor {
		return LevelOK, []string{fmt.Sprintf("düşük hacim (%d span ≤ %d) — puanlanmadı", cur.Spans, VolumeFloor)}, 0, 0
	}
	level = LevelOK
	bump := func(l string) {
		if l == LevelCrit || (l == LevelWarn && level == LevelOK) {
			level = l
		}
	}
	if !hasBase {
		if cur.ErrorRate >= ErrCritAbs {
			bump(LevelCrit)
			reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f ≥ %%%.0f (baseline yok)", cur.ErrorRate, ErrCritAbs))
			errDelta = cur.ErrorRate
		}
		return level, reasons, errDelta, 0
	}
	errUp := cur.ErrorRate - base.ErrorRate
	switch {
	case errUp >= ErrCritPt || cur.ErrorRate >= ErrCritAbs:
		bump(LevelCrit)
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f (baseline %%%.2f)", cur.ErrorRate, base.ErrorRate))
	case errUp >= ErrWarnPt:
		bump(LevelWarn)
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, baseline'dan +%.1f puan", cur.ErrorRate, errUp))
	}
	if errUp > 0 && level != LevelOK {
		errDelta = errUp
	}
	if base.P99Ms > 0 && cur.P99Ms > 0 {
		rel := (cur.P99Ms - base.P99Ms) / base.P99Ms
		switch {
		case cur.P99Ms >= base.P99Ms*P99CritMult && cur.P99Ms >= P99CritMin:
			bump(LevelCrit)
			p99Mult = cur.P99Ms / base.P99Ms
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms (baseline %.0f ms, %.1f×)", cur.P99Ms, base.P99Ms, p99Mult))
		case rel >= P99WarnRel:
			bump(LevelWarn)
			p99Mult = cur.P99Ms / base.P99Ms
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms, baseline'dan +%%%.0f", cur.P99Ms, rel*100))
		}
	}
	return level, reasons, errDelta, p99Mult
}

// LevelRank — sıralama: crit < warn < ok.
func LevelRank(l string) int {
	switch l {
	case LevelCrit:
		return 0
	case LevelWarn:
		return 1
	}
	return 2
}

// LabelForLevel — satır seviyesi → servis etiketi.
func LabelForLevel(l string) string {
	switch l {
	case LevelCrit:
		return LabelCrit
	case LevelWarn:
		return LabelWarn
	}
	return LabelHealthy
}

// LabelRank — servis etiketlerinin kötülük sırası (büyük = kötü). "Veri yok"
// sağlıklıdan kötü değil ama bilinmez: birleştirmede bulgu varsa bulgu kazanır.
func LabelRank(l string) int {
	switch l {
	case LabelCrit:
		return 3
	case LabelWarn:
		return 2
	case LabelHealthy:
		return 1
	}
	return 0
}
