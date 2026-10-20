package api

// rollout_workloads.go — v0.10.1420 (operatör, /rollouts Canlı sekmesi:
// "Workload seçilerek filtrelenebilsin").
//
//	GET /api/rollouts/workloads?q=&from&to&cluster&namespace&limit   viewer — Workload seçicisinin sunucu araması
//	→ {names: [...], capped: bool}
//
// Seçici ASLA tam katalog çekmez (CLAUDE.md "Picker = server-side search"):
// yazılan desen (joker * ? — ServicePicker sözleşmesi; jokersiz alt-dizge)
// pencerede rollout'u olan workload adlarına uygulanır, en çok
// chstore.RolloutWorkloadNamesMax ad. Kaynak (v1 workload_rollouts | v2
// rollout_events) liste ucuyla aynı (rolloutSource); platform namespace
// dışlaması da aynı (açık ?namespace= kazanır). Seçilen ad listeye
// ?workload= olarak SUNUCUDA uygulanır (rollouts.go listRollouts — liste 200'de
// kesilir; istemci süzgeci kesik sayfada satır kaçırırdı).
//
// Bayrak kapalıyken 404 {disabled:true} (rolloutEnabled). Okuma ucu: rol
// kapısı viewer (liste gibi), audit yok. serveCached 30 s, anahtar TÜM
// girdileri taşır (rolloutWorkloadNamesKey — saf, testli); limit anahtardan
// ÖNCE kelepçelenir. api.go BÜYÜMEZ: kayıt init()'te registerRoutesExtra.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func init() { registerRoutesExtra("rollout-workloads", (*Server).registerRolloutWorkloadRoutes) }

func (s *Server) registerRolloutWorkloadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/rollouts/workloads", s.getRolloutWorkloadNames)
}

const (
	rolloutWorkloadNamesDefault = 50
	rolloutWorkloadNamesTTL     = 30 * time.Second
	rolloutWorkloadQueryMaxLen  = 128
)

// rolloutWorkloadSearcher — depo dikişi (handler testi sahte depo verir;
// rolloutReader'a eklenmedi ki o arayüzün sahteleri büyümesin).
type rolloutWorkloadSearcher interface {
	RolloutWorkloadNames(ctx context.Context, v2 bool, f chstore.RolloutFilter, q string, from, to time.Time, limit int) ([]string, bool, error)
}

var rolloutWorkloadSearcherOf = func(s *Server) rolloutWorkloadSearcher { return s.store }

// parseRolloutWorkloadSearch — SAF: istek → süzgeç + desen + kelepçeli limit.
// Desen kırpılır ve rolloutWorkloadQueryMaxLen'de kesilir (anahtar ve SQL
// bağı sınırlı kalsın).
func parseRolloutWorkloadSearch(get func(string) string) (chstore.RolloutFilter, string, int) {
	f := chstore.RolloutFilter{ClusterID: strings.TrimSpace(get("cluster")), Namespace: strings.TrimSpace(get("namespace"))}
	q := strings.TrimSpace(get("q"))
	if len(q) > rolloutWorkloadQueryMaxLen {
		q = q[:rolloutWorkloadQueryMaxLen]
	}
	limit := parseInt(get("limit"), rolloutWorkloadNamesDefault)
	if limit <= 0 {
		limit = rolloutWorkloadNamesDefault
	}
	if limit > chstore.RolloutWorkloadNamesMax {
		limit = chstore.RolloutWorkloadNamesMax
	}
	return f, q, limit
}

func (s *Server) getRolloutWorkloadNames(w http.ResponseWriter, r *http.Request) {
	if !s.rolloutEnabled(w) {
		return
	}
	from, to := parseFromTo(r, 24*time.Hour)
	f, q, limit := parseRolloutWorkloadSearch(r.URL.Query().Get)
	if f.ClusterID != "" {
		if c, ok := s.resolveCluster(f.ClusterID); ok {
			f.ClusterID = c.EffectiveID()
		}
	}
	f.ExcludeNamespaces = rollout.EffectiveNamespaceExclude(s.rolloutExcludeNamespaces(), f.Namespace)
	src := s.rolloutSource()
	key := rolloutWorkloadNamesKey(src, f, q, limit, from, to)
	s.serveCached(w, r, key, rolloutWorkloadNamesTTL, func(ctx context.Context) (any, error) {
		names, capped, err := rolloutWorkloadSearcherOf(s).RolloutWorkloadNames(ctx, src == rollout.SourceV2, f, q, from, to, limit)
		if err != nil {
			return nil, err
		}
		if names == nil {
			names = []string{}
		}
		return map[string]any{"names": names, "capped": capped}, nil
	})
}
