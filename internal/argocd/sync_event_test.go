package argocd

// sync_event_test.go — v0.10.1445 — Argo CD Notifications webhook → argocd_sync_events
// satırları (sync_event.go). Sözleşme: SyncEventRow DDL'i aynalar; canlı satır +
// history tohumları (tohum version'ı her canlı version'ın altında — sonraki
// webhook'un tohumu önceki canlı satırı EZMEZ); iş yükü türü süzgeci; Argo
// zaman biçimleri; doğrulama + tavanlar.

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSyncEventRowMirrorsDDL(t *testing.T) {
	ddl := mpDDLColumns(t, "argocd_sync_events")
	rt := reflect.TypeOf(SyncEventRow{})
	if rt.NumField() != len(ddl) {
		t.Fatalf("DDL %d kolon, Go %d alan: %v", len(ddl), rt.NumField(), ddl)
	}
	goType := map[reflect.Type]string{
		reflect.TypeOf(""): "string", reflect.TypeOf(uint8(0)): "UInt8", reflect.TypeOf(uint32(0)): "UInt32",
		reflect.TypeOf(int64(0)): "Int64", reflect.TypeOf(uint64(0)): "UInt64", reflect.TypeOf(time.Time{}): "DateTime64(3)",
		reflect.TypeOf([]string{}): "Array(String)",
	}
	for i, c := range ddl {
		f := rt.Field(i)
		want := c[1]
		if want == "String" || want == "LowCardinality(String)" {
			want = "string"
		}
		if f.Tag.Get("ch") != c[0] || goType[f.Type] != want {
			t.Fatalf("kolon %d: DDL %v, Go %s %s", i, c, f.Tag.Get("ch"), f.Type)
		}
	}
}

const seT0 = "2026-10-21T09:12:00Z"

func seRawPayload() string {
	return `{"instanceId":"argo-main","app":{"namespace":"argocd","name":"shop-checkout-prod"},
"operation":{"phase":"Succeeded","startedAt":"2026-10-21T09:12:00Z","finishedAt":"2026-10-21T09:13:30Z","automated":true,"initiator":""},
"sync":{"revision":"4b7d0aa3","repoURL":"https://git.example.invalid/shop/deploy.git","targetRevision":"main"},
"history":[{"id":5,"revision":"9f3c2e1b","deployStartedAt":"2026-10-18T09:12:00Z","deployedAt":"2026-10-18T09:13:00Z"},
{"id":6,"revision":"77aa11cc","deployStartedAt":"<no value>","deployedAt":"2026-10-19 10:00:00 +0000 UTC"},
{"id":7,"revision":"","deployStartedAt":"2026-10-20T09:00:00Z","deployedAt":""},
{"id":8,"revision":"4b7d0aa3","deployStartedAt":"2026-10-21T09:12:00Z","deployedAt":"2026-10-21T09:13:30Z"}],
"resources":["Deployment/shop/checkout","Service/shop/checkout","ConfigMap/shop/checkout-cfg"],
"managed":["Deployment/shop/checkout","StatefulSet/shop/cache","Rollout/shop/canary","DaemonSet/shop/agent","DeploymentConfig/shop/legacy","Ingress/shop/web","Deployment/shop/checkout"],
"destination":{"server":"https://kubernetes.default.svc","namespace":"shop"}}`
}

func seDecode(t *testing.T, raw string) SyncWebhookPayload {
	t.Helper()
	var p SyncWebhookPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRowsFromSyncWebhookLiveAndSeededHistory(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 14, 0, 0, time.UTC)
	rows, err := RowsFromSyncWebhook(seDecode(t, seRawPayload()), now, func(ds string) string {
		if ds == "https://kubernetes.default.svc" {
			return "hub-a"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("canlı + 2 tohum beklenir (boş revizyon atlanır, canlı senkronun kendi girdisi tekrarlanmaz): %d %+v", len(rows), rows)
	}
	live := rows[0]
	t0, _ := time.Parse(time.RFC3339, seT0)
	if live.InstanceID != "argo-main" || live.AppNamespace != "argocd" || live.AppName != "shop-checkout-prod" || !live.OpStartedAt.Equal(t0) ||
		live.Phase != "Succeeded" || live.Automated != 1 || live.SyncRevision != "4b7d0aa3" || live.HistoryID != 8 ||
		live.RepoURL != "https://git.example.invalid/shop/deploy.git" || live.TargetRevision != "main" || live.ClusterID != "hub-a" ||
		live.DestNamespace != "shop" || live.Version != uint64(now.UnixNano()) || live.FinishedAt.IsZero() {
		t.Fatalf("canlı satır: %+v", live)
	}
	if strings.Join(live.SyncedWorkloads, ",") != "Deployment/shop/checkout" {
		t.Fatalf("synced: %v", live.SyncedWorkloads)
	}
	s1, s2 := rows[1], rows[2]
	if s1.SyncRevision != "9f3c2e1b" || s1.HistoryID != 5 || s1.Phase != SyncPhaseSucceeded || s1.OpStartedAt.Format(time.RFC3339) != "2026-10-18T09:12:00Z" {
		t.Fatalf("tohum 1: %+v", s1)
	}
	// deployStartedAt "<no value>" → deployedAt'e düşer (Go time.String biçimi)
	if s2.SyncRevision != "77aa11cc" || s2.OpStartedAt.Format(time.RFC3339) != "2026-10-19T10:00:00Z" {
		t.Fatalf("tohum 2 (deployedAt yedeği): %+v", s2)
	}
	for _, s := range rows[1:] {
		if s.Version >= live.Version || s.Version != uint64(s.OpStartedAt.UnixMilli()) {
			t.Fatalf("tohum version'ı canlı satırın altında ve op_started_at ms olmalı: %d vs %d", s.Version, live.Version)
		}
		if s.ClusterID != "hub-a" || s.Revisions == nil || s.SyncedWorkloads == nil || s.ManagedWorkloads == nil {
			t.Fatalf("tohum satırı tam satır taşımalı: %+v", s)
		}
		if err := ValidateSyncEventRow(s); err != nil {
			t.Fatal(err)
		}
	}
	// Sonraki webhook'un tohumu, bu webhook'un canlı satırını EZEMEZ (aynı RMT anahtarı).
	later, err := RowsFromSyncWebhook(seDecode(t, strings.Replace(strings.Replace(seRawPayload(),
		`"startedAt":"2026-10-21T09:12:00Z"`, `"startedAt":"2026-10-22T08:00:00Z"`, 1), `"revision":"4b7d0aa3","repoURL"`, `"revision":"aa00bb11","repoURL"`, 1)),
		now.Add(24*time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range later[1:] {
		if s.OpStartedAt.Equal(live.OpStartedAt) {
			if s.Version >= live.Version {
				t.Fatalf("sonraki tohum (%d) önceki canlı satırı (%d) ezer", s.Version, live.Version)
			}
			return
		}
	}
	t.Fatal("sonraki webhook önceki canlı senkronu tohum olarak taşımalı")
}

func TestSyncWebhookWorkloadKindFilter(t *testing.T) {
	got := FilterSyncWorkloads([]string{"Deployment/shop/checkout", "StatefulSet/shop/cache", "Rollout/shop/canary", "DaemonSet/shop/agent",
		"DeploymentConfig/shop/legacy", "Ingress/shop/web", "Deployment/shop/checkout", "Deployment//x", "Deployment/shop", "", "<no value>", "Pod/shop/p-1"})
	want := "Deployment/shop/checkout,StatefulSet/shop/cache,Rollout/shop/canary,DaemonSet/shop/agent,DeploymentConfig/shop/legacy"
	if strings.Join(got, ",") != want {
		t.Fatalf("süzgeç: %v", got)
	}
	many := make([]string, 0, 700)
	for i := 0; i < 700; i++ {
		many = append(many, fmt.Sprintf("Deployment/shop/w-%d", i))
	}
	if n := len(FilterSyncWorkloads(many)); n != SyncWebhookMaxResources {
		t.Fatalf("tavan %d: %d", SyncWebhookMaxResources, n)
	}
}

func TestParseArgoTimeFormats(t *testing.T) {
	want := time.Date(2026, 10, 18, 9, 12, 0, 0, time.UTC)
	for _, s := range []string{
		"2026-10-18T09:12:00Z",
		"2026-10-18T09:12:00.000000Z",
		"2026-10-18T12:12:00+03:00",
		"2026-10-18 09:12:00 +0000 UTC", // metav1.Time / time.Time String()
		"2026-10-18 12:12:00 +0300 +03",
		" 2026-10-18T09:12:00Z ",
	} {
		got, ok := ParseArgoTime(s)
		if !ok || !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("ParseArgoTime(%q) = %v, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "<no value>", "garbage", "0001-01-01T00:00:00Z", "1970-01-01T00:00:00Z"} {
		if _, ok := ParseArgoTime(s); ok {
			t.Errorf("ParseArgoTime(%q) kabul etmemeli", s)
		}
	}
}

func TestSyncWebhookValidationAndCaps(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 14, 0, 0, time.UTC)
	for name, mut := range map[string]func(string) string{
		"instanceId yok":     func(s string) string { return strings.Replace(s, `"instanceId":"argo-main"`, `"instanceId":""`, 1) },
		"app adı yok":        func(s string) string { return strings.Replace(s, `"name":"shop-checkout-prod"`, `"name":"<no value>"`, 1) },
		"startedAt yok":      func(s string) string { return strings.Replace(s, `"startedAt":"2026-10-21T09:12:00Z"`, `"startedAt":""`, 1) },
		"startedAt bozuk":    func(s string) string { return strings.Replace(s, `"startedAt":"2026-10-21T09:12:00Z"`, `"startedAt":"yesterday"`, 1) },
		"faz sözlük dışı":    func(s string) string { return strings.Replace(s, `"phase":"Succeeded"`, `"phase":"Done"`, 1) },
		"Succeeded revizyon": func(s string) string { return strings.Replace(s, `"revision":"4b7d0aa3","repoURL"`, `"revision":"<no value>","repoURL"`, 1) },
	} {
		_, err := RowsFromSyncWebhook(seDecode(t, mut(seRawPayload())), now, nil)
		if !errors.Is(err, ErrSyncWebhookInvalid) {
			t.Errorf("%s: ErrSyncWebhookInvalid beklenir, %v", name, err)
		}
	}
	// Failed senkron revizyonsuz kabul edilir.
	failed := strings.Replace(strings.Replace(seRawPayload(), `"phase":"Succeeded"`, `"phase":"Failed"`, 1), `"revision":"4b7d0aa3","repoURL"`, `"revision":"","repoURL"`, 1)
	if rows, err := RowsFromSyncWebhook(seDecode(t, failed), now, nil); err != nil || rows[0].Phase != "Failed" || rows[0].HistoryID != -1 {
		t.Fatalf("Failed revizyonsuz: %v %+v", err, rows)
	}
	// Çok kaynaklı: revision boş, revisions[] dolu → kabul; kimlik sıralı birleşim.
	multi := strings.Replace(seRawPayload(), `"revision":"4b7d0aa3","repoURL"`, `"revision":"","revisions":["bbb","aaa"],"repoURL"`, 1)
	rows, err := RowsFromSyncWebhook(seDecode(t, multi), now, nil)
	if err != nil || rows[0].Identity() != "aaa,bbb" {
		t.Fatalf("çok kaynaklı kimlik: %v %+v", err, rows)
	}
	// Tavanlar: history ≤ 20 (en yeniler), dizeler ≤ 256.
	p := seDecode(t, seRawPayload())
	p.History = nil
	for i := 0; i < 30; i++ {
		p.History = append(p.History, SyncWebhookHistory{ID: int64(i), Revision: fmt.Sprintf("r%02d", i), DeployStartedAt: time.Date(2026, 9, 1+i, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)})
	}
	p.Operation.Initiator = strings.Repeat("x", 1000)
	rows, err = RowsFromSyncWebhook(p, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1+SyncWebhookMaxHistory || rows[1].SyncRevision != "r10" {
		t.Fatalf("history tavanı: %d satır, ilk tohum %q", len(rows), rows[1].SyncRevision)
	}
	if len(rows[0].Initiator) != SyncWebhookMaxString {
		t.Fatalf("dize tavanı: %d", len(rows[0].Initiator))
	}
	if err := ValidateSyncEventRow(rows[0]); err != nil {
		t.Fatal(err)
	}
	bad := rows[0]
	bad.OpStartedAt, bad.Version = time.Time{}, 0
	if err := ValidateSyncEventRow(bad); err == nil {
		t.Fatal("sıfır op_started_at / version reddedilmeli")
	}
}
