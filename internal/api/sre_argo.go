package api

// sre_argo.go — v0.10.1293 — AI SRE Faz 4: geri alma önerisinin Argo eki için
// DAR okuma dikişi (mcptools.Deps.ArgoApps).
//
// Servis → Argo CD uygulaması eşlemesi api katmanında (service_gitops.go
// buildServiceGitOps: eşleyici tablosu kapsam kapısıyla, değilse hub başına
// canlı argocd_app_info sorgusu). internal/sre api'yi import edemez
// (TestSREImports) — o yüzden burada GitOps sekmesiyle AYNI kurucu çağrılır,
// yalnız uygulama adı + senkron/sağlık + iş yükleri döner. Rollouts bölümü
// istenmez (rolloutsOn=false: öneri metni rollout satırını zaten deploy
// adımından alır). Salt okuma; ajan hiçbir Argo işlemi (sync / rollback)
// tetiklemez — böyle bir yol bu pakette de yok.
//
// v0.10.1310 — AI SRE Faz 2 argo_sync: aynı dikiş koşu başına TEK okumayla
// sre.Facts.Argo'yu besler (skor + öneri ortak); satıra 24 saatlik senkron
// toplamı (Syncs24h) eklendi — GitOps sekmesinin zaten hesapladığı sayım,
// yeni sorgu yok.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// mcpArgoApps — Deps.ArgoApps. Argo ayarı yok / hub yok → (nil, nil).
func (s *Server) mcpArgoApps(ctx context.Context, service string) ([]mcptools.ArgoAppStatus, error) {
	service = strings.TrimSpace(service)
	svc := argocdSettingsSvc.Load()
	if service == "" || svc == nil || s.thanos == nil {
		return nil, nil
	}
	now := serviceGitOpsNow().UTC().Truncate(time.Minute)
	resp, err := s.buildServiceGitOps(ctx, service, now, svc, s.serviceGitOpsClusterMaps(), false, "")
	if err != nil || resp == nil || !resp.Argo.Configured {
		return nil, err
	}
	return argoAppStatuses(resp.Argo), nil
}

// argoAppStatuses — SAF: GitOps sekmesinin Argo bölümü → dikiş satırları.
func argoAppStatuses(a serviceGitOpsArgo) []mcptools.ArgoAppStatus {
	out := make([]mcptools.ArgoAppStatus, 0, len(a.Apps))
	for _, app := range a.Apps {
		row := mcptools.ArgoAppStatus{App: app.Name, Sync: app.SyncStatus, Health: app.HealthStatus}
		for _, w := range app.Workloads {
			row.Workloads = append(row.Workloads, w.Workload)
		}
		// v0.10.1310 — faz → adet toplamı; nil (okunmadı) nil kalır, {} → 0.
		if app.Syncs24h != nil {
			n := 0
			for _, c := range app.Syncs24h {
				n += c
			}
			row.Syncs24h = &n
		}
		out = append(out, row)
	}
	return out
}
