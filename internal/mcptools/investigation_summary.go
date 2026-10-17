package mcptools

// investigation_summary.go — v0.10.1287 (AI SRE Faz 1b): get_problem'in
// `investigation` alanı. Kaydın kendisi internal/sre'de (saved_views
// page='sre-investigation'); mcptools onu import edemez (sre → mcptools), o
// yüzden özet tipi burada, doldurucu Deps.Investigation (api/mcp_deps.go).
//
// Sohbet yeniden kullanımı: model get_problem'de bulguları ve AI özetini
// görür; aynı taramayı yeniden koşmak yerine bulguya göre ilerler. Özet
// ÖNERİDİR — hiçbir alan problem satırına ya da önceliğe yazılmaz.

import (
	"context"
	"time"
)

// InvestigationSummary — get_problem.investigation.
type InvestigationSummary struct {
	At         int64    `json:"at"` // unix ns — kaydın bittiği (yoksa başladığı) an
	AtISO      string   `json:"atIso,omitempty"`
	State      string   `json:"state"` // queued | running | done | partial | shared | error | skipped
	Findings   []string `json:"findings"`
	Summary    string   `json:"summary,omitempty"`
	SharedFrom string   `json:"sharedFrom,omitempty"`
	// Hypotheses — v0.10.1288 (AI SRE Faz 2): ajanın KOD skorlu en olası ≤3
	// hipotezi (skor sıralı). Model skoru değiştirmez; "doğrula" önerisi
	// yalnız sunucunun tıklanır aksiyon bloğundan (sre_hypothesis) gelir.
	Hypotheses []InvestigationHypothesis `json:"hypotheses,omitempty"`
}

// InvestigationHypothesis — get_problem.investigation.hypotheses[i].
type InvestigationHypothesis struct {
	Kind     string   `json:"kind"`
	Label    string   `json:"label"`
	Category string   `json:"category"`
	Score    float64  `json:"score"`
	Band     string   `json:"band"`               // yüksek | orta | düşük
	Support  []string `json:"support,omitempty"`  // doğrulayan kontrollerin gerekçeleri (≤2)
	Against  []string `json:"against,omitempty"`  // çürüten kontrollerin gerekçeleri (≤2)
	Operator string   `json:"operator,omitempty"` // dismissed | confirmed (operatör işareti; skoru değiştirmez)
}

// investigationFindingsMax — sohbete giden bulgu tavanı.
const investigationFindingsMax = 3

// investigationHypothesesMax — sohbete giden hipotez tavanı (kartla aynı).
const investigationHypothesesMax = 3

// problemInvestigationField — Deps.Investigation'dan get_problem alanı.
// Okuyucu yok / kayıt yok / okuma hatası → ok=false (alan yazılmaz; hata
// get_problem'i düşürmez).
func problemInvestigationField(ctx context.Context, d Deps, problemID string) (*InvestigationSummary, bool) {
	if d.Investigation == nil || problemID == "" {
		return nil, false
	}
	sum, err := d.Investigation(ctx, problemID)
	if err != nil || sum == nil || sum.State == "" {
		return nil, false
	}
	out := *sum
	if len(out.Findings) > investigationFindingsMax {
		out.Findings = out.Findings[:investigationFindingsMax]
	}
	if out.Findings == nil {
		out.Findings = []string{}
	}
	out.Summary = truncateRunes(out.Summary, 1200)
	if len(out.Hypotheses) > investigationHypothesesMax {
		out.Hypotheses = out.Hypotheses[:investigationHypothesesMax]
	}
	if out.At > 0 && out.AtISO == "" {
		out.AtISO = time.Unix(0, out.At).UTC().Format(time.RFC3339)
	}
	return &out, true
}
