package api

import (
	"net/http"
	"time"
)

// reqTagWriter — v0.10.918 (operatör prod logu: "[api] error: code: 241 …
// memory limit" ve "code: 159 … Timeout exceeded" satırları hangi uçtan
// geldiğini söylemiyordu). writeErr yalnız ResponseWriter alır (296 çağrı
// yeri); imzayı değiştirmek yerine mux'a giren her istek METOT + YOL ile
// etiketlenir ve writeErr etiketi yazıcıdan okur. Sorgu dizesi YAZILMAZ
// (arama metni / token taşıyabilir).
//
// Flush + Unwrap korunur: SSE uçları `w.(http.Flusher)` bekler,
// http.ResponseController Unwrap zincirini izler.
//
// v0.10.1422 — durum kodu da yakalanır (http_server_metrics.go): ilk
// WriteHeader ya da başlıksız ilk Write (= 200).
type reqTagWriter struct {
	http.ResponseWriter
	tag    string
	status int
}

func (w *reqTagWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *reqTagWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *reqTagWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *reqTagWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func withReqTag(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		tw := &reqTagWriter{ResponseWriter: w, tag: r.Method + " " + r.URL.Path}
		next.ServeHTTP(tw, r)
		recordHTTPServer(r, tw, time.Since(start)) // v0.10.1422
	})
}

// reqTagSuffix — SAF: " [GET /api/x]" ya da "" (etiketsiz yazıcı; handler
// yazıcıyı kendi tipine sardıysa Unwrap zinciri izlenir).
func reqTagSuffix(w http.ResponseWriter) string {
	for i := 0; i < 8 && w != nil; i++ {
		if t, ok := w.(*reqTagWriter); ok {
			return " [" + t.tag + "]"
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return ""
		}
		w = u.Unwrap()
	}
	return ""
}
