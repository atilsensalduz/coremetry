package api

// rollouts_origin.go — v0.10.1445 (operatör: "KSM dedektörü doğru mu"):
// v2 rollout satırına GİT kaynağı — gerçek geri alma (Argo daha önce
// senkronlanmış bir revizyona döndü) mı, eski bir yapının ileri commit'le
// yeniden deploy'u mu. KSM ikisini ayıramaz (pod şablonu aynıysa Kubernetes
// eski ReplicaSet'i yeniden kullanır); ayrım argocd_sync_events'ten
// (Argo CD Notifications webhook'u, argocd_sync_webhook.go) okuma anında.
//
// v0.10.1494 (CoSRE 2d): çekirdek internal/chstore/rollout_origin.go'ya
// taşındı; burada yalnız ayar kapısı + sarmalayıcı (mcp_deps.go RolloutOrigin
// aynı sarmalayıcıyı SRE / list_deployments'a verir).
//
// Akış (liste başına iki sınırlı sorgu, listeyle birlikte önbellekte):
//  1. satırların iş yükleri → argocd_app_mapping İŞ YÜKÜ kenarları (exact |
//     estimated; zayıf namespace kenarı kanıt değil); iş yüküne >1 uygulama
//     → belirsiz (unknown).
//  2. uygulamaların senkronları [en eski başlangıç − 90 gün, en yeni + W + 1 sa].
//  3. SAF matchSyncForRollout: started_at'i [op_started − W, max(finished,
//     op_started) + W] içinde tutan, en yakın Succeeded senkron; ondan önceki
//     Succeeded = prev; yeni revizyon prev'den DAHA ESKİ bir senkronda varsa
//     SeenBefore.
//  4. rollout.ClassifyRolloutOrigin (doğruluk tablosu; Argo kanıtı yoksa
//     HER ZAMAN unknown).
//
// Soft-fail: okuma hatası → log, satırlarda `origin` yok (liste yine döner).
// Argo entegrasyonu kapalıysa hiç sorgu atılmaz, alan eklenmez.

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// rolloutOriginStore — CH dikişi (*chstore.Store; testte sahte). v0.10.1494:
// çekirdek chstore.AttachRolloutOriginWith'e taşındı (SRE okuyucuları ve
// list_deployments mcptools.Deps.RolloutOrigin ile aynı yolu görür).
type rolloutOriginStore = chstore.RolloutOriginReader

var rolloutOriginStoreOf = func(s *Server) rolloutOriginStore {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// rolloutOriginSettings — etkin mi + pencere (dk). Entegrasyon kapalı ya da
// etkin instance yok → false (sorgu yok).
var rolloutOriginSettings = func() (bool, int) {
	svc := argocdSettingsSvc.Load()
	if svc == nil {
		return false, 0
	}
	cfg := svc.Resolved()
	if !cfg.Enabled {
		return false, 0
	}
	for _, in := range cfg.Instances {
		if in.Enabled {
			return true, cfg.Classification.WindowMin
		}
	}
	return false, 0
}

// attachRolloutOrigin — liste / tek satır / çekmece yükleyicileri çağırır.
func (s *Server) attachRolloutOrigin(ctx context.Context, rows []chstore.RolloutRow) {
	on, win := rolloutOriginSettings()
	st := rolloutOriginStoreOf(s)
	if !on || st == nil {
		return
	}
	if err := chstore.AttachRolloutOriginWith(ctx, rows, st, win, time.Now()); err != nil {
		log.Printf("[rollouts] origin (argocd senkron) okunamadı: %v — rozet yok", err)
	}
}

// attachRolloutOriginOne — tek satır (getRollout v2 dalı, çekmece).
func (s *Server) attachRolloutOriginOne(ctx context.Context, row *chstore.RolloutRow) {
	if row == nil {
		return
	}
	rs := []chstore.RolloutRow{*row}
	s.attachRolloutOrigin(ctx, rs)
	*row = rs[0]
}
