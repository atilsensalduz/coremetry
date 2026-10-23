package sre

// contextpack_golden_test.go — v0.10.1462 (CoSRE A1, Context Pack): anlatım
// girdisi (renderInvestigationTR) ve sohbetin "KAYITLI ARAŞTIRMA" bloğu
// (RenderSavedTR) için bayt-bayt altın metinler. Altınlar refactor ÖNCESİ
// HEAD'de üretildi (`go test ./internal/sre -run TestNarrationGolden -update`);
// pack refactor'u (contextpack.go) metni tek bayt değiştirmez. Yeni bölüm
// (A2) yalnız bayrak arkasında gelir — bayrak kapalıyken bu pinler yeşil kalır.

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var updateNarrationGolden = flag.Bool("update", false, "anlatım / sohbet altın metinlerini yeniden yaz")

type narrationScenario struct {
	name string
	inv  Investigation
	p    chstore.Problem
	loc  *time.Location
}

// narrationScenarios — sentetik adlar (orders-api, kxzone, db.example.test).
func narrationScenarios() []narrationScenario {
	ist := time.FixedZone("+03", 3*3600)
	at := time.Date(2026, 10, 17, 14, 5, 0, 0, time.UTC)
	ns := func(d time.Duration) int64 { return at.Add(d).UnixNano() }
	anchor := Anchor{From: ns(-15 * time.Minute), To: ns(30 * time.Minute), Symptom: "error"}
	steps := []Step{
		{Name: stepSweep, State: StepOK, Summary: "Sorunlu: POST /orders hata %9 (önceki %0,5)"},
		{Name: stepDeploys, State: StepOK, Summary: "2 değişiklik (son 2 sa); başlangıca yakın: orders-api@rev 42"},
		{Name: stepDeps, State: StepPartial, Summary: "topoloji\nokunamadı;   korele bağımlılık yok"},
		{Name: stepPivot, State: StepOK, Summary: "POST /orders — 2300 ms, 41 span (3 hatalı)"},
		{Name: stepLogSpike, State: StepError, Summary: "log arka ucu bağlı değil"},
		{Name: stepEvents, State: StepSkipped},
		{Name: stepNode, State: StepOK, Summary: "node baskısı yok"},
		{Name: stepSummary, State: StepOK, Summary: "eski özet — anlatıma girmez"},
		{Name: "custom_step", State: "weird", Summary: "bilinmeyen adım adı aynen"},
	}
	findings := []Finding{
		{Kind: "deploy", Name: "orders-api@rev 42", Level: "crit", Reasons: []string{"başlangıçtan 12 dk önce rollout (rev 42←rev 41)", "rollout stalled"}},
		{Kind: "operation", Name: "POST /orders", Level: "crit", Reasons: []string{"hata oranı %9\n(önceki %0,5)"}},
		{Kind: "dependency", Name: "db.example.test", Level: "warn", Reasons: []string{"p99 +180%", "aşağı akış", "nedensellik değil", "dördüncü gerekçe düşer"}},
		{Kind: "jvm", Name: "orders-api-7d9f", Level: "warn"},
		{Kind: "k8s", Name: "kxzone/orders-api", Level: "info", Reasons: []string{"restart 3"}},
		{Kind: "function", Name: "fn-0042", Level: "warn", Reasons: []string{"hata payı %40"}},
	}
	hyps := []Hypothesis{
		{Kind: "deploy", Label: "orders-api@rev 42 rollout'u", Band: BandHigh, Score: 0.71,
			Checks: []HypothesisCheck{{Outcome: -1, Reason: "çürüten"}, {Outcome: 1, Reason: "deploy başlangıçtan\n12 dk önce"}, {Outcome: 1, Reason: "ikinci destek"}}},
		{Kind: "dependency", Label: "db.example.test yavaşlığı", Band: BandMedium, Score: 0.44,
			Checks:     []HypothesisCheck{{Outcome: -1, Reason: "kenar zayıf"}},
			HumanState: &HumanState{State: MarkConfirmed, Note: "not", By: "op", At: ns(time.Hour)}},
		{Kind: "node", Label: "node baskısı", Band: BandLow, Score: 0.12,
			HumanState: &HumanState{State: MarkDismissed}},
		{Kind: "log", Label: "dördüncü hipotez gösterilmez", Band: BandLow, Score: 0.05},
	}
	recs := []Recommendation{
		{ID: "a", Text: "Elenen öneri", State: RecDismissed},
		{ID: "b", Text: "orders-api rev 41'e\ngeri dönmeyi değerlendir", State: RecPending},
		{ID: "c", Text: "db.example.test bağlantı havuzunu kontrol et", State: RecAck},
		{ID: "d", Text: "üçüncü öneri düşer", State: RecPending},
	}
	rule := chstore.Problem{RuleName: "Error rate > 5%", Metric: "error_rate", Value: 9.123456, Threshold: 5}

	big := Investigation{ProblemID: "p-big", DisplayID: "P-9000", Service: "orders-api", Env: "prod", Priority: "P1",
		Kind: KindAuto, State: StateDone, FinishedAt: ns(20 * time.Minute), ProblemAt: ns(0), Anchor: anchor,
		SynthRef: &SynthRef{TopSuspect: "db.example.test", Confidence: 0.58}, Hypotheses: hyps, Recommendations: recs,
		Summary: &Narration{Text: strings.Repeat("Ne oldu: ğüşiöç ", 120)}}
	for _, s := range steps {
		s.Summary = strings.Repeat("ç", 500)
		big.Steps = append(big.Steps, s)
	}
	for i := 0; i < 8; i++ {
		big.Findings = append(big.Findings, Finding{Kind: "log", Name: strings.Repeat("ğ", 200), Level: "warn", Reasons: []string{strings.Repeat("ş", 400)}})
	}

	return []narrationScenario{
		{name: "min", inv: Investigation{ProblemID: "p-min", Service: "orders-api", State: StateDone}},
		{name: "full", loc: ist, p: rule, inv: Investigation{ProblemID: "p-full", DisplayID: "P-1042", Service: "orders-api", Env: "prod",
			Priority: "P1", Kind: KindAuto, State: StateDone, FinishedAt: ns(20 * time.Minute), ProblemAt: ns(0), Anchor: anchor,
			Steps: steps, Findings: findings, SynthRef: &SynthRef{TopSuspect: "db.example.test", Confidence: 0.584},
			Hypotheses: hyps, Recommendations: recs, Summary: &Narration{Text: "Ne oldu: POST /orders hata oranı arttı.\nKanıt: rev 42."}}},
		{name: "replay", p: chstore.Problem{RuleID: "rule-err-001"}, inv: Investigation{ProblemID: "p-replay", Service: "orders-api",
			Kind: KindReplay, EvalRun: "run-1", AsOf: ns(10 * time.Minute), State: StatePartial, StartedAt: ns(5 * time.Minute),
			ProblemAt: ns(0), Anchor: Anchor{From: ns(-15 * time.Minute), To: ns(30 * time.Minute)},
			Steps: steps[:3], Findings: findings[:2], SynthRef: &SynthRef{Confidence: 0.1},
			Hypotheses: []Hypothesis{{Kind: "deploy", Label: "eleme", Band: BandHigh, Score: 0.9, HumanState: &HumanState{State: MarkDismissed}}}}},
		{name: "verify", p: chstore.Problem{RuleName: strings.Repeat("uzun kural adı ", 12), Metric: "p99_ms", Value: 2300, Threshold: 800},
			inv: Investigation{ProblemID: "p-verify", DisplayID: "P-1043", Service: "orders-api", Env: "staging", Kind: KindVerify,
				State: StateShared, SharedFrom: "p-full", EnqueuedAt: ns(-time.Minute), ProblemAt: ns(0), Anchor: anchor,
				Steps: steps[3:6], Findings: findings[2:],
				Hypotheses: []Hypothesis{{Kind: "node", Label: "node baskısı", Band: BandLow, Score: 0.18}, hyps[1]},
				Summary:    &Narration{Text: "paylaşılan kaydın özeti"}}},
		{name: "with_synth", loc: ist, p: rule, inv: Investigation{ProblemID: "p-synth", DisplayID: "P-1044", Service: "orders-api",
			Priority: "P2", Kind: KindManual, State: StateDone, FinishedAt: ns(25 * time.Minute), ProblemAt: ns(0), Anchor: anchor,
			Steps: steps[:2], Findings: findings[:1], SynthRef: &SynthRef{TopSuspect: "kxzone/orders-api", Confidence: 0.995},
			Summary: &Narration{Text: NarrationGatedText, Gated: true, Reason: "kapalı"}}},
		{name: "no_findings", p: rule, inv: Investigation{ProblemID: "p-nof", DisplayID: "P-1045", Service: "orders-api", Env: "prod",
			Kind: KindAuto, State: StateDone, FinishedAt: ns(15 * time.Minute), ProblemAt: ns(0), Anchor: anchor,
			Steps: steps, Hypotheses: hyps, Recommendations: recs[:1]}},
		{name: "capped", p: rule, inv: big},
	}
}

func goldenRef(inv Investigation) string {
	if inv.DisplayID != "" {
		return inv.DisplayID
	}
	return inv.ProblemID
}

// TestNarrationGoldenByteIdentical — her senaryoda anlatım girdisi ve sohbet
// bloğu altınla bayt-bayt aynı; anlatım ≤4 KB ve geçerli UTF-8.
func TestNarrationGoldenByteIdentical(t *testing.T) {
	for _, sc := range narrationScenarios() {
		narr := renderInvestigationTR(sc.inv, sc.p, sc.loc)
		chat := RenderSavedTR(goldenRef(sc.inv), SummaryOf(sc.inv), sc.loc)
		if len(narr) > renderMaxBytes || !utf8Valid(narr) || !utf8Valid(chat) {
			t.Errorf("%s: tavan / UTF-8: %d B", sc.name, len(narr))
		}
		for kind, got := range map[string]string{"narration": narr, "chat": chat} {
			path := filepath.Join("testdata", "narration", sc.name+"."+kind+".txt")
			if *updateNarrationGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%s: altın yok (-update ile üret): %v", path, err)
			}
			if got != string(want) {
				t.Errorf("%s bayt farkı:\n--- got\n%s\n--- want\n%s", path, got, want)
			}
		}
	}
}
