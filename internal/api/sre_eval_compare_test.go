package api

// v0.10.1450 — program 2a: yeniden koşu POST'u (rerunOf, build, audit) ve
// karşılaştırma ucu (viewer; 400 / 404 / 409; önbellek anahtarı iki kimlik).

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

func putEvalRun(t *testing.T, ef *sreEvalFake, run sre.EvalRun) {
	t.Helper()
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	ef.evalRows[chstore.SREEvalID(run.RunID)] = chstore.SavedView{ID: chstore.SREEvalID(run.RunID), Page: chstore.SREEvalPage,
		Name: run.State, QueryString: string(raw), CreatedAt: run.CreatedAt}
}

func TestEvalRerunAndCompare(t *testing.T) {
	s, _, st := newSRETestEnv(t)
	s.buildVersion = "v0.10.1450"
	ef := &sreEvalFake{sreFakeStore: st, evalRows: map[string]chstore.SavedView{}}
	prevE := sreEvalStoreOf
	sreEvalStoreOf = func(*Server) sre.EvalStore { return ef }
	t.Cleanup(func() { sreEvalStoreOf = prevE })
	mux := http.NewServeMux()
	s.registerSREEvalRoutes(mux)
	s.registerSREEvalCompareRoutes(mux)

	start := time.Now().Add(-48 * time.Hour)
	created := time.Now().Add(-24 * time.Hour).UnixNano()
	base := sre.EvalRun{RunID: "ebase", State: sre.EvalDone, From: start.Add(-time.Hour).UnixNano(), To: start.Add(time.Hour).UnixNano(),
		Limit: 20, Found: 1, CreatedAt: created, FinishedAt: created + int64(time.Minute), SuggestionKnown: true,
		Cases: []sre.EvalCase{{CaseID: "p:p9", ProblemID: "p9", Service: "orders-api", Category: "deploy", FinalCause: "orders-api deploy",
			StartedAt: start.UnixNano(), State: sre.StateDone, AcceptedSuggestion: true,
			Top: []sre.EvalHyp{{Kind: sre.HypDeploy, Category: "deploy", Score: 0.8}}, Coverage: &sre.EvalCoverage{Data: 3}}}}
	putEvalRun(t, ef, base)
	other := sre.EvalRun{RunID: "eother", State: sre.EvalDone, CreatedAt: created - 1, Cases: []sre.EvalCase{{CaseID: "p:x", ProblemID: "x"}}}
	putEvalRun(t, ef, other)

	for body, want := range map[string]int{
		`{"rerunOf":"ebase","from":1,"to":2}`: 400, // pencere + rerunOf
		`{"rerunOf":"ebase","limit":5}`:       400,
		`{"rerunOf":"e-x"}`:                   400,
		`{"rerunOf":"ebase","loop":true}`:     400, // döngü 2c
		`{"rerunOf":"enope"}`:                 404,
	} {
		if w := sreDo(mux, "POST", "/api/sre/eval/runs", body, auth.RoleAdmin); w.Code != want {
			t.Errorf("POST %s = %d %s, %d beklenir", body, w.Code, w.Body, want)
		}
	}
	if len(s.auditQ) != 0 {
		t.Fatal("reddedilen POST audit yazmamalı")
	}
	if w := sreDo(mux, "POST", "/api/sre/eval/runs", `{"rerunOf":"ebase"}`, auth.RoleEditor); w.Code != http.StatusForbidden {
		t.Fatalf("editor yeniden koşu = %d", w.Code)
	}
	w := sreDo(mux, "POST", "/api/sre/eval/runs", `{"rerunOf":"ebase"}`, auth.RoleAdmin)
	if w.Code != 200 {
		t.Fatalf("admin yeniden koşu = %d %s", w.Code, w.Body)
	}
	var item sreEvalRunItem
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	if item.RerunOf != "ebase" || item.Build != "v0.10.1450" || len(item.Cases) != 1 || item.Cases[0].State != sre.StateQueued ||
		!item.Cases[0].AcceptedSuggestion || item.Cases[0].StartedAt != start.UnixNano() {
		t.Fatalf("yeniden koşu: %s", w.Body)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "sre.eval.run" || !strings.Contains(a.Details, `"rerunOf":"ebase"`) || !strings.Contains(a.Details, `"build":"v0.10.1450"`) ||
			!strings.Contains(a.Details, `"loop":false`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("yeniden koşu audit yazmalı")
	}
	if _, ok := st.rows[chstore.SREInvestigationID(chstore.SREReplayKey(item.RunID, "p9"))]; !ok {
		t.Fatal("yeniden koşunun tekrar işi kendi anahtarında")
	}

	// Karşılaştırma — viewer görür.
	w = sreDo(mux, "GET", "/api/sre/eval/compare?base=ebase&cand="+item.RunID, "", auth.RoleViewer)
	if w.Code != 200 {
		t.Fatalf("compare = %d %s", w.Code, w.Body)
	}
	var cmp sre.EvalCompare
	_ = json.Unmarshal(w.Body.Bytes(), &cmp)
	if cmp.Headline != "independent" || cmp.Base.RunID != "ebase" || cmp.Cand.RerunOf != "ebase" || len(cmp.Pairs) != 1 ||
		cmp.Independent.Cases != 0 || cmp.Control.Cases != 1 || cmp.Note == "" || cmp.Cost.Cand.LLMCalls != 0 {
		t.Fatalf("compare gövdesi: %s", w.Body)
	}
	for q, want := range map[string]int{
		"base=ebase&cand=enope":  404,
		"base=enope&cand=ebase":  404,
		"base=ebase&cand=eother": 409,
		"base=ebase":             400,
		"base=e-b&cand=ebase":    400,
	} {
		if w := sreDo(mux, "GET", "/api/sre/eval/compare?"+q, "", auth.RoleViewer); w.Code != want {
			t.Errorf("compare?%s = %d %s, %d beklenir", q, w.Code, w.Body, want)
		}
	}
	if sreEvalCompareKey("a", "b") == sreEvalCompareKey("b", "a") || sreEvalCompareKey("a", "b") == sreEvalRunKey("a") {
		t.Fatal("karşılaştırma anahtarı iki kimliği sıralı taşımalı")
	}
}

func TestSREEvalCompareRouteRegistered(t *testing.T) {
	src, err := os.ReadFile("sre_eval_compare.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("sre-eval-compare", (*Server).registerSREEvalCompareRoutes)`) {
		t.Fatal("sre_eval_compare.go defterden kaydolmalı (kayıtsız uç 200 + boş sayfa döner)")
	}
}
