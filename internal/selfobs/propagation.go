package selfobs

// propagation.go — v0.10.1470 (D1 takibi, operatör kararı 2026-10-23).
//
//  1. Süreç geneli W3C yayıcısı. v0.6.42'den beri frontend (lib/otel.ts) her
//     fetch'e `traceparent` basıyor ve api.listen otelhttp.NewHandler ile
//     sarılı — ama otelhttp'nin varsayılan yayıcısı otel.GetTextMapPropagator()
//     ve süreçte hiç kurulmamıştı (boş bileşik): gelen başlık okunmuyor, her
//     backend isteği kendi kökünü açıyordu (tarayıcı trace'i HTTP sınırında
//     kopuk). Init trace hattını kurduğunda tracecontext + baggage kurulur.
//     Self-obs kapalıyken HİÇ kurulmaz: aksi halde gelen başlık noop span
//     bağlamına girer ve copilot istemcisinin açık TraceContext yayıcısı
//     (copilot/trace.go) onu sağlayıcıya iletirdi — kapalıyken davranış
//     bayt bayt eski.
//  2. TracesQueryable — self-obs trace'leri BU Coremetry'de sorgulanabilir mi
//     (/ai çağrı çekmecesindeki "Trace" linkinin kapısı). Uç nokta pod'un
//     kendi alıcısı (loopback; all kipinin varsayılanı) ise evet; dış bir
//     collector ise trace orada — link boş bir /trace sayfasına giderdi.
//     COREMETRY_SELF_OBS_TRACES_LOCAL=true|false otomatiği ezer (chart kendi
//     otelcol / ingest servisine yönlendirirken true basar; v0.10.1471).

import (
	"net"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const envTracesLocal = "COREMETRY_SELF_OBS_TRACES_LOCAL"

var tracesQueryable bool

// Propagator — Coremetry'nin tek yayıcısı: W3C tracecontext + baggage
// (/otel-conventions §1).
func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// installTraceGlobals — trace hattı kurulduğunda (Init, enabled) süreç
// geneli yayıcı + sorgulanabilirlik bayrağı. Init dışında çağrılmaz.
func installTraceGlobals(endpoint, override string) {
	otel.SetTextMapPropagator(Propagator())
	tracesQueryable = tracesLocal(endpoint, override)
}

// TracesQueryable — self-obs trace'leri bu kurulumda sorgulanabilir mi.
// Self-obs kapalıyken her zaman false.
func TracesQueryable() bool { return enabled && tracesQueryable }

// tracesLocal — SAF. override "true/1/yes/on" → true, "false/0/no/off" →
// false; aksi halde uç nokta ana makinesi loopback ise true.
func tracesLocal(endpoint, override string) bool {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return isLoopbackEndpoint(endpoint)
}

// isLoopbackEndpoint — "localhost:4317", "127.0.0.1:4317", "[::1]:4317",
// "http://localhost:4317" → true.
func isLoopbackEndpoint(endpoint string) bool {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return false
	}
	if strings.Contains(ep, "://") {
		if u, err := url.Parse(ep); err == nil {
			ep = u.Host
		}
	}
	host := ep
	if h, _, err := net.SplitHostPort(ep); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
