package api

// explain_runbook.go — problem runbook'u ✨ (POST /api/copilot/runbook/{id}).
//
// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."): api.go'dan taşındı; buildRunbookExplain'in iki
// çağıranı (HTTP düğmesi + sohbetin açıklama turu) aynı explainBuilt'i — yani
// aynı istem çiftini ve explainCacheKey(SystemPromptRunbook(), user, "")
// anahtarını — kullanır. "N geçmiş çözüm" satırı (similarCount) HTTP gövdesinde
// aynen; sohbette künyenin `note`u (CopilotExplain'in meta satırıyla aynı metin).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// runbookLabel — sohbet künyesindeki etiket (CopilotExplain'in kart başlığı).
const runbookLabel = "RUNBOOK"

// runbookSimilarNote — SAF: "N geçmiş çözüm" satırı; CopilotExplain.tsx'in
// runbook meta metniyle birebir (çekmece ile sohbet aynı cümleyi gösterir).
func runbookSimilarNote(n int) string {
	if n > 0 {
		plural := "s"
		if n == 1 {
			plural = ""
		}
		return fmt.Sprintf("Based on %d past resolved instance%s of this rule on this service.", n, plural)
	}
	return "No past resolutions found — first-principles only."
}

// buildRunbookExplain — numaralı, uygulanabilir runbook'un girdisi: problem +
// aynı kuralın aynı servisteki geçmiş çözümleri + deploy + kanıt zinciri.
// Bulunamayan problem → explainNotFound("problem not found").
func (s *Server) buildRunbookExplain(ctx context.Context, id string) (explainBuilt, error) {
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
	similar, _ := st.FindSimilarResolvedProblems(ctx, p.Service, p.RuleID, 8)
	// Deploy correlation — same enrichment the problem list
	// shows, threaded into the runbook prompt so the model
	// can lead with "rollback the recent deploy" when the
	// timing is suspicious.
	enriched := st.EnrichProblemsWithDeploys(ctx,
		[]chstore.Problem{*p}, 30*time.Minute)
	p = &enriched[0]

	var sb strings.Builder
	fmt.Fprintf(&sb, "Current problem (status %s):\n", p.Status)
	fmt.Fprintf(&sb,
		"  Service: %s\n  Metric: %s\n  Value: %.2f%s (threshold %.2f%s)\n  Severity: %s\n  Rule: %s\n  Description: %s\n",
		p.Service, p.Metric, p.Value, problemMetricUnitTR(p.Metric), p.Threshold, problemMetricUnitTR(p.Metric), p.Severity, p.RuleName, p.Description)
	if p.RecentDeploy != nil {
		fmt.Fprintf(&sb,
			"\nRecent deploy: service.version=%q first seen %d seconds before this problem opened — strong signal that step 1 should be \"check / roll back this deploy\".\n",
			p.RecentDeploy.Version, p.RecentDeploy.AgeSeconds)
	}

	if len(similar) > 0 {
		fmt.Fprintf(&sb, "\nPast resolved instances of this rule on this service (%d, most recent first):\n", len(similar))
		var totalTTR time.Duration
		var ttrSeen int
		for _, sp := range similar {
			ttr := "unknown"
			if sp.ResolvedAt != nil {
				d := time.Unix(0, *sp.ResolvedAt).Sub(time.Unix(0, sp.StartedAt))
				ttr = d.Round(time.Minute).String()
				totalTTR += d
				ttrSeen++
			}
			fmt.Fprintf(&sb,
				"  • opened %s — peak value %.2f%s (sev %s) — resolved in %s\n",
				time.Unix(0, sp.StartedAt).Format("2006-01-02 15:04"),
				sp.Value, problemMetricUnitTR(sp.Metric), sp.Severity, ttr,
			)
		}
		if ttrSeen > 0 {
			avg := totalTTR / time.Duration(ttrSeen)
			fmt.Fprintf(&sb,
				"\nAverage time-to-resolve across past instances: %s\n",
				avg.Round(time.Minute))
		}
	} else {
		sb.WriteString("\nNo past resolved instances of this exact rule on this service — use first-principles reasoning grounded in the metric + service.\n")
	}

	// v0.9.1198 (Faz 5.5) — kanıt zinciri girdiye eklendi: checklist
	// artık "geçmişte ne kadar sürdü"nün yanında otomatik araştırmacının
	// neyi suçlu bulduğunu da biliyor (şüpheli + aday yollar + bulunan
	// derin sinyaller). Hipotez yoksa blok hiç eklenmez.
	if hyp, _ := st.GetHypothesis(ctx, "problem", id); hyp != nil {
		if chain := renderRunbookEvidenceChain(hyp); chain != "" {
			sb.WriteString("\n" + chain)
		}
	}

	b := newExplainBuilt(copilot.SystemPromptRunbook(), sb.String())
	b.Extra = map[string]any{"similarCount": len(similar)}
	b.Label = runbookLabel
	b.Note = runbookSimilarNote(len(similar))
	b.Service = p.Service
	b.Links = []guidedAnswerLink{{Label: "Problemi aç", Href: "/problems?problem=" + url.QueryEscape(p.ID)}}
	return b, nil
}

// copilotRunbook generates a numbered, actionable runbook for
// an open Problem, anchored in past resolved instances of the
// same rule on the same service. Distinct from
// explain-problem — explain gives context bullets, runbook
// gives an executable checklist. The past-resolutions context
// (time-to-resolve, value at fire) lets the model bias step
// order: if similar issues consistently resolved in <5 min,
// lead with the fastest path that worked before; if they
// dragged past 30 min, surface escalation up front.
//
// Caps at 8 past instances to keep the prompt bounded — the
// most-recent 8 carry the freshest pattern.
func (s *Server) copilotRunbook(w http.ResponseWriter, r *http.Request) {
	b, err := s.buildRunbookExplain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeExplainBuildErr(w, err)
		return
	}
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, b.Extra, s.explainPrompt(r, b.System, b.User), "", b.CacheKey)
}
