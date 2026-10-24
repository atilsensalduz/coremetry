package api

// anomaly_explain.go — "servis X için şu pencerede neden anomali yok?"
// (operatör, prod 2026-10-24: mobil BFF gateway'inin 5xx'i saatlerce yükseldi,
// Problem açılmadı). Metrik dedektörünün kararını tek servis × tek metrik için
// geçmiş pencerede replay eder (internal/anomaly/anomaly_explain.go): kaynak
// (VM / span / hold + gerekçe), taban, z, sürekli teknik hata yükselişi ve
// açmayı durduran İLK kapı, adım adım. v0.10.1521 — aynı adımlarda davranış
// motorunun hükmü de (`steps[].behavior`, `behaviorVerdict`).
//
//	GET /api/settings/anomaly-red-source/explain?service=<ad>&metric=error_rate
//	    &from=<unix sn | RFC3339>&to=<unix sn | RFC3339>
//
// Admin (durum ucuyla aynı yetki). Pencere ≤ 12 sa (varsayılan son 6 sa), 5 dk
// ızgara. Okumalar istek anında, sınırlı: span MV'sine servis-filtreli tek
// sorgu + VM'e servis eşleştiricili ≤ 60 küçük sorgu; lider belleğine
// dokunmaz, her podda çalışır. Önbelleksiz: teşhis ucu, tazelik şart.

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
)

func init() { registerRoutesExtra("anomaly-explain", (*Server).registerAnomalyExplainRoutes) }

func (s *Server) registerAnomalyExplainRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/anomaly-red-source/explain", auth.RequireRole(auth.RoleAdmin, s.getAnomalyExplain))
}

// parseExplainTime — SAF: unix saniye ya da RFC3339 (ofsetli yerel saat dahil).
// Boş / geçersiz → sıfır (pencere normalizasyonu varsayılana düşer).
func parseExplainTime(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n > 1e12 { // milisaniye / nanosaniye toleransı
			for n > 1e12 {
				n /= 1000
			}
		}
		return time.Unix(n, 0)
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t
	}
	return time.Time{}
}

func (s *Server) getAnomalyExplain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	svc := strings.TrimSpace(q.Get("service"))
	if svc == "" || len(svc) > 512 {
		writeJSONError(w, http.StatusBadRequest, "service gerekli")
		return
	}
	from, to := anomaly.NormalizeExplainWindow(parseExplainTime(q.Get("from")), parseExplainTime(q.Get("to")), time.Now())
	deps := anomaly.ExplainDeps{Store: s.store}
	if s.vmetrics != nil {
		deps.VM = s.vmetrics
	}
	rep, err := anomaly.ExplainServiceWindow(r.Context(), deps, anomaly.ExplainRequest{Service: svc, Metric: q.Get("metric"), From: from, To: to})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, rep)
}
