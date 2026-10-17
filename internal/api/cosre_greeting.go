package api

// cosre_greeting.go — v0.10.1255 (operatör: CoSRE karşılamasında P1 sayısı
// yerine "son 1 saatteki exception / hata sayısı").
//
// v0.10.1278 (operatör 2026-10-17: "Son 1 saatte 445.394 hata · 812
// exception grubu" — "occurrences mı bu sayı?"). Eski tanımlar:
//   - errors1h = service_summary_5m'in hatalı SPAN sayısı: her tür (server/
//     client/db/internal), her ortam → tek başarısız istek N kez sayılıyordu;
//     giriş-span ilkesine aykırı.
//   - exceptionGroups1h = son 1 saatte görülen grup sayısı, resolved DAHİL;
//     oluşum değil.
//   - ortam seçicisi yok sayılıyordu.
//
// Yeni tanımlar (chstore.FleetFailedRequests / RecentExceptionGroupCounts):
//   - failedRequests1h = hatalı GİRİŞ isteği (server+consumer) — spanmetrics_1m;
//     ?env= doluyken ortam MV'si payıyla dağıtılır (fleet_errors.go başlığı).
//   - openExceptionGroups1h = açık (new/acknowledged/regressed) ve son 1 saatte
//     görülmüş grup; newExceptionGroups1h = bunlardan son 1 saatte doğan.
//     exception_groups'ta ortam yok → ortam, Exceptions sayfasıyla aynı kuralla
//     (o ortamda koşan servislerin grupları, EnvMemberServices) uygulanır.
//   - Oluşum sayısı YOK: pencere başına oluşum taşıyan bir MV/state yok
//     (exception_groups.occurrences ömür boyu); ham spans taranmaz.

import (
	"context"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"strings"
	"time"
)

func init() { registerRoutesExtra("cosre-greeting", (*Server).registerCosreGreetingRoutes) }

func (s *Server) registerCosreGreetingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cosre/greeting", s.getCosreGreeting)
}

type cosreGreeting struct {
	FailedRequests1h      uint64 `json:"failedRequests1h"`
	OpenExceptionGroups1h uint64 `json:"openExceptionGroups1h"`
	NewExceptionGroups1h  uint64 `json:"newExceptionGroups1h"`
	// Env — istenen (normalize) ortam; "" = tüm ortamlar.
	Env string `json:"env"`
	// EnvApplied — hatalı istek sayısı ortama daraltıldı mı. Ortam MV'si
	// pencereyi kapsamıyorsa (geriye dolmaz) false: sayı tüm ortamlardır.
	EnvApplied bool `json:"envApplied"`
	// ExceptionsEnvApplied — grup sayıları ortam üyesi servislere daraltıldı
	// mı (env haritası çözülemezse soft-fail → kısıtsız, false).
	ExceptionsEnvApplied bool `json:"exceptionsEnvApplied"`
}

// normalizeGreetingEnv — "" / "all" (büyük-küçük harf) = tüm ortamlar.
func normalizeGreetingEnv(raw string) string {
	v := strings.TrimSpace(raw)
	if strings.EqualFold(v, "all") {
		return ""
	}
	if len(v) > 64 { // lib/useUrlEnv MAX_ENV_LEN ile aynı tavan
		v = v[:64]
	}
	return v
}

// cosreGreetingKey — SAF (hash-all-inputs): tek girdi env (FNV özeti) +
// dakika kovası. v2: yanıt şekli değişti, v1 kayıtları okunmasın.
func cosreGreetingKey(env string, now time.Time) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(env))
	return fmt.Sprintf("cosre-greeting:v2:%d:%x", now.Unix()/60, h.Sum64())
}

func (s *Server) getCosreGreeting(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	env := normalizeGreetingEnv(r.URL.Query().Get("env"))
	s.serveCached(w, r, cosreGreetingKey(env, now), 60*time.Second, func(ctx context.Context) (any, error) {
		from := now.Add(-time.Hour)
		out := cosreGreeting{Env: env}
		reqEnv := ""
		if env != "" && s.store.EnvSummaryCovers(ctx, from) {
			reqEnv, out.EnvApplied = env, true
		}
		n, err := s.store.FleetFailedRequests(ctx, reqEnv, from, now)
		if err != nil {
			return nil, err
		}
		out.FailedRequests1h = n

		var services []string
		if env != "" {
			members, merr := s.store.EnvMemberServices(ctx, env)
			switch {
			case merr != nil:
				// Soft-fail → kısıtsız (Exceptions sayfasının duruşu).
				log.Printf("[cosre-greeting] env %q çözülemedi (exception sayıları kısıtsız): %v", env, merr)
			case len(members) == 0:
				// Ortamda servis yok → grup da yok (Exceptions sayfası boş sayfa döndürür).
				out.ExceptionsEnvApplied = true
				return out, nil
			default:
				services, out.ExceptionsEnvApplied = members, true
			}
		}
		open, fresh, gerr := s.store.RecentExceptionGroupCounts(ctx, from, services)
		if gerr == nil {
			out.OpenExceptionGroups1h, out.NewExceptionGroups1h = open, fresh
		} else {
			log.Printf("[cosre-greeting] exception grup sayımı: %v", gerr)
		}
		return out, nil
	})
}
