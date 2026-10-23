package chstore

import (
	"strings"
	"unicode/utf8"
)

// anomaly_sensitivity_red_source.go — v0.10.1478: metrik dedektörü + davranış
// motorunun RED serisi kaynağı (anomaly_sensitivity.redSource / redSourceEnv).
//
// Neden: prod trace'leri gateway'de KUYRUK örnekleniyor (hatalar hep tutulur +
// ~%5 taban). Span MV'sinden okunan error_rate yukarı yanlı ve örnekleme
// politikasıyla oynuyor; istek hızı %5'lik gürültülü bir örnek (gece / küçük
// servis sahte "trafik düşüşü"). VictoriaMetrics OTel collector'ın ÖRNEKLENMEMİŞ
// HTTP/gRPC sunucu histogramını taşıyor. Kaynak seçimi servis başına,
// internal/anomaly/red_vm_select.go'da.

const (
	// AnomalyREDSourceAuto — VM varsa ve servisin ailesi varsa VM, yoksa span.
	AnomalyREDSourceAuto = "auto"
	// AnomalyREDSourceSpan — v0.10.1477 davranışı: yalnız span MV'si (geri alma).
	AnomalyREDSourceSpan = "span"
)

// redSourceEnvMax — ortam adı tavanı (httpEnvScopeTermMax ile aynı sayı).
const redSourceEnvMax = 200

// normalizeREDSource — SAF: bilinmeyen/boş → auto; büyük-küçük harf toleranslı.
func normalizeREDSource(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), AnomalyREDSourceSpan) {
		return AnomalyREDSourceSpan
	}
	return AnomalyREDSourceAuto
}

// normalizeREDSourceEnv — SAF: kırpılır; tavanı aşan ad düşer (boş = kapsam yok).
func normalizeREDSourceEnv(v string) string {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > redSourceEnvMax {
		return ""
	}
	return v
}

// REDSourceMode — nil-güvenli okuma: "auto" | "span".
func (c AnomalySensitivityConfig) REDSourceMode() string { return normalizeREDSource(c.REDSource) }

// REDSourceEnvScope — normalize edilmiş ortam kapsamı ("" = tüm ortamlar).
func (c AnomalySensitivityConfig) REDSourceEnvScope() string {
	return normalizeREDSourceEnv(c.REDSourceEnv)
}

// AlertSourceMode — v0.10.1489: alarm kurallarının ölçü kaynağı ("auto" |
// "span"); aynı normalizasyon (bilinmeyen/boş → auto).
func (c AnomalySensitivityConfig) AlertSourceMode() string { return normalizeREDSource(c.AlertSource) }
