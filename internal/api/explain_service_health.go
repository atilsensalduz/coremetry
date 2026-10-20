package api

// explain_service_health.go — servis sağlığı ✨ Explain ("AI triage";
// POST /api/copilot/explain-service?service=&from=&to=).
//
// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."): api.go'dan taşındı; buildServiceHealthExplain'in
// iki çağıranı (HTTP düğmesi + sohbetin açıklama turu, pencere `?ai=` kodeğinin
// service-health:<svc>:<fromNs>:<toNs> segmentlerinden) aynı explainBuilt'i —
// yani aynı istem çiftini ve explainCacheKey(SystemPromptServiceHealth(), user,
// "") anahtarını — kullanır. Gövde v0.10.1404'teki handler'ın aynısı.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// buildServiceHealthExplain — servisin üç RED serisi (RPS / hata oranı / P99)
// + açık problemleri, verilen pencerede.
func (s *Server) buildServiceHealthExplain(ctx context.Context, service string, from, to time.Time) (explainBuilt, error) {
	st := s.explainReads()
	// Service-name predicate as a structured filter — the
	// SpanMetricFilter API takes []FilterExpr, not a DSL
	// string. One filter chip is all we need here.
	svcFilter := []chstore.FilterExpr{{Key: "service.name", Op: "=", Values: []string{service}}}
	// v0.9.392 (Faz B-2) — üç ayrı sorgu tek Multi'ye indi: aynı WHERE,
	// tek CH taraması (SPA'nın batch yolu ile aynı şekil). Soft-fail aynı.
	redM, _, _ := st.QuerySpanMetricMulti(ctx, chstore.SpanMetricBatchFilter{
		Filters: svcFilter, From: from, To: to, GroupBy: []string{"name"},
		Aggs: []chstore.SpanMetricAggSpec{
			{Name: "rate", Aggregation: "rate"},
			{Name: "error_rate", Aggregation: "error_rate"},
			{Name: "p99", Aggregation: "p99", Field: "duration_ms"},
		},
	})
	rps, errs, p99 := redM["rate"], redM["error_rate"], redM["p99"]

	// Active problems for context.
	probs, _ := st.ListProblems(ctx, chstore.ProblemFilter{
		Service: service, Status: "open", Limit: 20,
	})
	var probLines string
	for _, p := range probs {
		probLines += fmt.Sprintf("  • [%s] %s — %s: value=%.2f%s threshold=%.2f%s\n",
			strings.ToUpper(p.Severity), p.RuleName, p.Metric, p.Value, problemMetricUnitTR(p.Metric), p.Threshold, problemMetricUnitTR(p.Metric))
	}

	payload, _ := json.Marshal(map[string]any{
		"service":              service,
		"window":               fmt.Sprintf("%s → %s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)),
		"rpsByOperation":       summarizeHealthSeries(rps, 3),
		"errorRateByOperation": summarizeHealthSeries(errs, 3),
		"p99MsByOperation":     summarizeHealthSeries(p99, 3),
		"openProblems":         len(probs),
	})
	user := fmt.Sprintf("Service health snapshot:\n%s\n%s",
		string(payload),
		func() string {
			if probLines == "" {
				return ""
			}
			return "Active problems:\n" + probLines
		}(),
	)
	b := newExplainBuilt(copilot.SystemPromptServiceHealth(), user)
	b.Label = service
	b.Service = service
	b.Links = []guidedAnswerLink{{Label: service + " servisini aç", Href: "/service?name=" + url.QueryEscape(service)}}
	return b, nil
}

// healthSeriesSummary — bir operasyon serisinin şekli (min / max / ort / son).
type healthSeriesSummary struct {
	Name    string  `json:"name"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Avg     float64 `json:"avg"`
	Current float64 `json:"current"`
}

// summarizeHealthSeries — SAF: seriyi küçük bir JSON şekline sıkıştırır.
// Compact the series into a small JSON blob the LLM can reason about — full
// point lists are token-heavy and the model only needs the shape (min / max /
// current / trend) per operation. We pick the top-N operations by rate so a
// noisy 50-operation service doesn't dominate the prompt. (v0.10.1405 —
// handler'ın kapanışından çıkarıldı; gövde ve alan sırası aynı.)
func summarizeHealthSeries(rows []chstore.SpanMetricSeries, topN int) []healthSeriesSummary {
	type bag struct {
		name string
		sum  float64
		cnt  int
		mn   float64
		mx   float64
		last float64
	}
	bags := []bag{}
	for _, s := range rows {
		b := bag{name: strings.Join(s.GroupKey, " / "), mn: 1e300}
		for _, p := range s.Points {
			b.sum += p.Value
			b.cnt++
			if p.Value < b.mn {
				b.mn = p.Value
			}
			if p.Value > b.mx {
				b.mx = p.Value
			}
			b.last = p.Value
		}
		if b.cnt > 0 {
			bags = append(bags, b)
		}
	}
	// Sort by sum desc, top N.
	for i := 0; i < len(bags); i++ {
		for j := i + 1; j < len(bags); j++ {
			if bags[j].sum > bags[i].sum {
				bags[i], bags[j] = bags[j], bags[i]
			}
		}
	}
	if len(bags) > topN {
		bags = bags[:topN]
	}
	out := make([]healthSeriesSummary, 0, len(bags))
	for _, b := range bags {
		avg := 0.0
		if b.cnt > 0 {
			avg = b.sum / float64(b.cnt)
		}
		out = append(out, healthSeriesSummary{
			Name: b.name, Min: b.mn, Max: b.mx, Avg: avg, Current: b.last,
		})
	}
	return out
}

// copilotExplainServiceHealth feeds the three RED series
// (RPS / error rate / P99 latency) for one service + the
// window's deploy markers + currently-open problems to the
// LLM and asks for a "is this healthy right now?" triage
// summary. Operator hits this when staring at a chart and
// wants a sanity-check — distinct from explain-problem
// (which fires on a specific alert) because the chart may
// look fine and the answer should say so plainly.
func (s *Server) copilotExplainServiceHealth(w http.ResponseWriter, r *http.Request) {
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service == "" {
		http.Error(w, `{"error":"service required"}`, http.StatusBadRequest)
		return
	}
	from, to := parseFromTo(r, time.Hour)
	b, err := s.buildServiceHealthExplain(r.Context(), service, from, to)
	if err != nil {
		writeExplainBuildErr(w, err)
		return
	}
	r, xid := withExchange(r)
	s.deliverExplain(w, r, xid, nil, s.explainPrompt(r, b.System, b.User), "", b.CacheKey)
}
