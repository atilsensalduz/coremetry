package api

// explain_problem.go — problem ✨ Explain (POST /api/copilot/explain-problem/{id}).
//
// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."): api.go'dan taşındı. Girdi kurucusu
// (buildProblemExplain) ctx tabanlı ve İKİ çağıranı var — HTTP düğmesi ve
// sohbetin açıklama turu (chat_explain_registry.go). İkisi aynı explainBuilt'i
// kullanır: sistem + kullanıcı istemi ve önbellek anahtarı
// explainCacheKey(SystemPromptProblem(), user, "") BAYT BAYT aynı → düğme ve
// sohbet tek önbellek satırını paylaşır. Gövde v0.10.1404'teki handler'ın
// aynısı (yalnız r.Context() → ctx, s.store → s.explainReads()).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// buildProblemExplain — problem açıklamasının girdisi (likely-cause +
// first-action). Bulunamayan problem → explainNotFound("problem not found").
func (s *Server) buildProblemExplain(ctx context.Context, id string) (explainBuilt, error) {
	st := s.explainReads()
	// v0.9.343 — ask for the row, don't page the newest 1000 and scan.
	//
	// This paged ListProblems(Limit: 1000) and linear-scanned in Go, so any
	// problem outside the newest 1000 answered "problem not found" — the same
	// answer as "deleted", for a row the operator is looking at. Identical
	// shape to the v0.9.332 GetIncident bug, with the right primitive
	// (GetProblem, WHERE id = ?) already sitting in the same package.
	p, err := st.GetProblem(ctx, id)
	if err != nil {
		return explainBuilt{}, err
	}
	if p == nil {
		return explainBuilt{}, explainNotFound("problem not found")
	}
	// Attach deploy correlation for the explain prompt — the
	// "regression right after a deploy" pattern is the single
	// highest-signal cause hint the model should consider first.
	enriched := st.EnrichProblemsWithDeploys(ctx,
		[]chstore.Problem{*p}, 30*time.Minute)
	p = &enriched[0]
	user := fmt.Sprintf(
		"Service: %s\nMetric: %s\nValue: %.2f%s (threshold %.2f%s)\nSeverity: %s\nRule: %s\nDescription: %s",
		p.Service, p.Metric, p.Value, problemMetricUnitTR(p.Metric), p.Threshold, problemMetricUnitTR(p.Metric), p.Severity, p.RuleName, p.Description,
	)
	if p.RecentDeploy != nil {
		user += fmt.Sprintf(
			"\n\nRecent deploy: service.version=%q first seen %d seconds before this problem opened. Consider whether this regression coincides with that deploy.",
			p.RecentDeploy.Version, p.RecentDeploy.AgeSeconds)
	}
	// v0.8.394 (AI audit A1) — fuse the persisted deterministic root-cause
	// hypothesis (RootCauseSynthesizer) into the operator-clicked explain,
	// the SAME block the background auto-explainer injects — the operator
	// who sees the "Root cause: X (NN%)" ribbon must not get an Explain
	// that ignores it. Best-effort: absent/erroring hypothesis renders "".
	if hyp, err := st.GetHypothesis(ctx, "problem", p.ID); err == nil {
		if block := anomaly.HypothesisPromptBlockTR(hyp); block != "" {
			user += "\n" + block
		}
	}
	// v0.6.54 — multi-signal root-cause correlation. Beyond the
	// deploy hint, gather the service's topology neighbours, error-
	// trace exemplars, and significant log patterns around the
	// problem's open time so the model ranks causes from evidence
	// instead of metric-shape priors alone. All bounded (top-5,
	// windowed) and best-effort — a failed signal lookup just omits
	// that block rather than failing the explain. Only meaningful
	// when the problem is scoped to a concrete service.
	if p.Service != "" {
		user += s.serviceCorrelationContext(ctx, p.Service, time.Unix(0, p.StartedAt))
	}
	b := newExplainBuilt(copilot.SystemPromptProblem(), user)
	b.Label = firstNonEmpty(p.RuleName, p.Metric)
	b.Service = p.Service
	b.Links = []guidedAnswerLink{{Label: "Problemi aç", Href: "/problems?problem=" + url.QueryEscape(p.ID)}}
	return b, nil
}

// copilotExplainProblem fetches a Problem and asks the model for a
// likely-cause + first-action summary. Useful on a fresh page during
// an incident.
func (s *Server) copilotExplainProblem(w http.ResponseWriter, r *http.Request) {
	b, err := s.buildProblemExplain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeExplainBuildErr(w, err)
		return
	}
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, nil, s.explainPrompt(r, b.System, b.User), "", b.CacheKey)
}

// serviceCorrelationContext gathers the multi-signal evidence the
// root-cause prompt reasons over (v0.6.54; v0.9.418'de problem'dan
// servis-genel hale getirildi — anomaly explain de kullanır): 1-hop
// topology neighbours + error-trace exemplars around the anchor time.
// All bounded (top-5, windowed) and best-effort — any signal that
// errors or comes up empty is simply omitted so a flaky lookup never
// blocks the explain. Window is [anchor-30m, anchor+5m]; the leading
// 30m captures the run-up, the trailing 5m the immediate aftermath.
// v0.10.1405 — api.go'dan taşındı (gövde aynı; okuma s.explainReads()).
func (s *Server) serviceCorrelationContext(ctx context.Context, service string, anchor time.Time) string {
	st := s.explainReads()
	var b strings.Builder
	from, to := anchor.Add(-30*time.Minute), anchor.Add(5*time.Minute)

	// 1) Topology neighbours — who calls / is called by this service.
	// Direction is the key root-cause hint: a callee erroring points
	// downstream, a caller spike points upstream.
	// v0.10.1410 — topology_edges_5m over the SAME [anchor-30m, anchor+5m]
	// window (serviceNeighborsRollup, the Topology tab's read): DB /
	// queue / external callees now appear (the old raw-span walk of the
	// 50 largest traces only saw instrumented child services, and read
	// now-35m regardless of the anchor). Numbers are the edge's calls +
	// error rate, as on the Topology tab.
	if up, down, err := s.serviceNeighborsRollup(ctx, service, from, to); err == nil && (len(up) > 0 || len(down) > 0) {
		b.WriteString("\n\nTopology neighbours (topology rollup, 1 hop, in window):")
		if len(up) > 0 {
			b.WriteString("\n  callers: " + topNeighborNames(up, 5))
		}
		if len(down) > 0 {
			b.WriteString("\n  callees: " + topNeighborNames(down, 5))
		}
	}

	// 2) Error-trace exemplars for this service in the window.
	if traces, _, _, terr := st.GetTraces(ctx, chstore.TraceFilter{
		Service: service, HasError: true, From: from, To: to,
		Limit: 5, Sort: "time", Order: "desc", CountMode: "skip",
	}); terr == nil && len(traces) > 0 {
		b.WriteString("\n\nError-trace exemplars (this service, in window):")
		for _, t := range traces {
			b.WriteString(fmt.Sprintf("\n  %s — %.0fms, %d spans", t.RootName, t.DurationMs, t.SpanCount))
		}
	}

	return b.String()
}

// topNeighborNames renders the top-N neighbours (already calls-DESC from
// serviceNeighborsFromEdges) as a compact "name [kind] (N calls, X% err), …"
// line for the root-cause prompt; the rest is counted, not dropped silently.
func topNeighborNames(ns []serviceNeighbor, n int) string {
	parts := make([]string, 0, n+1)
	for i, x := range ns {
		if i >= n {
			parts = append(parts, fmt.Sprintf("+%d more", len(ns)-n))
			break
		}
		parts = append(parts, neighbourEvidenceLabel(x, false))
	}
	return strings.Join(parts, ", ")
}
