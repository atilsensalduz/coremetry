package api

// explain_incident.go — incident ✨ Explain (POST /api/copilot/explain-incident/{id}).
//
// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."): api.go'dan taşındı; buildIncidentExplain'in iki
// çağıranı (HTTP düğmesi + sohbetin açıklama turu) aynı explainBuilt'i — yani
// aynı istem çiftini ve explainCacheKey(SystemPromptIncident(), user, "")
// anahtarını — kullanır. Gövde v0.10.1404'teki handler'ın aynısı.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// buildIncidentExplain — incident (+ bağlı problemler) açıklamasının girdisi.
// Bulunamayan incident → explainNotFound("incident not found").
func (s *Server) buildIncidentExplain(ctx context.Context, id string) (explainBuilt, error) {
	st := s.explainReads()
	inc, err := st.GetIncident(ctx, id)
	if err != nil {
		return explainBuilt{}, err
	}
	if inc == nil {
		return explainBuilt{}, explainNotFound("incident not found")
	}
	// Pull attached problems so the prompt carries the actual
	// signals that opened the incident, not just the title.
	// Two-stage lookup: IncidentProblems returns IDs only, then the rows.
	// Capped at 8 to keep the prompt size reasonable.
	//
	// v0.9.343 — the second stage used to page ListProblems(Limit: 2000) and
	// keep the wanted subset, so an attached problem older than the newest
	// 2000 silently vanished from the prompt: the model was told the incident
	// had FEWER attached problems than it does, and answered confidently on
	// the smaller set. Fails quiet — it degrades the explanation instead of
	// erroring. Now the ids go into the query.
	pIDs, _ := st.IncidentProblems(ctx, id)
	if len(pIDs) > 8 {
		pIDs = pIDs[:8]
	}
	var probs []chstore.Problem
	if len(pIDs) > 0 {
		probs, _ = st.ListProblems(ctx, chstore.ProblemFilter{
			IDs: pIDs, Limit: len(pIDs),
		})
	}
	var probLines string
	for _, p := range probs {
		probLines += fmt.Sprintf(
			"  • [%s] %s — %s: value=%.2f%s threshold=%.2f%s\n",
			strings.ToUpper(p.Severity), p.Service, p.RuleName, p.Value, problemMetricUnitTR(p.Metric), p.Threshold, problemMetricUnitTR(p.Metric))
	}
	user := fmt.Sprintf(
		"Incident: %s\nService: %s\nSeverity: %s\nStatus: %s\nSummary: %s\nAttached problems (%d):\n%s",
		inc.Title, inc.Service, inc.Severity, inc.Status, inc.Summary, len(probs), probLines,
	)
	b := newExplainBuilt(copilot.SystemPromptIncident(), user)
	b.Label = inc.Title
	b.Service = inc.Service
	b.Links = []guidedAnswerLink{{Label: "Olayı aç", Href: "/incident?id=" + url.QueryEscape(inc.ID)}}
	return b, nil
}

// copilotExplainIncident fetches an Incident (plus its attached
// problems for context) and asks the model for a SEV-grade
// triage summary: what's happening, blast radius, first three
// coordination actions, escalate-or-not call. Distinct from
// explain-problem because incidents bundle multiple firings —
// the LLM needs the wider scope to call escalation correctly.
func (s *Server) copilotExplainIncident(w http.ResponseWriter, r *http.Request) {
	b, err := s.buildIncidentExplain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeExplainBuildErr(w, err)
		return
	}
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, nil, s.explainPrompt(r, b.System, b.User), "", b.CacheKey)
}
