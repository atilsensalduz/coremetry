package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

// argocd_sync_store.go — v0.10.1445 — argocd_sync_events CH kapısı (DDL
// rollout_v2_schema.go §10.3.4 ≡ migrations/0015; v0.10.957'den beri vardı,
// yazıcısı yoktu). Yazıcı: Argo CD Notifications webhook'u
// (api/argocd_sync_webhook.go; satırlar argocd.RowsFromSyncWebhook). Okuyucu:
// Rollouts satırının git kaynağı sınıflandırması (api/rollouts_origin.go).
//
// Yazım ArgoCDWriteStatuses emsali: tam satır (RMT(version), invariant #4),
// açık istemci version'ı, asyncInsertCtx (async_insert korunur), batch'in
// HEPSİ argocd.ValidateSyncEventRow'dan geçer — tek geçersiz satır bütün
// batch'i reddeder. Okuma FINAL + uygulama demeti IN (ORDER BY öneki) +
// op_started_at aralığı (DateTime64(3) → chDateTime64Arg) + uygulama başına
// LIMIT ? BY + toplam LIMIT + max_execution_time; ana bağlantı (stateTables
// "FROM argocd_sync_events" pinli).

const (
	// ArgoCDSyncEventsMax — tek okumanın toplam satır tavanı.
	ArgoCDSyncEventsMax = 10_000
	// ArgoCDSyncPerAppMax — uygulama başına satır tavanının üst sınırı.
	ArgoCDSyncPerAppMax = 200
)

// argocdSyncCols — SyncEventRow `ch` etiketleri, sırasıyla (tek kaynak).
var argocdSyncCols = strings.Join(argocd.SyncEventColumns(), ", ")

// argocdSyncEventsForSQL — bağlar: n × (instance_id, app_namespace, app_name),
// from, to (DateTime64 metni), uygulama başına limit.
func argocdSyncEventsForSQL(n int) string {
	return fmt.Sprintf(`SELECT %s
		FROM argocd_sync_events FINAL
		WHERE (instance_id, app_namespace, app_name) IN (%s)
		  AND op_started_at >= toDateTime64(?, 3, 'UTC')
		  AND op_started_at <= toDateTime64(?, 3, 'UTC')
		ORDER BY instance_id, app_namespace, app_name, op_started_at DESC
		LIMIT ? BY instance_id, app_namespace, app_name
		LIMIT %d
		SETTINGS max_execution_time = 10`, argocdSyncCols, placeholders(n, "(?, ?, ?)"), ArgoCDSyncEventsMax)
}

// argocdSyncEventsForArgs — SAF: okuma bağları (sıra argocdSyncEventsForSQL).
func argocdSyncEventsForArgs(keys []argocd.AppKey, from, to time.Time, perApp int) []any {
	args := appKeyArgs(keys)
	return append(args, chDateTime64Arg(from), chDateTime64Arg(to), perApp)
}

// ArgoCDSyncEventsFor — v0.10.1445 — uygulamaların [from, to] aralığındaki
// senkron satırları (canlı + history tohumları), uygulama başına en yeni
// perApp tanesi, op_started_at DESC.
func (s *Store) ArgoCDSyncEventsFor(ctx context.Context, keys []argocd.AppKey, from, to time.Time, perApp int) ([]argocd.SyncEventRow, error) {
	out := []argocd.SyncEventRow{}
	keys = capAppKeys(keys)
	if len(keys) == 0 {
		return out, nil
	}
	perApp = clampRolloutLimit(perApp, 50, ArgoCDSyncPerAppMax)
	rows, err := s.conn.Query(ctx, argocdSyncEventsForSQL(len(keys)), argocdSyncEventsForArgs(keys, from, to, perApp)...)
	if err != nil {
		return nil, fmt.Errorf("argocd_sync_events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r argocd.SyncEventRow
		if err := rows.Scan(&r.InstanceID, &r.AppNamespace, &r.AppName, &r.OpStartedAt, &r.Phase, &r.FinishedAt, &r.Automated,
			&r.Initiator, &r.SyncRevision, &r.Revisions, &r.RepoURL, &r.TargetRevision, &r.HistoryID, &r.SyncedWorkloads,
			&r.ManagedWorkloads, &r.DestServer, &r.DestNamespace, &r.ClusterID, &r.RetryCount, &r.Message, &r.Version); err != nil {
			return nil, fmt.Errorf("argocd_sync_events scan: %w", err)
		}
		r.FinishedAt = zeroIfEpoch(r.FinishedAt) // DEFAULT 0 okumada 1970
		out = append(out, r)
	}
	return out, rows.Err()
}

// argocdSyncArgs — SAF: tam satır bağları (kolon sırası argocdSyncCols).
func argocdSyncArgs(r argocd.SyncEventRow) []any {
	nz := func(v []string) []string {
		if v == nil {
			return []string{}
		}
		return v
	}
	return []any{r.InstanceID, r.AppNamespace, r.AppName, r.OpStartedAt.UTC(), r.Phase, epochIfZero(r.FinishedAt).UTC(), r.Automated,
		r.Initiator, r.SyncRevision, nz(r.Revisions), r.RepoURL, r.TargetRevision, r.HistoryID, nz(r.SyncedWorkloads),
		nz(r.ManagedWorkloads), r.DestServer, r.DestNamespace, r.ClusterID, r.RetryCount, r.Message, r.Version}
}

// validateArgoCDSyncEvents — SAF: batch'in HEPSİ yazıcı sözleşmesinden geçmeli.
func validateArgoCDSyncEvents(rows []argocd.SyncEventRow) error {
	for _, r := range rows {
		if err := argocd.ValidateSyncEventRow(r); err != nil {
			return fmt.Errorf("argocd_sync_events %s/%s/%s: %s", r.InstanceID, r.AppNamespace, r.AppName,
				strings.ReplaceAll(err.Error(), "\n", "; "))
		}
	}
	return nil
}

// ArgoCDWriteSyncEvents — v0.10.1445 — argocd_sync_events tam satır batch'i.
func (s *Store) ArgoCDWriteSyncEvents(ctx context.Context, rows []argocd.SyncEventRow) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateArgoCDSyncEvents(rows); err != nil {
		return err
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO argocd_sync_events (`+argocdSyncCols+`)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := b.Append(argocdSyncArgs(r)...); err != nil {
			return err
		}
	}
	return b.Send()
}

// ArgoCDWorkloadEdgesFor — v0.10.1445 — iş yüklerinin canlı ve taze İŞ YÜKÜ
// kenarları (servis kartının iş yükü okumasıyla aynı SQL; zayıf kenar yok).
// capped: ArgoCDServiceEdgeMax aşıldı (liste eksik olabilir).
func (s *Store) ArgoCDWorkloadEdgesFor(ctx context.Context, workloads []argocd.ServiceWorkload, since time.Time) ([]argocd.MappingRow, bool, error) {
	if len(workloads) == 0 {
		return []argocd.MappingRow{}, false, nil
	}
	args := make([]any, 0, 3*len(workloads)+1)
	for _, w := range workloads {
		args = append(args, w.ClusterID, w.Namespace, w.Workload)
	}
	return s.argocdServiceEdgeRead(ctx, argocdServiceWorkloadEdgesSQL(len(workloads)), append(args, chDateTime64Arg(since)))
}
