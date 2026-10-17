package sre

// narrate.go — v0.10.1287 (AI SRE Faz 1b): araştırmanın TEK anlatım çağrısı.
//
// Yüzey "sre-investigation" (/ai sayfasında ayrı satır), UserID "system".
// Sistem promptu copilot.SystemPromptSREInvestigation (Türkçe-native, dört
// bölüm: Ne oldu / Kanıt / Muhtemel neden / Sonraki adım + tek örnek + veri
// çerçevesi). Girdi deterministik renderInvestigationTR (≤4 KB, serbest metin
// EN SONDA).
//
// Arka plan AI kapıları (ProblemExplainer emsali, anomaly/problem_explainer.go):
// Active (yapılandırılmış + açık), AutoExplainEnabled (Settings → AI otomatik
// açıklama), QuotaBackoffActive (429 sonrası 1 saat), sre_agent.MaxLLMCallsPerHour
// (saatlik sayaç). Kapı kapalı → çağrı YOK, özet "AI özeti yok (otomatik AI
// kapalı)" + nedeni. Bütçe: araştırma başına ≤1 çağrı (resume taze özeti
// yeniden üretmez).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/rca"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// NarrationSurface — ai_calls.surface.
const NarrationSurface = "sre-investigation"

// NarrationGatedText — kapı kapalıyken özetin metni (tasarım sabiti).
const NarrationGatedText = "AI özeti yok (otomatik AI kapalı)"

// narrationMinLeft — bütçede bundan az süre kaldıysa çağrı denenmez.
const narrationMinLeft = 5 * time.Second

// renderMaxBytes — anlatım girdisinin tavanı.
const renderMaxBytes = 4 << 10

// Narrator — *copilot.Service karşılar (test dikişi).
type Narrator interface {
	Active() bool
	AutoExplainEnabled() bool
	QuotaBackoffActive() bool
	ActiveModel() string
	Explain(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// narrationGate — SAF: arka plan AI çağrısı yapılabilir mi; değilse nedeni.
func narrationGate(n Narrator, cfg Settings, usedThisHour int) (bool, string) {
	switch {
	case n == nil || !n.Active():
		return false, "AI yapılandırılmamış ya da kapalı"
	case !n.AutoExplainEnabled():
		return false, "otomatik AI açıklaması kapalı (Ayarlar → AI)"
	case n.QuotaBackoffActive():
		return false, "sağlayıcı kotası doldu — arka plan AI 1 saat bekliyor"
	case usedThisHour >= cfg.MaxLLMCallsPerHour:
		return false, fmt.Sprintf("saatlik AI çağrı tavanı doldu (%d)", cfg.MaxLLMCallsPerHour)
	}
	return true, ""
}

// narrate — anlatım adımı. Kayda dokunmaz; dönen adımı koordinatör yazar,
// özeti inv.Summary'ye bu işlev koyar (koordinatör goroutine'inde çağrılır).
func narrate(ctx context.Context, rd readers, inv *Investigation, p chstore.Problem, now func() time.Time) Step {
	st := Step{Name: stepSummary}
	t0 := now()
	finish := func() Step {
		end := now()
		st.DurationMs, st.At = end.Sub(t0).Milliseconds(), end.UnixNano()
		return st
	}
	cfg := CurrentSettings()
	if rd.settings != nil {
		cfg = rd.settings()
	}
	ok, why := narrationGate(rd.narrator, cfg, rd.budget.used(ctx))
	if !ok {
		inv.Summary = &Narration{Text: NarrationGatedText, At: t0.UnixNano(), Gated: true, Reason: why}
		st.State, st.Summary = StepSkipped, NarrationGatedText+" — "+why
		return finish()
	}
	if dl, has := ctx.Deadline(); (has && time.Until(dl) < narrationMinLeft) || ctx.Err() != nil {
		st.State, st.Summary = StepError, "süre bütçesi doldu — AI özeti istenmedi"
		return finish()
	}
	user := renderInvestigationTR(*inv, p, tzdefault.Location())
	known := []string{p.Service}
	for _, f := range inv.Findings {
		known = append(known, f.Name)
	}
	nctx, cancel := context.WithTimeout(ctx, capSummary)
	defer cancel()
	nctx = copilot.WithMeta(nctx, copilot.CallMeta{
		Surface: NarrationSurface, UserID: "system",
		Shield: func(prompt, answer string) uint8 {
			return rca.CountUnknownEntities(rca.LowerKnownSet(known...), prompt, answer)
		},
	})
	rd.budget.inc(ctx)
	text, err := rd.narrator.Explain(nctx, copilot.SystemPromptSREInvestigation(), user)
	text = strings.TrimSpace(text)
	if err != nil || text == "" {
		msg := "boş cevap"
		if err != nil {
			msg = err.Error()
		}
		st.State, st.Summary = StepError, truncRunes("AI özeti alınamadı: "+msg, 400)
		return finish()
	}
	inv.Summary = &Narration{Text: truncRunes(text, 4000), Model: rd.narrator.ActiveModel(), At: now().UnixNano()}
	st.State, st.Summary = StepOK, truncRunes(firstLine(text), 300)
	return finish()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// stepLabelTR — adım adının Türkçe etiketi (anlatım girdisi + FE aynası
// sreInvestigationView STEP_LABEL).
func stepLabelTR(n string) string {
	switch n {
	case stepSweep:
		return "servis taraması"
	case stepDeploys:
		return "deploy"
	case stepDeps:
		return "bağımlılık"
	case stepPivot:
		return "trace pivotu"
	case stepLogSpike:
		return "log artışı"
	case stepSummary:
		return "AI özeti"
	case stepEvents:
		return "olaylar"
	case stepNode:
		return "node baskısı"
	}
	return n
}

func stepStateTR(s string) string {
	switch s {
	case StepOK:
		return "tamam"
	case StepPartial:
		return "kısmi"
	case StepError:
		return "OKUNAMADI"
	case StepSkipped:
		return "atlandı"
	}
	return s
}

// kindTR — bulgu türünün Türkçe adı.
func kindTR(k string) string {
	switch k {
	case "operation":
		return "operasyon"
	case "function":
		return "fonksiyon"
	case "dependency":
		return "bağımlılık"
	case "k8s":
		return "Kubernetes"
	case "jvm":
		return "JVM"
	}
	return k
}

// renderInvestigationTR — SAF: kayıt → anlatım girdisi (≤4 KB, rune sınırında
// kesilir). Sıra: başlık → pencere → adımlar → sentezleyici → bulgular (serbest
// metin, EN SONDA — veri talimat değildir). Damga loc diliminde (nil → UTC).
func renderInvestigationTR(inv Investigation, p chstore.Problem, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	hm := func(ns int64) string { return time.Unix(0, ns).In(loc).Format("15:04") }
	zone := time.Unix(0, inv.Anchor.To).In(loc).Format("MST")
	var b strings.Builder
	head := []string{firstNonEmpty(inv.DisplayID, inv.ProblemID), "servis=" + inv.Service}
	if inv.Env != "" {
		head = append(head, "env="+inv.Env)
	}
	if inv.Priority != "" {
		head = append(head, "öncelik="+inv.Priority)
	}
	if inv.Anchor.Symptom != "" {
		head = append(head, "belirti="+inv.Anchor.Symptom)
	}
	b.WriteString("PROBLEM: " + strings.Join(head, " · ") + "\n")
	if rule := firstNonEmpty(p.RuleName, p.RuleID); rule != "" {
		fmt.Fprintf(&b, "KURAL: %s", truncRunes(rule, 120))
		if p.Metric != "" {
			fmt.Fprintf(&b, " · %s=%.4g (eşik %.4g)", p.Metric, p.Value, p.Threshold)
		}
		b.WriteString("\n")
	}
	if inv.ProblemAt > 0 {
		fmt.Fprintf(&b, "BAŞLANGIÇ: %s %s · pencere %s–%s %s\n", hm(inv.ProblemAt), zone, hm(inv.Anchor.From), hm(inv.Anchor.To), zone)
	}
	b.WriteString("ADIMLAR:\n")
	for _, s := range inv.Steps {
		if s.Name == stepSummary {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s", stepLabelTR(s.Name), stepStateTR(s.State))
		if s.Summary != "" {
			b.WriteString(" — " + truncRunes(oneLine(s.Summary), 240))
		}
		b.WriteString("\n")
	}
	if sr := inv.SynthRef; sr != nil {
		if sr.TopSuspect != "" {
			fmt.Fprintf(&b, "SENTEZLEYİCİ: şüpheli %s (güven %%%.0f)\n", sr.TopSuspect, sr.Confidence*100)
		} else {
			b.WriteString("SENTEZLEYİCİ: belirgin şüpheli yok\n")
		}
	}
	b.WriteString(renderHypothesesTR(inv.Hypotheses))
	if len(inv.Findings) == 0 {
		b.WriteString("BULGULAR: yok — okunan bölümlerde eşik aşan bulgu çıkmadı\n")
	} else {
		b.WriteString("BULGULAR (sunucu sırası):\n")
		for i, f := range inv.Findings {
			fmt.Fprintf(&b, "%d. [%s] %s %s", i+1, f.Level, kindTR(f.Kind), truncRunes(oneLine(f.Name), 120))
			if len(f.Reasons) > 0 {
				rs := f.Reasons
				if len(rs) > 3 {
					rs = rs[:3]
				}
				b.WriteString(" — " + truncRunes(oneLine(strings.Join(rs, "; ")), 260))
			}
			b.WriteString("\n")
		}
	}
	return capBytes(b.String(), renderMaxBytes)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// capBytes — SAF: UTF-8 rune sınırında bayt tavanı.
func capBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len("…\n")
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…\n"
}

// humanTR — operatör işaretinin kısa etiketi.
func humanTR(h Hypothesis) string {
	if h.HumanState == nil {
		return ""
	}
	switch h.HumanState.State {
	case MarkDismissed:
		return " (operatör eledi)"
	case MarkConfirmed:
		return " (operatör onayladı)"
	}
	return ""
}

// renderHypothesesTR — SAF (v0.10.1288): anlatım girdisinin hipotez satırları.
// Skor KODDAN gelir; model yalnız anabilir (prompt: "skoru değiştirme").
// En olası = operatörün elemediği ilk kart; düşük bantta "belirgin hipotez yok".
func renderHypothesesTR(hs []Hypothesis) string {
	if len(hs) == 0 {
		return ""
	}
	top := -1
	for i, h := range hs {
		if h.HumanState == nil || h.HumanState.State != MarkDismissed {
			top = i
			break
		}
	}
	var b strings.Builder
	switch {
	case top < 0:
		b.WriteString("AJAN HİPOTEZİ: operatör tüm hipotezleri eledi\n")
	case hs[top].Band == BandLow:
		fmt.Fprintf(&b, "AJAN HİPOTEZİ (kod skoru): belirgin hipotez yok — en yüksek %s %s %.2f\n", hs[top].Label, hs[top].Band, hs[top].Score)
	default:
		h := hs[top]
		fmt.Fprintf(&b, "AJAN HİPOTEZİ (kod skoru): %s — %s %.2f%s", h.Label, h.Band, h.Score, humanTR(h))
		for _, c := range h.Checks {
			if c.Outcome > 0 {
				b.WriteString(" — " + truncRunes(oneLine(c.Reason), 160))
				break
			}
		}
		b.WriteString("\n")
	}
	var others []string
	for i, h := range hs {
		if i == top || len(others) >= HypothesesShown-1 {
			continue
		}
		others = append(others, fmt.Sprintf("%s %s %.2f%s", h.Label, h.Band, h.Score, humanTR(h)))
	}
	if len(others) > 0 {
		b.WriteString("DİĞER HİPOTEZLER: " + strings.Join(others, " · ") + "\n")
	}
	return b.String()
}
