// Package otlpenc — v0.10.1422 (operatör: "Kendi metriclerini de
// VictoriaMetrics ayarlardaki göndersin ve gözüksün").
//
// Coremetry'nin KENDİ metriklerini (SDK metricdata) VictoriaMetrics'in OTLP
// ucuna gidecek ExportMetricsServiceRequest'e çevirir — ÖNCEDEN Prometheus
// biçiminde: ad `http_server_request_duration_seconds`, etiketler
// `service_name` / `http_route` / `deployment_environment_name`, birim BOŞ.
//
// Neden önceden şekillendirme: VM'in adlandırması `-opentelemetry.
// usePrometheusNaming` bayrağına bağlı. Bayrak kapalıyken noktalı ad +
// noktalı etiket (service.name) olduğu gibi saklanır; frontend'in metrik
// kipi (lib/httpMetrics.ts) ise `http_server_request_duration_seconds_*` +
// `service_name` okur. Adı ve etiketleri burada sanitize edip birimi boş
// gönderince sonuç bayrağın İKİ hâlinde de aynıdır: sanitize edilmiş ad
// yeniden sanitize edilince değişmez, birim boş olduğu için sonek tekrar
// eklenmez, `_total` zaten varsa eklenmez.
//
// SAF ve bağımlılıksız (yalnız otel SDK + proto): selfobs bunu içe aktarır,
// vmetrics de (pin testi için) — vmetrics→chstore→selfobs zinciri yüzünden
// bu kod vmetrics'te YAŞAYAMAZ (import döngüsü).
package otlpenc

import "strings"

// Kind — enstrüman sınıfı; sonek kuralı buna bağlı.
type Kind int

const (
	KindGauge     Kind = iota // gauge ya da monoton olmayan sum
	KindCounter               // monoton sum → `_total`
	KindHistogram             // `_bucket/_sum/_count` VM'de eklenir
)

// unitSuffix — internal/vmetrics/names.go promUnitSuffixes'in yazım
// karşılığı (okuma tarafı bu dört soneki aday üretir; burada fazlası
// üretilseydi okuma onu hiç bulamazdı). TestNamesMatchReadCandidates
// (vmetrics) iki tarafı pinler.
func unitSuffix(unit string, k Kind) string {
	switch strings.TrimSpace(unit) {
	case "s":
		return "_seconds"
	case "ms":
		return "_milliseconds"
	case "By":
		return "_bytes"
	case "1":
		if k == KindGauge {
			return "_ratio"
		}
	}
	return ""
}

// PromMetricName — OTel adı + birim + sınıf → Prometheus adı. İdempotent:
// çıktısı yeniden verilince (birim boş ya da aynı) değişmez.
func PromMetricName(name, unit string, k Kind) string {
	n := sanitize(strings.TrimSpace(name), true)
	if n == "" {
		return ""
	}
	if k == KindCounter {
		n = strings.TrimSuffix(n, "_total")
	}
	if suf := unitSuffix(unit, k); suf != "" && !strings.HasSuffix(n, suf) {
		n += suf
	}
	if k == KindCounter {
		n += "_total"
	}
	return n
}

// PromLabel — etiket anahtarı: [a-zA-Z0-9_] dışı `_`, baştaki rakama `_`
// öneki (vmetrics.promLabel ile aynı kural; pin testi orada).
func PromLabel(k string) string { return sanitize(strings.TrimSpace(k), false) }

func sanitize(s string, allowColon bool) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s) + 1)
	if s[0] >= '0' && s[0] <= '9' {
		b.WriteByte('_')
	}
	for _, c := range s { // rune başına bir `_` (regexp yazımıyla aynı)
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			(allowColon && c == ':')
		if ok {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
