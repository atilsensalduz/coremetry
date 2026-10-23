package chstore

// argocd_sync_store_test.go — v0.10.1445 — argocd_sync_events CH kapısı
// sözleşmesi (argocd_sync_store.go): okuma FINAL + uygulama demeti IN +
// DateTime64(3) aralığı (chDateTime64Arg, tz'siz) + uygulama başına LIMIT ? BY
// (toplam LIMIT'ten ÖNCE) + max_execution_time; INSERT kolonları DDL'in 21
// kolonu, bağlar tam satır (UTC, sıfır finished_at → epoch, nil dizi → boş).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

func TestArgoCDSyncEventsForSQLShape(t *testing.T) {
	q := argocdSyncEventsForSQL(2)
	for _, want := range []string{
		"FROM argocd_sync_events FINAL",
		"WHERE (instance_id, app_namespace, app_name) IN ((?, ?, ?), (?, ?, ?))",
		"op_started_at >= toDateTime64(?, 3, 'UTC')",
		"op_started_at <= toDateTime64(?, 3, 'UTC')",
		"ORDER BY instance_id, app_namespace, app_name, op_started_at DESC",
		"LIMIT ? BY instance_id, app_namespace, app_name",
		"LIMIT 10000",
		"max_execution_time = 10",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("senkron okumasında %q yok:\n%s", want, q)
		}
	}
	if i, j := strings.Index(q, "LIMIT ? BY"), strings.Index(q, "LIMIT 10000"); i < 0 || j < i {
		t.Error("LIMIT ? BY toplam LIMIT'ten ÖNCE olmalı")
	}
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.FixedZone("x", 3*3600))
	to := from.Add(48 * time.Hour)
	args := argocdSyncEventsForArgs([]argocd.AppKey{{InstanceID: "argo-main", AppNamespace: "argocd", Name: "shop-checkout-prod"}, {InstanceID: "argo-main", AppNamespace: "argocd", Name: "b"}}, from, to, 50)
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("bağ sayısı %d ≠ yer tutucu %d", len(args), strings.Count(q, "?"))
	}
	if args[6] != "2026-09-30 21:00:00" || args[7] != "2026-10-02 21:00:00" || args[8] != 50 {
		t.Fatalf("aralık bağları tz'siz UTC DateTime64 metni olmalı: %v", args[6:])
	}
	if s, ok := args[6].(string); !ok || strings.HasSuffix(s, "Z") {
		t.Fatalf("toDateTime64 bağı tz'siz olmalı: %v", args[6])
	}
}

func TestArgoCDWriteSyncEventsArgsOrder(t *testing.T) {
	cols := strings.Split(argocdSyncCols, ", ")
	if len(cols) != 21 || cols[0] != "instance_id" || cols[3] != "op_started_at" || cols[5] != "finished_at" || cols[20] != "version" {
		t.Fatalf("kolonlar (DDL 21): %v", cols)
	}
	at := time.Date(2026, 10, 21, 12, 12, 0, 0, time.FixedZone("x", 3*3600))
	r := argocd.SyncEventRow{InstanceID: "argo-main", AppNamespace: "argocd", AppName: "shop-checkout-prod", OpStartedAt: at,
		Phase: "Succeeded", Automated: 1, SyncRevision: "4b7d0aa3", HistoryID: 8, ClusterID: "c-a", RetryCount: 2, Message: "ok", Version: 9}
	args := argocdSyncArgs(r)
	if len(args) != len(cols) {
		t.Fatalf("bağ sayısı %d ≠ kolon %d", len(args), len(cols))
	}
	if ts := args[3].(time.Time); ts.Location() != time.UTC || !ts.Equal(at) {
		t.Fatalf("op_started_at UTC bağlanmalı: %v", ts)
	}
	if fin := args[5].(time.Time); fin.Unix() != 0 {
		t.Fatalf("sıfır finished_at epoch'a (DEFAULT 0) bağlanmalı: %v", fin)
	}
	if v, ok := args[9].([]string); !ok || v == nil {
		t.Fatalf("nil revisions boş diziye bağlanmalı: %#v", args[9])
	}
	if args[6] != uint8(1) || args[8] != "4b7d0aa3" || args[12] != int64(8) || args[17] != "c-a" || args[18] != uint32(2) || args[20] != uint64(9) {
		t.Fatalf("bağ sırası: %v", args)
	}
	bad := r
	bad.Version = 0
	if err := validateArgoCDSyncEvents([]argocd.SyncEventRow{r, bad}); err == nil || !strings.Contains(err.Error(), "argo-main/argocd/shop-checkout-prod") {
		t.Fatalf("tek geçersiz satır batch'i reddetmeli: %v", err)
	}
	if err := validateArgoCDSyncEvents([]argocd.SyncEventRow{r}); err != nil {
		t.Fatal(err)
	}
}
