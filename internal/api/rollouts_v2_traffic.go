package api

// rollouts_v2_traffic.go — v0.10.1212 (operatör: "sıradaki adımı da yap"):
// v2 (ReplicaSet) rollout satırına "trafik geçti" zamanı. v2 rollout'u RS
// oluştuğu an görür; yeni revizyonun İLK span'i workload_revision_activity_1m
// MV'sinden (span tarafı v1'in kaynağı) tek sorguyla eklenir → satırda iki
// zaman: başladı (KSM) ve trafik yeni sürüme geçti (span). Okunamazsa alan
// boş kalır (soft-fail; liste yine döner).

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// rolloutTrafficSlack — RS'ten hemen önce gelen span'i (saat kayması) kabul payı.
const rolloutTrafficSlack = time.Minute

// v2TrafficRefs — SAF: satırların MV anahtarları (span cluster değerleri × satır).
func v2TrafficRefs(rows []chstore.RolloutRow, spanKeys func(clusterID string) []string) []chstore.WorkloadRevisionRef {
	var out []chstore.WorkloadRevisionRef
	for _, r := range rows {
		if r.Revision == "" || r.Workload == "" {
			continue
		}
		for _, c := range spanKeys(r.ClusterID) {
			out = append(out, chstore.WorkloadRevisionRef{Cluster: c, Namespace: r.Namespace, Workload: r.Workload, Revision: r.Revision})
		}
	}
	return out
}

// applyV2Traffic — SAF: her satıra, başlangıçtan (−pay) sonraki en erken ilk
// span zamanı; v1'in doldurduğu (boş olmayan) değer ezilmez.
func applyV2Traffic(rows []chstore.RolloutRow, spanKeys func(clusterID string) []string, first map[chstore.WorkloadRevisionRef]time.Time) {
	for i := range rows {
		r := &rows[i]
		if !r.FirstSpanAt.IsZero() {
			continue
		}
		var best time.Time
		for _, c := range spanKeys(r.ClusterID) {
			t, ok := first[chstore.WorkloadRevisionRef{Cluster: c, Namespace: r.Namespace, Workload: r.Workload, Revision: r.Revision}]
			if !ok || t.Before(r.StartedAt.Add(-rolloutTrafficSlack)) {
				continue
			}
			if best.IsZero() || t.Before(best) {
				best = t
			}
		}
		r.FirstSpanAt = best
	}
}

func (s *Server) attachV2Traffic(ctx context.Context, rows []chstore.RolloutRow, from, to time.Time) {
	if s.store == nil || len(rows) == 0 {
		return
	}
	spanKeys := func(id string) []string {
		if c, ok := s.resolveCluster(id); ok {
			return c.SpanClusterKeys()
		}
		return nil
	}
	refs := v2TrafficRefs(rows, spanKeys)
	if len(refs) == 0 {
		return
	}
	first, err := s.store.RevisionFirstSpans(ctx, refs, from.Add(-rolloutTrafficSlack), to.Add(time.Hour))
	if err != nil {
		return
	}
	applyV2Traffic(rows, spanKeys, first)
}
