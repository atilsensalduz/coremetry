package api

// v0.10.1212 — v2 satırına "trafik geçti" (ilk span) ekleme: saf yardımcılar.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestApplyV2Traffic(t *testing.T) {
	t0 := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	mk := func(rev string, fs time.Time) chstore.RolloutRow {
		var r chstore.RolloutRow
		r.Rollout = rollout.Rollout{ClusterID: "c1", Namespace: "ns", Workload: "api", Revision: rev, StartedAt: t0, FirstSpanAt: fs}
		return r
	}
	rows := []chstore.RolloutRow{mk("rs-new", time.Time{}), mk("rs-old", time.Time{}), mk("rs-v1", t0.Add(time.Minute)), mk("", time.Time{})}
	keys := func(id string) []string { return []string{"prod-a", "prod-b"} }
	refs := v2TrafficRefs(rows, keys)
	if len(refs) != 6 {
		t.Fatalf("refs=%d want 6 (boş revizyon atlanır)", len(refs))
	}
	ref := func(c, rev string) chstore.WorkloadRevisionRef {
		return chstore.WorkloadRevisionRef{Cluster: c, Namespace: "ns", Workload: "api", Revision: rev}
	}
	first := map[chstore.WorkloadRevisionRef]time.Time{
		ref("prod-a", "rs-new"): t0.Add(4 * time.Minute),
		ref("prod-b", "rs-new"): t0.Add(2 * time.Minute), // en erken kazanır
		ref("prod-a", "rs-old"): t0.Add(-time.Hour),      // başlangıçtan önce → yok say
		ref("prod-a", "rs-v1"):  t0.Add(9 * time.Minute), // v1 değeri ezilmez
	}
	applyV2Traffic(rows, keys, first)
	if got := rows[0].FirstSpanAt; !got.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("rs-new=%v", got)
	}
	if !rows[1].FirstSpanAt.IsZero() {
		t.Errorf("rs-old should stay zero: %v", rows[1].FirstSpanAt)
	}
	if !rows[2].FirstSpanAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("rs-v1 overwritten: %v", rows[2].FirstSpanAt)
	}
}
