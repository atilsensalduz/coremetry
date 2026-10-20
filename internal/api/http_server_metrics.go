package api

// http_server_metrics.go — v0.10.1422 (operatör: "Kendi metriclerini de
// VictoriaMetrics ayarlardaki göndersin ve gözüksün" + "Coremetry api,
// worker ve ingester için yap").
//
// Coremetry'nin KENDİ HTTP RED'i: http.server.request.duration (semconv
// adı; VM'de http_server_request_duration_seconds_* — servis sayfasının
// metrik kipi tam bu aileyi okur). withReqTag'in içinde (req_tag.go) kayıt
// edilir, böylece api.go büyümez ve aynı mux'u sunan HER rol — api'nin
// /api/* uçları, ingest'in OTLP/HTTP alıcısı (/v1/traces|metrics|logs) —
// kendi service.name'iyle görünür.
//
// KARDİNALİTE: http.route = r.Pattern'in yol kısmı ("/api/traces/{id}") —
// ServeMux eşlemeyi withReqTag'in verdiği *Request'e yazar. Şablon kayıtla
// sınırlıdır; collapseRoute bilinçli kullanılmadı (span adı için yeterli ama
// slug parçalarını — pano/görünüm adları — açık bırakıyor). Eşleşmeyen
// istek (404/405, SPA geri düşüşü "/") route etiketi TAŞIMAZ: saldırganın
// seçtiği yol etiket olamaz. Metot semconv'un bilinen fiilleri ya da _OTHER.
//
// KAPSAM DIŞI: sağlık uçları (probe gürültüsü), text/event-stream cevapları
// (/api/events, /api/logs/tail, /api/mcp/sse — süreleri "bağlantı ömrü"dür,
// gecikme değil; p99'u saatlere çekerlerdi) ve /api/ (MCP dahil: /api/mcp/*), /v1/ dışındaki
// her şey (statik UI varlıkları). auth.Middleware dışarıda olduğu için 401
// ile reddedilen istekler de sayılmaz (kimlik katmanı ayrı sinyal).
//
// İSTEK YOLU: tek time.Now + atomik-yakın SDK kaydı; etiket kümesi anahtar
// başına önbellekte (selfobs/instruments.go). Bloklamaz, hata dönmez.

import (
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

func recordHTTPServer(r *http.Request, w *reqTagWriter, d time.Duration) {
	if !httpMetricPath(r.URL.Path) {
		return
	}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		return
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK // gövdesiz, başlıksız handler: net/http 200 yazar
	}
	selfobs.RecordHTTPServer(httpMethodLabel(r.Method), httpRouteLabel(r.Pattern), status, d)
}

// httpMetricPath — SAF: ölçülen yol mu.
func httpMetricPath(p string) bool {
	switch p {
	case "/api/health", "/livez", "/readyz":
		return false
	}
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/v1/")
}

// httpMethodLabel — SAF: semconv http.request.method (bilinmeyen → _OTHER).
func httpMethodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodConnect, http.MethodOptions, http.MethodTrace, http.MethodPatch:
		return m
	}
	return "_OTHER"
}

// httpRouteLabel — SAF: ServeMux deseni → http.route. "GET /api/x/{id}" →
// "/api/x/{id}"; boş ya da kök ("/", SPA geri düşüşü) → "" (etiket yok).
func httpRouteLabel(pattern string) string {
	p := strings.TrimSpace(pattern)
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = strings.TrimSpace(p[i+1:])
	}
	if p == "" || p == "/" || !strings.HasPrefix(p, "/") {
		// Host'lu desen ("example.com/x") bu kodda yok; olursa etiket yok.
		return ""
	}
	return p
}
