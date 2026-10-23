package sre

// v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü. Puanlama
// SAF ve yalnız onaylı vakalardan; tekrar işi problemin StartedAt'ine
// çıpalanır, canlı kaydı ezmez, LLM çağırmaz; oy puanlamaya girmez.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// ── sahte değerlendirme deposu ──────────────────────────────────────────────

type evalFakeStore struct {
	*fakeStore
	emu      sync.Mutex
	evalRows map[string]chstore.SavedView
	cases    []chstore.IncidentCase
	problems map[string]chstore.Problem
	caseQ    chstore.IncidentCaseQuery
}

func newEvalFakeStore() *evalFakeStore {
	return &evalFakeStore{fakeStore: newFakeStore(), evalRows: map[string]chstore.SavedView{}, problems: map[string]chstore.Problem{}}
}

func (f *evalFakeStore) GetSREEvalRun(_ context.Context, id string) (*chstore.SavedView, error) {
	f.emu.Lock()
	defer f.emu.Unlock()
	v, ok := f.evalRows[chstore.SREEvalID(id)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *evalFakeStore) PutSREEvalRun(_ context.Context, id, state, body string, createdAt int64) error {
	f.emu.Lock()
	defer f.emu.Unlock()
	f.evalRows[chstore.SREEvalID(id)] = chstore.SavedView{ID: chstore.SREEvalID(id), Page: chstore.SREEvalPage, Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}
func (f *evalFakeStore) ListSREEvalRuns(_ context.Context, _ int) ([]chstore.SavedView, error) {
	f.emu.Lock()
	defer f.emu.Unlock()
	out := []chstore.SavedView{}
	for _, v := range f.evalRows {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
func (f *evalFakeStore) ListSREReplayRows(_ context.Context, runID string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []chstore.SavedView{}
	for id, v := range f.rows {
		if strings.HasPrefix(id, chstore.SREInvestigationID(chstore.SREReplayKeyPrefix+runID+"-")) {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *evalFakeStore) ListRecentSREInvestigations(_ context.Context, states []string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]bool{}
	for _, s := range states {
		want[s] = true
	}
	out := []chstore.SavedView{}
	for id, v := range f.rows {
		if want[v.Name] && !strings.HasPrefix(id, "sre-replay-") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (f *evalFakeStore) ListIncidentCases(_ context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error) {
	f.caseQ = q
	var out []chstore.IncidentCase
	for _, c := range f.cases {
		if c.Status == q.Status && c.Source == q.Source && len(out) < q.Limit {
			out = append(out, c)
		}
	}
	return out, nil
}
func (f *evalFakeStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	p, ok := f.problems[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}
func (f *evalFakeStore) EnrichProblemsForRead(_ context.Context, probs []chstore.Problem, _ time.Duration) []chstore.Problem {
	return chstore.EnrichProblemsWithPriority(probs)
}

var evalT0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func confirmedCase(pid, svc, cat, cause string, opened time.Time) chstore.IncidentCase {
	return chstore.IncidentCase{CaseID: chstore.CaseIDFor("problem", pid), Source: "problem", SourceID: pid, Service: svc,
		Category: cat, FinalCause: cause, Status: chstore.CaseConfirmed, OpenedAt: opened}
}

// ── TestHypKindCategoryMap ──────────────────────────────────────────────────

func TestHypKindCategoryMap(t *testing.T) {
	cats := map[string]bool{}
	for _, c := range cosrelearn.CaseCategories {
		cats[c] = true
	}
	m := hypKindCategory()
	if len(m) != len(hypothesisTable) {
		t.Fatalf("her hipotez türü eşlenmeli: %v", m)
	}
	mapped := map[string]bool{}
	for kind, cat := range m {
		if !cats[cat] {
			t.Errorf("%s → %q incident_cases kategorisi değil (cosrelearn.CaseCategories)", kind, cat)
		}
		mapped[cat] = true
	}
	for _, c := range cosrelearn.CaseCategories {
		_, un := evalUnmappedCategories[c]
		switch {
		case mapped[c] && un:
			t.Errorf("%q hem eşlenmiş hem eşlenmemiş", c)
		case !mapped[c] && !un:
			t.Errorf("%q kategorisi ne bir hipotez türüne eşleniyor ne de evalUnmappedCategories'te gerekçeli", c)
		case un && strings.TrimSpace(evalUnmappedCategories[c]) == "":
			t.Errorf("%q eşlenmeme gerekçesi boş", c)
		}
		if evalCategoryMapped(c) != mapped[c] {
			t.Errorf("evalCategoryMapped(%q) tablo ile çelişiyor", c)
		}
	}
	for c := range evalUnmappedCategories {
		if !cats[c] {
			t.Errorf("evalUnmappedCategories %q bilinmeyen kategori", c)
		}
	}
}

// ── TestScoreEval ───────────────────────────────────────────────────────────

func TestScoreEval(t *testing.T) {
	cov := func(d, n, f int) *EvalCoverage { return &EvalCoverage{Data: d, NoData: n, Failed: f} }
	h := func(kind, cat, subj string, sc float64) EvalHyp {
		return EvalHyp{Kind: kind, Category: cat, Subject: subj, Score: sc, Band: BandOf(sc)}
	}
	cases := []EvalCase{
		{CaseID: "c1", State: StateDone, Category: "deploy", FinalCause: "checkout'un rev 42 deploy'u hatayı başlattı",
			Top: []EvalHyp{h(HypDeploy, "deploy", "checkout", 0.8), h(HypCodeRegression, "code_bug", "checkout", 0.5)}, Coverage: cov(3, 0, 0)},
		{CaseID: "c2", State: StatePartial, Category: "dependency", FinalCause: "payments-db yavaşladı",
			Top:      []EvalHyp{h(HypDeploy, "deploy", "checkout", 0.5), h(HypDBSlow, "dependency", "db:payments-db", 0.45), h(HypSaturation, "capacity", "checkout", 0.2)},
			Coverage: cov(2, 1, 0)},
		{CaseID: "c3", State: StateDone, Category: "deploy", Top: []EvalHyp{h(HypDeploy, "deploy", "x", 0.2)}, Coverage: cov(0, 2, 0)}, // veri yok
		{CaseID: "c4", State: CaseUnmapped, Category: "noise"},
		{CaseID: "c5", State: CaseSkipped, Category: "deploy"},
		{CaseID: "c6", State: StateQueued, Category: "deploy"},
		{CaseID: "c7", State: StateError, Category: "capacity", Coverage: cov(0, 0, 3)},
		// dördüncü sıradaki isabet top-3 sayılmaz
		{CaseID: "c8", State: StateDone, Category: "external", FinalCause: "dış banka",
			Top:      []EvalHyp{h(HypDeploy, "deploy", "", 0.3), h(HypConfig, "config", "", 0.2), h(HypSaturation, "capacity", "", 0.1), h(HypExternal, "external", "", 0.05)},
			Coverage: cov(1, 0, 0)},
	}
	live := []LiveSample{{TTFHMs: 4000}, {TTFHMs: 1000}, {TTFHMs: 10000}, {TTFHMs: 2000}, {TTFHMs: 3000}}
	sc := scoreEval(evalInput{Cases: cases, Live: live})
	want := struct{ cases, pending, unmapped, skipped, errored, noData, scored, t1, t3, es, eh int }{8, 1, 1, 1, 1, 1, 3, 1, 2, 2, 1}
	got := struct{ cases, pending, unmapped, skipped, errored, noData, scored, t1, t3, es, eh int }{sc.Cases, sc.Pending, sc.Unmapped, sc.Skipped,
		sc.Errored, sc.NoData, sc.Scored, sc.Top1Hits, sc.Top3Hits, sc.EntityScored, sc.EntityHits}
	if got != want {
		t.Fatalf("sayılar:\n got %+v\nwant %+v", got, want)
	}
	if *sc.Top1Acc != 0.3333 || *sc.Top3Acc != 0.6667 || *sc.EntityAcc != 0.5 {
		t.Fatalf("oranlar: top1=%v top3=%v entity=%v", *sc.Top1Acc, *sc.Top3Acc, *sc.EntityAcc)
	}
	// kapsam: veri (3+2+0+1) / toplam (3 + 3 + 2 + 3 + 1) = 6/12
	if sc.CoveragePct == nil || *sc.CoveragePct != 50 {
		t.Fatalf("kapsam %%: %v", sc.CoveragePct)
	}
	if sc.Live.Runs != 5 || *sc.Live.P50Ms != 3000 || *sc.Live.P90Ms != 10000 {
		t.Fatalf("canlı p50/p90: %+v %d %d", sc.Live, *sc.Live.P50Ms, *sc.Live.P90Ms)
	}
	// boş girdi: oranlar null (0 değil), sıfır sayım.
	empty := scoreEval(evalInput{})
	if empty.Top1Acc != nil || empty.Top3Acc != nil || empty.EntityAcc != nil || empty.CoveragePct != nil || empty.Live.P50Ms != nil {
		t.Fatalf("paydasız oran null olmalı: %+v", empty)
	}
	// deterministik + girdiyi değiştirmez
	before, _ := json.Marshal(cases)
	again := scoreEval(evalInput{Cases: cases, Live: live})
	after, _ := json.Marshal(cases)
	if !reflect.DeepEqual(sc, again) || string(before) != string(after) {
		t.Fatal("scoreEval SAF olmalı")
	}
}

func TestEntityMatch(t *testing.T) {
	for _, tc := range []struct {
		subj, cause string
		want        bool
	}{
		{"payment-service", "payment-service'in bağlantı havuzu doldu", true},
		{"db:payments-db", "Payments-DB indeks eksikti", true},
		{"ext:bank-api", "bank-api kesintisi", true},
		{"pay", "payment-service çöktü", false}, // sözcük sınırı
		{"", "her şey", false},
		{"checkout", "", false},
	} {
		if got := entityMatch(tc.subj, tc.cause); got != tc.want {
			t.Errorf("entityMatch(%q, %q) = %v", tc.subj, tc.cause, got)
		}
	}
}

func TestHypothesisSubject(t *testing.T) {
	ev := Evidence{Service: "checkout", Facts: Facts{Deps: &DepsFacts{TopologyOK: true, ChangesOK: true,
		Edges:  []EdgeFact{{Target: "payments"}, {Target: "db:pg", AvgMs: 80}, {Target: "db:redis", AvgMs: 900}, {Target: "ext:bank", ErrorRate: 30}},
		Causes: []depsCause{{Service: "payments", Relation: chstore.RelationDownstream, Direction: chstore.ChangeWorse}}}}}
	for kind, want := range map[string]string{HypDependency: "payments", HypDBSlow: "db:redis", HypExternal: "ext:bank", HypDeploy: "checkout", HypSaturation: "checkout"} {
		if got := hypothesisSubject(kind, ev); got != want {
			t.Errorf("%s öznesi %q, %q beklenir", kind, got, want)
		}
	}
	if got := hypothesisSubject(HypDependency, Evidence{Service: "checkout"}); got != "" {
		t.Errorf("kanıtsız bağımlılık öznesi boş olmalı: %q", got)
	}
}

func TestCaseCoverage(t *testing.T) {
	c := caseCoverage([]Step{
		{Name: stepSweep, State: StepOK},
		{Name: stepPivot, State: StepOK, NoData: true},
		{Name: stepLogSpike, State: StepPartial, NoData: true},
		{Name: stepDeps, State: StepError},
		{Name: stepNode, State: StepSkipped},
		{Name: stepDeploys, State: StepOK, NoData: true}, // kapsam dışı (gerçek yokluk)
		{Name: stepSummary, State: StepSkipped},
	})
	if c.Data != 1 || c.NoData != 2 || c.Failed != 1 || strings.Join(c.NoDataSteps, ",") != "pivot,log_spike" {
		t.Fatalf("kapsam: %+v", c)
	}
}

// ── TestReplayAnchorsAtStartedAt ────────────────────────────────────────────

func TestReplayAnchorsAtStartedAt(t *testing.T) {
	old := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC) // ~2.5 ay önce
	inv := replayInvestigation(ReplayRequest{RunID: "e1", CaseID: "p:p1", ProblemID: "p1", Service: "checkout", ProblemStartedAt: old.UnixNano(), Now: t0})
	if inv.QueueKey() != "replay-e1-p1" || inv.Kind != KindReplay || inv.State != StateQueued {
		t.Fatalf("tekrar işi: key=%s %+v", inv.QueueKey(), inv)
	}
	wantFrom, wantTo := old.Add(-15*time.Minute), old.Add(replayAsOfDelay)
	if inv.AsOf != wantTo.UnixNano() || inv.Anchor.From != wantFrom.UnixNano() || inv.Anchor.To != wantTo.UnixNano() {
		t.Fatalf("kuyruk çıpası [start−15m, start+15m] olmalı: %+v", inv.Anchor)
	}

	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "anlatım"}
	rd := okReaders(c, n)
	rd.problem = func(context.Context, string) (*chstore.Problem, error) {
		return &chstore.Problem{ID: "p1", Service: "checkout", Metric: "error_rate", RuleName: "Error rate", StartedAt: old.UnixNano()}, nil
	}
	var mu sync.Mutex
	var sweepReq mcptools.SweepRequest
	var deployTo, evTo time.Time
	var spikeReq mcptools.LogSpikeRequest
	rd.sweep = func(_ context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error) {
		mu.Lock()
		sweepReq = req
		mu.Unlock()
		return mcptools.SweepResult{OK: true, Empty: true, Message: "veri yok: checkout için pencerede hiçbir bölümde veri bulunamadı"}, nil
	}
	rd.deploys = func(_ context.Context, _ string, _, to time.Time) (mcptools.DeployCheck, error) {
		mu.Lock()
		deployTo = to
		mu.Unlock()
		return mcptools.DeployCheck{Section: mcptools.SweepSection{Name: "deploys", State: "empty"}}, nil
	}
	rd.events = func(_ context.Context, _ string, _, to time.Time, _ int) (mcptools.ProblemWindowData, error) {
		mu.Lock()
		evTo = to
		mu.Unlock()
		return mcptools.ProblemWindowData{}, nil
	}
	rd.spike = func(_ context.Context, req mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
		mu.Lock()
		spikeReq = req
		mu.Unlock()
		return mcptools.LogSpikeResult{OK: true, Empty: true, Message: "veri yok: log yok"}, nil
	}
	recCalled := false
	rd.runbooks = func(context.Context) ([]chstore.Runbook, error) { recCalled = true; return nil, nil }

	runInvestigation(context.Background(), rd, &inv, func(*Investigation) {}, func() time.Time { return t0 })

	if inv.Anchor.From != wantFrom.UnixNano() || inv.Anchor.To != wantTo.UnixNano() {
		t.Fatalf("koşu çıpası StartedAt'e göre olmalı (duvar saati değil): %v – %v", time.Unix(0, inv.Anchor.From).UTC(), time.Unix(0, inv.Anchor.To).UTC())
	}
	if !sweepReq.From.Equal(wantFrom) || !sweepReq.To.Equal(wantTo) || !sweepReq.Anchor.Equal(old) {
		t.Fatalf("tarama penceresi: %v – %v anchor %v", sweepReq.From, sweepReq.To, sweepReq.Anchor)
	}
	if !deployTo.Equal(wantTo) || !evTo.Equal(wantTo) || !spikeReq.SpikeTo.Equal(wantTo) {
		t.Fatalf("plan 'şimdi'si AsOf olmalı: deploy %v olay %v log %v", deployTo, evTo, spikeReq.SpikeTo)
	}
	// LLM yok, bütçe düşmez, öneri yok, ilk-hipotez anı yok (canlı ölçü değil).
	if n.calls != 0 || rd.budget.used(context.Background()) != 0 {
		t.Fatalf("tekrar LLM çağırmamalı: calls=%d used=%d", n.calls, rd.budget.used(context.Background()))
	}
	if s := inv.step(stepSummary); s == nil || s.State != StepSkipped || inv.Summary == nil || !inv.Summary.Gated || inv.Summary.Text != ReplayNarrationText {
		t.Fatalf("anlatım atlanmalı: %+v %+v", s, inv.Summary)
	}
	if recCalled || len(inv.Recommendations) != 0 || inv.FirstHypothesisAt != 0 {
		t.Fatalf("tekrarda öneri / ilk-hipotez anı olmaz: rec=%v %+v first=%d", recCalled, inv.Recommendations, inv.FirstHypothesisAt)
	}
	if len(inv.Hypotheses) != len(hypothesisTable) {
		t.Fatalf("hipotezler skorlanmalı: %d", len(inv.Hypotheses))
	}
	// "veri yok" adımları kapsamda sayılır.
	if sw := inv.step(stepSweep); sw == nil || !sw.NoData || !strings.HasPrefix(sw.Summary, "veri yok") {
		t.Fatalf("aged-out tarama 'veri yok' olmalı: %+v", sw)
	}
	if cov := caseCoverage(inv.Steps); cov.NoData < 2 {
		t.Fatalf("kapsam veri yok adımlarını saymalı: %+v", cov)
	}

	// Canlı koşu ise ilk-hipotez anı yazılır ve yeniden koşuda taşınır.
	live := newInv()
	runInvestigation(context.Background(), okReaders(&calls{}, &fakeNarrator{}), live, func(*Investigation) {}, func() time.Time { return t0 })
	if live.FirstHypothesisAt != t0.UnixNano() {
		t.Fatalf("canlı koşu FirstHypothesisAt yazmalı: %d", live.FirstHypothesisAt)
	}
}

// ── TestReplayDoesNotOverwriteLive ──────────────────────────────────────────

func TestReplayDoesNotOverwriteLive(t *testing.T) {
	SetSettings(DefaultSettings())
	defer SetSettings(DefaultSettings())
	st := newEvalFakeStore()
	startedAt := evalT0.Add(-40 * 24 * time.Hour)
	// Canlı kayıt: bitmiş, operatör işaretli hipotez.
	live := Investigation{ProblemID: "p1", Service: "checkout", Kind: KindAuto, State: StateDone, Run: 1, EnqueuedAt: startedAt.UnixNano(),
		ProblemAt: startedAt.UnixNano(), Hypotheses: []Hypothesis{{Kind: HypDeploy, Category: "deploy", Score: 0.8,
			HumanState: &HumanState{State: MarkConfirmed, By: "op"}}}}
	if err := save(context.Background(), st, live); err != nil {
		t.Fatal(err)
	}
	liveRow, _ := st.GetSREInvestigation(context.Background(), "p1")
	st.problems["p1"] = chstore.Problem{ID: "p1", Service: "checkout", StartedAt: startedAt.UnixNano(), Status: "resolved", Severity: "critical", RuleID: "r"}
	st.cases = []chstore.IncidentCase{confirmedCase("p1", "checkout", "dependency", "payments yavaşladı", startedAt)}

	run, err := StartEvalRun(context.Background(), st, EvalRunRequest{From: startedAt.Add(-time.Hour), To: evalT0, Limit: 10, By: "admin@x", Now: evalT0}, CurrentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if run.State != EvalRunning || len(run.Cases) != 1 || run.Cases[0].State != StateQueued {
		t.Fatalf("koşu: %+v", run)
	}
	runner := func(_ context.Context, inv *Investigation, cp func(*Investigation)) {
		if inv.Kind != KindReplay || inv.ProblemAt != startedAt.UnixNano() {
			t.Errorf("drain tekrar işini almalı: %+v", inv)
		}
		inv.setStep(Step{Name: stepSweep, State: StepOK, At: time.Now().UnixNano()})
		inv.setStep(Step{Name: stepDeps, State: StepOK, At: time.Now().UnixNano()})
		inv.Facts = &Facts{Deps: &DepsFacts{TopologyOK: true, ChangesOK: true, Edges: []EdgeFact{{Target: "payments"}},
			Causes: []depsCause{{Service: "payments", Relation: chstore.RelationDownstream, Direction: chstore.ChangeWorse}}}}
		inv.Hypotheses = []Hypothesis{{Kind: HypDependency, Category: "dependency", Score: 0.75, Band: BandHigh}, {Kind: HypDeploy, Category: "deploy", Score: 0.3}}
		cp(inv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := StartDrain(ctx, DrainDeps{Store: st, Eval: st, Lock: newFakeLock(), PodID: "sre-1", Poll: time.Millisecond, Runner: runner})
	deadline := time.After(3 * time.Second)
	for {
		v, _ := st.GetSREEvalRun(context.Background(), run.RunID)
		if v != nil && v.Name == EvalDone {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("koşu bitmedi: %+v", v)
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	after, _ := st.GetSREInvestigation(context.Background(), "p1")
	if after == nil || after.QueryString != liveRow.QueryString || after.Name != liveRow.Name {
		t.Fatalf("canlı kayıt DEĞİŞMEMELİ:\nönce %s\nsonra %+v", liveRow.QueryString, after)
	}
	rep := st.get(t, chstore.SREReplayKey(run.RunID, "p1"))
	if rep.State != StateDone || rep.Kind != KindReplay || rep.EvalRun != run.RunID {
		t.Fatalf("tekrar kendi satırında bitmeli: %+v", rep)
	}
	got, ok, err := LoadEvalRun(context.Background(), st, run.RunID, evalT0)
	if err != nil || !ok {
		t.Fatal(err)
	}
	c := got.Cases[0]
	if c.State != StateDone || len(c.Top) != 2 || c.Top[0].Kind != HypDependency || c.Top[0].Subject != "payments" || c.Coverage == nil || c.Coverage.Data != 2 {
		t.Fatalf("vaka özeti koşu satırına birleşmeli: %+v", c)
	}
	sc := ScoreEvalRun(got, nil)
	if sc.Scored != 1 || sc.Top1Hits != 1 || sc.EntityHits != 1 {
		t.Fatalf("puan: %+v", sc)
	}
	// Canlı kaydın problem ucu tekrar satırını görmez; kuyruk sırası tekrarı en sona koyar.
	if inv, ok, _ := Load(context.Background(), st, "p1"); !ok || inv.Kind != KindAuto {
		t.Fatalf("Load(problemId) canlı kaydı döndürmeli: %+v", inv)
	}
}

func TestOrderQueuedReplayLast(t *testing.T) {
	q := OrderQueued([]Investigation{
		{ProblemID: "r", Kind: KindReplay, EvalRun: "e1", State: StateQueued, Priority: "P1", EnqueuedAt: 1},
		{ProblemID: "a", Kind: KindAuto, State: StateQueued, Priority: "P3", EnqueuedAt: 5},
		{ProblemID: "m", Kind: KindManual, State: StateQueued, EnqueuedAt: 9},
	})
	if q[0].ProblemID != "m" || q[1].ProblemID != "a" || q[2].ProblemID != "r" {
		t.Fatalf("tekrar canlı işlerin hepsinden sonra: %v %v %v", q[0].ProblemID, q[1].ProblemID, q[2].ProblemID)
	}
	// Tekrar satırı canlı kimlikle çözülmez (ve tersi).
	rep := Investigation{ProblemID: "p1", Kind: KindReplay, EvalRun: "e1", State: StateQueued}
	body, _ := Encode(rep)
	if _, ok := Decode(chstore.SavedView{ID: chstore.SREInvestigationID("p1"), Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: body}); ok {
		t.Fatal("tekrar gövdesi canlı satır kimliğinde kabul edilmemeli")
	}
	if _, ok := Decode(chstore.SavedView{ID: chstore.SREInvestigationID("replay-e1-p1"), Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: body}); !ok {
		t.Fatal("tekrar gövdesi kendi anahtarında çözülmeli")
	}
}

func TestStartEvalRunBounds(t *testing.T) {
	SetSettings(DefaultSettings())
	defer SetSettings(DefaultSettings())
	st := newEvalFakeStore()
	at := evalT0.Add(-10 * 24 * time.Hour)
	st.cases = []chstore.IncidentCase{
		confirmedCase("p1", "a", "deploy", "x", at),
		confirmedCase("p2", "b", "noise", "x", at),         // eşlenmez
		confirmedCase("p3", "pg:main", "capacity", "", at), // servis problemi değil
		confirmedCase("p4", "d", "config", "", at),         // problem satırı yok → vaka açılışı
		{CaseID: "p:p5", Source: "problem", SourceID: "p5", Category: "deploy", Status: chstore.CaseDraft, OpenedAt: at}, // taslak: okunmaz
	}
	st.problems["p1"] = chstore.Problem{ID: "p1", Service: "a", StartedAt: at.Add(time.Minute).UnixNano(), RuleID: "r"}
	st.problems["p3"] = chstore.Problem{ID: "p3", Service: "pg:main", Kind: "db", RuleID: "db-health:x"}
	if _, err := StartEvalRun(context.Background(), st, EvalRunRequest{From: evalT0, To: evalT0.Add(-time.Hour), Now: evalT0}, CurrentSettings()); err == nil {
		t.Fatal("ters pencere reddedilmeli")
	}
	run, err := StartEvalRun(context.Background(), st, EvalRunRequest{From: at.Add(-time.Hour), To: evalT0.Add(time.Hour), Limit: 500, Now: evalT0}, CurrentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if st.caseQ.Status != chstore.CaseConfirmed || st.caseQ.Source != "problem" || st.caseQ.Limit != EvalMaxCases || st.caseQ.To.After(evalT0) {
		t.Fatalf("vaka okuması onaylı + sınırlı olmalı: %+v", st.caseQ)
	}
	states := map[string]string{}
	for _, c := range run.Cases {
		states[c.ProblemID] = c.State
	}
	if states["p1"] != StateQueued || states["p2"] != CaseUnmapped || states["p3"] != CaseSkipped || states["p4"] != StateQueued || len(states) != 4 {
		t.Fatalf("vaka durumları: %v", states)
	}
	rep := st.get(t, chstore.SREReplayKey(run.RunID, "p1"))
	if rep.ProblemAt != at.Add(time.Minute).UnixNano() {
		t.Fatal("çıpa problemin StartedAt'i olmalı")
	}
	// İkinci koşu: ilki bitmeden 409.
	if _, err := StartEvalRun(context.Background(), st, EvalRunRequest{From: at.Add(-time.Hour), To: evalT0, Now: evalT0.Add(time.Minute)}, CurrentSettings()); err != ErrEvalBusy {
		t.Fatalf("koşu sürerken yeni koşu: %v", err)
	}
	// Kuyruk payı: MaxQueue/2'yi canlı iş doldurduysa tekrar kuyruğa girmez.
	if got := evalQueueRoom(Settings{MaxQueue: 200}, 120); got != 0 {
		t.Fatalf("pay: %d", got)
	}
	if got := evalQueueRoom(Settings{MaxQueue: 200}, 30); got != 70 {
		t.Fatalf("pay: %d", got)
	}
	// Ajan kapalı → ErrDisabled.
	off := false
	cfg := DefaultSettings()
	cfg.Enabled = &off
	if _, err := StartEvalRun(context.Background(), newEvalFakeStore(), EvalRunRequest{From: at, To: evalT0, Now: evalT0}, cfg); err != ErrDisabled {
		t.Fatalf("kapalı: %v", err)
	}
}

func TestReconcileEvalRun(t *testing.T) {
	run := EvalRun{RunID: "e1", State: EvalRunning, CreatedAt: evalT0.UnixNano(), Cases: []EvalCase{
		{ProblemID: "p1", State: StateQueued, Category: "deploy"},
		{ProblemID: "p2", State: StateQueued, Category: "deploy"},
	}}
	rep := Investigation{ProblemID: "p1", Kind: KindReplay, EvalRun: "e1", State: StateDone, Steps: []Step{{Name: stepSweep, State: StepOK}},
		Hypotheses: []Hypothesis{{Kind: HypDeploy, Category: "deploy", Score: 0.7}}}
	body, _ := Encode(rep)
	rows := []chstore.SavedView{{ID: chstore.SREInvestigationID(rep.QueueKey()), Page: chstore.SREInvestigationPage, Name: StateDone, QueryString: body}}
	got := reconcileEvalRun(run, rows, evalT0.Add(time.Minute))
	if got.Cases[0].State != StateDone || got.Cases[1].State != StateQueued || got.State != EvalRunning {
		t.Fatalf("uzlaştırma: %+v", got)
	}
	if run.Cases[0].State != StateQueued {
		t.Fatal("uzlaştırma girdiyi değiştirmemeli")
	}
	stale := reconcileEvalRun(run, rows, evalT0.Add(evalStaleAfter+time.Minute))
	if stale.Cases[1].State != StateError || stale.State != EvalDone {
		t.Fatalf("satırı olmayan bayat vaka error, koşu done: %+v", stale)
	}
}

func TestLiveSamples(t *testing.T) {
	p := evalT0.UnixNano()
	s := liveSamples([]Investigation{
		{ProblemID: "a", Kind: KindAuto, State: StateDone, ProblemAt: p, FirstHypothesisAt: p + int64(90*time.Second), Hypotheses: []Hypothesis{{}}},
		{ProblemID: "b", Kind: KindAuto, State: StateDone, Run: 1, ProblemAt: p, FinishedAt: p + int64(2*time.Minute), Hypotheses: []Hypothesis{{}}}, // eski kayıt
		{ProblemID: "c", Kind: KindManual, State: StateDone, Run: 2, ProblemAt: p, FinishedAt: p + int64(time.Hour), Hypotheses: []Hypothesis{{}}},   // yeniden koşu bitişi ilk varış değil
		{ProblemID: "d", Kind: KindReplay, State: StateDone, ProblemAt: p, FirstHypothesisAt: p + 1, Hypotheses: []Hypothesis{{}}},
		{ProblemID: "e", Kind: KindAuto, State: StateShared, ProblemAt: p, FinishedAt: p + 1},
	})
	if len(s) != 2 || s[0].TTFHMs != 90000 || s[1].TTFHMs != 120000 {
		t.Fatalf("örneklem: %+v", s)
	}
}

// ── TestEvalIgnoresFeedback ─────────────────────────────────────────────────

// TestEvalIgnoresFeedback — operatör: "oylara çok güvenmeyelim". Puanlamanın
// tek girdisi evalInput: içinde (iç içe tipler dahil) oy / geri bildirim alanı
// yok; eval.go hiçbir geri bildirim okuyucusunu anmıyor. 👎 listesi yalnız
// api/sre_eval.go'nun inceleme ucunda (cosrelearn ratchet'i oraya izin verir).
func TestEvalIgnoresFeedback(t *testing.T) {
	// v0.10.1450: "human" — hipotezin insan işaretleri (human_*) de puanlamaya /
	// karşılaştırmaya girmez; karşılaştırma gövdesi (EvalCompare) de taranır.
	banned := []string{"feedback", "vote", "verdict", "thumb", "downvote", "review", "human"}
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			low := strings.ToLower(f.Name + " " + f.Tag.Get("json"))
			for _, b := range banned {
				if strings.Contains(low, b) {
					t.Errorf("%s.%s — puanlama girdisi oy / geri bildirim taşımamalı", path, f.Name)
				}
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	walk(reflect.TypeOf(evalInput{}), "evalInput")
	walk(reflect.TypeOf(EvalCompare{}), "EvalCompare")
	// Yeni bayrak + bağımsız ölçüler evalInput'un içinde (yürüyüş onları da kapsar).
	if _, ok := reflect.TypeOf(EvalCase{}).FieldByName("AcceptedSuggestion"); !ok {
		t.Fatal("EvalCase.AcceptedSuggestion beklenir (öneri kabulü = etiket kaynağı, oy değil)")
	}

	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "eval.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"eval_compare.go"} {
		cf, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(cf, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				low := strings.ToLower(id.Name)
				for _, b := range []string{"feedback", "downvote", "vote", "thumb", "human"} {
					if strings.Contains(low, b) {
						t.Errorf("%s: %s — karşılaştırma oy / insan işareti okumaz", fset.Position(id.Pos()), id.Name)
					}
				}
			}
			return true
		})
	}
	ast.Inspect(af, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		low := strings.ToLower(id.Name)
		for _, b := range []string{"feedback", "downvote", "vote", "thumb"} {
			if strings.Contains(low, b) {
				t.Errorf("%s: %s — değerlendirme oy okumaz", fset.Position(id.Pos()), id.Name)
			}
		}
		return true
	})
	// scoreEval imzası: tek parametre evalInput.
	var sig *ast.FuncType
	for _, d := range af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "scoreEval" {
			sig = fd.Type
		}
	}
	if sig == nil || len(sig.Params.List) != 1 || types0(sig.Params.List[0].Type) != "evalInput" {
		t.Fatal("scoreEval(in evalInput) imzası değişmemeli (yeni girdi = bu testte gerekçe)")
	}
}

func types0(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func TestEncodeEvalRunCap(t *testing.T) {
	run := EvalRun{RunID: "e1", State: EvalDone}
	long := strings.Repeat("ç", 3000)
	for i := 0; i < EvalMaxCases; i++ {
		run.Cases = append(run.Cases, EvalCase{ProblemID: "p", FinalCause: truncRunes(long, evalCauseRunes), Reason: long,
			Top:      []EvalHyp{{Kind: HypDeploy, Category: "deploy", Subject: "svc"}, {Kind: HypConfig}, {Kind: HypDBSlow}},
			Coverage: &EvalCoverage{Data: 1, NoDataSteps: []string{"sweep", "pivot"}}})
	}
	body, err := encodeEvalRun(run)
	if err != nil || len(body) > chstore.SREEvalMaxBytes {
		t.Fatalf("koşu satırı tavana sığmalı: %v %d", err, len(body))
	}
	v := chstore.SavedView{ID: chstore.SREEvalID("e1"), Page: chstore.SREEvalPage, Name: EvalDone, QueryString: body}
	if got, ok := decodeEvalRun(v); !ok || len(got.Cases) != EvalMaxCases {
		t.Fatal("kodlanan koşu geri çözülmeli")
	}
	v.ID = chstore.SREEvalID("e2")
	if _, ok := decodeEvalRun(v); ok {
		t.Fatal("kimliği gövdeyle eşleşmeyen satır reddedilmeli")
	}
}
