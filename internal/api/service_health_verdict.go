package api

// service_health_verdict.go — CoSRE servis sağlık özeti (operatör isteği
// 2026-10-15: "bir servisin loguna, metriklerine hızlıca bakıp hata var mı
// diye dönsün, hatalı trace'lere yönlendirsin; sayfayı kendisi açmasın,
// kullanıcıyı yönlendirsin"). Hüküm MODELDEN değil, sabit eşiklerden:
// küçük model "sorunlu/sağlıklı" uydurmasın, kanıtta sayılarıyla gelsin.

import (
	"fmt"
	"strings"
)

// Hüküm eşikleri (pencere vs baseline; ErrorRate yüzde).
const (
	hvErrWarnPt   = 1.0  // hata oranı ≥ +1 puan → Dikkat
	hvErrCritPt   = 5.0  // ≥ +5 puan → Sorunlu
	hvErrCritAbs  = 5.0  // mutlak ≥ %5 → Sorunlu
	hvP99WarnRel  = 0.25 // p99 ≥ +%25 → Dikkat
	hvP99CritMult = 2.0  // p99 ≥ 2× (ve ≥ 200 ms) → Sorunlu
	hvP99CritMin  = 200.0
)

// serviceHealthVerdict — SAF: (etiket, gerekçeler, önerilen adım).
func serviceHealthVerdict(cur, base aiRED, curErr error, openProblems, errTraces int) (label string, reasons []string, next string) {
	if curErr != nil || cur.Spans == 0 {
		return "Veri yok", []string{"bu pencerede span verisi okunamadı"}, "Zaman aralığını genişletin ya da servis adını kontrol edin."
	}
	crit, warn := false, false
	errUp := cur.ErrorRate - base.ErrorRate
	latUp := false
	switch {
	case errUp >= hvErrCritPt || cur.ErrorRate >= hvErrCritAbs:
		crit = true
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f (baseline %%%.2f)", cur.ErrorRate, base.ErrorRate))
	case errUp >= hvErrWarnPt:
		warn = true
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, baseline'dan +%.1f puan", cur.ErrorRate, errUp))
	}
	if base.P99Ms > 0 {
		rel := (cur.P99Ms - base.P99Ms) / base.P99Ms
		switch {
		case cur.P99Ms >= base.P99Ms*hvP99CritMult && cur.P99Ms >= hvP99CritMin:
			crit, latUp = true, true
			reasons = append(reasons, fmt.Sprintf("p99 %.0f ms (baseline %.0f ms, %.1f×)", cur.P99Ms, base.P99Ms, cur.P99Ms/base.P99Ms))
		case rel >= hvP99WarnRel:
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
		label = "Sorunlu"
	case warn:
		label = "Dikkat"
	default:
		label = "Sağlıklı"
		reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, p99 %.0f ms — baseline içinde", cur.ErrorRate, cur.P99Ms))
	}
	switch {
	case errUp >= hvErrWarnPt && errTraces > 0:
		next = "Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın."
	case latUp:
		next = "\"Overview\" bağlantısında en yavaş operasyona bakın; trace'te zamanın gittiği span'i bulun."
	case openProblems > 0:
		next = "Açık problemi açıp kök neden panelini inceleyin."
	case label == "Sağlıklı":
		next = "Şu an aksiyon gerekmiyor."
	default:
		next = "Hatalı trace'ler ve error loglarına bakın."
	}
	return label, reasons, next
}

// renderServiceHealthVerdictTR — SAF: kanıtın İLK satırı (model bunu anlatır).
func renderServiceHealthVerdictTR(label string, reasons []string, next string) string {
	return fmt.Sprintf("HÜKÜM (deterministik eşik, model değil): %s — %s.\nÖNERİLEN ADIM: %s\n", label, strings.Join(reasons, "; "), next)
}
