package api

// rollouts_origin_test.go — v0.10.1445; v0.10.1494 (CoSRE 2d): çekirdeğin
// davranış pinleri chstore/rollout_origin_test.go'ya taşındı. Burada yalnız
// api'de kalan ayar kapısı: Argo entegrasyonu kapalıyken sarmalayıcı sorgu
// atmaz, alan yazmaz.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

type fakeOriginStore struct{ edgeCalls, syncCalls int }

func (f *fakeOriginStore) ArgoCDWorkloadEdgesFor(_ context.Context, _ []argocd.ServiceWorkload, _ time.Time) ([]argocd.MappingRow, bool, error) {
	f.edgeCalls++
	return nil, false, nil
}

func (f *fakeOriginStore) ArgoCDSyncEventsFor(_ context.Context, _ []argocd.AppKey, _, _ time.Time, _ int) ([]argocd.SyncEventRow, error) {
	f.syncCalls++
	return nil, nil
}

func TestAttachRolloutOriginGateOff(t *testing.T) {
	originT0 := time.Date(2026, 10, 21, 9, 12, 0, 0, time.UTC)
	row := chstore.RolloutRow{
		Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "shop", Workload: "checkout", Kind: "Deployment", StartedAt: originT0},
		V2:      &chstore.RolloutV2Fields{ChangeType: rollout.V2ChangeRollback},
	}
	// Entegrasyon kapalı → hiç sorgu yok.
	prev, prevSt := rolloutOriginSettings, rolloutOriginStoreOf
	t.Cleanup(func() { rolloutOriginSettings, rolloutOriginStoreOf = prev, prevSt })
	st := &fakeOriginStore{}
	rolloutOriginSettings = func() (bool, int) { return false, 0 }
	rolloutOriginStoreOf = func(*Server) rolloutOriginStore { return st }
	rows := []chstore.RolloutRow{row}
	(&Server{}).attachRolloutOrigin(context.Background(), rows)
	if st.edgeCalls+st.syncCalls != 0 || rows[0].Origin != nil {
		t.Fatal("Argo kapalıyken sorgu/alan olmamalı")
	}
}
