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
	"math"
	"sort"
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

// Gecikme kapıları — v0.10.1299 (operatör 2026-10-17: CoSRE taraması
// "SELECT db0p.WGT_TKART: p99 3 ms, baseline'dan +%45" gibi bulgular üretti;
// 5 dk p99'u 2–7 ms arasında salınan bir sorguda göreli eşik tek başına
// gürültüdür — ms kuantizasyonu + ~300 örneğin p99'u ~3 örneğe dayanır).
// Kapıların HEPSİ VE'lenir. Model: Dynatrace varsayılan anomali tespiti
// (medyan +100 ms VE +%50, en yavaş %10 +1000 ms VE +%100, ≥10 istek/dk).
//
//	hacim       — iki pencerede de ≥ LatMinCalls span (az örnekte p99 = tek istek)
//	taban       — cari p99 < LatMinP99Ms ise gecikme hiç puanlanmaz
//	kritik      — cari ≥ LatCritMult × baseline VE ≥ LatCritMinMs
//	uyarı       — göreli ≥ LatWarnRel VE fark ≥ LatWarnDeltaMs VE (seri
//	              verildiyse) fark ≥ LatDispersionK × 1.4826 × MAD(önceki
//	              pencerenin kova p99'ları); ≥ LatDispersionMinBuckets dolu kova
//	              yoksa ya da MAD == 0 ise oynaklık kolu atlanır. Kritik
//	              oynaklık istemez.
//
// Eşikler bilinçli SABİT (hüküm modelden değil, sabit eşikten — dosya başı).
// Tüketiciler: Service, Row (sweep operasyon/fonksiyon), mcptools
// pivotOpLevel, sre chkP99Up.
const (
	LatMinCalls             = 300
	LatMinP99Ms             = 100.0
	LatWarnRel              = P99WarnRel
	LatWarnDeltaMs          = 50.0
	LatCritMult             = P99CritMult
	LatCritMinMs            = P99CritMin
	LatDispersionK          = 3.0
	LatDispersionMinBuckets = 6
	madToSigma              = 1.4826 // MAD → σ (normal dağılım tutarlılık katsayısı)
)

// LatencyLevel — SAF: iki pencerenin PENCERE düzeyi birleşik p99'u (+ isteğe
// bağlı önceki pencerenin kova p99'ları) → gecikme seviyesi. rel = göreli
// artış, mult = cur/base (0 = kıyas yok). why: seviye ≠ ok ise bulgu
// gerekçesi; ok ise SUSTURMA gerekçesi (yalnız göreli eşik geçtiyse dolu —
// operatör "+%45 neden sayılmadı" sorusunun cevabını satırda görür).
func LatencyLevel(cur, base RED, priorBucketP99 []float64) (level string, rel, mult float64, why string) {
	if base.P99Ms <= 0 || cur.P99Ms <= 0 {
		return LevelOK, 0, 0, ""
	}
	rel = (cur.P99Ms - base.P99Ms) / base.P99Ms
	mult = cur.P99Ms / base.P99Ms
	flagged := rel >= LatWarnRel
	if cur.Spans < LatMinCalls || base.Spans < LatMinCalls {
		if flagged {
			why = fmt.Sprintf("p99 %.0f ms (+%%%.0f) — p99 kıyası yok (hacim < %d span)", cur.P99Ms, rel*100, LatMinCalls)
		}
		return LevelOK, rel, mult, why
	}
	if cur.P99Ms < LatMinP99Ms {
		if flagged {
			why = fmt.Sprintf("p99 %.0f ms (+%%%.0f) — %.0f ms altı, gecikme puanlanmaz", cur.P99Ms, rel*100, LatMinP99Ms)
		}
		return LevelOK, rel, mult, why
	}
	delta := cur.P99Ms - base.P99Ms
	if cur.P99Ms >= base.P99Ms*LatCritMult && cur.P99Ms >= LatCritMinMs {
		return LevelCrit, rel, mult, fmt.Sprintf("p99 %.0f ms (baseline %.0f ms, %.1f×)", cur.P99Ms, base.P99Ms, mult)
	}
	if !flagged {
		return LevelOK, rel, mult, ""
	}
	if delta < LatWarnDeltaMs {
		return LevelOK, rel, mult, fmt.Sprintf("p99 %.0f ms (+%%%.0f) — fark %.0f ms < %.0f ms, puanlanmadı", cur.P99Ms, rel*100, delta, LatWarnDeltaMs)
	}
	if band := DispersionBand(priorBucketP99); band > 0 && delta < band {
		return LevelOK, rel, mult, fmt.Sprintf("p99 %.0f ms (+%%%.0f) — fark %.0f ms önceki pencerenin kova oynaklığı içinde (±%.0f ms), puanlanmadı", cur.P99Ms, rel*100, delta, band)
	}
	return LevelWarn, rel, mult, fmt.Sprintf("p99 %.0f ms, baseline'dan +%%%.0f", cur.P99Ms, rel*100)
}

// DispersionBand — SAF: LatDispersionK × 1.4826 × MAD(dolu kova p99'ları).
// ≤ 0 değerler (boş kova) atlanır; < LatDispersionMinBuckets dolu kova ya da
// MAD == 0 → 0 (kol atlanır).
func DispersionBand(vals []float64) float64 {
	xs := make([]float64, 0, len(vals))
	for _, v := range vals {
		if v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
			xs = append(xs, v)
		}
	}
	if len(xs) < LatDispersionMinBuckets {
		return 0
	}
	med := median(xs)
	dev := make([]float64, len(xs))
	for i, v := range xs {
		dev[i] = math.Abs(v - med)
	}
	return LatDispersionK * madToSigma * median(dev)
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Ham span doğrulaması (v0.10.1299, tasarım §2b). MV p99'u tek başına yalnız
// OLASI sinyaldir; gecikme BULGUSU (warn/crit) eşiği (max(baseline p99 ×
// (1+LatWarnRel), LatMinP99Ms)) aşan ham istek sayısıyla doğrulanır: cari
// pencerede ≥ LatCorrobMinSlow VE ≥ LatCorrobRiseMult × (uzunluğa normalize)
// önceki pencere.
const (
	LatCorrobMinSlow  = 10
	LatCorrobRiseMult = 2.0
)

// CorroborationThresholdMs — SAF: doğrulamanın "yavaş istek" eşiği.
func CorroborationThresholdMs(baseP99Ms float64) float64 {
	return math.Max(baseP99Ms*(1+LatWarnRel), LatMinP99Ms)
}

// Corroborated — SAF: ham sayımlar gecikme bulgusunu taşıyor mu? Önceki
// pencere cari uzunluğa normalize edilir (eşit uzunlukta oran 1).
func Corroborated(curSlow, priorSlow uint64, curLen, priorLen float64) bool {
	if curSlow < LatCorrobMinSlow {
		return false
	}
	norm := float64(priorSlow)
	if priorLen > 0 && curLen > 0 {
		norm *= curLen / priorLen
	}
	return float64(curSlow) >= LatCorrobRiseMult*norm
}

// LatencyFloorsMet — SAF: tek değer kıyası (pivot op_baseline: trace'teki op
// süresi vs op p99; sre chkP99Up: servis p99 cari vs baseline) için mutlak
// tabanlar: cari ≥ LatMinP99Ms VE fark ≥ LatWarnDeltaMs.
func LatencyFloorsMet(curMs, baseMs float64) bool {
	return curMs >= LatMinP99Ms && curMs-baseMs >= LatWarnDeltaMs
}

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
	// v0.10.1299 — gecikme kolu LatencyLevel kapılarından geçer (servis
	// düzeyinde kova serisi yok → oynaklık kolu atlanır).
	latNote := ""
	switch lv, _, _, why := LatencyLevel(cur, base, nil); lv {
	case LevelCrit:
		crit, latUp = true, true
		reasons = append(reasons, why)
	case LevelWarn:
		warn, latUp = true, true
		reasons = append(reasons, why)
	default:
		latNote = why
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
		if latNote != "" {
			// v0.10.1299 — eski kural burada uyarırdı: "baseline içinde" yalan
			// olurdu; hata oranı + p99'un NEDEN puanlanmadığı.
			reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f — baseline içinde", cur.ErrorRate), latNote)
		} else {
			reasons = append(reasons, fmt.Sprintf("hata oranı %%%.2f, p99 %.0f ms — baseline içinde", cur.ErrorRate, cur.P99Ms))
		}
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
	v := RowSplit(cur, base, hasBase, nil)
	return v.Level, v.Reasons, v.ErrDelta, v.P99Mult
}

// RowVerdict — v0.10.1299: satır hükmünün hata ve gecikme kolları AYRI (CoSRE
// taraması gecikme kolunu ham span'le doğrular; doğrulanmazsa YALNIZ o kol
// düşer, hata kolu kalır).
type RowVerdict struct {
	Level      string   // birleşik (kötüsü)
	Reasons    []string // birleşik gerekçeler (hata önce, sonra gecikme / susturma notu)
	ErrLevel   string
	ErrReasons []string
	LatLevel   string
	LatReason  string // LatLevel ≠ ok → bulgu gerekçesi; ok → susturma notu ("" = yok)
	ErrDelta   float64
	P99Mult    float64 // yalnız LatLevel ≠ ok iken dolu (etki çarpanı)
	P99Rel     float64
}

// Compose — SAF: kolları birleştir (doğrulama bir kolu değiştirdikten sonra
// da çağrılır).
func (v *RowVerdict) Compose() {
	v.Level = LevelOK
	if LevelRank(v.ErrLevel) < LevelRank(v.Level) {
		v.Level = v.ErrLevel
	}
	if LevelRank(v.LatLevel) < LevelRank(v.Level) {
		v.Level = v.LatLevel
	}
	v.Reasons = append([]string(nil), v.ErrReasons...)
	if v.LatReason != "" {
		v.Reasons = append(v.Reasons, v.LatReason)
	}
	if v.LatLevel == LevelOK {
		v.P99Mult = 0
	}
}

// RowSplit — SAF: Row'un kollu hâli; priorBucketP99 = önceki pencerenin kova
// p99'ları (oynaklık kolu; nil = yok).
func RowSplit(cur, base RED, hasBase bool, priorBucketP99 []float64) (v RowVerdict) {
	v = RowVerdict{ErrLevel: LevelOK, LatLevel: LevelOK}
	defer v.Compose() // adlı sonuç: her dönüş yolunda birleştirilir
	if cur.Spans <= VolumeFloor {
		v.ErrReasons = []string{fmt.Sprintf("düşük hacim (%d span ≤ %d) — puanlanmadı", cur.Spans, VolumeFloor)}
		return v
	}
	if !hasBase {
		if cur.ErrorRate >= ErrCritAbs {
			v.ErrLevel = LevelCrit
			v.ErrReasons = append(v.ErrReasons, fmt.Sprintf("hata oranı %%%.2f ≥ %%%.0f (baseline yok)", cur.ErrorRate, ErrCritAbs))
			v.ErrDelta = cur.ErrorRate
		}
		return v
	}
	errUp := cur.ErrorRate - base.ErrorRate
	switch {
	case errUp >= ErrCritPt || cur.ErrorRate >= ErrCritAbs:
		v.ErrLevel = LevelCrit
		v.ErrReasons = append(v.ErrReasons, fmt.Sprintf("hata oranı %%%.2f (baseline %%%.2f)", cur.ErrorRate, base.ErrorRate))
	case errUp >= ErrWarnPt:
		v.ErrLevel = LevelWarn
		v.ErrReasons = append(v.ErrReasons, fmt.Sprintf("hata oranı %%%.2f, baseline'dan +%.1f puan", cur.ErrorRate, errUp))
	}
	if errUp > 0 && v.ErrLevel != LevelOK {
		v.ErrDelta = errUp
	}
	lv, rel, mult, why := LatencyLevel(cur, base, priorBucketP99)
	v.LatLevel, v.LatReason, v.P99Rel = lv, why, rel
	if lv != LevelOK {
		v.P99Mult = mult
	}
	return v
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
