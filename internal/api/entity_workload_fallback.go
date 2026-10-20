package api

// entity_workload_fallback.go — v0.10.1420 (operatör, /rollouts: "Deployment
// Workload basınca service'e girsin, şu an bulamıyor." — eski hedef
// /entity?id=wl:<cid>/<ns>/Deployment/<ad> "Kayıt bulunamadı" diyordu).
//
// KÖK NEDEN (kod okuması): /api/entity BİREBİR entity_id araması yapar.
// Rollouts satırının kimliği türünü rollout kaynağından alır (v2: KSM
// kube_deployment_* → "Deployment"), envanter ise türü POD SAHİPLİK ZİNCİRİNDEN
// çözer (entity/normalize.go ResolveWorkload): kube_replicaset_owner serisi
// okunamazsa (namespace kalkanı, kazıma eksikliği, RS sahip etiketi yok) iş
// yükü wl:<cid>/<ns>/ReplicaSet/<ad>-<hash> olarak; KSM pod'u hiç görmezse
// span geçişi pod adından wl:<cid>/<ns>/Derived/<ad> (DerivedKind) olarak
// yazılır. Aynı iş yükü, farklı kimlik → birebir arama boş döner. (Hangi
// dalın prod'da ısırdığı canlı CH olmadan doğrulanmadı — iki dal da bu
// yedekle kapanır.)
//
// YEDEK: birebir kayıt yoksa aynı cluster + namespace'te ada göre envanter
// araması (EntityList, LIMIT 50, şu an geçerli kayıtlar) ve deterministik
// seçim (pickWorkloadEntityFallback). Başarısızsa bugünkü 404.

import (
	"context"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

func (s *Server) workloadEntityFallback(ctx context.Context, ref entity.Ref) string {
	if s.store == nil || ref.Name == "" {
		return ""
	}
	// Önce birebir ad (yaygın ad "api" ILIKE'ta 50'yi doldurup birebiri
	// dışarıda bırakmasın), sonra "<ad>-" önekli ReplicaSet için alt-dizge.
	base := chstore.EntityListQuery{ClusterID: ref.ClusterID, Type: entity.TypeWorkload, Namespace: ref.Namespace, Limit: 50}
	for _, q := range []chstore.EntityListQuery{withName(base, ref.Name), withSearch(base, ref.Name+"-")} {
		recs, err := s.store.EntityList(ctx, q)
		if err != nil {
			return ""
		}
		if id := pickWorkloadEntityFallback(ref, recs); id != "" {
			return id
		}
	}
	return ""
}

func withName(q chstore.EntityListQuery, n string) chstore.EntityListQuery   { q.Name = n; return q }
func withSearch(q chstore.EntityListQuery, n string) chstore.EntityListQuery { q.Search = n; return q }

// pickWorkloadEntityFallback — SAF. Adaylar: aynı cluster + namespace'te
// iş yükü kaydı ve
//  1. adı BİREBİR aynı (tür farkı: Derived / başka sahip türü) — istenen
//     tür etiketiyle eşleşen önce, sonra kimlik alfabetik;
//  2. yoksa ReplicaSet adı "<ad>-<hash>" (RS sahibi okunamamış Deployment) —
//     en son görülen (LastSeen), eşitlikte kimlik alfabetik.
//
// Aday yoksa "".
func pickWorkloadEntityFallback(ref entity.Ref, recs []chstore.EntityRecord) string {
	var exact, rs []chstore.EntityRecord
	for _, r := range recs {
		if r.Type != entity.TypeWorkload || r.ClusterID != ref.ClusterID || r.Namespace != ref.Namespace {
			continue
		}
		switch {
		case r.Name == ref.Name:
			exact = append(exact, r)
		case strings.HasPrefix(r.Name, ref.Name+"-") && r.Labels["kind"] == "ReplicaSet" &&
			!strings.Contains(strings.TrimPrefix(r.Name, ref.Name+"-"), "-"):
			rs = append(rs, r)
		}
	}
	if len(exact) > 0 {
		sort.SliceStable(exact, func(i, j int) bool {
			ki, kj := exact[i].Labels["kind"] == ref.Kind, exact[j].Labels["kind"] == ref.Kind
			if ki != kj {
				return ki
			}
			return exact[i].ID < exact[j].ID
		})
		return exact[0].ID
	}
	if len(rs) > 0 {
		sort.SliceStable(rs, func(i, j int) bool {
			if !rs[i].LastSeen.Equal(rs[j].LastSeen) {
				return rs[i].LastSeen.After(rs[j].LastSeen)
			}
			return rs[i].ID < rs[j].ID
		})
		return rs[0].ID
	}
	return ""
}
