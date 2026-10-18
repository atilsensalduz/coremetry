package api

import (
	"net/http/httptest"
	"testing"
)

// v0.9.471 (olay: v0.9.465-470 tag'leri boot'ta panic) — Go 1.22 ServeMux
// kalıp çakışmaları KAYIT anında panic'ler; kayıt Start() içinde
// gömülüyken hiçbir test mux'u kurmuyordu. "GET /api/anomalies/events/{id}"
// (v465) mevcut "GET /api/anomalies/{id}/rootcause" ile çakıştı ve altı
// sürüm açılamaz çıktı — go test yeşil, prod boot kırmızıydı.
//
// Bu test route kayıtlarını GERÇEKTEN kurar: bir sonraki çakışma prod
// yerine ilk test koşusunda patlar. Handler'lar çağrılmaz — yalnız kayıt;
// zero-value Server yeterlidir (kayıt aşaması alan dereference etmez;
// ederse o da yakalanmaya değer bir boot bağımlılığıdır).
func TestMuxRoutePatterns(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("route kaydı panic'ledi (ServeMux kalıp çakışması ya da kayıt-anı bağımlılığı): %v", r)
		}
	}()
	s := &Server{}
	// v0.10.247 — buildMux: init() defteri (route_registry.go) rotaları da
	// çakışma testine girer; yalnız registerRoutes olsaydı defter rotası
	// boot'ta panic'lerdi (v0.9.465-470 sınıfı).
	_ = s.buildMux()
}

// v0.10.1361 — /api/endpoints/metric SİLİNDİ: /endpoints listesi
// (v0.10.1359) ve detayı (v0.10.1352) metrik kipinde HTTP Metrics
// PromQL'ini okuyor; uç ikinci, tutarsız bir cevap hesaplıyordu. Rota
// geri gelmesin: ne defterde ne mux'ta. (Kayıtsız yol SPA catch-all'ına
// düşer — 404 değil 200 + index.html, /api-route; o yüzden kalıba bakılır.)
func TestEndpointsMetricRouteRemoved_v0101361(t *testing.T) {
	if _, ok := extraRouteRegistrars["endpoints-metric"]; ok {
		t.Fatal("endpoints-metric defterde olmamalı (v0.10.1361'de silindi)")
	}
	mux := (&Server{}).buildMux()
	_, pattern := mux.Handler(httptest.NewRequest("GET", "/api/endpoints/metric", nil))
	if pattern == "GET /api/endpoints/metric" {
		t.Fatalf("GET /api/endpoints/metric hâlâ kayıtlı (kalıp %q)", pattern)
	}
	// Span yolu yerinde kalır.
	if _, p := mux.Handler(httptest.NewRequest("GET", "/api/endpoints", nil)); p != "GET /api/endpoints" {
		t.Fatalf("span listesi GET /api/endpoints kayıtlı olmalı, kalıp %q", p)
	}
}
