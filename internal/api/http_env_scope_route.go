package api

// http_env_scope_route.go — v0.10.1377 (metrik kipi: env/cluster süzgeci VM
// PromQL'inde). Service → HTTP Metrics sekmesi, endpoint sayfası, /endpoints
// listesi ve Overview pencere karoları PromQL'i TARAYICIDA üretir
// (lib/httpMetrics.ts); env/cluster süzgeci için ortam etiketinin ADINI ve
// istenen adın CANLI değer(ler)ini bilmeleri gerekiyor. Bu uç yalnız onu
// söyler — v0.10.1376'nın çözücüsü (http_env_scope.go resolveHTTPEnvScope)
// AYNEN; sorgu yazmaz, yalnız matcher döner.
//
//   GET /api/metrics/http-env-scope?env=&cluster=&to=   (to = unix ns)
//
// Neden ayrı uç, neden PromQL'i sunucuya taşımıyoruz DEĞİL: dört yüzeyin
// ~40 sorgusu tarayıcıda bayt bayt pinli (sekme ↔ endpoint ↔ liste paritesi,
// v0.10.1352/1359/1360); onları sunucuya taşımak o pinleri yeniden yazmak
// olurdu. Matcher seçicide service_name'den hemen sonra durur; Go tarafının
// aynı yeri altın dosyayla pinli (httpMetrics.envScope.golden.json).
//
// Rol kapısı YOK — salt-okunur, viewer da süzgecin uygulanıp uygulanmadığını
// GÖRMELİ. Yalnız VM: CH kaynağında env zaten EnvFilterExpr ile süzülüyor ve
// bu yüzeyler zaten metricsrc=vm'e sabit → CH isteği 400. Önbellek 60 sn,
// anahtar TÜM girdiler (LabelMap'in env/cluster etiketleri, env, cluster,
// 10 dk'ya yuvarlanmış uç — değer penceresi ondan türer). Etiket okuma HATASI
// önbelleğe yazılmaz ve 200 {applied:false, failed:true, reason} döner: sayfa
// süzgeçsiz çizer ve bunu söyler (boş ekran ya da 5xx değil).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() { registerRoutesExtra("http-env-scope", (*Server).registerHTTPEnvScopeRoutes) }

func (s *Server) registerHTTPEnvScopeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/http-env-scope", s.getHTTPEnvScope)
}

// v0.10.1377 — uç sabitleri.
const (
	httpEnvScopeTTL     = 60 * time.Second
	httpEnvScopeTermMax = 200 // env/cluster adı uzunluk tavanı (anahtar şişmesin)
)

// httpEnvScopeKey — SAF (v0.10.1377): önbellek anahtarı. Serbest metinler
// fnvStr ile AYRI AYRI özetlenir ("a|b"+"c" ≠ "a"+"b|c"); LabelMap'in iki
// etiketi de anahtarda (Settings'te etiket değişirse eski matcher servis
// edilmesin); uç 10 dk'ya yuvarlanır (değer penceresi httpEnvValuesWindow).
func httpEnvScopeKey(lm vmetrics.LabelMap, env, cluster string, to time.Time) string {
	return fmt.Sprintf("http-env-scope:v1:src=vm:lm=%s:q=%s:to=%d",
		fnvStr(lm.Env, lm.Cluster), fnvStr(env, cluster), to.Truncate(httpEnvValuesSnap).Unix())
}

// httpEnvScopeTerms — SAF (v0.10.1377): sorgu parametreleri → çözücü terimleri.
func httpEnvScopeTerms(env, cluster string) map[string]string {
	t := map[string]string{}
	if env != "" {
		t[vmetrics.RoleEnv] = env
	}
	if cluster != "" {
		t[vmetrics.RoleCluster] = cluster
	}
	return t
}

// httpEnvScopeFailed — SAF (v0.10.1377): etiket okuma hatası → gövde. Ham hata
// metni (VM host/port) viewer'a gitmez; yalnız sınıfı.
func httpEnvScopeFailed(err error) httpEnvScope {
	return httpEnvScope{Failed: true, Reason: "VM etiket sorgusu başarısız: " + string(sourcestate.Classify(err))}
}

// getHTTPEnvScope — GET /api/metrics/http-env-scope
func (s *Server) getHTTPEnvScope(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	cluster := strings.TrimSpace(q.Get("cluster"))
	if env == "" && cluster == "" {
		writeJSONError(w, http.StatusBadRequest, "env ya da cluster parametresi zorunlu")
		return
	}
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
	s.serveHTTPEnvScope(w, r, vm, env, cluster, to)
}

// serveHTTPEnvScope — v0.10.1377: önbellekli çözüm. serveCached yerine
// cachedJSON: hata önbelleğe YAZILMAZ ve 5xx değil 200 + failed gövdesi döner
// (iptal hariç — writeErr 499).
func (s *Server) serveHTTPEnvScope(w http.ResponseWriter, r *http.Request, vm httpEnvLabelReader, env, cluster string, to time.Time) {
	key := httpEnvScopeKey(vm.MetricLabelMap().Normalized(), env, cluster, to)
	terms := httpEnvScopeTerms(env, cluster)
	body, tier, err := s.cachedJSON(r.Context(), key, httpEnvScopeTTL, r.URL.Query().Get("refresh") == "1",
		func(ctx context.Context) (any, error) {
			return s.resolveHTTPEnvScope(ctx, vm, terms, to)
		})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeErr(w, err)
			return
		}
		writeJSON(w, httpEnvScopeFailed(err))
		return
	}
	writeCacheHit(w, tier, body)
}
