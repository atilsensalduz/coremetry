package chstore

// rollout_origin.go — v0.10.1445 (operatör: "KSM dedektörü doğru mu"), çekirdek
// v0.10.1494'de (CoSRE 2d) internal/api/rollouts_origin.go'dan buraya taşındı:
// v2 rollout satırına GİT kaynağı — gerçek geri alma (Argo daha önce
// senkronlanmış bir revizyona döndü) mı, eski bir yapının ileri commit'le
// yeniden deploy'u mu. KSM ikisini ayıramaz (pod şablonu aynıysa Kubernetes
// eski ReplicaSet'i yeniden kullanır); ayrım argocd_sync_events'ten okuma
// anında. Neden chstore: api kapanışında kalan çekirdeği SRE okuyucuları
// (internal/sre → mcptools) göremiyordu; sre api'yi import edemez. api'de
// yalnız ayar kapısı (rolloutOriginSettings) + ince sarmalayıcı kaldı.
//
// Akış (liste başına iki sınırlı sorgu):
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

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/rollout"
)

const (
	// rolloutOriginHistoryLookback — "daha önce senkronlandı mı" geçmişi.
	rolloutOriginHistoryLookback = 90 * 24 * time.Hour
	// rolloutOriginWindowSlack — üst uçta geç gelen senkron bitişi payı.
	rolloutOriginWindowSlack = time.Hour
)

// RolloutOriginReader — CH dikişi (*Store karşılar; testte sahte).
type RolloutOriginReader interface {
	ArgoCDWorkloadEdgesFor(ctx context.Context, workloads []argocd.ServiceWorkload, since time.Time) ([]argocd.MappingRow, bool, error)
	ArgoCDSyncEventsFor(ctx context.Context, keys []argocd.AppKey, from, to time.Time, perApp int) ([]argocd.SyncEventRow, error)
}

type originWorkloadKey struct{ cluster, ns, workload string }

// AttachRolloutOriginWith — satırlara git kaynağını (Origin) okuma anında
// ekler. Hata → hiçbir satıra alan yazılmaz (çağıran loglar, liste yine döner).
// v0.10.1494: api kapanışından buraya taşındı — SRE DeployFacts ve
// list_deployments aynı çekirdeği mcptools.Deps.RolloutOrigin üzerinden görür.
func AttachRolloutOriginWith(ctx context.Context, rows []RolloutRow, st RolloutOriginReader, windowMin int, now time.Time) error {
	if windowMin <= 0 {
		windowMin = 5
	}
	var idx []int
	seenWL := map[originWorkloadKey]bool{}
	var wls []argocd.ServiceWorkload
	var minAt, maxAt time.Time
	for i := range rows {
		r := &rows[i]
		if r.V2 == nil || r.Workload == "" || r.StartedAt.IsZero() {
			continue
		}
		idx = append(idx, i)
		k := originWorkloadKey{r.ClusterID, r.Namespace, r.Workload}
		if !seenWL[k] {
			seenWL[k] = true
			wls = append(wls, argocd.ServiceWorkload{ClusterID: r.ClusterID, Namespace: r.Namespace, Workload: r.Workload})
		}
		if minAt.IsZero() || r.StartedAt.Before(minAt) {
			minAt = r.StartedAt
		}
		if r.StartedAt.After(maxAt) {
			maxAt = r.StartedAt
		}
	}
	if len(idx) == 0 {
		return nil
	}
	edges, _, err := st.ArgoCDWorkloadEdgesFor(ctx, wls, now.Add(-argocd.MappingFreshness))
	if err != nil {
		return err
	}
	appsOf := originAppsByWorkload(edges)
	var keys []argocd.AppKey
	seenApp := map[argocd.AppKey]bool{}
	for _, apps := range appsOf {
		for _, a := range apps {
			k := argocd.AppKey{InstanceID: a.InstanceID, AppNamespace: a.AppNamespace, Name: a.AppName}
			if !seenApp[k] {
				seenApp[k] = true
				keys = append(keys, k)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return originAppKeyLess(keys[i], keys[j]) })
	W := time.Duration(windowMin) * time.Minute
	syncsOf := map[argocd.AppKey][]argocd.SyncEventRow{}
	if len(keys) > 0 {
		syncs, err := st.ArgoCDSyncEventsFor(ctx, keys, minAt.Add(-rolloutOriginHistoryLookback), maxAt.Add(W+rolloutOriginWindowSlack), ArgoCDSyncPerAppMax)
		if err != nil {
			return err
		}
		for _, sy := range syncs {
			syncsOf[sy.Key()] = append(syncsOf[sy.Key()], sy)
		}
	}
	for _, i := range idx {
		r := &rows[i]
		apps := originMatchingApps(appsOf[originWorkloadKey{r.ClusterID, r.Namespace, r.Workload}], r.Kind)
		r.Origin = originForRow(*r, apps, syncsOf, W, windowMin)
	}
	return nil
}

func originAppKeyLess(a, b argocd.AppKey) bool {
	if a.InstanceID != b.InstanceID {
		return a.InstanceID < b.InstanceID
	}
	if a.AppNamespace != b.AppNamespace {
		return a.AppNamespace < b.AppNamespace
	}
	return a.Name < b.Name
}

// originAppsByWorkload — SAF: iş yükü → kanıt sayılan kenarlar (exact |
// estimated; zayıf yok).
func originAppsByWorkload(edges []argocd.MappingRow) map[originWorkloadKey][]argocd.MappingRow {
	out := map[originWorkloadKey][]argocd.MappingRow{}
	for _, e := range edges {
		if e.Workload == "" || (e.MatchClass != argocd.ClassExact && e.MatchClass != argocd.ClassEstimated) {
			continue
		}
		k := originWorkloadKey{e.ClusterID, e.Namespace, e.Workload}
		out[k] = append(out[k], e)
	}
	return out
}

// originMatchingApps — SAF: satırın türüne uyan kenarlar, uygulama başına bir
// (exact önce). Tür boşsa tür süzgeci yok.
func originMatchingApps(edges []argocd.MappingRow, kind string) []argocd.MappingRow {
	best := map[argocd.AppKey]argocd.MappingRow{}
	for _, e := range edges {
		if kind != "" && e.WorkloadKind != "" && !strings.EqualFold(e.WorkloadKind, kind) {
			continue
		}
		k := argocd.AppKey{InstanceID: e.InstanceID, AppNamespace: e.AppNamespace, Name: e.AppName}
		if cur, ok := best[k]; !ok || (cur.MatchClass != argocd.ClassExact && e.MatchClass == argocd.ClassExact) {
			best[k] = e
		}
	}
	out := make([]argocd.MappingRow, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return originAppKeyLess(argocd.AppKey{InstanceID: out[i].InstanceID, AppNamespace: out[i].AppNamespace, Name: out[i].AppName},
			argocd.AppKey{InstanceID: out[j].InstanceID, AppNamespace: out[j].AppNamespace, Name: out[j].AppName})
	})
	return out
}

// originSyncMatch — matchSyncForRollout sonucu.
type originSyncMatch struct {
	Matched      *argocd.SyncEventRow
	Prev         *argocd.SyncEventRow
	FirstSeen    *argocd.SyncEventRow // SeenBefore: yeni revizyonun prev'den önceki en eski senkronu
	SeenBefore   bool
	HistoryDepth int
	Candidates   int // pencerede ayrı git kimliği taşıyan senkron sayısı (>1 belirsiz)
}

// matchSyncForRollout — SAF (plan §3.1 adım 3). syncs aynı uygulamanın
// satırları (sıra önemsiz). Yalnız Succeeded + kimliği dolu senkronlar
// sayılır; senkronlanan iş yükü listesi doluysa ve bu iş yükünü içermiyorsa
// (aynı uygulamada başka kaynağın self-heal'i) aday değildir.
func matchSyncForRollout(row RolloutRow, syncs []argocd.SyncEventRow, W time.Duration) originSyncMatch {
	var ok []argocd.SyncEventRow
	for _, s := range syncs {
		if s.Phase == argocd.SyncPhaseSucceeded && s.Identity() != "" && !s.OpStartedAt.IsZero() {
			ok = append(ok, s)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].OpStartedAt.Before(ok[j].OpStartedAt) })
	wlRef := row.Kind + "/" + row.Namespace + "/" + row.Workload
	var res originSyncMatch
	var best int = -1
	var bestD time.Duration
	idents := map[string]bool{}
	for i, s := range ok {
		end := s.OpStartedAt
		if s.FinishedAt.After(end) {
			end = s.FinishedAt
		}
		if row.StartedAt.Before(s.OpStartedAt.Add(-W)) || row.StartedAt.After(end.Add(W)) {
			continue
		}
		if len(s.SyncedWorkloads) > 0 && !originListsWorkload(s.SyncedWorkloads, wlRef, row.Namespace, row.Workload) {
			continue
		}
		idents[s.Identity()] = true
		d := row.StartedAt.Sub(s.OpStartedAt)
		if d < 0 {
			d = -d
		}
		// Eşit uzaklıkta canlı satır tohumdan zengin: onu tercih et.
		if best < 0 || d < bestD || (d == bestD && ok[best].IsHistorySeed() && !s.IsHistorySeed()) {
			best, bestD = i, d
		}
	}
	res.Candidates = len(idents)
	if best < 0 {
		return res
	}
	m := ok[best]
	res.Matched = &m
	newID := m.Identity()
	prevIdx := -1
	for i := best - 1; i >= 0; i-- {
		if ok[i].OpStartedAt.Equal(m.OpStartedAt) {
			continue // aynı senkronun ikizi (canlı ↔ tohum)
		}
		if prevIdx < 0 {
			prevIdx = i
		}
		res.HistoryDepth++
	}
	if prevIdx < 0 {
		return res
	}
	p := ok[prevIdx]
	res.Prev = &p
	for i := 0; i < prevIdx; i++ {
		if ok[i].Identity() == newID && ok[i].OpStartedAt.Before(p.OpStartedAt) {
			fs := ok[i]
			res.FirstSeen, res.SeenBefore = &fs, true
			break
		}
	}
	return res
}

// originListsWorkload — "Kind/ns/name" listesinde iş yükü var mı (tür satırda
// boşsa yalnız ns/name).
func originListsWorkload(list []string, ref, ns, name string) bool {
	for _, e := range list {
		if strings.EqualFold(e, ref) || strings.HasSuffix(e, "/"+ns+"/"+name) {
			return true
		}
	}
	return false
}

// originForRow — SAF: tek satırın origin nesnesi.
func originForRow(r RolloutRow, apps []argocd.MappingRow, syncsOf map[argocd.AppKey][]argocd.SyncEventRow, W time.Duration, windowMin int) *rollout.RolloutOrigin {
	in := rollout.OriginInput{KSMChangeType: r.V2.ChangeType, PrevTag: r.PrevImageTag, NewTag: r.ImageTag, WindowMin: windowMin}
	o := &rollout.RolloutOrigin{Basis: rollout.OriginBasisNone, PrevTag: r.PrevImageTag, NewTag: r.ImageTag}
	if len(apps) == 0 {
		res := rollout.ClassifyRolloutOrigin(in)
		o.Kind, o.Confidence, o.TagOrder = res.Origin, res.Confidence, res.TagOrder
		o.Reason = "no Argo CD application is mapped to this workload; git source unknown"
		return o
	}
	if len(apps) > 1 {
		in.Ambiguous, in.AmbiguousApps = true, len(apps)
		res := rollout.ClassifyRolloutOrigin(in)
		o.Kind, o.Confidence, o.Reason, o.TagOrder = res.Origin, res.Confidence, res.Reason, res.TagOrder
		return o
	}
	a := apps[0]
	o.App = &rollout.OriginApp{InstanceID: a.InstanceID, AppNamespace: a.AppNamespace, AppName: a.AppName, MatchMethod: a.MatchMethod, MatchClass: a.MatchClass}
	m := matchSyncForRollout(r, syncsOf[argocd.AppKey{InstanceID: a.InstanceID, AppNamespace: a.AppNamespace, Name: a.AppName}], W)
	if m.Candidates > 1 {
		in.Ambiguous, in.AmbiguousApps, in.AmbiguousSyncs = true, 1, m.Candidates
	}
	if m.Matched != nil {
		in.ArgoNewRevision = m.Matched.Identity()
		o.NewRevision, o.NewRevisionAt = in.ArgoNewRevision, m.Matched.OpStartedAt.UnixMilli()
		o.Basis = rollout.OriginBasisArgo
		if !m.Matched.IsHistorySeed() {
			auto := m.Matched.Automated == 1
			o.Automated, o.Initiator = &auto, m.Matched.Initiator
		}
	}
	if m.Prev != nil {
		in.ArgoPrevRevision = m.Prev.Identity()
		o.PrevRevision, o.PrevRevisionAt = in.ArgoPrevRevision, m.Prev.OpStartedAt.UnixMilli()
	}
	if m.FirstSeen != nil {
		o.FirstSyncedAt = m.FirstSeen.OpStartedAt.UnixMilli()
	}
	in.SeenBefore, in.HistoryDepth = m.SeenBefore, m.HistoryDepth
	o.HistoryDepth = m.HistoryDepth
	res := rollout.ClassifyRolloutOrigin(in)
	o.Kind, o.Confidence, o.Reason, o.TagOrder = res.Origin, res.Confidence, res.Reason, res.TagOrder
	return o
}
