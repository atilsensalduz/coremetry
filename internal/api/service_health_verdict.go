package api

// service_health_verdict.go — CoSRE servis sağlık özeti (operatör isteği
// 2026-10-15: "bir servisin loguna, metriklerine hızlıca bakıp hata var mı
// diye dönsün, hatalı trace'lere yönlendirsin; sayfayı kendisi açmasın,
// kullanıcıyı yönlendirsin"). Hüküm MODELDEN değil, sabit eşiklerden:
// küçük model "sorunlu/sağlıklı" uydurmasın, kanıtta sayılarıyla gelsin.

import "github.com/cilcenk/coremetry/internal/healthverdict"

// v0.10.1276 (CoSRE adım 7) — eşikler ve gövde internal/healthverdict'e taşındı
// (service_health_sweep aynı eşiklerle operasyon/fonksiyon satırlarını
// puanlıyor; mcptools api'yi import edemez). Burası delegasyon; çıktı
// bayt-bayt aynı (service_health_verdict_golden_test.go).

// serviceHealthVerdict — SAF: (etiket, gerekçeler, önerilen adım).
func serviceHealthVerdict(cur, base aiRED, curErr error, openProblems, errTraces int) (label string, reasons []string, next string) {
	return healthverdict.Service(hvRED(cur), hvRED(base), curErr, openProblems, errTraces)
}

// hvRED — aiRED → hükmün okuduğu üç sayı.
func hvRED(r aiRED) healthverdict.RED {
	return healthverdict.RED{Spans: r.Spans, ErrorRate: r.ErrorRate, P99Ms: r.P99Ms}
}

// renderServiceHealthVerdictTR — SAF: kanıtın İLK satırı (model bunu anlatır).
func renderServiceHealthVerdictTR(label string, reasons []string, next string) string {
	return healthverdict.RenderTR(label, reasons, next)
}
