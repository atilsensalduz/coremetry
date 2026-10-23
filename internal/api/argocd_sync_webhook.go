package api

// argocd_sync_webhook.go — v0.10.1445 — Argo CD Notifications webhook'u →
// argocd_sync_events (geri almanın git kaynağı; okuyucu rollouts_origin.go).
// Gövde şablonu + operatör kurulumu: docs/DEPLOY-EVENTS.md "Argo CD senkron
// webhook'u". Editor rolü (Coremetry API token'ı); instanceId kayıtlı ve
// etkin bir argocd.Settings.Instances[].ID olmalı (aksi 400). Gövde
// http.MaxBytesReader ile 256 KiB; satırlar SAF argocd.RowsFromSyncWebhook
// (canlı satır + status.history[] tohumları). Önbellek süpürmesi yok: liste
// TTL'i kısa, rozet bir sonraki yüklemede görünür.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	writeJSON(w, map[string]any{"written": len(rows)})
}
