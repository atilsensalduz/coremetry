package api

// problem_affected.go — v0.10.707 (Dynatrace paritesi #5, dilim 2):
//
//	GET /api/problems/{id}/affected
//
// Davis'in "affected entities" listesi: problem öznesinin blast-radius
// ÇAĞIRANLARI (service_callers_5m, ≤25, çağrıya göre) ∪ kök-neden hipotezinin
// etkilenen POD'ları (Deep.AffectedPods; yalnız derin soruşturma koştuysa) ∪
// k8s CLUSTER'lar (okuma-anı Clusters). Liste ucunda DEĞİL (200 satır ×
// blast radius); yalnız çekmece/detay, aç-üzerine-getir. Servissiz problem
// (exception-storm, watcher, self-*) → boş liste, dürüst. {id} "P-…"
// görüntü kimliğini de kabul eder. serveCached 60 s (blast-radius ile aynı).
// Rol kapısı yok — viewer görür. api.go BÜYÜMEZ: route defteri.
//
// v0.10.1495 (operatör, prod JVM GC problemi): liste artık yalnız bağı
// DOĞRULANMIŞ ve etkisi GÖZLENMİŞ varlıkları sayar; hata görmeyen çağıranlar
// ayrı `linked` grubunda (bağ: gerçek çağrı kenarı, etki yok), pod kapsamlı
// problemde özne pod'u başta, cluster yalnız doğrulanabiliyorsa. Ayrıntı:
// affectedSet.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-affected", (*Server).registerProblemAffectedRoutes) }

func (s *Server) registerProblemAffectedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/affected", s.getProblemAffected)
}

// affectedEntity — tek satır. Kind: service | pod | cluster.
// Relation (v0.10.1495) — varlığın özneye DOĞRULANMIŞ bağı:
//   - caller       — çağıran servis (service_callers_5m: ebeveyn→çocuk span
//     kenarı; zamanla birlikte hareket DEĞİL, gerçek çağrı)
//   - subject-pod  — problemin kendi pod'u (runtime / pod kapsamlı problem)
//   - evidence-pod — kök-neden hipotezinin pod kanıtı (Deep.AffectedPods)
//   - cluster      — öznenin çalıştığı k8s cluster'ı (sahiplik)
type affectedEntity struct {
	Kind           string  `json:"kind"`
	ID             string  `json:"id"`
	Relation       string  `json:"relation"`
	Calls          uint64  `json:"calls,omitempty"`
	Errors         uint64  `json:"errors,omitempty"`
	ErrorRate      float64 `json:"errorRate,omitempty"`
	HasOpenProblem bool    `json:"hasOpenProblem,omitempty"`
	Count          int     `json:"count,omitempty"` // pod: hipotez sayımı
}

const affectedCallersMax = 25

// affectedInput — buildAffectedEntities girdileri (hepsi nil/boş olabilir).
type affectedInput struct {
	Subject string
	// Pod — pod kapsamlı problemin pod'u ("" = servis kapsamlı).
	Pod string
	// Callers — blast radius okuması; CallersKnown=false → okunamadı/atlandı.
	Callers      *chstore.BlastRadius
	CallersKnown bool
	Pods         []chstore.PodHit
	Clusters     []string
}

// affectedSet — v0.10.1495 (operatör, prod JVM GC problemi: "AFFECTED
// ENTITIES (37)" bağlantısız görünen onlarca servis listeliyordu).
//   - Entities: bağı DOĞRULANMIŞ ve etkisi GÖZLENMİŞ (çağıran hata görüyor ya
//     da kendi problemi açık) varlıklar + öznenin kapsamı (pod / cluster).
//     Sayım (Total) yalnız bunlar.
//   - Linked: bağı doğrulanmış (gerçek çağrı kenarı) ama pencerede etki
//     GÖRÜLMEYEN çağıranlar — "etkilenen" sayılmaz, ayrı ve kapalı grup.
//   - TopologyKnown=false: çağıran okuması düştü — liste yalnız
//     doğrulanabilenden ibaret, panel bunu söyler.
//   - ClustersUnverified: pod kapsamlı problemde servis birden çok cluster'da
//     — pod'un hangisinde olduğu doğrulanamaz, cluster listelenmez.
//
// Zamanla birlikte hareket eden ama bağı doğrulanmayan servisler BURAYA HİÇ
// GİRMEZ (kök-neden panelinin "What else changed" bölümünde ayrı grup).
type affectedSet struct {
	Entities           []affectedEntity
	Linked             []affectedEntity
	TopologyKnown      bool
	PodScoped          bool
	ClustersUnverified bool
}

// buildAffectedEntities — SAF (problem_affected_test.go tablo). Sıra: özne
// pod'u → etkilenen çağıranlar (açık problem önce, sonra çağrı desc; ≤25
// çağıran toplam) → kanıt pod'ları (sayım desc) → cluster'lar; tekrarsız
// (kind+id); özne servisin kendisi listeye girmez.
func buildAffectedEntities(in affectedInput) affectedSet {
	out := affectedSet{Entities: []affectedEntity{}, Linked: []affectedEntity{}, TopologyKnown: in.CallersKnown, PodScoped: in.Pod != ""}
	seen := map[string]bool{}
	ok := func(e affectedEntity) bool {
		k := e.Kind + "|" + e.ID
		if e.ID == "" || seen[k] || (e.Kind == "service" && e.ID == in.Subject) {
			return false
		}
		seen[k] = true
		return true
	}
	if in.Pod != "" && in.Pod != in.Subject {
		if e := (affectedEntity{Kind: "pod", ID: in.Pod, Relation: "subject-pod"}); ok(e) {
			out.Entities = append(out.Entities, e)
		}
	}
	if in.Callers != nil {
		callers := append([]chstore.BlastRadiusCaller(nil), in.Callers.Callers...)
		sort.SliceStable(callers, func(i, j int) bool {
			if callers[i].HasOpenProblem != callers[j].HasOpenProblem {
				return callers[i].HasOpenProblem
			}
			return callers[i].Calls > callers[j].Calls
		})
		if len(callers) > affectedCallersMax {
			callers = callers[:affectedCallersMax]
		}
		for _, c := range callers {
			e := affectedEntity{Kind: "service", ID: c.Service, Relation: "caller", Calls: c.Calls, Errors: c.Errors, ErrorRate: c.ErrorRate, HasOpenProblem: c.HasOpenProblem}
			if !ok(e) {
				continue
			}
			if c.Errors > 0 || c.HasOpenProblem {
				out.Entities = append(out.Entities, e)
			} else {
				out.Linked = append(out.Linked, e)
			}
		}
	}
	ps := append([]chstore.PodHit(nil), in.Pods...)
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Count > ps[j].Count })
	for _, p := range ps {
		if e := (affectedEntity{Kind: "pod", ID: p.Pod, Relation: "evidence-pod", Count: p.Count}); ok(e) {
			out.Entities = append(out.Entities, e)
		}
	}
	clusters := []string{}
	cseen := map[string]bool{}
	for _, c := range in.Clusters {
		if c != "" && !cseen[c] {
			cseen[c] = true
			clusters = append(clusters, c)
		}
	}
	if out.PodScoped && len(clusters) > 1 {
		out.ClustersUnverified = true
		clusters = nil
	}
	for _, c := range clusters {
		if e := (affectedEntity{Kind: "cluster", ID: c, Relation: "cluster"}); ok(e) {
			out.Entities = append(out.Entities, e)
		}
	}
	return out
}

// problemPod — SAF: pod kapsamlı problemin pod'u. Yapısal alan önce; v0.9.403
// öncesi satırlarda runtime:<check>:<svc>:<pod> kimliğinin son parçası
// (FE ProblemDetail ile aynı köprü).
func problemPod(p chstore.Problem) string {
	if p.Pod != "" {
		return p.Pod
	}
	if strings.HasPrefix(p.ID, "runtime:") {
		if segs := strings.Split(p.ID, ":"); len(segs) >= 4 {
			return segs[len(segs)-1]
		}
	}
	return ""
}

// affectedWindow — SAF: [onset−1h, çözüm|şimdi], en çok 24 s.
func affectedWindow(p chstore.Problem, now time.Time) (from, to time.Time) {
	to = now
	if p.ResolvedAt != nil && *p.ResolvedAt > 0 {
		to = time.Unix(0, *p.ResolvedAt)
	}
	from = time.Unix(0, p.StartedAt).Add(-time.Hour)
	if p.StartedAt <= 0 || from.After(to) {
		from = to.Add(-time.Hour)
	}
	if to.Sub(from) > 24*time.Hour {
		from = to.Add(-24 * time.Hour)
	}
	return from, to
}

func (s *Server) getProblemAffected(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem id required")
		return
	}
	s.serveCached(w, r, "problem:affected:v2:"+id, 60*time.Second, func(ctx context.Context) (any, error) {
		var p *chstore.Problem
		var err error
		if chstore.IsProblemDisplayID(id) {
			p, err = s.store.GetProblemByDisplayID(ctx, id)
		} else {
			p, err = s.store.GetProblem(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, notFoundf("problem %q", id)
		}
		from, to := affectedWindow(*p, time.Now())
		in := affectedInput{Subject: p.Service, Pod: problemPod(*p)}
		// Servis olmayan öznede çağıran okuması yok — doğrulanamayan bir şey de
		// yok (TopologyKnown "okunamadı" demesin).
		in.CallersKnown = p.Service == "" || (p.Kind != "" && p.Kind != chstore.ProblemKindService)
		if !in.CallersKnown {
			if b, berr := s.store.GetServiceBlastRadius(ctx, p.Service, from, to); berr == nil {
				in.Callers, in.CallersKnown = &b, true
			}
		}
		if h, herr := s.store.GetHypothesis(ctx, "problem", p.ID); herr == nil && h != nil && h.Deep != nil {
			in.Pods = h.Deep.AffectedPods
		}
		probs := s.store.EnrichProblemsWithClusters(ctx, []chstore.Problem{*p}, time.Hour)
		in.Clusters = probs[0].Clusters
		set := buildAffectedEntities(in)
		return map[string]any{
			"problemId": p.ID, "displayId": chstore.ProblemDisplayID(p.ID), "service": p.Service,
			"windowFromNs": from.UnixNano(), "windowToNs": to.UnixNano(),
			"entities": set.Entities, "total": len(set.Entities),
			// v0.10.1495 — bağlı ama etkisiz çağıranlar ayrı; topoloji/pod kapsamı.
			"linked": set.Linked, "linkedTotal": len(set.Linked),
			"topologyKnown": set.TopologyKnown, "podScoped": set.PodScoped,
			"clustersUnverified": set.ClustersUnverified,
		}, nil
	})
}
