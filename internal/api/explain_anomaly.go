package api

// explain_anomaly.go — anomaly ✨ Explain (POST /api/copilot/explain-anomaly/{id}).
//
// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."): api.go'dan taşındı; buildAnomalyExplain'in iki
// çağıranı (HTTP düğmesi + sohbetin açıklama turu) aynı explainBuilt'i — yani
// aynı istem çiftini ve explainCacheKey(SystemPromptAnomaly(), user, "")
// anahtarını — kullanır. Gövde v0.10.1404'teki handler'ın aynısı.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// buildAnomalyExplain — log-pattern / trace-op anomali satırının girdisi.
// Bulunamayan anomali → explainNotFound("anomaly not found").
func (s *Server) buildAnomalyExplain(ctx context.Context, id string) (explainBuilt, error) {
	st := s.explainReads()
	// v0.9.343 — ask for the row, don't page 30 days and scan.
	//
	// The old comment argued "anomalies have short retention (30d) so the
	// universe is small", but the read was capped at 2000 rows AND — since
	// v0.9.326 — ordered `status = 'active' DESC, last_seen DESC`. So a
	// CLEARED anomaly past the 2000th row could not be explained: the handler
	// answered "anomaly not found" for a row the operator had open in front
	// of them. GetAnomalyEvent does WHERE id = ?, which is both correct and
	// cheaper than the window it replaces.
	ev, err := st.GetAnomalyEvent(ctx, id, 0)
	if err != nil {
		return explainBuilt{}, err
	}
	if ev == nil {
		return explainBuilt{}, explainNotFound("anomaly not found")
	}
	user := fmt.Sprintf(
		"Anomaly kind: %s\nPattern: %s\nService: %s\nPeak ratio: %.2fx baseline\nCurrent ratio: %.2fx\nCurrent count: %d\nSample: %s",
		ev.Kind, ev.Pattern, ev.Service,
		ev.PeakRatio, ev.CurrentRatio, ev.CurrentCount,
		truncate(ev.Sample, 600),
	)
	// v0.9.418 (CoSRE fikir #3 — korelasyon zinciri): explain-problem'ın
	// üçlüsü artık anomaly'de de. Sıra bilinçli: deterministik hipotez
	// ÖNCE (model onu birincil kanıt sayar), sonra deploy penceresi,
	// sonra topology komşuları + hata-trace örnekleri. Hepsi best-effort.
	if hyp, herr := st.GetHypothesis(ctx, "anomaly", ev.ID); herr == nil {
		if block := anomaly.HypothesisPromptBlockTR(hyp); block != "" {
			user += "\n" + block
		}
	}
	if ev.Service != "" {
		started := time.Unix(0, ev.StartedAt)
		if deps, derr := st.GetServiceDeploys(ctx, ev.Service,
			started.Add(-6*time.Hour), time.Unix(0, ev.LastSeen)); derr == nil && len(deps) > 0 {
			if parts := anomaly.PickDeploysAroundStart(deps, ev.StartedAt); len(parts) > 0 {
				user += "\n\nAynı servisin yakın DEPLOY'ları: " + fmt.Sprintf("%v", parts)
			}
		}
		user += s.serviceCorrelationContext(ctx, ev.Service, started)
		user += "\n\nZİNCİRİ anlat: anomali → (varsa) tetikleyen deploy → komşu servislere etkisi. Yönü belirt (upstream'den mi geldi, downstream'e mi yayılıyor); kanıt yoksa 'bilinmiyor' de."
	}
	b := newExplainBuilt(copilot.SystemPromptAnomaly(), user)
	b.Label = clipRunes(firstNonEmpty(ev.Pattern, ev.Kind), chatExplainLabelMax)
	b.Service = ev.Service
	// anomalyDetailHref (lib/inboxHref.ts) — anomalinin tam sayfa detayı.
	b.Links = []guidedAnswerLink{{Label: "Anomaliyi aç", Href: "/inbox?anomaly=" + url.QueryEscape(ev.ID)}}
	return b, nil
}

// copilotExplainAnomaly handles log-pattern / trace-op anomaly
// rows. Different model prompt — anomalies are soft signals
// ("something changed") so the response should help an
// operator decide whether to act, not assume something is
// broken.
func (s *Server) copilotExplainAnomaly(w http.ResponseWriter, r *http.Request) {
	b, err := s.buildAnomalyExplain(r.Context(), r.PathValue("id"))
	if err != nil {
		writeExplainBuildErr(w, err)
		return
	}
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, nil, s.explainPrompt(r, b.System, b.User), "", b.CacheKey)
}
