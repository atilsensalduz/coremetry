package rollout

import (
	"reflect"
	"strings"
	"testing"
)

// v2rollback_proof_test.go — v0.10.1421 — Operator-reported (prod /rollouts
// çekmecesi): "Bazen yeni image bulamadığında geri alındı yazıyor." Örnek:
// Deployment, revizyon <eski RS> → <yeni RS>, nesil 18 · gözlenen 18,
// replika 4/4/4, imaj önceki dolu / yeni "—", durum "geri alındı", not "geç
// kanıt: revizyon nesil artışından sonra göründü; geri alındı: nesil 19",
// Başladı / Pod'lar hazır / Geri alındı hepsi +0s, kaynak ksm.
//
// Kök neden: (1) RS spec ailesi varken kapı zaman aşımındaki GENEL START
// (hedef revizyon görülmedi) change_type=rollback tahminiyle açılıyordu ve
// start() önceki başarılı olayı kanıtsız rolled_back yapıyordu; (2) geç
// START (emilen artıştan sonra gelen RS) imaj hedefi değildi, aynı tikte
// succeeded kapandığı için yeni imajı hiç okunmadı. Kural: rolled_back YALNIZ
// olumlu kanıtla — somut, bilinen bir hedef revizyon VE olayın yeni imajı
// biliniyor; imaj bilinmiyorsa başarılı olay başarılı kalır, gerekçe notta.
// Sentetik adlar: orders-api, registry.example.test.

const (
	rpWL     = "orders-api"
	rpOld    = "orders-api-7a"
	rpNew    = "orders-api-8b"
	rpImgOld = "registry.example.test/shop/orders-api:1.0"
	rpImgNew = "registry.example.test/shop/orders-api:2.0"
)

// rpLateStart — KSM-only örnek: nesil 18 artışı yeni RS görünmeden emilir
// (scale_only), RS bir tik sonra 4/4/4 hazır ve imajsız görünür → geç START,
// aynı tikte succeeded (+0s).
func rpLateStart(s *detSim) {
	s.tick(0, detDep(rpWL, 0, 17).reps(4, 4, 4, 4).rs(rpOld, 4).img(rpOld, rpImgOld).V())
	s.tick(30, detDep(rpWL, 30, 18).reps(4, 4, 4, 4).rs(rpOld, 0).V())
	s.tick(60, detDep(rpWL, 60, 18).reps(4, 4, 4, 4).rs(rpOld, 0).rs(rpNew, 4).V())
}

func TestDetectV2_RollbackNeedsPositiveEvidence(t *testing.T) {
	const D = V2KindDeployment
	cases := []struct {
		name     string
		run      func(s *detSim)
		g18      string // nesil 18 olayının durumu
		g18Note  string // notta beklenen parça ("" = denetlenmez)
		g19Type  string
		g18Image []string
		counts   map[string]int
	}{
		{name: "KSM-only: geç START imajsız; sonraki artışta yeni RS görünmez (kapı zaman aşımı) → geri alındı DENMEZ",
			run: func(s *detSim) {
				rpLateStart(s)
				for at := 90; at <= 420; at += 30 { // tutma var, yeni RS okumada yok
					s.tick(at, detDep(rpWL, at, 19).reps(4, 5, 1, 4).rs(rpOld, 0).rs(rpNew, 4).V())
				}
			},
			g18: V2StatusSucceeded, g19Type: V2ChangeRollout,
			counts: map[string]int{"late_evidence": 1, "generic_start": 1}},
		{name: "yeni imaj bilinmiyor + bilinen RS yeniden etkin → başarılı olay başarılı kalır, gerekçe notta",
			run: func(s *detSim) {
				rpLateStart(s)
				s.tick(90, detDep(rpWL, 90, 19).reps(4, 5, 2, 4).rs(rpOld, 2).rs(rpNew, 2).V())
			},
			g18: V2StatusSucceeded, g18Note: "nesil 19 geri alma sayılmadı: yeni imaj bilinmiyor", g19Type: V2ChangeRollback,
			counts: map[string]int{"rollback_unproven": 1}},
		{name: "gerçek geri alma: yeni imaj biliniyor, sonraki rollout önceki revizyona döner → rolled_back",
			run: func(s *detSim) {
				rpLateStart(s)
				s.tick(90, detDep(rpWL, 90, 19).reps(4, 5, 2, 4).rs(rpOld, 2).rs(rpNew, 2).
					img(rpOld, rpImgOld).img(rpNew, rpImgNew).V())
			},
			g18: V2StatusRolledBack, g18Note: "geri alındı: nesil 19", g19Type: V2ChangeRollback, g18Image: []string{rpImgNew},
			counts: map[string]int{"rollback_unproven": 0}},
		{name: "geç okunan imaj kapanmış olaya yazılır (çekmece 'yeni: —' göstermez)",
			run: func(s *detSim) {
				rpLateStart(s)
				s.tick(90, detDep(rpWL, 90, 18).reps(4, 4, 4, 4).rs(rpOld, 0).rs(rpNew, 4).img(rpNew, rpImgNew).V())
			},
			g18: V2StatusSucceeded, g18Image: []string{rpImgNew},
			counts: map[string]int{"images_backfill": 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newDetSim(t)
			tc.run(s)
			g18 := s.ev(D, rpWL, 18)
			if g18.Status != tc.g18 {
				t.Fatalf("nesil 18 durumu %q, beklenen %q (%v)", g18.Status, tc.g18, s.evs(D, rpWL))
			}
			if !strings.Contains(g18.Note, "geç kanıt") {
				t.Fatalf("fixture geç START değil: %+v", g18)
			}
			if tc.g18Note != "" && !strings.Contains(g18.Note, tc.g18Note) {
				t.Fatalf("not %q, beklenen parça %q", g18.Note, tc.g18Note)
			}
			if tc.g18Image != nil && !reflect.DeepEqual(g18.Images, tc.g18Image) {
				t.Fatalf("nesil 18 imajı %v, beklenen %v", g18.Images, tc.g18Image)
			}
			if tc.g19Type != "" {
				if g19 := s.ev(D, rpWL, 19); g19.ChangeType != tc.g19Type {
					t.Fatalf("nesil 19 change_type %q, beklenen %q", g19.ChangeType, tc.g19Type)
				}
			}
			for k, v := range tc.counts {
				if s.counts[k] != v {
					t.Fatalf("sayaç %s = %d, beklenen %d (%v)", k, s.counts[k], v, s.counts)
				}
			}
		})
	}
}

// StatefulSet: update_revision bilinen revizyona döner ama pod imajı yok
// (controller-revision-hash birleşimi okunamadı) → rolled_back denmez.
func TestDetectV2_StsRollbackWithoutImageNotRolledBack(t *testing.T) {
	s := newDetSim(t)
	s.tick(0, detSts("db", 0, 1, "db-r1", "db-r1").V())
	s.tick(30, detSts("db", 30, 2, "db-r1", "db-r2").reps(3, 3, 1, 3).V())
	s.tick(60, detSts("db", 60, 2, "db-r2", "db-r2").V())
	s.tick(90, detSts("db", 90, 3, "db-r2", "db-r1").reps(3, 3, 1, 3).V())
	got := s.evs(V2KindStatefulSet, "db")
	want := []string{"g2 rollout succeeded db-r1>db-r2", "g3 rollback progressing db-r2>db-r1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("olaylar %q, beklenen %q", got, want)
	}
}

func TestV2RollbackUnproven(t *testing.T) {
	ev := func(images ...string) V2Event { return V2Event{NewRevision: rpNew, Images: images} }
	cases := []struct {
		name   string
		e      V2Event
		target string
		want   string
	}{
		{"somut hedef + bilinen imaj = kanıtlı", ev(rpImgNew), rpOld, ""},
		{"genel START (hedef boş) tahmindir", ev(rpImgNew), "", "hedef revizyon görülmedi"},
		{"hedef olayın kendi revizyonu", ev(rpImgNew), rpNew, "hedef bu olayın revizyonu"},
		{"yeni imaj bilinmiyor", ev(), rpOld, "yeni imaj bilinmiyor"},
	}
	for _, tc := range cases {
		if got := v2RollbackUnproven(tc.e, tc.target); got != tc.want {
			t.Errorf("%s: %q, beklenen %q", tc.name, got, tc.want)
		}
	}
}

// Geç START adayı (nesil değişmedi, okumada bilinmeyen RS) imaj hedefidir.
func TestV2ImageTargetsLateEvidence(t *testing.T) {
	const cid = "c-1"
	o := func(name string, rs ...string) V2Observation {
		x := V2Observation{Kind: V2KindDeployment, Namespace: "shop", Name: name, Generation: 18, SpecReplicas: 4}
		for _, r := range rs {
			x.ReplicaSets = append(x.ReplicaSets, V2ReplicaSet{Name: r, SpecReplicas: 4})
		}
		return x
	}
	st := func(name string, known ...string) V2WorkloadState {
		return V2WorkloadState{ClusterID: cid, Namespace: "shop", WorkloadKind: V2KindDeployment, Workload: name,
			Generation: 18, IncarnationAt: detT0, CurrentRevision: known[0], KnownRevisions: known, Images: []string{rpImgOld}}
	}
	snap := V2Snapshot{Complete: true, Workloads: []V2Observation{o(rpWL, rpOld, rpNew), o("cart-api", "cart-api-1")}}
	primary, _, _ := V2ImageTargets(snap, cid, []V2WorkloadState{st(rpWL, rpOld), st("cart-api", "cart-api-1")}, nil, 100, 20)
	if want := []V2Key{{cid, "shop", V2KindDeployment, rpWL}}; !reflect.DeepEqual(primary, want) {
		t.Fatalf("primary %v, beklenen %v", primary, want)
	}
}
