package sre

// steps.go — v0.10.1282 (Faz 0a) / v0.10.1287 (Faz 1b) — adım sonuçlarını
// kayda çeviren SAF yardımcılar: mcptools zarfı → Step + bulgular + kanıt
// künyeleri. Okumanın kendisi orchestrator.go'da; burada I/O yok.
//
// Durum eşlemesi (tüm adımlar): zarf ok + kısmi değil → ok; ok + kısmi →
// partial; ok değil / hata → error. "veri yok" (empty) ok sayılır — bakıldı,
// yok (okunamadı ≠ yok).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

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

// evidenceOf — SAF: bileşik aracın sonucundan kanıt künyeleri (sohbet çipiyle
// aynı dönüşüm: mcptools.ToolResultEvidenceList — href dahil).
func evidenceOf(tool string, res any) []mcptools.ChatEvidence {
	raw, err := json.Marshal(res)
	if err != nil {
		return nil
	}
	evs, ok := mcptools.ToolResultEvidenceList(tool, string(raw))
	if !ok {
		return nil
	}
	if len(evs) > maxStepEvidence {
		evs = evs[:maxStepEvidence]
	}
	return evs
}

// sweepStep — SAF: tarama sonucu → adım + bulgular + uyarılar.
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
		findings = append(findings, Finding{Kind: f.Kind, Name: f.Name, Level: f.Level, Reasons: f.Reasons, EvidenceRefs: f.EvidenceRefs, Step: stepSweep})
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
	st.Evidence = evidenceOf(mcptools.ServiceHealthSweepName, res)
	return st, findings, res.Warnings
}

// sectionStepState — SAF: tarama bölüm durumu → adım durumu.
func sectionStepState(s string) string {
	switch s {
	case "ok", "empty":
		return StepOK
	case "skipped":
		return StepSkipped
	case "error", "timeout":
		return StepError
	}
	return StepPartial
}

// deployStep — SAF: deploy kontrolü → adım + bulgular.
func deployStep(dc mcptools.DeployCheck, err error) (Step, []Finding) {
	st := Step{Name: stepDeploys}
	if err != nil {
		st.State, st.Summary = StepError, truncRunes(err.Error(), 400)
		return st, nil
	}
	st.State = sectionStepState(dc.Section.State)
	st.Summary = truncRunes(dc.Section.Summary, 400)
	if dc.Section.Note != "" {
		st.Summary = truncRunes(st.Summary+" ("+dc.Section.Note+")", 500)
	}
	st.Evidence = dc.Evidences
	var out []Finding
	for _, f := range dc.Findings {
		out = append(out, Finding{Kind: f.Kind, Name: f.Name, Level: f.Level, Reasons: f.Reasons, EvidenceRefs: f.EvidenceRefs, Step: stepDeploys})
	}
	type row struct {
		At      string `json:"at"`
		Source  string `json:"source"`
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
		Prev    string `json:"prev,omitempty"`
		Status  string `json:"status,omitempty"`
	}
	rows := make([]row, 0, len(dc.Rows))
	for _, r := range dc.Rows {
		rows = append(rows, row{At: r.TimeISO, Source: r.Source, Name: firstNonEmpty(r.Workload, r.Service), Version: r.Version, Prev: r.PrevVersion, Status: r.Status})
	}
	if raw, err := json.Marshal(map[string]any{"rows": rows}); err == nil && len(raw) <= maxStepPayload {
		st.Payload = raw
	}
	return st, out
}

// pivotKind — pivot halkası → bulgu türü.
func pivotKind(hop string) string {
	switch hop {
	case "pods":
		return "pod"
	case "logs":
		return "log"
	case "op_baseline":
		return "operation"
	}
	return hop
}

// hopRefs — SAF: kanıt künyelerinde halka → "evidences[i]" (ilk eşleşme).
func hopRefs(evs []mcptools.ChatEvidence) map[string]string {
	out := map[string]string{}
	for i, e := range evs {
		if _, ok := out[e.Hop]; !ok && e.Hop != "" {
			out[e.Hop] = fmt.Sprintf("evidences[%d]", i)
		}
	}
	return out
}

// pivotStep — SAF: pivot sonucu → adım + bulgular. via: temsilci trace'in
// kaynağı (hipotez / rollup exemplar / pivotun kendi seçimi).
func pivotStep(res mcptools.PivotResult, err error, via string) (Step, []Finding) {
	st := Step{Name: stepPivot}
	if err != nil {
		st.State, st.Summary = StepError, truncRunes(err.Error(), 400)
		return st, nil
	}
	if !res.OK || res.Error != nil {
		st.State = StepError
		msg := res.Message
		if msg == "" && res.Error != nil {
			msg = res.Error.Message
		}
		st.Summary = truncRunes(firstNonEmpty(msg, "pivot sonuç vermedi"), 400)
		return st, nil
	}
	st.State = StepOK
	if res.Partial {
		st.State = StepPartial
	}
	st.Evidence = evidenceOf(mcptools.PivotFromTraceName, res)
	refs := hopRefs(st.Evidence)
	switch {
	case res.Trace != nil:
		root := firstNonEmpty(res.Trace.Root, res.Trace.Service)
		st.Summary = fmt.Sprintf("%s — %.0f ms, %d span (%d hatalı) · trace %s", root, res.Trace.WallMs, res.Trace.SpanCount, res.Trace.ErrorSpans, shortID(res.Trace.TraceID))
	case res.Message != "":
		st.Summary = res.Message
	default:
		st.Summary = "temsilci trace bulunamadı"
	}
	if via != "" {
		st.Summary += " (" + via + ")"
	}
	var out []Finding
	for i, f := range res.Findings {
		if i >= 5 {
			break
		}
		fd := Finding{Kind: pivotKind(f.Hop), Name: f.Subject, Level: f.Level, Step: stepPivot}
		if f.Reason != "" {
			fd.Reasons = []string{f.Reason}
		}
		if r, ok := refs[f.Hop]; ok {
			fd.EvidenceRefs = []string{r}
		}
		out = append(out, fd)
	}
	if len(out) > 0 {
		st.Summary += fmt.Sprintf("; %d bulgu", len(out))
	}
	st.Summary = truncRunes(st.Summary, 500)
	return st, out
}

// spikeFindingFactor — log artışı katkıları ancak artış bu katın üstündeyse
// bulgu olur (verilen aralık "artış doğrulanamadı" iken katkı bulgu değil).
const spikeFindingFactor = 1.5

// spikeStep — SAF: log artışı sonucu → adım + bulgular (≤3 katkı).
func spikeStep(res mcptools.LogSpikeResult, err error) (Step, []Finding) {
	st := Step{Name: stepLogSpike}
	if err != nil {
		st.State, st.Summary = StepError, truncRunes(err.Error(), 400)
		return st, nil
	}
	if !res.OK || res.Error != nil {
		st.State = StepError
		msg := res.Message
		if msg == "" && res.Error != nil {
			msg = res.Error.Message
		}
		st.Summary = truncRunes(firstNonEmpty(msg, "log artışı okunamadı"), 400)
		return st, nil
	}
	st.State = StepOK
	if res.Partial {
		st.State = StepPartial
	}
	st.Evidence = evidenceOf(mcptools.ExplainLogSpikeName, res)
	refs := hopRefs(st.Evidence)
	hm := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("15:04") }
	var parts []string
	if sp := res.Spike; sp != nil {
		parts = append(parts, fmt.Sprintf("%s–%s UTC ×%.1f (dakikada %.0f → %.0f)", hm(sp.FromMs), hm(sp.ToMs), sp.Factor, sp.RatePerMinBase, sp.RatePerMinSpike))
	}
	if res.Message != "" {
		parts = append(parts, res.Message)
	}
	var out []Finding
	if res.Spike != nil && res.Spike.Factor >= spikeFindingFactor {
		for _, c := range res.Contributors {
			if len(out) >= 3 {
				break
			}
			f := Finding{Kind: "log", Name: truncRunes(c.Dim+"="+c.Value, 120), Level: "warn", Reasons: []string{c.Reason}, Step: stepLogSpike}
			if r, ok := refs["fields"]; ok {
				f.EvidenceRefs = []string{r}
			}
			if c.Dim == "pattern" {
				if r, ok := refs["patterns"]; ok {
					f.EvidenceRefs = []string{r}
				}
			}
			out = append(out, f)
		}
		if len(res.Contributors) > 0 {
			parts = append(parts, res.Contributors[0].Reason)
		} else {
			parts = append(parts, "artış tek bir boyutta yoğunlaşmıyor")
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "belirgin artış yok")
	}
	st.Summary = truncRunes(strings.Join(parts, "; "), 500)
	return st, out
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
