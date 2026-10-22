package api

// service_red_family_route.go — v0.10.1429 (servis sayfası metrik kipi:
// yalnız gRPC sunan servislerde RED). Service → Overview / HTTP Metrics,
// /endpoints listesi ve endpoint detayı PromQL'i TARAYICIDA üretir
// (lib/httpMetrics.ts); hangi AİLEYİ (HTTP / gRPC legacy / gRPC stable)
// okuyacaklarını ve ortam eşleyicisini bilmeleri gerekiyor. Bu uç yalnız
// onu söyler — çözücü red_family.go resolveServiceREDFamily (CoSRE servis
// sağlığı / raporu AYNI çözücüyü kullanır); sorgu yazmaz.
//
//   GET /api/services/{name}/red-family?env=&cluster=&to=   (to = unix ns)
//
// v0.10.1377'nin http-env-scope ucunun yerine geçer (ortam çözümü artık
// ailenin içinde, aile başına): dört yüzey tek istekle hem aileyi hem
// eşleyiciyi alır. Rol kapısı YOK — salt-okunur, viewer da görür. Yalnız VM
// (metricsrc=vm; CH kaynağında 400 — bu yüzeyler zaten VM'e sabit). Çözüm
// 60 sn önbellekte (çözücünün içinde; anahtar TÜM girdiler). Etiket / yoklama
// HATASI önbelleğe yazılmaz ve 200 {state:"unknown", primary:"http", reason}
// döner: sayfa bugünkü HTTP sorgularını çizer ve bunu söyler ("okunamadı ≠
// yok"; boş ekran ya da 5xx değil).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func init() { registerRoutesExtra("service-red-family", (*Server).registerServiceREDFamilyRoutes) }

func (s *Server) registerServiceREDFamilyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/red-family", s.getServiceREDFamily)
}

// getServiceREDFamily — GET /api/services/{name}/red-family
func (s *Server) getServiceREDFamily(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, fmt.Errorf("%w: service required", errBadRequest))
		return
	}
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	cluster := strings.TrimSpace(q.Get("cluster"))
	if len(env) > httpEnvScopeTermMax || len(cluster) > httpEnvScopeTermMax {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("env/cluster en çok %d karakter", httpEnvScopeTermMax))
		return
	}
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	vm, ok := src.(vmMetricSource)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "yalnız VictoriaMetrics kaynağında (metricsrc=vm)")
		return
	}
	now := time.Now()
	to := parseTime(q.Get("to"))
	if to.IsZero() || to.After(now) {
		to = now
	}
	res, rerr := s.resolveServiceREDFamily(r.Context(), vm, name, httpEnvScopeTerms(env, cluster), to)
	if rerr != nil && errors.Is(rerr, context.Canceled) {
		writeErr(w, rerr)
		return
	}
	if rerr != nil {
		// unknown gövdesi 200 döner; ham hata metni (VM host/port) viewer'a
		// gitmez, yalnız sınıfı (http-env-scope ile aynı duruş).
		res.Reason = "VictoriaMetrics okuması BAŞARISIZ (okunamadı ≠ yok): " + string(sourcestate.Classify(rerr))
	}
	writeJSON(w, res)
}
