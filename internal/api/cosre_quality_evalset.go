package api

// cosre_quality_evalset.go — CoSRE öğrenme Faz 4: haftanın en kötü
// cevaplarından regresyon vakası dosyası (coremetry.evalset/1). Prompt /
// model değişikliğinde aynı sorular yeniden koşulur:
//   expect.expectedTools — o turda BAŞARIYLA çalışan tool'lar (beklenen yol),
//   expect.keyFacts      — hakemin "grounded" bulduğu olgu belirteçleri,
//   expect.mustNotContain— hakemin "ungrounded" bulduğu belirteçler (cevap
//                          aynı uydurmayı tekrarlamasın).
// Kaynak 👍/👎 değil, örtük sinyal + çevrimdışı hakem — kullanıcı geri
// bildirimi seyrek. keyFacts makine önerisidir: repoya alınmadan önce insan
// gözden geçirir (dosya provenance'ı taşır). Admin kapılı, audit'li.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// qualityEvalsetCase — SAF: en kötü cevap + tur + hakem → evalset vakası.
func qualityEvalsetCase(w chstore.CosreWorstAnswer, inter chstore.CosreInteraction, g *chstore.CosreGrounding) (evalsetExportCase, bool) {
	q := strings.TrimSpace(inter.Question)
	if q == "" {
		q = strings.TrimSpace(w.Question)
	}
	if q == "" {
		return evalsetExportCase{}, false
	}
	tools := []string{}
	seen := map[string]bool{}
	for _, t := range inter.Tools {
		if t.OK && !seen[t.Name] {
			seen[t.Name] = true
			tools = append(tools, t.Name)
		}
	}
	facts, forbidden := []string{}, []string{}
	if g != nil && g.Details != "" {
		var claims []struct{ Label, Fact string }
		if json.Unmarshal([]byte(g.Details), &claims) == nil {
			for _, c := range claims {
				f := strings.TrimSpace(c.Fact)
				if f == "" {
					continue
				}
				switch c.Label {
				case "grounded":
					facts = append(facts, f)
				case "ungrounded":
					forbidden = append(forbidden, f)
				}
			}
		}
	}
	expect := map[string]any{"expectedTools": tools, "keyFacts": facts}
	if len(forbidden) > 0 {
		expect["mustNotContain"] = forbidden
	}
	answer := inter.Answer
	if answer == "" {
		answer = w.Answer
	}
	return evalsetExportCase{
		ID:       "q-" + w.ExchangeID,
		Surface:  evalSurfaceFromLabel("chat"),
		Why:      fmt.Sprintf("örtük kalite puanı %.2f; sinyaller: %s", w.Score, strings.Join(w.Reasons, ", ")),
		Prompt:   q,
		Response: answer,
		Expect:   expect,
		Provenance: evalsetProvenance{
			ExchangeID: w.ExchangeID, SurfaceLabel: "cosre-turn:" + w.Tier,
			CreatedAt: w.CreatedAt.UTC().Format(time.RFC3339Nano), Model: w.Model,
			PromptChars: uint32(len(q)), ResponseChars: uint32(len(answer)),
		},
	}, true
}

func (s *Server) exportCosreQualityEvalset(w http.ResponseWriter, r *http.Request) {
	days := cosreQualityDays(r.URL.Query().Get("days"))
	to := time.Now()
	from := to.UTC().AddDate(0, 0, -days)
	worst, err := s.store.WorstCosreAnswers(r.Context(), from, 20)
	if err != nil {
		writeErr(w, err)
		return
	}
	ids := make([]string, 0, len(worst))
	for _, x := range worst {
		ids = append(ids, x.ExchangeID)
	}
	inter, err := s.store.CosreInteractionsForEval(r.Context(), ids, from.Add(-time.Hour))
	if err != nil {
		writeErr(w, err)
		return
	}
	ground, err := s.store.CosreGroundingByExchange(r.Context(), ids, from.Add(-time.Hour))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := evalsetExportPayload{
		Schema: evalsetExportSchema, Source: "cosre-quality",
		ExportedAt: to.UTC().Format(time.RFC3339), CoremetryVersion: s.version,
		RangeS: int64(days) * 86400, Cases: []evalsetExportCase{},
	}
	for _, x := range worst {
		var gp *chstore.CosreGrounding
		if g, ok := ground[x.ExchangeID]; ok {
			gp = &g
		}
		c, ok := qualityEvalsetCase(x, inter[x.ExchangeID], gp)
		if !ok {
			out.Dropped++
			continue
		}
		out.Cases = append(out.Cases, c)
	}
	fname := fmt.Sprintf("coremetry-evalset-quality-%s.json", to.UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return
	}
	s.audit(r, "ai.evalset.export", "ai_evalset", "cosre-quality",
		fmt.Sprintf(`{"cases":%d,"dropped":%d,"days":%d}`, len(out.Cases), out.Dropped, days))
}
