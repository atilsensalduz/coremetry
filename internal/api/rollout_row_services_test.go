package api

// rollout_row_services_test.go — v0.10.1420 — operatör (/rollouts):
// "Deployment Workload basınca service'e girsin, şu an bulamıyor."
//
// NE ÇİVİLİYOR:
//   - workload → servis eşlemesi: eşleşme (ad = workload kazanır), belirsiz
//     (birden çok aday → deterministik alfabetik ilk, hepsi `services`te),
//     yok (alan boş → FE entity yedeğine düşer).
//   - Görünen TÜM satırlar için TEK toplu arama (N+1 yok), demetler tekil,
//     span-cluster değerleri birleşir; arama hatası satırları bozmaz.
//   - JSON: alanlar yalnız doluyken (v1 gövde eşliği korunur).
//   - entities.go wl: yedeği: tür farkı (Derived / ReplicaSet) çözülür,
//     başka namespace/cluster asla.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestPickRolloutService(t *testing.T) {
	for _, c := range []struct {
		name, workload string
		svcs           []string
		want           string
	}{
		{"eşleşme: tek servis", "orders-api", []string{"orders-api"}, "orders-api"},
		{"eşleşme: ad = workload kazanır (alfabetik ilk değil)", "orders-api", []string{"billing", "orders-api", "orders-worker"}, "orders-api"},
		{"eşleşme: harf duyarsız", "Orders-API", []string{"audit", "orders-api"}, "orders-api"},
		{"tek aday, farklı ad", "orders-deploy", []string{"orders-api"}, "orders-api"},
		{"belirsiz: deterministik alfabetik ilk", "orders", []string{"orders-worker", "orders-api"}, "orders-api"},
		{"yok", "orders-api", nil, ""},
	} {
		if got := pickRolloutService(c.workload, c.svcs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAttachRolloutServicesBatchesOnce(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Minute) // 30 günlük kelepçenin içinde kalsın
	row := func(cid, ns, wl string, at time.Time) chstore.RolloutRow {
		return chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: cid, Namespace: ns, Workload: wl, StartedAt: at}}
	}
	rows := []chstore.RolloutRow{
		row("c-1", "team-a", "orders-api", t0),                      // eşleşme
		row("c-1", "team-a", "orders-api", t0.Add(-2*time.Hour)),    // aynı demet (tekilleşir)
		row("c-1", "team-a", "checkout", t0),                        // belirsiz
		row("c-1", "team-a", "batch-job", t0),                       // yok
		row("c-unmapped", "team-a", "orders-api", t0),               // cluster çözülmedi
		row("c-2", "team-b", "orders-api", t0.Add(-30*time.Minute)), // iki span değeri birleşir
	}
	clustersOf := func(id string) []string {
		switch id {
		case "c-1":
			return []string{"prod-east"}
		case "c-2":
			return []string{"prod-west", "prod-west-alias"}
		}
		return nil
	}
	m := map[chstore.RolloutServiceKey][]string{
		{Cluster: "prod-east", Namespace: "team-a", Workload: "orders-api"}:       {"orders-api"},
		{Cluster: "prod-east", Namespace: "team-a", Workload: "checkout"}:         {"checkout-web", "checkout-api"},
		{Cluster: "prod-west", Namespace: "team-b", Workload: "orders-api"}:       {"orders-api"},
		{Cluster: "prod-west-alias", Namespace: "team-b", Workload: "orders-api"}: {"orders-api", "orders-api-canary"},
	}
	calls := 0
	var gotKeys []chstore.RolloutServiceKey
	var gotSince time.Time
	lookup := func(_ context.Context, keys []chstore.RolloutServiceKey, since time.Time) (map[chstore.RolloutServiceKey][]string, error) {
		calls++
		gotKeys, gotSince = keys, since
		return m, nil
	}
	if err := attachRolloutServicesWith(context.Background(), rows, clustersOf, lookup); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("toplu arama %d kez çağrıldı, want 1 (N+1 yok)", calls)
	}
	if len(gotKeys) != 5 { // c-1×3 workload + c-2×2 span değeri; tekrar ve çözülmeyen cluster yok
		t.Fatalf("demetler %d: %+v", len(gotKeys), gotKeys)
	}
	if want := t0.Add(-3 * time.Hour); !gotSince.Equal(want) {
		t.Fatalf("since %v, want en eski başlangıç − 1 sa %v", gotSince, want)
	}
	type want struct {
		svc  string
		svcs []string
	}
	for i, w := range []want{
		{"orders-api", []string{"orders-api"}},
		{"orders-api", []string{"orders-api"}},
		{"checkout-api", []string{"checkout-api", "checkout-web"}},
		{"", nil},
		{"", nil},
		{"orders-api", []string{"orders-api", "orders-api-canary"}},
	} {
		if rows[i].Service != w.svc || !reflect.DeepEqual(rows[i].Services, w.svcs) {
			t.Errorf("satır %d (%s): service=%q services=%v, want %q %v", i, rows[i].Workload, rows[i].Service, rows[i].Services, w.svc, w.svcs)
		}
	}

	// Hiç çözülen cluster yok → arama yok.
	calls = 0
	none := []chstore.RolloutRow{row("c-unmapped", "team-a", "orders-api", t0)}
	_ = attachRolloutServicesWith(context.Background(), none, clustersOf, lookup)
	if calls != 0 {
		t.Fatal("çözülen cluster yokken arama atılmamalı")
	}
	// Arama hatası: satırlar alan almaz, hata döner (çağıran loglar).
	fresh := []chstore.RolloutRow{row("c-1", "team-a", "orders-api", t0)}
	err := attachRolloutServicesWith(context.Background(), fresh, clustersOf, func(context.Context, []chstore.RolloutServiceKey, time.Time) (map[chstore.RolloutServiceKey][]string, error) {
		return nil, errors.New("ch down")
	})
	if err == nil || fresh[0].Service != "" || fresh[0].Services != nil {
		t.Fatalf("hata yolunda alan basılmamalı: err=%v %+v", err, fresh[0])
	}
}

func TestRolloutRowJSONServicesOnlyWhenSet(t *testing.T) {
	r := chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "team-a", Workload: "orders-api"}}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), `"service`) {
		t.Fatalf("boş alanlar JSON'a girmemeli: %s", b)
	}
	r.Service, r.Services = "orders-api", []string{"orders-api", "orders-worker"}
	b, _ = json.Marshal(r)
	if !strings.Contains(string(b), `"service":"orders-api"`) || !strings.Contains(string(b), `"services":["orders-api","orders-worker"]`) {
		t.Fatalf("dolu alanlar JSON'da yok: %s", b)
	}
}

func TestPickWorkloadEntityFallback(t *testing.T) {
	ref := entity.Ref{Type: entity.TypeWorkload, ClusterID: "c-1", Namespace: "team-a", Kind: "Deployment", Name: "orders-api"}
	wl := func(id, cid, ns, kind, name string, seen time.Time) chstore.EntityRecord {
		return chstore.EntityRecord{Type: entity.TypeWorkload, ID: id, ClusterID: cid, Namespace: ns, Name: name, Labels: map[string]string{"kind": kind}, LastSeen: seen}
	}
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		recs []chstore.EntityRecord
		want string
	}{
		{"pod-adı türetmesi (Derived) aynı ad", []chstore.EntityRecord{
			wl("wl:c-1/team-a/Derived/orders-api", "c-1", "team-a", entity.DerivedKind, "orders-api", t0)},
			"wl:c-1/team-a/Derived/orders-api"},
		{"aynı ad iki tür: istenen tür önce", []chstore.EntityRecord{
			wl("wl:c-1/team-a/Derived/orders-api", "c-1", "team-a", entity.DerivedKind, "orders-api", t0),
			wl("wl:c-1/team-a/Deployment/orders-api", "c-1", "team-a", "Deployment", "orders-api", t0)},
			"wl:c-1/team-a/Deployment/orders-api"},
		{"RS sahibi okunamamış: en son görülen <ad>-<hash>", []chstore.EntityRecord{
			wl("wl:c-1/team-a/ReplicaSet/orders-api-6a1b", "c-1", "team-a", "ReplicaSet", "orders-api-6a1b", t0.Add(-time.Hour)),
			wl("wl:c-1/team-a/ReplicaSet/orders-api-7f9c", "c-1", "team-a", "ReplicaSet", "orders-api-7f9c", t0)},
			"wl:c-1/team-a/ReplicaSet/orders-api-7f9c"},
		{"başka iş yükünün RS'i (orders-api-worker-…) değil", []chstore.EntityRecord{
			wl("wl:c-1/team-a/ReplicaSet/orders-api-worker-7f9c", "c-1", "team-a", "ReplicaSet", "orders-api-worker-7f9c", t0)},
			""},
		{"başka namespace / cluster asla", []chstore.EntityRecord{
			wl("wl:c-1/team-b/Deployment/orders-api", "c-1", "team-b", "Deployment", "orders-api", t0),
			wl("wl:c-2/team-a/Deployment/orders-api", "c-2", "team-a", "Deployment", "orders-api", t0)},
			""},
		{"aday yok", nil, ""},
	} {
		if got := pickWorkloadEntityFallback(ref, c.recs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
