package sre

// steps.go — v0.10.1282 — 0a yürütücüsü: TEK adım `sweep` =
// mcptools.ReadServiceSweep{Service, Env, From: başlangıç − 15 dk,
// To: min(now, başlangıç + 30 dk)}. CoSRE sohbetinin service_health_sweep
// aracıyla AYNI okuma (kendi 15 s bütçesi, MV/Thanos okuyucuları) — ajan
// yeni bir sorgu yazmaz. Faz 1b planSteps ile deploy / bağımlılık / pivot
// adımları eklenir; adım adları önbellek anahtarıdır (resume).

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

const stepSweep = "sweep"

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// sweepPayload — adım yükü (≤ 12 KB): hüküm + taranan + bölüm durumları.
type sweepPayload struct {
	Verdict  mcptools.SweepVerdict `json:"verdict"`
	Scanned  mcptools.SweepScanned `json:"scanned"`
	Sections []sweepSection        `json:"sections"`
}

type sweepSection struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Summary string `json:"summary,omitempty"`
}

// runSteps — 0a adım planı.
func runSteps(ctx context.Context, deps mcptools.Deps, inv *Investigation, checkpoint func(*Investigation), now func() time.Time) {
	t0 := now()
	if s := inv.step(stepSweep); s != nil && s.State == StepOK && t0.Sub(time.Unix(0, s.At)) < stepFreshForReuse {
		return // resume: taze ok adım yeniden koşmaz
	}
	inv.Anchor = anchorFor(inv.ProblemAt, t0)
	res, err := mcptools.ReadServiceSweep(ctx, deps, mcptools.SweepRequest{
		Service: inv.Service, Env: inv.Env,
		From: time.Unix(0, inv.Anchor.From).UTC(), To: time.Unix(0, inv.Anchor.To).UTC(),
	})
	t1 := now()
	step, findings, warnings := sweepStep(res, err)
	step.DurationMs = t1.Sub(t0).Milliseconds()
	step.At = t1.UnixNano()
	inv.setStep(step)
	if step.State != StepError {
		inv.Findings = findings
		inv.Warnings = warnings
	}
	checkpoint(inv)
}

// sweepStep — SAF: tarama sonucu → adım + bulgular.
func sweepStep(res mcptools.SweepResult, err error) (Step, []Finding, []string) {
	st := Step{Name: stepSweep}
	if err != nil {
		st.State = StepError
		st.Summary = truncRunes(err.Error(), 400)
		return st, nil, nil
	}
	if !res.OK {
		st.State = StepError
		msg := res.Message
		if msg == "" && res.Error != nil {
			msg = res.Error.Message
		}
		if msg == "" {
			msg = "tarama sonuç vermedi"
		}
		st.Summary = truncRunes(msg, 400)
		return st, nil, res.Warnings
	}
	st.State = StepOK
	if res.Partial {
		st.State = StepPartial
	}
	sum := res.Verdict.Label
	if len(res.Verdict.Reasons) > 0 {
		sum += ": " + strings.Join(res.Verdict.Reasons, "; ")
	}
	st.Summary = truncRunes(sum, 600)
	findings := make([]Finding, 0, len(res.Findings))
	for i, f := range res.Findings {
		if i >= maxFindings {
			break
		}
		findings = append(findings, Finding{Kind: f.Kind, Name: f.Name, Level: f.Level, Reasons: f.Reasons, EvidenceRefs: f.EvidenceRefs})
		st.EvidenceRefs = append(st.EvidenceRefs, f.EvidenceRefs...)
	}
	if len(st.EvidenceRefs) > 20 {
		st.EvidenceRefs = st.EvidenceRefs[:20]
	}
	p := sweepPayload{Verdict: res.Verdict, Scanned: res.Scanned, Sections: make([]sweepSection, 0, len(res.Sections))}
	for _, s := range res.Sections {
		p.Sections = append(p.Sections, sweepSection{Name: s.Name, State: s.State, Summary: truncRunes(s.Summary, 200)})
	}
	if raw, err := json.Marshal(p); err == nil && len(raw) <= maxStepPayload {
		st.Payload = raw
	}
	return st, findings, res.Warnings
}
