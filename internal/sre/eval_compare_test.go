package sre

// v0.10.1450 — program 2a: dondurulmuş küme + yeniden koşu + karşılaştırma.
// Yeniden koşu aynı vakaları aynı çıpalarla kuyruğa alır; karşılaştırma SAF,
// manşet "bağımsız" vakalar (operatör kararı 2); tekrar LLM çağırmaz.

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func evalHypS(kind, cat string, score float64) EvalHyp {
	return EvalHyp{Kind: kind, Category: cat, Score: score, Subject: "orders-api"}
}

func covD(d, n, f int) *EvalCoverage { return &EvalCoverage{Data: d, NoData: n, Failed: f} }

func TestEvalDetOutcomeTable(t *testing.T) {
	for _, c := range []struct {
		name   string
		c      EvalCase
		want   string
		reason string
	}{
		{"bitmemiş", EvalCase{State: StateQueued}, "", ""},
		{"eşlenmeyen", EvalCase{State: CaseUnmapped}, "", ""},
		{"hata", EvalCase{State: StateError}, detError, detReasonNoSteps},
		{"kapsam yok", EvalCase{State: StateDone, Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.9)}}, detNoData, ""},
		{"kapsam paydası 0", EvalCase{State: StateDone, Coverage: covD(0, 0, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.9)}}, detNoData, ""},
		{"hipotez yok", EvalCase{State: StateDone, Coverage: covD(3, 0, 0)}, detInconclusive, detReasonLowScore},
		{"düşük skor", EvalCase{State: StateDone, Coverage: covD(3, 0, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.39)}}, detInconclusive, detReasonLowScore},
		{"iki eşik — skor önce", EvalCase{State: StateDone, Coverage: covD(1, 3, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.3)}}, detInconclusive, detReasonLowScore},
		{"düşük kapsam", EvalCase{State: StatePartial, Coverage: covD(1, 2, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.8)}}, detInconclusive, detReasonLowCoverage},
		{"belirsiz", EvalCase{State: StateDone, Coverage: covD(4, 0, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.55), evalHypS(HypConfig, "config", 0.48)}}, detAmbiguous, ""},
		{"fark tam 0.10 belirsiz değil", EvalCase{State: StateDone, Coverage: covD(4, 0, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.55), evalHypS(HypConfig, "config", 0.45)}}, detLikely, ""},
		{"doğrulandı", EvalCase{State: StateDone, Coverage: covD(4, 1, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.75), evalHypS(HypConfig, "config", 0.3)}}, detConfirmed, ""},
		{"olası", EvalCase{State: StateDone, Coverage: covD(2, 2, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.55)}}, detLikely, ""},
	} {
		got := evalDetOutcome(c.c)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%s: sonuç olmamalı, %+v", c.name, got)
		case c.want != "" && (got == nil || got.Value != c.want || got.Reason != c.reason):
			t.Errorf("%s: %+v, %s/%s beklenir", c.name, got, c.want, c.reason)
		}
	}
	o := evalDetOutcome(EvalCase{State: StateDone, Coverage: covD(2, 1, 0), Top: []EvalHyp{evalHypS(HypDeploy, "deploy", 0.756)}})
	if o.Confidence != 0.76 || o.Coverage != 0.67 {
		t.Fatalf("güven / kapsam 2 basamak: %+v", o)
	}
}

// compareCase — puanlı bir vaka: beklenen kategori + ilk hipotez.
func compareCase(id, cat string, accepted bool, state string, cov *EvalCoverage, top ...EvalHyp) EvalCase {
	return EvalCase{CaseID: "p:" + id, ProblemID: id, Service: "orders-api", Category: cat, FinalCause: "orders-api deploy",
		StartedAt: evalT0.UnixNano(), State: state, Coverage: cov, Top: top, AcceptedSuggestion: accepted}
}

func TestCompareEvalRunsTable(t *testing.T) {
	dep := func(s float64) EvalHyp { return evalHypS(HypDeploy, "deploy", s) }
	cfg := func(s float64) EvalHyp { return evalHypS(HypConfig, "config", s) }
	base := EvalRun{RunID: "eb", State: EvalDone, SuggestionKnown: true, CreatedAt: 1e9, FinishedAt: 61e9, Build: "v0.10.1450", Cases: []EvalCase{
		compareCase("a", "deploy", true, StateDone, covD(4, 0, 0), dep(0.8)),             // kabul edilmiş öneri, isabet
		compareCase("b", "deploy", false, StateDone, covD(4, 0, 0), cfg(0.75), dep(0.3)), // bağımsız, top-3 (kendinden emin yanlış)
		compareCase("c", "config", false, StateDone, covD(4, 0, 0), cfg(0.5)),            // bağımsız, isabet (olası)
		compareCase("d", "deploy", false, StateDone, covD(1, 3, 0), cfg(0.6)),            // bağımsız, kaçtı, düşük kapsam
		{CaseID: "p:e", ProblemID: "e", Category: "noise", State: CaseUnmapped},
	}}
	cand := base
	cand.RunID, cand.RerunOf, cand.State, cand.FinishedAt = "ec", "eb", EvalRunning, 0
	cand.Cases = append([]EvalCase(nil), base.Cases...)
	// Ters sıra: eşleme CaseID ile.
	cand.Cases[0], cand.Cases[3] = cand.Cases[3], cand.Cases[0]
	cand.Cases[0] = compareCase("d", "deploy", false, StateDone, covD(0, 4, 0), cfg(0.6)) // veri kayması: saklama dışı
	cand.Cases[2] = compareCase("c", "config", false, StateDone, covD(4, 0, 0), dep(0.7)) // c artık kaçıyor

	cmp, err := compareEvalRuns(base, cand)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Headline != "independent" || !cmp.IndependentKnown || len(cmp.Pairs) != 5 || cmp.Pairs[0].CaseID != "p:a" {
		t.Fatalf("başlık / sıra: %+v", cmp)
	}
	if cmp.Base.RunID != "eb" || cmp.Cand.RerunOf != "eb" || cmp.Base.Build != "v0.10.1450" {
		t.Fatalf("koşu kimlikleri: %+v / %+v", cmp.Base, cmp.Cand)
	}
	// Kontrol (tüm vakalar): taban 4 puanlı, top-1 a+c = 2; aday d veri yok → 3 puanlı, top-1 a = 1.
	ctl := cmp.Control
	if ctl.Base.Scored != 4 || ctl.Base.Top1Hits != 2 || ctl.Cand.Scored != 3 || ctl.Cand.Top1Hits != 1 {
		t.Fatalf("kontrol: %+v", ctl)
	}
	if ctl.DeltaTop1 == nil || *ctl.DeltaTop1 != -0.1667 {
		t.Fatalf("Δ top-1 = %v", ctl.DeltaTop1)
	}
	// Bağımsız (manşet): a hariç — taban b,c,d puanlı, top-1 c; aday b,c puanlı, top-1 0.
	ind := cmp.Independent
	if ind.Cases != 4 || ind.Base.Scored != 3 || ind.Base.Top1Hits != 1 || ind.Cand.Scored != 2 || ind.Cand.Top1Hits != 0 {
		t.Fatalf("bağımsız: %+v", ind)
	}
	if !reflect.DeepEqual(cmp.DriftCases, []string{"p:d"}) || !cmp.Pairs[3].CoverageDrift || cmp.Pairs[0].CoverageDrift {
		t.Fatalf("kayma: %v", cmp.DriftCases)
	}
	if p := cmp.Pairs[2]; p.Base.Hit != evalHitTop1 || p.Cand.Hit != evalHitMiss || p.Base.DetOutcome.Value != detLikely || p.Cand.DetOutcome.Value != detConfirmed {
		t.Fatalf("c çifti: %+v", p)
	}
	if p := cmp.Pairs[4]; p.Base.Hit != evalHitUnscored || p.Base.DetOutcome != nil {
		t.Fatalf("eşlenmeyen çift: %+v", p)
	}
	// Kalibrasyon (taban): puanlı 4; decisive a(conf) b(conf) c(likely) = 3; doğru a,c = 2; kendinden emin yanlış b = 1;
	// top-1 kaçan b,d = 2, çekimser d (low_coverage) = 1.
	cb := cmp.Calibration.Base
	if cb.Decisive.N != 3 || cb.Decisive.D != 4 || cb.ConfirmedPrecision.N != 2 || cb.ConfirmedPrecision.D != 3 ||
		cb.ConfidentWrong != 1 || cb.AbstainOnMiss.N != 1 || cb.AbstainOnMiss.D != 2 || cb.Outcomes[detConfirmed] != 2 {
		t.Fatalf("kalibrasyon taban: %+v", cb)
	}
	// Maliyet: tekrar LLM'siz; taban 60 s duvar, aday bitmedi.
	if !cmp.Cost.Base.LLMFree || cmp.Cost.Base.LLMCalls != 0 || cmp.Cost.Base.Replayed != 4 || cmp.Cost.Base.WallMs == nil ||
		*cmp.Cost.Base.WallMs != 60000 || cmp.Cost.Cand.WallMs != nil {
		t.Fatalf("maliyet: %+v", cmp.Cost)
	}
	// Uyum: iki tarafta puanlı a,b,c → top-1 türü a,b aynı (2/3); det. sonuç a,b,d aynı (d iki tarafta low_coverage), c değil (3/4).
	if a := cmp.Agreement; a.Top1Same.N != 2 || a.Top1Same.D != 3 || a.DetOutcomeSame.N != 3 || a.DetOutcomeSame.D != 4 {
		t.Fatalf("uyum: %+v", a)
	}
	if !strings.Contains(cmp.Note, "bağımsız") || strings.Contains(cmp.Note, "öneri bayrağından önce") {
		t.Fatalf("not: %s", cmp.Note)
	}

	// Aynı koşu kendisiyle: Δ = 0, kayma yok, tam uyum.
	self, err := compareEvalRuns(base, base)
	if err != nil || *self.Control.DeltaTop1 != 0 || len(self.DriftCases) != 0 || self.Agreement.Top1Same.N != self.Agreement.Top1Same.D {
		t.Fatalf("öz-karşılaştırma: %v %+v", err, self)
	}

	// Eski taban (bayrak bilinmiyor): bağımsız = tüm vakalar + not.
	legacy := base
	legacy.SuggestionKnown = false
	lc, _ := compareEvalRuns(legacy, base)
	if lc.IndependentKnown || lc.Independent.Cases != 5 || !strings.Contains(lc.Note, "öneri bayrağından önce") {
		t.Fatalf("eski taban: %+v", lc.Independent)
	}

	// Küme uyuşmazlığı.
	for name, c := range map[string][]EvalCase{
		"eksik":  base.Cases[:4],
		"farklı": append(append([]EvalCase(nil), base.Cases[:4]...), EvalCase{CaseID: "p:z", ProblemID: "z"}),
		"çift":   append(append([]EvalCase(nil), base.Cases[:4]...), base.Cases[0]),
	} {
		other := cand
		other.Cases = c
		if _, err := compareEvalRuns(base, other); !errors.Is(err, ErrEvalCompareMismatch) {
			t.Errorf("%s: %v, uyuşmazlık beklenir", name, err)
		}
	}
}

// TestScoreEvalIndependent — bağımsız alt küme: kabul edilmiş öneri vakaları
// manşetten düşer; bayrak bilinmiyorsa oran null (eski koşu yalan söylemez).
func TestScoreEvalIndependent(t *testing.T) {
	dep := evalHypS(HypDeploy, "deploy", 0.8)
	cfg := evalHypS(HypConfig, "config", 0.8)
	for _, c := range []struct {
		name                     string
		cases                    []EvalCase
		known                    bool
		accepted, scored, h1, h3 int
		acc1                     *float64
	}{
		{"boş", nil, true, 0, 0, 0, 0, nil},
		{"hepsi kabul", []EvalCase{compareCase("a", "deploy", true, StateDone, covD(1, 0, 0), dep)}, true, 1, 0, 0, 0, nil},
		{"karışık", []EvalCase{
			compareCase("a", "deploy", true, StateDone, covD(1, 0, 0), dep),
			compareCase("b", "deploy", false, StateDone, covD(1, 0, 0), dep),
			compareCase("c", "deploy", false, StateDone, covD(1, 0, 0), cfg, dep),
			compareCase("d", "deploy", false, StateDone, covD(0, 2, 0), dep), // veri yok — puansız
			compareCase("e", "deploy", true, StateQueued, nil),               // sürüyor ama bayraklı
		}, true, 2, 2, 1, 2, ratio(1, 2)},
		{"bayrak bilinmiyor", []EvalCase{compareCase("b", "deploy", false, StateDone, covD(1, 0, 0), dep)}, false, 0, 1, 1, 1, nil},
	} {
		sc := scoreEval(evalInput{Cases: c.cases, SuggestionKnown: c.known})
		if sc.AcceptedSuggestion != c.accepted || sc.IndependentScored != c.scored || sc.IndependentTop1Hits != c.h1 || sc.IndependentTop3Hits != c.h3 {
			t.Errorf("%s: %+v", c.name, sc)
		}
		if (sc.IndependentTop1Acc == nil) != (c.acc1 == nil) || (c.acc1 != nil && *sc.IndependentTop1Acc != *c.acc1) {
			t.Errorf("%s: bağımsız top-1 = %v, %v beklenir", c.name, sc.IndependentTop1Acc, c.acc1)
		}
	}
}

func TestRerunCasesFrozen(t *testing.T) {
	base := EvalRun{RunID: "eb", Cases: []EvalCase{
		{CaseID: "p:a", ProblemID: "a", Service: "orders-api", Category: "deploy", FinalCause: "x", StartedAt: 5, DisplayID: "P-1",
			State: StateDone, Top: []EvalHyp{{Kind: HypDeploy}}, Coverage: covD(1, 0, 0), FinishedAt: 9, AcceptedSuggestion: true},
		{CaseID: "p:b", ProblemID: "b", Category: "noise", State: CaseUnmapped, Reason: "kategori hipotez türlerine eşlenmiyor: gürültü"},
		{CaseID: "p:c", ProblemID: "c", Service: "pg", Category: "capacity", StartedAt: 5, State: CaseSkipped, Reason: evalReasonNotService},
		{CaseID: "p:d", ProblemID: "d", Service: "s", Category: "deploy", StartedAt: 5, State: CaseSkipped, Reason: evalReasonQueueFull},
		{CaseID: "p:e", ProblemID: "e", Service: "s", Category: "deploy", StartedAt: 5, State: CaseSkipped, Reason: "kuyruğa alınamadı: x"},
		{CaseID: "p:f", ProblemID: "f", Category: "deploy", State: CaseSkipped, Reason: evalReasonNoAnchor},
		{CaseID: "p:g", ProblemID: "g", Service: "s", Category: "deploy", StartedAt: 5, State: StateError, Reason: "tekrar tamamlanamadı"},
		{CaseID: "p:h", ProblemID: "h", Service: "s", Category: "deploy", StartedAt: 5, State: StateRunning},
		{CaseID: "p:i", ProblemID: "i", Service: "pg", Category: "capacity", StartedAt: 5, State: CaseSkipped, Reason: evalFrozenPrefix + truncRunes(evalReasonNotService, 30)},
	}}
	got := rerunCases(base)
	want := map[string]string{"p:a": StateQueued, "p:b": CaseUnmapped, "p:c": CaseSkipped, "p:d": StateQueued, "p:e": StateQueued,
		"p:f": CaseSkipped, "p:g": StateQueued, "p:h": StateQueued, "p:i": CaseSkipped}
	if len(got) != len(base.Cases) {
		t.Fatalf("vaka sayısı %d", len(got))
	}
	for i, c := range got {
		if c.CaseID != base.Cases[i].CaseID || c.State != want[c.CaseID] {
			t.Errorf("%s: %s, %s beklenir", c.CaseID, c.State, want[c.CaseID])
		}
		if c.StartedAt != base.Cases[i].StartedAt || c.FinalCause != base.Cases[i].FinalCause || c.AcceptedSuggestion != base.Cases[i].AcceptedSuggestion {
			t.Errorf("%s: kimlik / çıpa / bayrak kopyalanmalı", c.CaseID)
		}
		if c.Top != nil || c.Coverage != nil || c.FinishedAt != 0 {
			t.Errorf("%s: sonuç taşınmamalı", c.CaseID)
		}
		frozen := c.State != StateQueued
		if frozen != strings.HasPrefix(c.Reason, evalFrozenPrefix) || strings.Count(c.Reason, evalFrozenPrefix) > 1 {
			t.Errorf("%s: neden %q", c.CaseID, c.Reason)
		}
	}
	if got[0].DisplayID != "P-1" || got[0].Service != "orders-api" {
		t.Fatalf("görünen kimlik / servis: %+v", got[0])
	}
}

func TestNormalizeEvalRequestRerun(t *testing.T) {
	now := evalT0
	for _, c := range []struct {
		name string
		req  EvalRunRequest
		ok   bool
	}{
		{"yalın rerun", EvalRunRequest{RerunOf: "e1abc", Now: now}, true},
		{"rerun + pencere", EvalRunRequest{RerunOf: "e1abc", From: now.Add(-time.Hour), To: now, Now: now}, false},
		{"rerun + limit", EvalRunRequest{RerunOf: "e1abc", Limit: 5, Now: now}, false},
		{"tireli kimlik", EvalRunRequest{RerunOf: "e1-x", Now: now}, false},
		{"büyük harf", EvalRunRequest{RerunOf: "E1", Now: now}, false},
		{"uzun", EvalRunRequest{RerunOf: strings.Repeat("a", 41), Now: now}, false},
		{"loop 2a'da yok", EvalRunRequest{From: now.Add(-time.Hour), To: now, Loop: true, Now: now}, false},
		{"loop + rerun", EvalRunRequest{RerunOf: "e1abc", Loop: true, Now: now}, false},
	} {
		_, err := NormalizeEvalRequest(c.req)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrEvalInvalid)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// TestStartEvalRunRerun — yeniden koşu aynı vakaları AYNI çıpalarla, yeni koşu
// kimliğiyle kuyruğa alır; vakaları yeniden OKUMAZ; tabanın kaydına dokunmaz.
func TestStartEvalRunRerun(t *testing.T) {
	SetSettings(DefaultSettings())
	defer SetSettings(DefaultSettings())
	st := newEvalFakeStore()
	at := evalT0.Add(-10 * 24 * time.Hour)
	c1 := confirmedCase("p1", "orders-api", "deploy", "orders-api deploy v2", at)
	c1.SuggestedCause = "Orders-API  deploy v2" // değiştirilmeden kabul
	c2 := confirmedCase("p2", "kxzone", "config", "kxzone config flag", at)
	c2.SuggestedCause = "dependency"
	st.cases = []chstore.IncidentCase{c1, c2, confirmedCase("p3", "b", "noise", "x", at)}
	st.problems["p1"] = chstore.Problem{ID: "p1", Service: "orders-api", StartedAt: at.Add(time.Minute).UnixNano(), RuleID: "r"}
	st.problems["p2"] = chstore.Problem{ID: "p2", Service: "kxzone", StartedAt: at.Add(2 * time.Minute).UnixNano(), RuleID: "r"}
	ctx := context.Background()
	base, err := StartEvalRun(ctx, st, EvalRunRequest{From: at.Add(-time.Hour), To: evalT0, Now: evalT0, Build: "v0.10.1450"}, CurrentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if !base.SuggestionKnown || base.Build != "v0.10.1450" || !base.Cases[0].AcceptedSuggestion || base.Cases[1].AcceptedSuggestion {
		t.Fatalf("taban bayrakları: %+v", base)
	}
	// Taban bitsin (yoksa yeni koşu 409).
	for _, pid := range []string{"p1", "p2"} {
		inv := st.get(t, chstore.SREReplayKey(base.RunID, pid))
		inv.State, inv.FinishedAt = StateDone, evalT0.Add(time.Minute).UnixNano()
		v, _ := st.GetSREEvalRun(ctx, base.RunID)
		b, _ := decodeEvalRun(*v)
		if err := saveEvalRun(ctx, st, mergeEvalCase(b, inv, evalT0.Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	// Vaka sonradan düzeltilse de yeniden koşu tabanın listesini kullanır.
	st.cases = nil
	if _, err := StartEvalRun(ctx, st, EvalRunRequest{RerunOf: "enope", Now: evalT0.Add(time.Hour)}, CurrentSettings()); !errors.Is(err, ErrEvalBaseMissing) {
		t.Fatalf("olmayan taban: %v", err)
	}
	re, err := StartEvalRun(ctx, st, EvalRunRequest{RerunOf: base.RunID, Now: evalT0.Add(time.Hour), By: "admin@example.test", Build: "v0.10.1451"}, CurrentSettings())
	if err != nil {
		t.Fatal(err)
	}
	if re.RunID == base.RunID || re.RerunOf != base.RunID || re.Build != "v0.10.1451" || re.From != base.From || re.To != base.To ||
		re.Found != base.Found || !re.SuggestionKnown || len(re.Cases) != len(base.Cases) {
		t.Fatalf("yeniden koşu: %+v", re)
	}
	for i, c := range re.Cases {
		b := base.Cases[i]
		if c.CaseID != b.CaseID || c.StartedAt != b.StartedAt || c.AcceptedSuggestion != b.AcceptedSuggestion {
			t.Fatalf("%s: dondurulmuş alanlar", c.CaseID)
		}
	}
	if re.Cases[0].State != StateQueued || re.Cases[1].State != StateQueued || re.Cases[2].State != CaseUnmapped {
		t.Fatalf("durumlar: %+v", re.Cases)
	}
	for _, pid := range []string{"p1", "p2"} {
		old := st.get(t, chstore.SREReplayKey(base.RunID, pid))
		nw := st.get(t, chstore.SREReplayKey(re.RunID, pid))
		if nw.EvalRun != re.RunID || nw.State != StateQueued || nw.AsOf != old.AsOf || !reflect.DeepEqual(nw.Anchor, old.Anchor) || nw.ProblemAt != old.ProblemAt {
			t.Fatalf("%s: aynı çıpa beklenir: eski %+v yeni %+v", pid, old.Anchor, nw.Anchor)
		}
		if old.EvalRun != base.RunID {
			t.Fatalf("%s: tabanın tekrar kaydı ezilmemeli", pid)
		}
	}
	// Karşılaştırma yeniden koşuyla aynı kümeyi görür.
	v, _ := st.GetSREEvalRun(ctx, base.RunID)
	b, _ := decodeEvalRun(*v)
	if _, err := CompareEvalRuns(b, re); err != nil {
		t.Fatalf("taban ↔ yeniden koşu: %v", err)
	}
}
