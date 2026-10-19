package api

// copilot_guided_sweep.go — v0.10.1276 (CoSRE adım 7): guided servis-sağlık
// demetinin TARAMA yarısı (mcptools.ReadServiceSweep tüketicisi).
//
//   - emitGuidedCompositeResult — bileşik aracın step-result'ı (bölüm başına
//     kanıt çipi, attachStepEvidenceList) + sohbet içi blokları (ev_block →
//     withBlockSeq; serbest döngünün ToolResultBlocks yoluyla aynı).
//   - renderSweepEvidenceTR — modelin okuduğu "TARAMA:" bloğu (SAF).
//   - mergeSweepVerdict — servis HÜKMÜ + tarama bulguları → kötüsü (SAF).

import (
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// emitGuidedCompositeResult — guided adımın bileşik sonucu: step-result (çip
// listesi) + blokları. Kimlik yoksa (sarmalanmamış emit) hiçbir şey yayınlanmaz.
func emitGuidedCompositeResult(emit func(string, any), i int, tool, content string) {
	if i <= 0 {
		return
	}
	emitStepEvidence(emit, i, tool, content, nil)
	scratch := map[string]any{"i": i}
	evs := attachStepEvidenceList(scratch, tool, content)
	for _, b := range mcptools.ToolResultBlocks(tool, content, evs) {
		emit("ev_block", b)
	}
}

// sweepSectionLineTR — bölümün tek satırı; boş bölüm "bakıldı, veri yok".
func sweepSectionLineTR(sec mcptools.SweepSection) string {
	label := map[string]string{
		"red": "servis RED", "problems": "açık problemler", "ops": "operasyonlar", "functions": "fonksiyonlar",
		"pods": "pod'lar", "jvm": "JVM", "logs": "sorunluların logları", "code": "kod fonksiyonları (örneklem)", "patterns": "log desenleri",
		"corroborate": "gecikme doğrulaması (ham span)", // v0.10.1299
	}[sec.Name]
	if label == "" {
		label = sec.Name
	}
	state := ""
	switch sec.State {
	case "empty":
		state = "bakıldı, veri yok — "
	case "error", "timeout", "degraded":
		state = "OKUNAMADI (" + sec.State + ", okunamadı ≠ yok) — "
	case "skipped":
		state = "atlandı — "
	case "partial":
		state = "kısmi — "
	}
	line := fmt.Sprintf("- %s: %s%s", label, state, sec.Summary)
	if sec.Note != "" {
		line += " [not: " + sec.Note + "]"
	}
	return line + "\n"
}

// renderSweepEvidenceTR — SAF: taramanın kanıt bloğu. Sıra: özet satırı
// ("TARAMA: N pod, M operasyon, K fonksiyon; sorunlular: 1)… 2)…"), bulgular
// (gerekçe + logları), bölüm durumları.
func renderSweepEvidenceTR(sw mcptools.SweepResult) string {
	var b strings.Builder
	sc := sw.Scanned
	fmt.Fprintf(&b, "TARAMA (deterministik eşik, model değil): %d pod, %d operasyon, %d fonksiyon (%s), %d JVM pod'u",
		sc.Pods, sc.Operations, sc.Functions, sc.FunctionsSource, sc.JVMPods)
	if len(sw.Findings) == 0 {
		b.WriteString("; sorunlu bulgu yok.\n")
	} else {
		b.WriteString("; sorunlular: ")
		for i, f := range sw.Findings {
			if i > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%d) %s %s%s [%s]", f.Rank, sweepKindLabelTR(f.Kind), f.Name, sweepSpanKindTR(f.SpanKind), f.Level)
		}
		if sw.FindingsHasMore {
			b.WriteString(" (+ daha fazlası)")
		}
		b.WriteString(".\n")
		for _, f := range sw.Findings {
			where := ""
			if f.Cluster != "" {
				where = " (" + f.Cluster
				if f.Namespace != "" {
					where += "/" + f.Namespace
				}
				where += ")"
			}
			fmt.Fprintf(&b, "%d) %s %s%s%s — %s: %s\n", f.Rank, sweepKindLabelTR(f.Kind), f.Name, sweepSpanKindTR(f.SpanKind), where, f.Level, strings.Join(f.Reasons, "; "))
			if f.Logs != nil {
				fmt.Fprintf(&b, "   loglar: %s\n", f.Logs.Summary)
				for k, l := range f.Logs.Rows {
					if k >= 2 {
						break
					}
					// v0.10.1299 — ham JSON değil kısa insan satırı (ham gövde açılır tabloda).
					fmt.Fprintf(&b, "   · %s %s\n", l.Severity, mcptools.LogBodyPreview(l.Body, 200))
				}
			}
		}
	}
	b.WriteString("Taranan bölümler:\n")
	for _, sec := range sw.Sections {
		b.WriteString(sweepSectionLineTR(sec))
	}
	return b.String()
}

// sweepSpanKindTR — SAF (v0.10.1391): operasyon bulgusunun span türü eki.
// Giden çağrı (CLIENT/PRODUCER) açıkça söylenir: anlatım servisin GİDEN gRPC
// çağrısını servisin gelen trafiği sanmasın.
func sweepSpanKindTR(k string) string {
	switch k {
	case "":
		return ""
	case "CLIENT", "PRODUCER":
		return " (giden çağrı · " + k + ")"
	case "SERVER", "CONSUMER":
		return " (gelen · " + k + ")"
	}
	return " (" + k + ")"
}

func sweepKindLabelTR(k string) string {
	switch k {
	case "pod":
		return "pod"
	case "jvm":
		return "JVM"
	case "operation":
		return "operasyon"
	case "function":
		return "fonksiyon"
	}
	return k
}

// mergeSweepVerdict — SAF: servis hükmü + tarama bulguları. Etiket ikisinin
// KÖTÜSÜ; tarama kötüyse önerilen adım taramanınki. İlk 3 bulgu gerekçeye
// "tarama:" önekiyle eklenir. Tarama okunamadıysa (ok=false) hüküm aynen.
func mergeSweepVerdict(label string, reasons []string, next string, sw mcptools.SweepResult, ok bool) (string, []string, string) {
	if !ok || len(sw.Findings) == 0 {
		return label, reasons, next
	}
	worst := healthverdict.LevelOK
	for _, f := range sw.Findings {
		if healthverdict.LevelRank(f.Level) < healthverdict.LevelRank(worst) {
			worst = f.Level
		}
	}
	out := append([]string(nil), reasons...)
	if fl := healthverdict.LabelForLevel(worst); worst != healthverdict.LevelOK && healthverdict.LabelRank(fl) > healthverdict.LabelRank(label) {
		if label == healthverdict.LabelNoData || label == healthverdict.LabelHealthy {
			// "baseline içinde" / "span okunamadı" gerekçesi tarama bulgusuyla çelişir.
			out = nil
		}
		label = fl
		if sw.Verdict.Next != "" {
			next = sw.Verdict.Next
		}
	}
	for i, f := range sw.Findings {
		if i >= 3 {
			break
		}
		out = append(out, fmt.Sprintf("tarama: %s %s — %s", sweepKindLabelTR(f.Kind), f.Name, strings.Join(f.Reasons, ", ")))
	}
	return label, out, next
}
