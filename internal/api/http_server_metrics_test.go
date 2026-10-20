package api

// v0.10.1422 — operatör: 'Kendi metriclerini de VictoriaMetrics ayarlardaki
// göndersin ve gözüksün' + "Coremetry api, worker ve ingester için yap".
// HTTP RED kaydının saf parçaları + reqTagWriter'ın durum yakalaması.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestHTTPMetricPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/services":    true,
		"/api/mcp/message": true,
		"/v1/traces":       true,
		"/v1/metrics":      true,
		"/api/health":      false,
		"/livez":           false,
		"/readyz":          false,
		"/":                false,
		"/assets/index.js": false,
		"/services/x":      false,
	} {
		if got := httpMetricPath(p); got != want {
			t.Errorf("httpMetricPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestHTTPMethodLabel(t *testing.T) {
	for m, want := range map[string]string{"GET": "GET", "PATCH": "PATCH", "OPTIONS": "OPTIONS", "PROPFIND": "_OTHER", "get": "_OTHER", "": "_OTHER"} {
		if got := httpMethodLabel(m); got != want {
			t.Errorf("httpMethodLabel(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestHTTPRouteLabel(t *testing.T) {
	for p, want := range map[string]string{
		"GET /api/traces/{id}":     "/api/traces/{id}",
		"POST /v1/traces":          "/v1/traces",
		"/api/plain":               "/api/plain",
		"/":                        "",
		"":                         "",
		"example.com/api/x":        "",
		"GET /api/files/{path...}": "/api/files/{path...}",
	} {
		if got := httpRouteLabel(p); got != want {
			t.Errorf("httpRouteLabel(%q) = %q, want %q", p, got, want)
		}
	}
}

// withReqTag'in ServeMux'a verdiği *Request'te desen yazılı olmalı (route
// kaynağı) ve yazıcı durum kodunu yakalarken Flusher'ı korumalı (SSE).
func TestWithReqTagCapturesStatusPatternFlusher(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("Flusher lost")
		}
		w.WriteHeader(http.StatusTeapot)
	})
	mux.HandleFunc("GET /api/implicit", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	var seen []string
	var codes []int
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &reqTagWriter{ResponseWriter: w}
		mux.ServeHTTP(tw, r)
		seen = append(seen, httpRouteLabel(r.Pattern))
		codes = append(codes, tw.status)
	})
	for _, p := range []string{"/api/things/abc123", "/api/implicit", "/api/nope"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", p, nil))
	}
	wantRoutes := []string{"/api/things/{id}", "/api/implicit", ""}
	wantCodes := []int{http.StatusTeapot, http.StatusOK, http.StatusNotFound}
	for i := range wantRoutes {
		if seen[i] != wantRoutes[i] || codes[i] != wantCodes[i] {
			t.Errorf("#%d: route=%q code=%d, want %q %d", i, seen[i], codes[i], wantRoutes[i], wantCodes[i])
		}
	}
	// Uçtan uca: withReqTag kaydı panik/blok olmadan geçer (noop meter).
	rec := httptest.NewRecorder()
	withReqTag(mux).ServeHTTP(rec, httptest.NewRequest("GET", "/api/things/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestMergeVMSettingsSelfMetrics(t *testing.T) {
	str := func(s string) *string { return &s }
	cur := vmetrics.Settings{SelfMetrics: "off"}
	for _, c := range []struct {
		name string
		in   *string
		want string
		bad  bool
	}{
		{"nil = saklı korunur", nil, "off", false},
		{"auto blob'a boş yazılır", str("auto"), "", false},
		{"on", str("on"), "on", false},
		{"OFF normalize", str("OFF"), "off", false},
		{"bilinmeyen 400", str("always"), "", true},
	} {
		got, bad := mergeVMSettings(vmSettingsInput{SelfMetrics: c.in}, cur)
		if (bad != "") != c.bad || (!c.bad && got.SelfMetrics != c.want) {
			t.Errorf("%s: got %q bad=%q", c.name, got.SelfMetrics, bad)
		}
	}
}
