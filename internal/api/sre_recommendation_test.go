package api

// v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook eşleme + geri alma
// metni). "Uygulandı / Ele" ucunun rol kapısı (viewer 403), audit
// sre.recommendation.ack, koşan kayıtta 409, kayıt yok 404; işaret yalnız
// etiket (hipotezler değişmez); defter kaydı; Argo dikişinin saf dönüştürücüsü.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func seedSRERecommendations(t *testing.T, st *sreFakeStore, state string) {
	t.Helper()
	body, err := sre.Encode(sre.Investigation{ProblemID: "p1", Service: "pay", Kind: sre.KindAuto, State: state, Run: 1, EnqueuedAt: 1,
		Hypotheses: []sre.Hypothesis{{Kind: sre.HypDeploy, Label: "Deploy", Category: "deploy", Score: 0.78, Band: sre.BandHigh}},
		Recommendations: []sre.Recommendation{
			{ID: "rollback", Text: "pay rev 42 → rev 41 geri alma değerlendirilebilir", Rationale: "r", HypothesisKind: sre.HypDeploy, State: sre.RecPending,
				Rollout: &sre.RecRollout{Workload: "pay", From: "rev 42", To: "rev 41"}},
			{ID: "rb-rb-1", Text: "Runbook'u gözden geçirin: Pay", Rationale: "service:pay", State: sre.RecPending,
				Runbook: &sre.RecRunbook{ID: "rb-1", Title: "Pay", URL: "/runbook?id=rb-1&problemId=p1"}},
		},
		Steps: []sre.Step{{Name: "sweep", State: sre.StepOK}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSREInvestigation(context.Background(), "p1", state, body, 1); err != nil {
		t.Fatal(err)
	}
}

func TestRecommendationAckRoles(t *testing.T) {
	s, mux, st := newSRETestEnv(t)
	s.registerSRERecommendationRoutes(mux)
	seedSRERecommendations(t, st, sre.StateDone)
	path := "/api/problems/p1/investigation/recommendations/rollback"

	if w := sreDo(mux, "POST", path, `{"state":"ack"}`, auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d", w.Code)
	}
	for _, c := range []struct{ path, body string }{
		{path, `{"state":"applied"}`},
		{path, `{`},
	} {
		if w := sreDo(mux, "POST", c.path, c.body, auth.RoleEditor); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s = %d", c.path, c.body, w.Code)
		}
	}
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation/recommendations/nope", `{"state":"ack"}`, auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("öneri yok = %d", w.Code)
	}
	w := sreDo(mux, "POST", path, `{"state":"ack"}`, auth.RoleEditor)
	if w.Code != 200 {
		t.Fatalf("editor = %d %s", w.Code, w.Body)
	}
	var got sre.Recommendation
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.State != sre.RecAck || got.AckBy != "editor@example.test" || got.AckAt == 0 || got.Rollout == nil {
		t.Fatalf("cevap: %+v", got)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "sre.recommendation.ack" || a.TargetID != "p1" || !strings.Contains(a.Details, `"recommendation":"rollback"`) ||
			!strings.Contains(a.Details, `"state":"ack"`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("işaret audit yazmalı")
	}
	inv, _, _ := sre.Load(context.Background(), st, "p1")
	if inv.Recommendations[0].State != sre.RecAck || inv.Recommendations[1].State != sre.RecPending || inv.Hypotheses[0].Score != 0.78 || inv.State != sre.StateDone {
		t.Fatalf("kayıt: %+v", inv)
	}
	// admin eler (runbook önerisi) — audit runbookId taşır.
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation/recommendations/rb-rb-1", `{"state":"dismissed"}`, auth.RoleAdmin); w.Code != 200 {
		t.Fatalf("admin = %d", w.Code)
	}
	if a := <-s.auditQ; !strings.Contains(a.Details, `"runbookId":"rb-1"`) {
		t.Fatalf("audit runbook: %+v", a)
	}
	// Koşan kayıt 409, kayıt yok 404.
	seedSRERecommendations(t, st, sre.StateRunning)
	if w := sreDo(mux, "POST", path, `{"state":"dismissed"}`, auth.RoleEditor); w.Code != http.StatusConflict {
		t.Fatalf("koşan kayıt = %d", w.Code)
	}
	if w := sreDo(mux, "POST", "/api/problems/zz/investigation/recommendations/rollback", `{"state":"ack"}`, auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("kayıt yok = %d", w.Code)
	}
	if len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek audit yazmamalı")
	}
}

func TestSRERecommendationRouteRegistered(t *testing.T) {
	src, err := os.ReadFile("sre_recommendation.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("sre-recommendation", (*Server).registerSRERecommendationRoutes)`) {
		t.Fatal("öneri ucu deftere kaydolmalı (kayıtsız rota 200 + boş sayfa döner)")
	}
	// Uç yalnız etiket yazar: runbook yürütmesi / Argo / rollout çağrısı yok.
	for _, f := range []string{"sre_recommendation.go", "sre_argo.go"} {
		b, _ := os.ReadFile(f)
		code := stripGoComments(string(b))
		for _, needle := range []string{"StartExecution", "executeRunbook", "ConsoleQuery(", "Rollback", "PostJSON", "http.Post"} {
			if strings.Contains(code, needle) {
				t.Errorf("%s %q içermemeli — ajan / uç hiçbir şey uygulamaz", f, needle)
			}
		}
	}
}

func TestArgoAppStatuses(t *testing.T) {
	a := serviceGitOpsArgo{Configured: true, Apps: []argocd.ServiceApp{
		{AppStatus: argocd.AppStatus{Name: "pay-prod", SyncStatus: "OutOfSync", HealthStatus: "Degraded"},
			Workloads: []argocd.ServiceWorkload{{ClusterID: "c1", Namespace: "pay", Workload: "pay"}}},
		{AppStatus: argocd.AppStatus{Name: "pay-canary", SyncStatus: "Synced", HealthStatus: "Healthy"}, Syncs24h: map[string]int{}},
		{AppStatus: argocd.AppStatus{Name: "pay-dr", SyncStatus: "Synced", HealthStatus: "Healthy"}, Syncs24h: map[string]int{"Succeeded": 2, "Failed": 1}},
	}}
	got := argoAppStatuses(a)
	if len(got) != 3 || got[0].App != "pay-prod" || got[0].Sync != "OutOfSync" || got[0].Health != "Degraded" ||
		len(got[0].Workloads) != 1 || got[0].Workloads[0] != "pay" || got[1].Workloads != nil {
		t.Fatalf("dönüşüm: %+v", got)
	}
	// v0.10.1310 — senkron toplamı: okunmadı (nil) nil kalır, {} → 0, fazlar toplanır.
	if got[0].Syncs24h != nil || got[1].Syncs24h == nil || *got[1].Syncs24h != 0 || got[2].Syncs24h == nil || *got[2].Syncs24h != 3 {
		t.Fatalf("Syncs24h: %v %v %v", got[0].Syncs24h, got[1].Syncs24h, got[2].Syncs24h)
	}
	if out, err := (&Server{}).mcpArgoApps(context.Background(), "pay"); out != nil || err != nil {
		t.Fatalf("Argo ayarı / thanos yokken (nil, nil): %v %v", out, err)
	}
}
