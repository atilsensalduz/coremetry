package api

// argocd_sync_webhook.go — v0.10.1445 — Argo CD Notifications webhook'u →
// argocd_sync_events (geri almanın git kaynağı; okuyucu rollouts_origin.go).
// Gövde şablonu + operatör kurulumu: docs/DEPLOY-EVENTS.md "Argo CD senkron
// webhook'u". Editor rolü (Coremetry API token'ı); instanceId kayıtlı ve
// etkin bir argocd.Settings.Instances[].ID olmalı (aksi 400). Gövde
// http.MaxBytesReader ile 256 KiB; satırlar SAF argocd.RowsFromSyncWebhook
// (canlı satır + status.history[] tohumları).
//
// v0.10.1466 — başarılı yazım "rollouts:" önbelleğini (liste / tek satır /
// çekmece / istatistik; origin okuma anında eklenir ve listeyle birlikte
// önbellektedir) süpürür ki rozet hemen görünsün. Süpürme birleştirilir
// (rolloutSweepGate): Argo'nun toplu senkron fırtınasında en çok
// argocdSyncSweepGap başına bir SCAN+UNLINK + bir artçı — istek başına değil.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() {
	registerRoutesExtra("argocd-sync-webhook", (*Server).registerArgoCDSyncWebhookRoutes)
}

func (s *Server) registerArgoCDSyncWebhookRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/argocd/sync-events", auth.RequireAnyRole(editorRoles, s.postArgoCDSyncEvent))
}

// argocdSyncWriter — yazım dikişi (*chstore.Store; testte sahte).
type argocdSyncWriter interface {
	ArgoCDWriteSyncEvents(ctx context.Context, rows []argocd.SyncEventRow) error
}

var argocdSyncWriterOf = func(s *Server) argocdSyncWriter {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// argocdSyncInstance — kayıtlı + etkin instance (entegrasyon açık).
var argocdSyncInstance = func(id string) (argocd.Instance, bool) {
	svc := argocdSettingsSvc.Load()
	if svc == nil || id == "" {
		return argocd.Instance{}, false
	}
	cfg := svc.Resolved()
	if !cfg.Enabled {
		return argocd.Instance{}, false
	}
	for _, in := range cfg.Instances {
		if in.ID == id && in.Enabled {
			return in, true
		}
	}
	return argocd.Instance{}, false
}

func (s *Server) postArgoCDSyncEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, argocd.SyncWebhookMaxBody)
	defer r.Body.Close()
	var p argocd.SyncWebhookPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "body exceeds 256 KiB")
			return
		}
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	inst, ok := argocdSyncInstance(p.InstanceID)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "unknown instanceId (register and enable it under Settings → Argo CD)")
		return
	}
	cm := s.serviceGitOpsClusterMaps()
	clusterOf := func(ds string) string {
		return argocd.DestClusterID(ds, inst.HubClusterID, cm.byServer, func(u string) string {
			if n, err := thanos.NormalizeAPIServerURL(u); err == nil {
				return n
			}
			return u
		})
	}
	rows, err := argocd.RowsFromSyncWebhook(p, time.Now(), clusterOf)
	if err != nil {
		if errors.Is(err, argocd.ErrSyncWebhookInvalid) {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "sync event mapping failed")
		return
	}
	st := argocdSyncWriterOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store unavailable")
		return
	}
	if err := st.ArgoCDWriteSyncEvents(r.Context(), rows); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write failed")
		return
	}
	live := rows[0]
	s.audit(r, "argocd.sync_event.create", "argocd_app", live.InstanceID+"/"+live.AppNamespace+"/"+live.AppName,
		fmt.Sprintf(`{"phase":%q,"revision":%q,"rows":%d}`, live.Phase, live.Identity(), len(rows)))
	argocdSyncSweep(s)
	writeJSON(w, map[string]any{"written": len(rows)})
}

// argocdSyncSweepGap — iki "rollouts:" süpürmesi arası en kısa aralık.
const argocdSyncSweepGap = 2 * time.Second

// argocdSyncSweepTimeout — süpürme bağlamı (istekten kopuk: Argo bağlantıyı
// kapatsa da süpürme biter; Redis takılırsa sınırlı).
const argocdSyncSweepTimeout = 5 * time.Second

// rolloutSweepGate — v0.10.1466 — eşzamanlı webhook'larda süpürme birleştirici.
// Kural (SAF, request): son süpürmeden ≥ gap geçtiyse hemen süpür; değilse ve
// bekleyen artçı yoksa kalan süre sonra TEK artçı planla; artçı zaten varsa hiçbir
// şey (o artçı bu yazımı da kapsar — yazım artçıdan ÖNCE işlendi).
type rolloutSweepGate struct {
	mu       sync.Mutex
	last     time.Time
	trailing bool
}

// request — (şimdi süpür, artçı gecikmesi; 0 = artçı yok).
func (g *rolloutSweepGate) request(now time.Time, gap time.Duration) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.trailing {
		return false, 0
	}
	if g.last.IsZero() || now.Sub(g.last) >= gap {
		g.last = now
		return true, 0
	}
	g.trailing = true
	return false, g.last.Add(gap).Sub(now)
}

// fire — artçı çalışırken çağrılır.
func (g *rolloutSweepGate) fire(now time.Time) {
	g.mu.Lock()
	g.trailing, g.last = false, now
	g.mu.Unlock()
}

var argocdSyncGate = &rolloutSweepGate{}

// argocdSyncInvalidate — tek süpürme (test dikişi). Önbellek bağlı değilse no-op.
var argocdSyncInvalidate = func(s *Server) {
	if s == nil || s.cache == nil || s.l1 == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), argocdSyncSweepTimeout)
	defer cancel()
	s.cacheInvalidatePrefix(ctx, "rollouts:")
}

// argocdSyncAfter — artçı zamanlayıcı (test dikişi).
var argocdSyncAfter = func(d time.Duration, f func()) { time.AfterFunc(d, f) }

func argocdSyncSweep(s *Server) {
	now, d := argocdSyncGate.request(time.Now(), argocdSyncSweepGap)
	if now {
		argocdSyncInvalidate(s)
		return
	}
	if d > 0 {
		argocdSyncAfter(d, func() {
			argocdSyncGate.fire(time.Now())
			argocdSyncInvalidate(s)
		})
	}
}
