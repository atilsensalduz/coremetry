package sre

// events.go — v0.10.1288 (AI SRE Faz 2): olaylar adımı + tembel node
// adımının dönüştürücüsü.
//
// Olaylar (dalga 2, 5 s): sohbetin list_problem_window_events okuması
// (mcptools.ReadProblemWindowEvents — ListProblemWindowEvents + OKUMA
// zenginleştirmesi) [başlangıç − 60 dk, şimdi] için
//   - öznenin alarmları (≤30) → zaman çizelgesi;
//   - aşağı akış db:/ext: düğümlerinin (bağımlılık adımının kenarları, ≤3)
//     problemleri → dış servis / DB hipotezleri;
//   - incident bağlanması (chstore.GetProblemIncident) → zaman çizelgesi.
// Okumalar paralel; biri düşerse adım kısmi, gerçek parçası "okunamadı"
// bayrağıyla (okunamadı ≠ yok).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func runEvents(ctx context.Context, rd readers, req eventsRequest) stepOutcome {
	ef := &EventFacts{Targets: req.Targets}
	var mu sync.Mutex
	var wg sync.WaitGroup
	var subErr, incErr error
	relFailed := 0
	var subRows, relRows int
	t0 := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		d, err := rd.events(ctx, req.Service, req.From, req.To, factsAlerts+1)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			subErr = err
			return
		}
		ef.AlertsOK = true
		ef.Alerts = alertFactsOf(d.Rows, req.ProblemID, factsAlerts)
		subRows = len(d.Rows)
	}()
	for _, t := range req.Targets {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			d, err := rd.events(ctx, t, req.From, req.To, factsRelated)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				relFailed++
				return
			}
			relRows += len(d.Rows)
			for _, a := range alertFactsOf(d.Rows, "", factsRelated) {
				if len(ef.Related) < factsRelated {
					ef.Related = append(ef.Related, a)
				}
			}
		}(t)
	}
	if rd.incident != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, at, err := rd.incident(ctx, req.ProblemID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				incErr = err
				return
			}
			ef.IncidentOK, ef.IncidentID, ef.IncidentAt = true, id, at
		}()
	}
	wg.Wait()
	ef.RelatedOK = relFailed == 0
	st := eventsStep(req, ef, subErr, relFailed, incErr, rd.incident != nil)
	rows := subRows + relRows
	st.Evidence = []mcptools.ChatEvidence{{
		Tool: "list_problem_window_events", Source: "ClickHouse", QueryKind: "summary",
		Window:     mcptools.EvidenceWindow{FromMs: req.From.UnixMilli(), ToMs: req.To.UnixMilli()},
		DurationMs: time.Since(t0).Milliseconds(), Rows: &rows,
		Query: fmt.Sprintf("ListProblemWindowEvents service='%s'%s (problems FINAL, başlangıç/kapanış pencerede, + EnrichProblemsForRead) · incident_problems problem_id='%s'",
			req.Service, targetsNote(req.Targets), req.ProblemID),
	}}
	if subErr != nil && (len(req.Targets) == 0 || relFailed == len(req.Targets)) {
		st.Evidence[0].Rows = nil
	}
	return stepOutcome{step: st, facts: &Facts{Events: ef}}
}

func targetsNote(ts []string) string {
	if len(ts) == 0 {
		return ""
	}
	return " + aşağı akış " + strings.Join(ts, ", ")
}

// eventsStep — SAF: olay okumaları → adım.
func eventsStep(req eventsRequest, ef *EventFacts, subErr error, relFailed int, incErr error, incWired bool) Step {
	st := Step{Name: stepEvents}
	var parts []string
	failed, total := 0, 1+len(req.Targets)
	if subErr != nil {
		failed++
		parts = append(parts, "alarmlar okunamadı")
	} else {
		parts = append(parts, fmt.Sprintf("%d alarm (servis, başlangıç −60 dk → şimdi)", len(ef.Alerts)))
	}
	if len(req.Targets) > 0 {
		failed += relFailed
		if relFailed > 0 {
			parts = append(parts, fmt.Sprintf("%d aşağı akış düğümünün problemi okunamadı", relFailed))
		} else {
			parts = append(parts, fmt.Sprintf("%d aşağı akış problemi (%s)", len(ef.Related), strings.Join(req.Targets, ", ")))
		}
	}
	if incWired {
		total++
		switch {
		case incErr != nil:
			failed++
			parts = append(parts, "incident bağı okunamadı")
		case ef.IncidentID != "":
			parts = append(parts, "incident "+ef.IncidentID)
		default:
			parts = append(parts, "incident yok")
		}
	}
	switch {
	case failed == 0:
		st.State = StepOK
	case failed >= total:
		st.State = StepError
	default:
		st.State = StepPartial
	}
	st.Summary = truncRunes(strings.Join(parts, "; "), 400)
	return st
}

// nodeStep — SAF: tembel node kontrolü → adım + bulgular.
func nodeStep(nc mcptools.NodeCheck, err error) (Step, []Finding) {
	st := Step{Name: stepNode}
	if err != nil {
		st.State, st.Summary = StepError, truncRunes(err.Error(), 400)
		return st, nil
	}
	st.State = sectionStepState(nc.Section.State)
	st.NoData = nc.Section.State == "empty" // v0.10.1301
	st.Summary = truncRunes(nc.Section.Summary, 400)
	if nc.Section.Note != "" {
		st.Summary = truncRunes(st.Summary+" ("+nc.Section.Note+")", 500)
	}
	st.Evidence = nc.Evidences
	var out []Finding
	for _, f := range nc.Findings {
		out = append(out, Finding{Kind: f.Kind, Name: f.Name, Level: f.Level, Reasons: f.Reasons, EvidenceRefs: f.EvidenceRefs, Step: stepNode})
	}
	return st, out
}
