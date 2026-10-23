package api

// rollouts_origin_test.go — v0.10.1445 — Rollouts satırının git kaynağı
// (rollback | redeploy_older | forward | unknown) okuma anı birleşimi:
// pencere + en yakın senkron, history tohumundan SeenBefore, belirsiz eşleme
// → unknown, okuma hatası listeyi bozmaz, JSON şekli (v1 satırı bayt bayt aynı).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

var originT0 = time.Date(2026, 10, 21, 9, 12, 0, 0, time.UTC)

type fakeOriginStore struct {
	edges     []argocd.MappingRow
	syncs     []argocd.SyncEventRow
	edgeErr   error
	syncErr   error
	edgeCalls int
	syncCalls int
	syncKeys  []argocd.AppKey
}

func (f *fakeOriginStore) ArgoCDWorkloadEdgesFor(_ context.Context, _ []argocd.ServiceWorkload, _ time.Time) ([]argocd.MappingRow, bool, error) {
	f.edgeCalls++
	return f.edges, false, f.edgeErr
}

func (f *fakeOriginStore) ArgoCDSyncEventsFor(_ context.Context, keys []argocd.AppKey, _, _ time.Time, _ int) ([]argocd.SyncEventRow, error) {
	f.syncCalls++
	f.syncKeys = keys
	return f.syncs, f.syncErr
}

func originRow(wl, changeType, prevTag, newTag string, at time.Time) chstore.RolloutRow {
	return chstore.RolloutRow{
		Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "shop", Workload: wl, Kind: "Deployment", StartedAt: at,
			ImageTag: newTag, PrevImageTag: prevTag},
		V2: &chstore.RolloutV2Fields{ChangeType: changeType},
	}
}

func originEdge(wl, app, class string) argocd.MappingRow {
	return argocd.MappingRow{ClusterID: "c-1", Namespace: "shop", WorkloadKind: "Deployment", Workload: wl,
		InstanceID: "argo-main", AppNamespace: "argocd", AppName: app, MatchMethod: "resource", MatchClass: class}
}

// originSync — canlı (seed=false) ya da history tohumu.
func originSync(app, rev string, at time.Time, seed bool, synced ...string) argocd.SyncEventRow {
	v := uint64(at.UnixNano())
	if seed {
		v = uint64(at.UnixMilli())
	}
	return argocd.SyncEventRow{InstanceID: "argo-main", AppNamespace: "argocd", AppName: app, OpStartedAt: at,
		FinishedAt: at.Add(time.Minute), Phase: argocd.SyncPhaseSucceeded, SyncRevision: rev, Automated: 1,
		SyncedWorkloads: synced, Version: v}
}

func TestMatchSyncForRolloutWindowAndNearest(t *testing.T) {
	W := 5 * time.Minute
	row := originRow("checkout", rollout.V2ChangeRollout, "", "", originT0)
	syncs := []argocd.SyncEventRow{
		originSync("a", "r1", originT0.Add(-72*time.Hour), true),
		originSync("a", "r2", originT0.Add(-24*time.Hour), true),
		originSync("a", "r3", originT0.Add(-2*time.Minute), false, "Deployment/shop/checkout"),
		originSync("a", "r3", originT0.Add(-2*time.Minute), true),                          // aynı senkronun tohum ikizi
		originSync("a", "rX", originT0.Add(3*time.Minute), false, "Deployment/shop/other"), // başka kaynak: aday değil
		originSync("a", "r9", originT0.Add(30*time.Minute), false),                         // pencere dışı
	}
	m := matchSyncForRollout(row, syncs, W)
	if m.Matched == nil || m.Matched.SyncRevision != "r3" || m.Matched.IsHistorySeed() {
		t.Fatalf("eşleşme r3 canlı satırı olmalı: %+v", m.Matched)
	}
	if m.Prev == nil || m.Prev.SyncRevision != "r2" || m.HistoryDepth != 2 || m.SeenBefore || m.Candidates != 1 {
		t.Fatalf("prev/derinlik: %+v", m)
	}
	// Saat kayması: KSM, senkron bitişinden W içinde geç → yine eşleşir.
	late := originRow("checkout", rollout.V2ChangeRollout, "", "", originT0.Add(-2*time.Minute+time.Minute+4*time.Minute))
	if mm := matchSyncForRollout(late, syncs, W); mm.Matched == nil || mm.Matched.SyncRevision != "r3" {
		t.Fatalf("bitiş + W içinde: %+v", mm.Matched)
	}
	// Pencerede iki ayrı revizyon → belirsiz.
	amb := append(syncs, originSync("a", "r4", originT0.Add(time.Minute), false))
	if mm := matchSyncForRollout(row, amb, W); mm.Candidates != 2 {
		t.Fatalf("iki aday: %d", mm.Candidates)
	}
	// Hiç senkron yok.
	if mm := matchSyncForRollout(row, nil, W); mm.Matched != nil {
		t.Fatal("senkron yokken eşleşme")
	}
}

func TestAttachRolloutOriginSeenBeforeFromSeededHistory(t *testing.T) {
	st := &fakeOriginStore{
		edges: []argocd.MappingRow{originEdge("checkout", "shop-checkout-prod", argocd.ClassExact), originEdge("payments", "shop-payments-prod", argocd.ClassExact)},
		syncs: []argocd.SyncEventRow{
			// checkout: r-old (tohum) → r-new (tohum) → r-old (canlı) = gerçek geri alma
			originSync("shop-checkout-prod", "9f3c2e1b", originT0.Add(-72*time.Hour), true),
			originSync("shop-checkout-prod", "4b7d0aa3", originT0.Add(-24*time.Hour), true),
			originSync("shop-checkout-prod", "9f3c2e1b", originT0.Add(-time.Minute), false),
			// payments: yeni revizyon, KSM bilinen RS'e döndü = eski yapının yeniden deploy'u
			originSync("shop-payments-prod", "aaa111", originT0.Add(-48*time.Hour), true),
			originSync("shop-payments-prod", "bbb222", originT0.Add(-time.Minute), false),
		},
	}
	rows := []chstore.RolloutRow{
		originRow("checkout", rollout.V2ChangeRollback, "2026.10.18.3", "2026.10.17.1", originT0),
		originRow("payments", rollout.V2ChangeRollback, "2026.10.18.3", "2026.10.17.1", originT0),
		originRow("orphan", rollout.V2ChangeRollback, "2026.10.18.3", "2026.10.17.1", originT0),
		{Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "shop", Workload: "v1-row", StartedAt: originT0}}, // v1: dokunulmaz
	}
	if err := attachRolloutOriginWith(context.Background(), rows, st, 5, originT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st.edgeCalls != 1 || st.syncCalls != 1 || len(st.syncKeys) != 2 {
		t.Fatalf("toplu okuma (N+1 yok): edges=%d syncs=%d keys=%d", st.edgeCalls, st.syncCalls, len(st.syncKeys))
	}
	c := rows[0].Origin
	if c == nil || c.Kind != rollout.OriginRollback || c.Confidence != 95 || c.Basis != rollout.OriginBasisArgo ||
		c.PrevRevision != "4b7d0aa3" || c.NewRevision != "9f3c2e1b" || c.FirstSyncedAt != originT0.Add(-72*time.Hour).UnixMilli() ||
		c.App == nil || c.App.AppName != "shop-checkout-prod" || c.Automated == nil || !*c.Automated || c.TagOrder != rollout.TagOlder {
		t.Fatalf("checkout rollback (git): %+v", c)
	}
	p := rows[1].Origin
	if p == nil || p.Kind != rollout.OriginRedeployOlder || p.Confidence != 70 || p.HistoryDepth != 1 || !strings.Contains(p.Reason, "history depth 1") {
		t.Fatalf("payments redeploy_older (derinlik<3 → 85−15): %+v", p)
	}
	o := rows[2].Origin
	if o == nil || o.Kind != rollout.OriginUnknown || o.Basis != rollout.OriginBasisNone || !strings.Contains(o.Reason, "no Argo CD application") {
		t.Fatalf("eşlenmemiş iş yükü unknown: %+v", o)
	}
	if rows[3].Origin != nil {
		t.Fatal("v1 satırına origin yazılmamalı")
	}
}

func TestAttachRolloutOriginAmbiguousMappingUnknown(t *testing.T) {
	st := &fakeOriginStore{
		edges: []argocd.MappingRow{
			originEdge("checkout", "app-a", argocd.ClassExact),
			originEdge("checkout", "app-b", argocd.ClassEstimated),
			originEdge("weakonly", "app-c", argocd.ClassWeak),
		},
		syncs: []argocd.SyncEventRow{originSync("app-a", "r1", originT0, false)},
	}
	rows := []chstore.RolloutRow{
		originRow("checkout", rollout.V2ChangeRollback, "", "", originT0),
		originRow("weakonly", rollout.V2ChangeRollback, "", "", originT0),
	}
	if err := attachRolloutOriginWith(context.Background(), rows, st, 5, originT0); err != nil {
		t.Fatal(err)
	}
	if o := rows[0].Origin; o == nil || o.Kind != rollout.OriginUnknown || !strings.Contains(o.Reason, "ambiguous: 2 Argo apps") {
		t.Fatalf("iki uygulama → belirsiz: %+v", o)
	}
	if o := rows[1].Origin; o == nil || o.Kind != rollout.OriginUnknown || o.App != nil {
		t.Fatalf("zayıf kenar kanıt değil: %+v", o)
	}
}

func TestAttachRolloutOriginNoSyncIsUnknownEvenForKSMRollback(t *testing.T) {
	st := &fakeOriginStore{edges: []argocd.MappingRow{originEdge("checkout", "app-a", argocd.ClassExact)}}
	rows := []chstore.RolloutRow{originRow("checkout", rollout.V2ChangeRollback, "2026.10.18.3", "2026.10.17.1", originT0)}
	if err := attachRolloutOriginWith(context.Background(), rows, st, 5, originT0); err != nil {
		t.Fatal(err)
	}
	if o := rows[0].Origin; o == nil || o.Kind != rollout.OriginUnknown || o.Basis != rollout.OriginBasisNone || o.App == nil {
		t.Fatalf("senkron yok → unknown (tag sırası kanıt değil): %+v", o)
	}
}

func TestAttachRolloutOriginSoftFailKeepsList(t *testing.T) {
	for _, st := range []*fakeOriginStore{
		{edgeErr: errors.New("ch down")},
		{edges: []argocd.MappingRow{originEdge("checkout", "app-a", argocd.ClassExact)}, syncErr: errors.New("timeout")},
	} {
		rows := []chstore.RolloutRow{originRow("checkout", rollout.V2ChangeRollback, "", "", originT0)}
		if err := attachRolloutOriginWith(context.Background(), rows, st, 5, originT0); err == nil {
			t.Fatal("hata yüzeye çıkmalı (çağıran loglar)")
		}
		if rows[0].Origin != nil || rows[0].Workload != "checkout" {
			t.Fatalf("hata → alan yok, satır sağlam: %+v", rows[0])
		}
	}
	// Entegrasyon kapalı → hiç sorgu yok.
	prev, prevSt := rolloutOriginSettings, rolloutOriginStoreOf
	t.Cleanup(func() { rolloutOriginSettings, rolloutOriginStoreOf = prev, prevSt })
	st := &fakeOriginStore{}
	rolloutOriginSettings = func() (bool, int) { return false, 0 }
	rolloutOriginStoreOf = func(*Server) rolloutOriginStore { return st }
	rows := []chstore.RolloutRow{originRow("checkout", rollout.V2ChangeRollback, "", "", originT0)}
	(&Server{}).attachRolloutOrigin(context.Background(), rows)
	if st.edgeCalls+st.syncCalls != 0 || rows[0].Origin != nil {
		t.Fatal("Argo kapalıyken sorgu/alan olmamalı")
	}
}

func TestRolloutRowJSONOriginShapeAndV1Unchanged(t *testing.T) {
	v1 := chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: "c-1", Workload: "checkout", StartedAt: originT0}}
	b1, _ := json.Marshal(v1)
	if strings.Contains(string(b1), "origin") {
		t.Fatalf("v1 satırı origin anahtarı taşımamalı: %s", b1)
	}
	auto := true
	r := originRow("checkout", rollout.V2ChangeRollback, "2026.10.18.3", "2026.10.17.1", originT0)
	r.Origin = &rollout.RolloutOrigin{Kind: rollout.OriginRollback, Confidence: 95, Reason: "x", Basis: rollout.OriginBasisArgo,
		App:          &rollout.OriginApp{InstanceID: "argo-main", AppNamespace: "argocd", AppName: "shop-checkout-prod", MatchMethod: "resource", MatchClass: "exact"},
		PrevRevision: "4b7d0aa3", NewRevision: "9f3c2e1b", TagOrder: rollout.TagOlder, Automated: &auto, HistoryDepth: 2}
	b, _ := json.Marshal(r)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	o, ok := m["origin"].(map[string]any)
	if !ok {
		t.Fatalf("origin nesnesi yok: %s", b)
	}
	for _, k := range []string{"kind", "confidence", "reason", "basis", "app", "prevRevision", "newRevision", "tagOrder", "automated", "historyDepth"} {
		if _, ok := o[k]; !ok {
			t.Errorf("origin.%s eksik: %s", k, b)
		}
	}
	if app := o["app"].(map[string]any); app["appName"] != "shop-checkout-prod" || app["instanceId"] != "argo-main" {
		t.Fatalf("app: %v", app)
	}
}
