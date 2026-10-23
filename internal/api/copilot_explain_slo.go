package api

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// copilot_explain_slo.go — POST /api/copilot/explain-slo/{id} (v0.9.1083
// yörünge kanıtı; v0.10.794'te api.go'dan buraya taşındı — ratchet aşağı).
// Burn pencereleri chstore.BurnExplainPolicy'den: evaluator alarmıyla aynı çift.

func (s *Server) copilotExplainSLO(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	slo, err := s.store.GetSLO(r.Context(), id)
	if err != nil || slo == nil {
		http.Error(w, "SLO not found", http.StatusNotFound)
		return
	}
	status, _ := s.store.ComputeSLOStatus(r.Context(), *slo)
	// Two-window burn samples — Workbook multi-burn-rate pattern.
	// v0.10.794 — pencereler evaluator'ın critical bandıyla AYNI
	// (chstore.BurnExplainPolicy): önce 5 dk / 1 sa ölçülüyor, problemde
	// 1 sa / 6 sa görünüyordu; operatör iki yerde iki farklı "fast burn"
	// okuyordu.
	pol := chstore.BurnExplainPolicy()
	fastRate, fastTotal, _ := s.store.ComputeSLOBurnRate(r.Context(), *slo, pol.FastWindow)
	slowRate, slowTotal, _ := s.store.ComputeSLOBurnRate(r.Context(), *slo, pol.SlowWindow)

	var sb strings.Builder
	fmt.Fprintf(&sb, "SLO: %s (service=%s)\n", slo.Name, slo.Service)
	switch slo.SLIType {
	case "latency":
		fmt.Fprintf(&sb, "Type: latency, threshold=%.0fms\n", slo.ThresholdMs)
	default:
		fmt.Fprintf(&sb, "Type: %s\n", slo.SLIType)
	}
	fmt.Fprintf(&sb, "Target: %.3f%% over %d-day rolling window\n",
		slo.Target*100, slo.WindowDays)
	if slo.Operation != "" {
		fmt.Fprintf(&sb, "Scope: operation=%q\n", slo.Operation)
	}
	if status != nil {
		fmt.Fprintf(&sb,
			"Current: SLI=%.3f%% (%d good / %d total) · budget remaining=%.2f%% · long-window burn=%.2f · healthy=%v\n",
			status.SLI*100, status.Good, status.Total,
			status.BudgetRemaining*100, status.BurnRate, status.Healthy)
	}
	fmt.Fprintf(&sb, "Fast burn (%s): rate=%.2f, n=%d (alarm threshold %.1fx)\n", pol.FastWindow, fastRate, fastTotal, pol.FastRate)
	fmt.Fprintf(&sb, "Slow burn (%s): rate=%.2f, n=%d (alarm threshold %.1fx)\n", pol.SlowWindow, slowRate, slowTotal, pol.SlowRate)
	// v0.9.1083 (F3.4) — yörünge artık DETERMİNİSTİK girdide: /forecast
	// ve /burn-series uçları vardı ama anlatım beslemesi yoktu; prompt
	// "Y hours to exhaustion" isteyince model Y'yi UYDURUYORDU. Soft-
	// fail: üretilemezse kanıt bunu açıkça söyler, model de öyle der.
	fc, _ := s.store.ComputeSLOForecast(r.Context(), *slo, time.Hour)
	series, _ := s.store.ComputeSLOBurnSeries(r.Context(), *slo, 7)
	sb.WriteString(sloTrajectoryEvidence(fc, series))

	r, xid := withExchange(r)
	out, err := s.copilotExplain(r,
		copilot.SystemPromptSLOBurn(), sb.String())
	if err != nil {
		writeErr(w, err)
		return
	}
	// v0.10.1476 — alarm eşiği karşılaştırması SUNUCUDAN (aynı kanıt metni).
	out = sloAnswerWithBurnVerdict(sb.String(), out)
	writeJSON(w, map[string]any{
		"explanation": out,
		"exchangeId":  xid,
		"status":      status,
		"fastBurn":    fastRate,
		"slowBurn":    slowRate,
		// v0.10.794 — FE etiketi bu pencereleri basar; eski istemci alanı görmezse
		// etiketsiz düşer (rolling deploy).
		"fastWindowS": int(pol.FastWindow.Seconds()),
		"slowWindowS": int(pol.SlowWindow.Seconds()),
		"fastRate":    pol.FastRate,
		"slowRate":    pol.SlowRate,
	})
}

// sloBurnLineRe — kanıttaki yakım satırı; biçim yukarıdaki Fprintf'lerle
// birebir ("Fast burn (1h0m0s): rate=16.80, n=11950 (alarm threshold 14.4x)").
var sloBurnLineRe = regexp.MustCompile(`(?m)^(Fast|Slow) burn \(([^)]+)\): rate=([0-9]+(?:\.[0-9]+)?), n=([0-9]+) \(alarm threshold ([0-9]+(?:\.[0-9]+)?)x\)`)

// sloAnswerWithBurnVerdict — SAF, v0.10.1476 (prod evalset 2026-10-23,
// slo-burn-above-alarm-cites-threshold "missing: 14"): yakım oranlarının
// alarm eşikleriyle karşılaştırması model anlatısına bırakılmaz — sunucu
// kanıt metnindeki iki satırdan tek bir deterministik satır kurar ve
// cevabın BAŞINA koyar ("sunucu toplar, model anlatır"). Sayılar kanıttaki
// yazımıyla aynen; n=0 penceresi "veri yok". Satır çözülemezse cevap aynen.
// Evalset SLOBurn yüzeyi aynı fonksiyonu aynı girdiyle çağırır
// (evalServerCompose) — puanlanan, operatörün gördüğü metindir.
func sloAnswerWithBurnVerdict(evidence, answer string) string {
	var parts []string
	for _, m := range sloBurnLineRe.FindAllStringSubmatch(evidence, 2) {
		label := "hızlı yakım"
		if m[1] == "Slow" {
			label = "yavaş yakım"
		}
		win := m[2]
		if d, err := time.ParseDuration(m[2]); err == nil {
			win = sloWindowTR(d)
		}
		if m[4] == "0" {
			parts = append(parts, fmt.Sprintf("%s (%s): veri yok — eşik %sx", label, win, m[5]))
			continue
		}
		rate, err1 := strconv.ParseFloat(m[3], 64)
		thr, err2 := strconv.ParseFloat(m[5], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		side := "eşiğin altında"
		if rate >= thr {
			side = "eşiğin ÜSTÜNDE"
		}
		parts = append(parts, fmt.Sprintf("%s (%s) %sx — alarm eşiği %sx, %s", label, win, m[3], m[5], side))
	}
	if len(parts) == 0 {
		return answer
	}
	return "**Alarm eşikleri:** " + strings.Join(parts, " · ") + "\n\n" + answer
}

// sloWindowTR — "1h0m0s" → "1 sa", "30m0s" → "30 dk".
func sloWindowTR(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%d sa", int(d/time.Hour))
	}
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d dk", int(d/time.Minute))
	}
	return d.String()
}
