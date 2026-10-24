package sre

// eval_compare.go — v0.10.1450 — AI SRE program 2a: iki değerlendirme
// koşusunun (taban B ↔ aday) vaka vaka karşılaştırması. SAF: yalnız iki koşu
// satırını okur (EvalRun), LLM / depo / oy / insan işareti okumaz
// (TestEvalIgnoresFeedback bu dosyayı da tarar).
//
// Ön koşul: AYNI vaka kümesi (rerunOf ile dondurulmuş küme) — değilse
// ErrEvalCompareMismatch. Bloklar:
//   - Independent (MANŞET, operatör kararı 2): onaylayanın CoSRE önerisini
//     değiştirmeden kabul ETMEDİĞİ vakalar (tabanın AcceptedSuggestion'ı).
//   - Control (tüm vakalar, ikinci satır): deterministik yol aynı olduğu için
//     Δ ≈ 0 beklenir; Δ ≠ 0 veri kayması (saklama) ya da gerilemedir.
//   - Calibration: depolanan Top[] skorları + Coverage'dan deterministik sonuç
//     (evalDetOutcome) — karar 4 kuralının yalnız deterministik kanıtla ne
//     diyeceği. Döngü sonucu 2c'de eklenir.
//   - Cost: 2a'da tekrar LLM çağırmaz (0) + koşu duvar süresi.
//   - Agreement: iki tarafta da puanlı vakalarda top-1 türü ve det. sonuç
//     aynı mı (kararlılık). Döngü uyumu (alt-hipotez) 2c'de.
// Neden yeni varlık yok: koşu satırları 180 gün kalır; karşılaştırma okuma
// anında hesaplanır, saklanmaz (iki satırdan her zaman yeniden kurulur).

import (
	"errors"
	"math"
)

// ErrEvalCompareMismatch — iki koşunun vaka kümesi farklı.
var ErrEvalCompareMismatch = errors.New("vaka kümesi farklı — karşılaştırma yalnız aynı dondurulmuş kümenin koşuları arasında")

// Deterministik sonuç değerleri / nedenleri (plan-pr2 §3.2 outcomeOf ile aynı
// yazım; 2b'nin outcomeOf'u gelince evalDetOutcome ona devreder).
const (
	detConfirmed    = "confirmed"
	detLikely       = "likely"
	detAmbiguous    = "ambiguous"
	detInconclusive = "inconclusive"
	detNoData       = "no_data"
	detError        = "error"

	detReasonLowScore    = "low_score"
	detReasonLowCoverage = "low_coverage"
	detReasonNoSteps     = "no_steps"

	detMinScore     = 0.40
	detMinCoverage  = 0.50
	detAmbiguousGap = 0.10
	detConfirmedMin = bandHighMin // 0.70
)

// Vaka isabet etiketi (FE caseHit ile aynı kural).
const (
	evalHitTop1     = "top1"
	evalHitTop3     = "top3"
	evalHitMiss     = "miss"
	evalHitUnscored = "unscored"
)

// EvalDetOutcome — depolanan özetten deterministik sonuç.
type EvalDetOutcome struct {
	Value      string  `json:"value"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence"` // en olası hipotezin skoru (2 basamak)
	Coverage   float64 `json:"coverage"`   // veri döndüren telemetri adımı payı (0..1)
}

// evalCaseScored — SAF: scoreEval paydası (biten tekrar + veri + hipotez).
func evalCaseScored(c EvalCase) bool {
	return (c.State == StateDone || c.State == StatePartial) && c.Coverage != nil && c.Coverage.Data > 0 && len(c.Top) > 0
}

// evalCaseHit — SAF: top1 | top3 | miss | unscored.
func evalCaseHit(c EvalCase) string {
	if !evalCaseScored(c) {
		return evalHitUnscored
	}
	if c.Top[0].Category == c.Category {
		return evalHitTop1
	}
	for i, h := range c.Top {
		if i < evalTopN && h.Category == c.Category {
			return evalHitTop3
		}
	}
	return evalHitMiss
}

// evalDetOutcome — SAF: biten vakanın deterministik sonucu (plan-pr2 §3.2
// kuralları, döngüsüz: sıra önemli, ilk eşleşen kazanır). Bitmemiş / atlanan /
// eşlenmeyen vaka nil.
//  1. tekrar hata → error / no_steps
//  2. kapsam paydası 0 → no_data
//  3. en iyi skor < 0.40 → inconclusive / low_score
//  4. kapsam < 0.50 → inconclusive / low_coverage
//  5. ikinci var ve fark < 0.10 → ambiguous
//  6. en iyi ≥ 0.70 → confirmed, yoksa likely
func evalDetOutcome(c EvalCase) *EvalDetOutcome {
	switch c.State {
	case StateDone, StatePartial:
	case StateError:
		return &EvalDetOutcome{Value: detError, Reason: detReasonNoSteps}
	default:
		return nil
	}
	out := &EvalDetOutcome{}
	best, second, hasSecond := 0.0, 0.0, false
	for i, h := range c.Top {
		switch {
		case i == 0 || h.Score > best:
			if i > 0 {
				second, hasSecond = best, true
			}
			best = h.Score
		case !hasSecond || h.Score > second:
			second, hasSecond = h.Score, true
		}
	}
	out.Confidence = round2(best)
	total := 0
	if c.Coverage != nil {
		total = c.Coverage.Data + c.Coverage.NoData + c.Coverage.Failed
	}
	if total == 0 {
		out.Value = detNoData
		return out
	}
	cov := float64(c.Coverage.Data) / float64(total)
	out.Coverage = round2(cov)
	switch {
	case len(c.Top) == 0 || best < detMinScore:
		out.Value, out.Reason = detInconclusive, detReasonLowScore
	case cov < detMinCoverage:
		out.Value, out.Reason = detInconclusive, detReasonLowCoverage
	case hasSecond && round2(best-second) < detAmbiguousGap:
		out.Value = detAmbiguous
	case best >= detConfirmedMin:
		out.Value = detConfirmed
	default:
		out.Value = detLikely
	}
	return out
}

// ── karşılaştırma şekilleri ─────────────────────────────────────────────────

// EvalCompareRun — koşunun kimliği (karşılaştırma başlığı).
type EvalCompareRun struct {
	RunID   string `json:"runId"`
	State   string `json:"state"`
	RerunOf string `json:"rerunOf,omitempty"`
	Loop    bool   `json:"loop"`
	// ContextPack — v0.10.1494: koşu paket okuyucularıyla (deterministik) koştu.
	ContextPack bool   `json:"contextPack"`
	Build       string `json:"build,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	FinishedAt  int64  `json:"finishedAt,omitempty"`
}

// EvalPairSide — bir vakanın bir koşudaki özeti.
type EvalPairSide struct {
	State      string          `json:"state"`
	Top1       *EvalHyp        `json:"top1,omitempty"`
	Hit        string          `json:"hit"` // top1 | top3 | miss | unscored
	Coverage   *EvalCoverage   `json:"coverage,omitempty"`
	DetOutcome *EvalDetOutcome `json:"detOutcome,omitempty"`
}

// EvalPair — aynı vakanın iki koşudaki hâli (beklenen tabandan).
type EvalPair struct {
	CaseID             string       `json:"caseId"`
	ProblemID          string       `json:"problemId"`
	DisplayID          string       `json:"displayId,omitempty"`
	Service            string       `json:"service"`
	Category           string       `json:"category"`
	FinalCause         string       `json:"finalCause"`
	AcceptedSuggestion bool         `json:"acceptedSuggestion,omitempty"`
	Base               EvalPairSide `json:"base"`
	Cand               EvalPairSide `json:"cand"`
	// CoverageDrift — iki taraf da bitti ve veri döndüren adım sayısı farklı
	// (saklama / veri kayması; Δ'yı bu vakalar açıklar).
	CoverageDrift bool `json:"coverageDrift,omitempty"`
}

// EvalSideScore — bir alt kümenin bir koşudaki isabet ölçüleri (scoreEval).
type EvalSideScore struct {
	Scored       int      `json:"scored"`
	Top1Hits     int      `json:"top1Hits"`
	Top3Hits     int      `json:"top3Hits"`
	EntityScored int      `json:"entityScored"`
	EntityHits   int      `json:"entityHits"`
	Top1Acc      *float64 `json:"top1Acc"`
	Top3Acc      *float64 `json:"top3Acc"`
	EntityAcc    *float64 `json:"entityAcc"`
	CoveragePct  *float64 `json:"coveragePct"`
}

// EvalCompareBlock — taban / aday + Δ (aday − taban; payda yoksa null).
type EvalCompareBlock struct {
	Cases         int           `json:"cases"`
	Base          EvalSideScore `json:"base"`
	Cand          EvalSideScore `json:"cand"`
	DeltaTop1     *float64      `json:"deltaTop1"`
	DeltaTop3     *float64      `json:"deltaTop3"`
	DeltaEntity   *float64      `json:"deltaEntity"`
	DeltaCoverage *float64      `json:"deltaCoverage"` // yüzde puanı
}

// EvalRatio — oran + pay / payda (küçük N: her oran sayısıyla gösterilir).
type EvalRatio struct {
	N int      `json:"n"`
	D int      `json:"d"`
	V *float64 `json:"v"`
}

func mkRatio(n, d int) EvalRatio { return EvalRatio{N: n, D: d, V: ratio(n, d)} }

// EvalCalibration — deterministik sonucun isabetle uyumu (bir koşu).
//   - Decisive: (confirmed + likely) / puanlı
//   - ConfirmedPrecision: P(top-1 isabet | confirmed ∨ likely)
//   - AbstainOnMiss: P(inconclusive ∨ ambiguous ∨ no_data | top-1 kaçtı)
//   - ConfidentWrong: confirmed ∨ likely ama top-1 kaçtı
type EvalCalibration struct {
	Outcomes           map[string]int `json:"outcomes"`
	Decisive           EvalRatio      `json:"decisive"`
	ConfirmedPrecision EvalRatio      `json:"confirmedPrecision"`
	AbstainOnMiss      EvalRatio      `json:"abstainOnMiss"`
	ConfidentWrong     int            `json:"confidentWrong"`
}

// EvalCost — koşunun maliyeti. 2a: tekrar LLM çağırmaz (LLMFree, LLMCalls 0).
type EvalCost struct {
	LLMFree  bool   `json:"llmFree"`
	LLMCalls int    `json:"llmCalls"`
	Replayed int    `json:"replayed"`         // biten tekrar (done / partial / error)
	WallMs   *int64 `json:"wallMs,omitempty"` // koşu açılışı → bitiş (bitmediyse yok)
}

// EvalAgreement — iki koşu aynı vakada aynı şeyi söyledi mi (kararlılık).
type EvalAgreement struct {
	Top1Same       EvalRatio `json:"top1Same"`       // iki tarafta puanlı vakada top-1 türü aynı
	DetOutcomeSame EvalRatio `json:"detOutcomeSame"` // iki tarafta biten vakada det. sonuç aynı
}

// EvalCalibrationPair / EvalCostPair — taban + aday.
type EvalCalibrationPair struct {
	Base EvalCalibration `json:"base"`
	Cand EvalCalibration `json:"cand"`
}
type EvalCostPair struct {
	Base EvalCost `json:"base"`
	Cand EvalCost `json:"cand"`
}

// EvalCompare — GET /api/sre/eval/compare gövdesi (FE: SREEvalCompare).
type EvalCompare struct {
	Base  EvalCompareRun `json:"base"`
	Cand  EvalCompareRun `json:"cand"`
	Pairs []EvalPair     `json:"pairs"`
	// Headline — manşet blok adı (operatör kararı 2: "independent").
	Headline string `json:"headline"`
	// IndependentKnown — tabanın öneri bayrağı hesaplanmış (v0.10.1450+).
	IndependentKnown bool                `json:"independentKnown"`
	Independent      EvalCompareBlock    `json:"independent"`
	Control          EvalCompareBlock    `json:"control"`
	DriftCases       []string            `json:"driftCases"`
	Calibration      EvalCalibrationPair `json:"calibration"`
	Cost             EvalCostPair        `json:"cost"`
	Agreement        EvalAgreement       `json:"agreement"`
	// Pack — v0.10.1494 (STOP C'): iki koşunun Context Pack özeti (paket kipi,
	// boyut, kırpma, paket okuyucularının kapsamı).
	Pack EvalPackPair `json:"pack"`
	Note string       `json:"note"`
}

// EvalPackSide — bir koşunun paket özeti (yalnız künyeli vakalar).
type EvalPackSide struct {
	On           bool     `json:"on"`
	Cases        int      `json:"cases"` // künyeli (paketli biten) vaka
	AvgTokens    *float64 `json:"avgTokens"`
	MaxTokens    int      `json:"maxTokens"`
	TrimmedCases int      `json:"trimmedCases"`
	ReaderData   int      `json:"readerData"`
	ReaderNoData int      `json:"readerNoData"`
	ReaderFailed int      `json:"readerFailed"`
}

// EvalPackPair — taban + aday.
type EvalPackPair struct {
	Base EvalPackSide `json:"base"`
	Cand EvalPackSide `json:"cand"`
}

// EvalCompareNotePack — taban ile aday paket kipi farklıysa eklenir.
const EvalCompareNotePack = " Paket kipi farklı: paketli koşuda deterministik okuyucular (baseline / anomaliler / hata özeti) ek adım olarak koştu ve anlatım paketi künyelendi; " +
	"hipotez skoru bu sürümde paketi OKUMAZ — isabet Δ ≈ 0 beklenir, fark paket boyutu / kırpma / okuyucu kapsamındadır (LLM çağrısı yok)."

// packSideOf — SAF.
func packSideOf(r EvalRun) EvalPackSide {
	s := EvalPackSide{On: r.ContextPack}
	sum := 0
	for _, c := range r.Cases {
		p := c.Pack
		if p == nil {
			continue
		}
		s.Cases++
		sum += p.EstTokens
		if p.EstTokens > s.MaxTokens {
			s.MaxTokens = p.EstTokens
		}
		if p.Trimmed {
			s.TrimmedCases++
		}
		s.ReaderData += p.ReaderData
		s.ReaderNoData += p.ReaderNoData
		s.ReaderFailed += p.ReaderFailed
	}
	if s.Cases > 0 {
		v := math.Round(float64(sum)/float64(s.Cases)*10) / 10
		s.AvgTokens = &v
	}
	return s
}

// EvalCompareNote — ekranda değişmez dürüstlük beyanı (plan-pr2-eval §1).
const EvalCompareNote = "Manşet = bağımsız vakalar: onaylayan CoSRE'nin önerisini değiştirmeden kabul ettiyse etiket ajandan bağımsız değildir (tüm vakalar ikinci satır). " +
	"Kontrol satırında Δ ≠ 0 veri kayması (saklama) ya da deterministik yolda gerileme demektir — \"ajan akıllandı\" DEĞİL. " +
	"Etiket kaba: 10 kategori + serbest metin; isabet doğru kategori demektir, doğru kök neden değil; varlık eşleşmesi serbest metinde sözcük eşleşmesidir. " +
	"Saklama dışına düşen pencere \"veri yok\" döner ve puanlanmaz. Küçük örneklem: her oran pay/payda ile okunmalı."

// EvalCompareNoteLegacy — taban öneri bayrağından önce koşulduysa eklenir.
const EvalCompareNoteLegacy = " Taban koşusu öneri bayrağından önce (v0.10.1450 öncesi) — bağımsız ayrımı bilinmiyor, manşet satırı tüm vakaları gösterir."

func evalCaseKey(c EvalCase) string {
	if c.CaseID != "" {
		return c.CaseID
	}
	return "p:" + c.ProblemID
}

func compareRunOf(r EvalRun) EvalCompareRun {
	return EvalCompareRun{RunID: r.RunID, State: r.State, RerunOf: r.RerunOf, Loop: r.Loop, ContextPack: r.ContextPack, Build: r.Build, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
}

func sideOf(c EvalCase) EvalPairSide {
	s := EvalPairSide{State: c.State, Hit: evalCaseHit(c), Coverage: c.Coverage, DetOutcome: evalDetOutcome(c)}
	if len(c.Top) > 0 {
		t := c.Top[0]
		s.Top1 = &t
	}
	return s
}

func caseFinished(state string) bool {
	return state == StateDone || state == StatePartial || state == StateError
}

func covData(c *EvalCoverage) int {
	if c == nil {
		return 0
	}
	return c.Data
}

func sideScore(cases []EvalCase) EvalSideScore {
	sc := scoreEval(evalInput{Cases: cases})
	return EvalSideScore{Scored: sc.Scored, Top1Hits: sc.Top1Hits, Top3Hits: sc.Top3Hits, EntityScored: sc.EntityScored,
		EntityHits: sc.EntityHits, Top1Acc: sc.Top1Acc, Top3Acc: sc.Top3Acc, EntityAcc: sc.EntityAcc, CoveragePct: sc.CoveragePct}
}

func delta(a, b *float64, scale float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := math.Round((*b-*a)*scale) / scale
	return &v
}

func compareBlock(base, cand []EvalCase) EvalCompareBlock {
	b, c := sideScore(base), sideScore(cand)
	return EvalCompareBlock{Cases: len(base), Base: b, Cand: c, DeltaTop1: delta(b.Top1Acc, c.Top1Acc, 10000),
		DeltaTop3: delta(b.Top3Acc, c.Top3Acc, 10000), DeltaEntity: delta(b.EntityAcc, c.EntityAcc, 10000),
		DeltaCoverage: delta(b.CoveragePct, c.CoveragePct, 10)}
}

func calibrationOf(cases []EvalCase) EvalCalibration {
	cal := EvalCalibration{Outcomes: map[string]int{}}
	scored, decisive, decisiveHit, miss, abstain := 0, 0, 0, 0, 0
	for _, c := range cases {
		o := evalDetOutcome(c)
		if o == nil {
			continue
		}
		cal.Outcomes[o.Value]++
		if !evalCaseScored(c) {
			continue
		}
		scored++
		hit := evalCaseHit(c) == evalHitTop1
		dec := o.Value == detConfirmed || o.Value == detLikely
		if dec {
			decisive++
			if hit {
				decisiveHit++
			} else {
				cal.ConfidentWrong++
			}
		}
		if !hit {
			miss++
			if o.Value == detInconclusive || o.Value == detAmbiguous || o.Value == detNoData {
				abstain++
			}
		}
	}
	cal.Decisive, cal.ConfirmedPrecision, cal.AbstainOnMiss = mkRatio(decisive, scored), mkRatio(decisiveHit, decisive), mkRatio(abstain, miss)
	return cal
}

func costOf(r EvalRun) EvalCost {
	cost := EvalCost{LLMFree: !r.Loop}
	for _, c := range r.Cases {
		if caseFinished(c.State) {
			cost.Replayed++
		}
	}
	if r.State == EvalDone && r.FinishedAt > r.CreatedAt {
		ms := (r.FinishedAt - r.CreatedAt) / 1e6
		cost.WallMs = &ms
	}
	return cost
}

// compareEvalRuns — SAF: iki koşunun vaka vaka karşılaştırması (taban sırası).
func compareEvalRuns(base, cand EvalRun) (EvalCompare, error) {
	candBy := make(map[string]EvalCase, len(cand.Cases))
	for _, c := range cand.Cases {
		candBy[evalCaseKey(c)] = c
	}
	if len(candBy) != len(cand.Cases) || len(base.Cases) != len(cand.Cases) {
		return EvalCompare{}, ErrEvalCompareMismatch
	}
	seen := make(map[string]bool, len(base.Cases))
	for _, c := range base.Cases {
		k := evalCaseKey(c)
		if _, ok := candBy[k]; !ok || seen[k] {
			return EvalCompare{}, ErrEvalCompareMismatch
		}
		seen[k] = true
	}
	out := EvalCompare{Base: compareRunOf(base), Cand: compareRunOf(cand), Pairs: make([]EvalPair, 0, len(base.Cases)),
		Headline: "independent", IndependentKnown: base.SuggestionKnown, DriftCases: []string{}, Note: EvalCompareNote}
	if !base.SuggestionKnown {
		out.Note += EvalCompareNoteLegacy
	}
	if base.ContextPack != cand.ContextPack {
		out.Note += EvalCompareNotePack
	}
	out.Pack = EvalPackPair{Base: packSideOf(base), Cand: packSideOf(cand)}
	candOrdered := make([]EvalCase, 0, len(base.Cases))
	var indBase, indCand []EvalCase
	top1N, top1D, detN, detD := 0, 0, 0, 0
	for _, b := range base.Cases {
		c := candBy[evalCaseKey(b)]
		candOrdered = append(candOrdered, c)
		p := EvalPair{CaseID: b.CaseID, ProblemID: b.ProblemID, DisplayID: firstNonEmpty(b.DisplayID, c.DisplayID), Service: b.Service,
			Category: b.Category, FinalCause: b.FinalCause, AcceptedSuggestion: b.AcceptedSuggestion, Base: sideOf(b), Cand: sideOf(c)}
		if caseFinished(b.State) && caseFinished(c.State) && covData(b.Coverage) != covData(c.Coverage) {
			p.CoverageDrift = true
			out.DriftCases = append(out.DriftCases, b.CaseID)
		}
		if !base.SuggestionKnown || !b.AcceptedSuggestion {
			indBase, indCand = append(indBase, b), append(indCand, c)
		}
		if evalCaseScored(b) && evalCaseScored(c) {
			top1D++
			if b.Top[0].Kind == c.Top[0].Kind {
				top1N++
			}
		}
		if p.Base.DetOutcome != nil && p.Cand.DetOutcome != nil {
			detD++
			if p.Base.DetOutcome.Value == p.Cand.DetOutcome.Value {
				detN++
			}
		}
		out.Pairs = append(out.Pairs, p)
	}
	out.Control = compareBlock(base.Cases, candOrdered)
	out.Independent = compareBlock(indBase, indCand)
	out.Calibration = EvalCalibrationPair{Base: calibrationOf(base.Cases), Cand: calibrationOf(candOrdered)}
	out.Cost = EvalCostPair{Base: costOf(base), Cand: costOf(cand)}
	out.Agreement = EvalAgreement{Top1Same: mkRatio(top1N, top1D), DetOutcomeSame: mkRatio(detN, detD)}
	return out, nil
}

// CompareEvalRuns — dışa açık sarmalayıcı (api ucu).
func CompareEvalRuns(base, cand EvalRun) (EvalCompare, error) { return compareEvalRuns(base, cand) }
