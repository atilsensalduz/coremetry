package api

// v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü. Uçların rol
// kapıları (okuma çıplak, POST + 👎 inceleme admin), audit, anahtar basamakları.

import (
	"context"
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

type sreEvalFake struct {
	*sreFakeStore
	evalRows map[string]chstore.SavedView
	cases    []chstore.IncidentCase
	reviewed int
}

func (f *sreEvalFake) GetSREEvalRun(_ context.Context, id string) (*chstore.SavedView, error) {
	v, ok := f.evalRows[chstore.SREEvalID(id)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *sreEvalFake) PutSREEvalRun(_ context.Context, id, state, body string, createdAt int64) error {
	f.evalRows[chstore.SREEvalID(id)] = chstore.SavedView{ID: chstore.SREEvalID(id), Page: chstore.SREEvalPage, Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}
func (f *sreEvalFake) ListSREEvalRuns(context.Context, int) ([]chstore.SavedView, error) {
	out := []chstore.SavedView{}
	for _, v := range f.evalRows {
		out = append(out, v)
	}
	return out, nil
}
func (f *sreEvalFake) ListSREReplayRows(_ context.Context, runID string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []chstore.SavedView{}
	for id, v := range f.rows {
		if strings.HasPrefix(id, "sre-replay-"+runID+"-") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *sreEvalFake) ListRecentSREInvestigations(context.Context, []string, int) ([]chstore.SavedView, error) {
	return []chstore.SavedView{}, nil
}
func (f *sreEvalFake) ListIncidentCases(_ context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error) {
	if q.Status != chstore.CaseConfirmed {
		return nil, nil
	}
	return f.cases, nil
}
func (f *sreEvalFake) ListSREReviewDownvotes(context.Context, time.Time, time.Time, int) ([]chstore.SREReviewDownvote, error) {
	f.reviewed++
	return []chstore.SREReviewDownvote{{ExchangeID: "x1", Surface: "sre-investigation", Reason: "wrong"}}, nil
}

func TestEvalRoles(t *testing.T) {
	s, _, st := newSRETestEnv(t)
	ef := &sreEvalFake{sreFakeStore: st, evalRows: map[string]chstore.SavedView{}}
	start := time.Now().Add(-48 * time.Hour)
	st.problems["p9"] = chstore.Problem{ID: "p9", Service: "pay", Status: "resolved", RuleID: "r", StartedAt: start.UnixNano()}
	ef.cases = []chstore.IncidentCase{{CaseID: "p:p9", Source: "problem", SourceID: "p9", Service: "pay", Category: "deploy",
		FinalCause: "pay deploy", Status: chstore.CaseConfirmed, OpenedAt: start}}
	prevE, prevR := sreEvalStoreOf, sreEvalReviewStoreOf
	sreEvalStoreOf = func(*Server) sre.EvalStore { return ef }
	sreEvalReviewStoreOf = func(*Server) sreEvalReviewStore { return ef }
	t.Cleanup(func() { sreEvalStoreOf, sreEvalReviewStoreOf = prevE, prevR })
	mux := http.NewServeMux()
	s.registerSREEvalRoutes(mux)

	if w := sreDo(mux, "GET", "/api/sre/eval/runs", "", auth.RoleViewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"runs":[]`) {
		t.Fatalf("viewer GET liste = %d %s", w.Code, w.Body)
	}
	body := `{"from":` + jsonInt(start.Add(-time.Hour).UnixMilli()) + `,"to":` + jsonInt(time.Now().UnixMilli()) + `,"limit":10}`
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := sreDo(mux, "POST", "/api/sre/eval/runs", body, role); w.Code != http.StatusForbidden {
			t.Fatalf("%s POST = %d, 403 beklenir", role, w.Code)
		}
		if w := sreDo(mux, "GET", "/api/sre/eval/review", "", role); w.Code != http.StatusForbidden {
			t.Fatalf("%s 👎 inceleme = %d, 403 beklenir (prompt + e-posta taşır)", role, w.Code)
		}
	}
	if len(ef.evalRows) != 0 || len(st.rows) != 0 {
		t.Fatal("reddedilen POST yazmamalı")
	}
	if w := sreDo(mux, "POST", "/api/sre/eval/runs", `{"from":1,"to":2,"limit":51}`, auth.RoleAdmin); w.Code != 400 {
		t.Fatalf("limit > 50 = %d", w.Code)
	}
	w := sreDo(mux, "POST", "/api/sre/eval/runs", body, auth.RoleAdmin)
	if w.Code != 200 {
		t.Fatalf("admin POST = %d %s", w.Code, w.Body)
	}
	var item sreEvalRunItem
	_ = json.Unmarshal(w.Body.Bytes(), &item)
	if item.RunID == "" || len(item.Cases) != 1 || item.Cases[0].State != sre.StateQueued {
		t.Fatalf("koşu: %s", w.Body)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "sre.eval.run" || a.TargetID != item.RunID || !strings.Contains(a.Details, `"queued":1`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("POST audit yazmalı")
	}
	// Tekrar işi kendi satırında; canlı kayıt yok.
	if _, ok := st.rows[chstore.SREInvestigationID("p9")]; ok {
		t.Fatal("değerlendirme canlı kayıt açmamalı")
	}
	if _, ok := st.rows[chstore.SREInvestigationID(chstore.SREReplayKey(item.RunID, "p9"))]; !ok {
		t.Fatal("tekrar işi kuyruğa girmeli")
	}
	// Canlı problem ucu tekrarı görmez.
	var inv problemInvestigationResponse
	_ = json.Unmarshal(sreDo(mux2(s), "GET", "/api/problems/p9/investigation", "", auth.RoleViewer).Body.Bytes(), &inv)
	if inv.State != "none" {
		t.Fatalf("canlı kayıt ucu tekrar satırını göstermemeli: %+v", inv)
	}
	if w := sreDo(mux, "GET", "/api/sre/eval/runs/"+item.RunID, "", auth.RoleViewer); w.Code != 200 || !strings.Contains(w.Body.String(), `"cases":[`) {
		t.Fatalf("viewer GET koşu = %d %s", w.Code, w.Body)
	}
	if w := sreDo(mux, "GET", "/api/sre/eval/runs/nope", "", auth.RoleViewer); w.Code != 404 {
		t.Fatalf("olmayan koşu = %d", w.Code)
	}
	if w := sreDo(mux, "GET", "/api/sre/eval/runs/a-b", "", auth.RoleViewer); w.Code != 400 {
		t.Fatalf("tireli kimlik = %d", w.Code)
	}
	// İkinci koşu: sürüyor → 409, audit yok.
	if w := sreDo(mux, "POST", "/api/sre/eval/runs", body, auth.RoleAdmin); w.Code != http.StatusConflict {
		t.Fatalf("koşu sürerken POST = %d", w.Code)
	}
	if len(s.auditQ) != 0 {
		t.Fatal("reddedilen POST audit yazmamalı")
	}
	w = sreDo(mux, "GET", "/api/sre/eval/review?days=3", "", auth.RoleAdmin)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "incelemeye değer") || !strings.Contains(w.Body.String(), `"days":7`) || ef.reviewed != 1 {
		t.Fatalf("admin 👎 inceleme = %d %s", w.Code, w.Body)
	}
}

func mux2(s *Server) *http.ServeMux {
	m := http.NewServeMux()
	s.registerSRERoutes(m)
	return m
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestSREEvalReviewDays(t *testing.T) {
	for in, want := range map[string]int{"": 1, "0": 1, "1": 1, "2": 7, "7": 7, "8": 30, "31": 90, "9999": 90, "x": 1} {
		if got := sreEvalReviewDays(in); got != want {
			t.Errorf("sreEvalReviewDays(%q) = %d, %d beklenir", in, got, want)
		}
	}
	if sreEvalReviewKey(7) == sreEvalReviewKey(30) || sreEvalRunKey("a") == sreEvalRunKey("b") {
		t.Fatal("anahtarlar girdiyi taşımalı")
	}
}

func TestSREEvalRoutesRegistered(t *testing.T) {
	src, err := os.ReadFile("sre_eval.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("sre-eval", (*Server).registerSREEvalRoutes)`) {
		t.Fatal("sre_eval.go defterden kaydolmalı (kayıtsız uç 200 + boş sayfa döner)")
	}
}
