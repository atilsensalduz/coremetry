package api

// copilot_service_report.go — v0.10.1337 (Feature D, operatör: "Bir servisi
// analiz ederken operasyonları, son 1 saatteki veya 3 saatteki teknik hata
// exceptions var mı ve logları analiz et, ona göre bir rapor çıkar. Teknik
// hatadaki birkaç trace'e hızlıca Ask CoSRE gibi bakıp birleştir, bütünle.").
//
// Yeni guided niyet `service_report`: "X servisini analiz et", "X için rapor
// çıkar". Demet DETERMİNİSTİK (trace başına model çağrısı YOK); tek anlatım
// çağrısı runGuidedRoute'ta (yüzey chat-guided, SystemPromptServiceReport).
//
//	Faz 1 (paralel, ≤6 sn): service_context (buildServiceContext +
//	  renderServiceSnapshot), list_problems (+ kök-neden hipotezi),
//	  top_operations (GetOperationSummaryCompared — iki yol da LIMIT 500),
//	  exception_groups (v0.10.1331 dikişi, servis düzeyi, son 3 sa),
//	  slow_traces (guidedTraceFilter + hatalı ikizi).
//	Faz 2 (SAF): pickReportTraces — ≤3 trace, tekrarsız.
//	Faz 3 (paralel, ≤8 sn): traceDrillFor (v0.10.1327 — ikinci uygulama yok).
//	Faz 4 (≤5 sn): TEK log okuması, readDrillLogs (yapılandırılmış depo).
//	Faz 5 (SAF): serviceReportVerdict → HÜKÜM kanıtın EN ÜSTÜNDE.
//
// Goroutine'ler emit ETMEZ: adım çipleri okumalardan önce açılır, sonuçlar
// hepsi bittikten sonra sırayla yazılır. Deterministik yük + kanıt 60 sn
// s.cache'te (anahtar: servis, ortam, dakikaya oturtulmuş pencere); anlatım
// önbelleğe GİRMEZ; isabette adım çipleri yine yayınlanır. Salt-okur: bu
// dosya hiçbir depoya yazmaz (TestServiceReportReadOnly).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// guidedServiceReport — servis raporu niyeti (router: tarama dalından sonra,
// neden dalından önce; sınıflandırıcı beyaz listesinde).
const guidedServiceReport guidedIntent = "service_report"

const (
	serviceReportDefaultRangeS = 3600 // açık pencere yoksa son 1 saat (operatör varsayılanı)
	serviceReportBudget        = 20 * time.Second
	serviceReportPhase1        = 6 * time.Second
	serviceReportDrillPhase    = 8 * time.Second
	serviceReportLogsPhase     = 5 * time.Second
	serviceReportExLookback    = 3 * time.Hour
	serviceReportOpsInputCap   = 500 // GetOperationSummary iki yolda da LIMIT 500
	serviceReportOpsTop        = 5
	serviceReportOpMinCalls    = 20
	serviceReportTracesMax     = 3
	serviceReportExTracesMax   = 2
	serviceReportLogsPerTrace  = traceDrillLogsPerTrace // kart: trace başına ≤8 satır
	serviceReportEvLogLines    = 3                      // kanıt: trace başına ≤3 satır (bütçe)
	serviceReportProblemsMax   = 5
	serviceReportEvidenceMax   = 6144 // kanıt metni tavanı (bayt, ~6 KB)
	serviceReportCacheTTL      = 60 * time.Second
	serviceReportBurstMin      = 10 // patlama Sorunlu sayılmak için son 1 sa asgari oluşum
	serviceReportSlowRatio     = 2.0
	serviceReportSlowMinMs     = 200.0
	serviceReportOpErrPct      = 5.0
	serviceReportOpErrMin      = 10
	serviceReportReasonsMax    = 5
)

// ─── Router sinyali (SAF) ───────────────────────────────────────────────

// hasServiceReportSignal — "analiz / rapor / incele / değerlendir / assess".
// Ad-şekilli jetonlar (ayraç ya da rakam taşıyan: "reporting-service",
// "analytics-v2") sinyal sayılmaz — servis adının parçası rapor isteği değil.
func hasServiceReportSignal(toks []string) bool {
	plain := make([]string, 0, len(toks))
	for _, t := range toks {
		if !strings.ContainsAny(t, "-_.0123456789") {
			plain = append(plain, t)
		}
	}
	return tokenHasPrefix(plain, "analiz", "analy", "rapor", "report", "incele", "değerlendir", "degerlendir", "assess")
}

// serviceReportShape — router + serviceIntentFor'un ORTAK kapısı: rapor
// sinyali VAR, spesifik bir sinyal (log / deploy / pod / db / messaging) YOK;
// nasıl-yapılır sorusu ("nasıl analiz edebilirim") yol tarifidir, rapor değil.
func serviceReportShape(toks []string) bool {
	return hasServiceReportSignal(toks) && !hasLogSignal(toks) && !hasDeploySignal(toks) &&
		!hasPodSignal(toks) && !hasDBSignal(toks) && !hasMessagingSignal(toks) && !hasHowToShape(toks)
}

// serviceReportChip — router'ın deterministik olarak service_report'a çözdüğü
// takip / aday çipi (D7: kök-neden ve sağlık cevaplarından tek tık).
func serviceReportChip(svc string) string { return svc + " servisini analiz et" }

// ─── Operasyon sıralaması (SAF) ────────────────────────────────────────

// reportOp — kartın ve kanıtın operasyon satırı. ErrorRate yüzde.
type reportOp struct {
	Name       string  `json:"name"`
	Calls      uint64  `json:"calls"`
	Errors     uint64  `json:"errors"`
	ErrorRate  float64 `json:"errorRate"`
	P99Ms      float64 `json:"p99Ms"`
	PriorP99Ms float64 `json:"priorP99Ms,omitempty"`
	Ratio      float64 `json:"ratio,omitempty"` // p99 / önceki pencere p99 (0 = kıyas yok)
	HasPrior   bool    `json:"hasPrior"`
}

func reportOpFrom(o chstore.OperationSummary) reportOp {
	r := reportOp{Name: o.Name, Calls: o.SpanCount, Errors: o.ErrorCount, ErrorRate: o.ErrorRate, P99Ms: o.P99Ms, HasPrior: o.HasPrior}
	if o.HasPrior && o.PriorP99Ms > 0 {
		r.PriorP99Ms = o.PriorP99Ms
		r.Ratio = o.P99Ms / o.PriorP99Ms
	}
	return r
}

// rankOpDeltas — SAF: girdi ≤500 satıra kırpılır. slow = ≥20 çağrılı
// operasyonlardan p99'u en yüksek 5'i, önceki pencereye KAT sırasıyla
// (kıyası olmayanlar sonra, p99 sırasıyla); erroring = hata sayısı en
// yüksek 5 (eşitlikte oran, sonra ad).
func rankOpDeltas(ops []chstore.OperationSummary) (slow, erroring []reportOp) {
	if len(ops) > serviceReportOpsInputCap {
		ops = ops[:serviceReportOpsInputCap]
	}
	slow, erroring = []reportOp{}, []reportOp{}
	for _, o := range ops {
		if o.SpanCount >= serviceReportOpMinCalls && o.P99Ms > 0 {
			slow = append(slow, reportOpFrom(o))
		}
		if o.ErrorCount > 0 {
			erroring = append(erroring, reportOpFrom(o))
		}
	}
	sort.SliceStable(slow, func(i, j int) bool {
		if slow[i].P99Ms != slow[j].P99Ms {
			return slow[i].P99Ms > slow[j].P99Ms
		}
		return slow[i].Name < slow[j].Name
	})
	if len(slow) > serviceReportOpsTop {
		slow = slow[:serviceReportOpsTop]
	}
	sort.SliceStable(slow, func(i, j int) bool {
		a, b := slow[i], slow[j]
		if (a.Ratio > 0) != (b.Ratio > 0) {
			return a.Ratio > 0
		}
		if a.Ratio != b.Ratio {
			return a.Ratio > b.Ratio
		}
		if a.P99Ms != b.P99Ms {
			return a.P99Ms > b.P99Ms
		}
		return a.Name < b.Name
	})
	sort.SliceStable(erroring, func(i, j int) bool {
		a, b := erroring[i], erroring[j]
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		if a.ErrorRate != b.ErrorRate {
			return a.ErrorRate > b.ErrorRate
		}
		return a.Name < b.Name
	})
	if len(erroring) > serviceReportOpsTop {
		erroring = erroring[:serviceReportOpsTop]
	}
	return slow, erroring
}

// ─── Trace seçimi (SAF) ─────────────────────────────────────────────────

// exGroupBursting — Oracle grubunun son 1 saati önceki saatin ≥3 katı
// (exGroupBurstFactor; önceki saat 0 iken her yeni akış).
func exGroupBursting(l exGroupLine) bool {
	return l.Oracle && l.HourKnown && l.LastHour > 0 && l.LastHour >= exGroupBurstFactor*l.PrevHour
}

// exGroupHead — grubun kısa adı (Oracle: kod · operasyon; span: tip).
func exGroupHead(l exGroupLine) string {
	if l.Oracle {
		h := clipRunes(l.Code, exGroupCodeRunes)
		if l.Operation != "" {
			h += " · " + clipRunes(l.Operation, exGroupOpRunes)
		}
		return h
	}
	if l.Type != "" {
		return clipRunes(l.Type, exGroupOpRunes)
	}
	return clipRunes(oneLine(l.Message), exGroupOpRunes)
}

// pickReportTraces — SAF: ≤3 trace, tekrarsız.
//   - Exceptions grubu örnek trace taşıyorsa: penceredeki EN YAVAŞ trace +
//     farklı gruplardan ≤2 örnek trace (patlayan Oracle grubu önce, sonra
//     liste sırası — en yeni önce); örnek eksik kalırsa en hatalı
//     operasyonun hatalı trace'i, sonra 2. en yavaş ile tamamlanır.
//   - Exceptions yoksa: 2 en yavaş + en hatalı operasyonun (topErrOp) hatalı
//     trace'i (kökü o operasyon olan; yoksa en yavaş hatalı trace).
func pickReportTraces(slowRows, errRows []chstore.TraceRow, ex exceptionGroupsEvidence, topErrOp string) []drillCandidate {
	slow := append([]chstore.TraceRow(nil), slowRows...)
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].DurationMs > slow[j].DurationMs })
	errs := append([]chstore.TraceRow(nil), errRows...)
	sort.SliceStable(errs, func(i, j int) bool { return errs[i].DurationMs > errs[j].DurationMs })

	var out []drillCandidate
	seen := map[string]bool{}
	add := func(r chstore.TraceRow, label string) {
		id := strings.ToLower(strings.TrimSpace(r.TraceID))
		if id == "" || seen[id] || len(out) >= serviceReportTracesMax {
			return
		}
		seen[id] = true
		out = append(out, drillCandidate{Row: r, Label: label})
	}
	if len(slow) > 0 {
		add(slow[0], "en yavaş")
	}
	if ex.Err == "" {
		groups := append([]exGroupLine(nil), ex.Groups...)
		sort.SliceStable(groups, func(i, j int) bool { return exGroupBursting(groups[i]) && !exGroupBursting(groups[j]) })
		picked := 0
		for _, g := range groups {
			if picked >= serviceReportExTracesMax {
				break
			}
			for _, id := range g.SampleTraceIDs {
				if id = strings.TrimSpace(id); id == "" || seen[strings.ToLower(id)] {
					continue
				}
				add(chstore.TraceRow{TraceID: id, HasError: true}, "teknik hata örneği · "+exGroupHead(g))
				picked++
				break
			}
		}
	}
	// Tamamlama: en hatalı operasyonun hatalı trace'i, sonra 2. en yavaş.
	var errPick *chstore.TraceRow
	for i := range errs {
		if topErrOp != "" && errs[i].RootName == topErrOp && !seen[strings.ToLower(errs[i].TraceID)] {
			errPick = &errs[i]
			break
		}
	}
	if errPick == nil {
		for i := range errs {
			if !seen[strings.ToLower(errs[i].TraceID)] {
				errPick = &errs[i]
				break
			}
		}
	}
	addSecondSlow := func() {
		for _, r := range slow {
			if !seen[strings.ToLower(r.TraceID)] {
				add(r, "2. en yavaş")
				return
			}
		}
	}
	addErr := func() {
		if errPick != nil {
			label := "en hatalı operasyonun hatalı trace'i"
			if topErrOp == "" || errPick.RootName != topErrOp {
				label = "en yavaş hatalı"
			}
			add(*errPick, label)
		}
	}
	if len(out) <= 1 { // Exceptions örneği yok: 2 en yavaş + 1 hatalı
		addSecondSlow()
		addErr()
	} else {
		addErr()
		addSecondSlow()
	}
	return out
}

// ─── Hüküm (SAF) ────────────────────────────────────────────────────────

// reportRED — hükmün servis RED girdisi.
type reportRED struct {
	Cur, Base    healthverdict.RED
	Err          error
	OpenProblems int
	ErrTraces    int
}

// reportOpsView — operasyon sıralaması + okuma durumu.
type reportOpsView struct {
	Slow     []reportOp
	Erroring []reportOp
	Err      error
	Read     int // okunan operasyon satırı sayısı
}

// exGroupSeenLastHour — grup pencerenin son 1 saatinde görüldü mü.
func exGroupSeenLastHour(l exGroupLine, at time.Time) bool {
	if at.IsZero() || l.LastSeen <= 0 {
		return false
	}
	return l.LastSeen >= at.Add(-time.Hour).UnixNano()
}

// serviceReportVerdict — SAF: Sağlıklı / Dikkat / Sorunlu + gerekçeler.
// Servis RED kolu healthverdict.Service eşikleriyle (servis sağlığı cevabıyla
// AYNI sayılar); üstüne teknik hata, operasyon ve trace kolları — etiket
// kollardan KÖTÜSÜ. Okunamayan çekirdek kaynak sağlığı doğrulatmaz: en az
// Dikkat ("okunamadı ≠ yok").
func serviceReportVerdict(red reportRED, ops reportOpsView, ex exceptionGroupsEvidence, drills []traceDrill) (string, []string) {
	label := healthverdict.LabelHealthy
	var reasons []string
	raise := func(l string) {
		if healthverdict.LabelRank(l) > healthverdict.LabelRank(label) {
			label = l
		}
	}
	rl, rr, _ := healthverdict.Service(red.Cur, red.Base, red.Err, red.OpenProblems, red.ErrTraces)
	if rl == healthverdict.LabelNoData {
		raise(healthverdict.LabelWarn)
		reason := "servis RED: bu pencerede span verisi yok — sağlık doğrulanamadı"
		if red.Err != nil {
			reason = fmt.Sprintf("servis RED OKUNAMADI (%s) — sağlık doğrulanamadı", sourcestate.Classify(red.Err))
		}
		reasons = append(reasons, reason)
	} else {
		raise(rl)
		reasons = append(reasons, rr...)
	}

	switch {
	case ex.Err != "":
		raise(healthverdict.LabelWarn)
		reasons = append(reasons, "teknik hata grupları OKUNAMADI — yokluk doğrulanamadı")
	case len(ex.Groups) > 0:
		lastHour := 0
		var burst, p1 *exGroupLine
		for i := range ex.Groups {
			g := &ex.Groups[i]
			if exGroupSeenLastHour(*g, ex.at) {
				lastHour++
				if p1 == nil && g.Priority == "P1" {
					p1 = g
				}
			}
			if burst == nil && exGroupBursting(*g) {
				burst = g
			}
		}
		switch {
		case burst != nil:
			if burst.LastHour >= serviceReportBurstMin {
				raise(healthverdict.LabelCrit)
			} else {
				raise(healthverdict.LabelWarn)
			}
			reasons = append(reasons, fmt.Sprintf("teknik hata patlaması: %s son 1 sa %s / önceki 1 sa %s", exGroupHead(*burst), fmtThousands(burst.LastHour), fmtThousands(burst.PrevHour)))
		case p1 != nil:
			raise(healthverdict.LabelCrit)
			reasons = append(reasons, "P1 teknik hata grubu son 1 saatte: "+exGroupHead(*p1))
		case lastHour > 0:
			raise(healthverdict.LabelWarn)
			reasons = append(reasons, fmt.Sprintf("son 1 saatte %d teknik hata grubu görüldü", lastHour))
		default:
			reasons = append(reasons, fmt.Sprintf("son 3 saatte %d teknik hata grubu (son 1 saatte yok)", len(ex.Groups)))
		}
	}

	if ops.Err != nil {
		raise(healthverdict.LabelWarn)
		reasons = append(reasons, "operasyonlar OKUNAMADI")
	} else {
		for _, o := range ops.Slow {
			if o.Ratio >= serviceReportSlowRatio && o.P99Ms >= serviceReportSlowMinMs {
				raise(healthverdict.LabelWarn)
				reasons = append(reasons, fmt.Sprintf("operasyon %s p99 %s (önceki pencere ×%.1f)", o.Name, fmtDrillMs(o.P99Ms), o.Ratio))
				break
			}
		}
		for _, o := range ops.Erroring {
			if o.ErrorRate >= serviceReportOpErrPct && o.Errors >= serviceReportOpErrMin {
				raise(healthverdict.LabelWarn)
				reasons = append(reasons, fmt.Sprintf("operasyon %s hata %%%.2f (%d hata)", o.Name, o.ErrorRate, o.Errors))
				break
			}
		}
	}

	for _, d := range drills {
		if d.Err != nil || d.NotFound || !d.Pick.OK {
			continue
		}
		if d.Fail.HTTPCode >= 500 || d.Fail.ExType != "" {
			raise(healthverdict.LabelWarn)
			what := d.Fail.ExType
			if what == "" {
				what = fmt.Sprintf("HTTP %d", d.Fail.HTTPCode)
			}
			reasons = append(reasons, fmt.Sprintf("trace kanıtı (%s): baskın span %s hatası, hedef %s", d.Label, what, d.Peer.Target))
			break
		}
	}
	if len(reasons) > serviceReportReasonsMax {
		reasons = reasons[:serviceReportReasonsMax]
	}
	return label, reasons
}

// ─── Kanıt metni (SAF) ──────────────────────────────────────────────────

// serviceReportData — render girdisi (okumaların sonucu, hükümle).
type serviceReportData struct {
	Service, Env string
	RangeS       int64
	Snapshot     string // renderServiceSnapshot çıktısı
	SnapshotErr  error
	NoSpans      bool
	Ops          reportOpsView
	Ex           exceptionGroupsEvidence
	Cands        []drillCandidate
	Drills       []traceDrill
	TracesErr    error // en yavaş trace listesi okunamadı
	LogBackend   string
	LogsNoStore  bool
	LogsErr      error
	LogsPage     *logstore.Page
	Problems     []chstore.Problem
	ProbTotal    problemsTotal
	ProbErr      error
	Verdict      string
	Reasons      []string
	Now          time.Time
}

// capSectionTR — SAF: bölümü bayt tavanına satır sınırında keser.
func capSectionTR(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndex(s[:max], "\n")
	if cut <= 0 {
		cut = max
	}
	return s[:cut+1] + "(… bölüm kanıt bütçesi için kısaltıldı)\n"
}

func renderReportOpsTR(ops reportOpsView, snapshot string, snapErr error, noSpans bool) string {
	var b strings.Builder
	b.WriteString("OPERASYONLAR (servis RED + operation_summary_5m; önceki eşit pencereyle kıyas):\n")
	for _, ln := range strings.Split(strings.TrimRight(snapshot, "\n"), "\n") {
		if ln = strings.TrimSpace(ln); ln == "" || strings.HasPrefix(ln, "Servis:") {
			continue
		}
		b.WriteString("KANIT: servis " + ln + "\n")
	}
	if snapErr == nil && noSpans {
		b.WriteString("KANIT: servis bu pencerede span verisi yok.\n")
	}
	switch {
	case ops.Err != nil:
		fmt.Fprintf(&b, "Operasyonlar OKUNAMADI (%s) — 'operasyon yok' DEMEK DEĞİL; sayı uydurma.\n", sourcestate.Classify(ops.Err))
		return b.String()
	case ops.Read == 0:
		b.WriteString("Bu pencerede operasyon verisi yok.\n")
		return b.String()
	}
	if len(ops.Slow) == 0 {
		fmt.Fprintf(&b, "En yavaş operasyonlar: ≥%d çağrılı operasyon yok (%d operasyon okundu).\n", serviceReportOpMinCalls, ops.Read)
	} else {
		fmt.Fprintf(&b, "En yavaş operasyonlar (p99, ≥%d çağrı; önceki pencereye kat):\n", serviceReportOpMinCalls)
		for _, o := range ops.Slow {
			prior := "önceki pencerede yok"
			if o.Ratio > 0 {
				prior = fmt.Sprintf("önceki %s, ×%.1f", fmtDrillMs(o.PriorP99Ms), o.Ratio)
			}
			fmt.Fprintf(&b, "KANIT: op %q — p99 %s (%s) · %s çağrı · hata %%%.2f\n", o.Name, fmtDrillMs(o.P99Ms), prior, fmtThousands(o.Calls), o.ErrorRate)
		}
	}
	if len(ops.Erroring) == 0 {
		b.WriteString("En çok hata alan operasyonlar: hata alan operasyon yok.\n")
	} else {
		b.WriteString("En çok hata alan operasyonlar:\n")
		for _, o := range ops.Erroring {
			fmt.Fprintf(&b, "KANIT: op %q — %s hata / %s çağrı (%%%.2f)\n", o.Name, fmtThousands(o.Errors), fmtThousands(o.Calls), o.ErrorRate)
		}
	}
	return b.String()
}

func renderReportExceptionsTR(ex exceptionGroupsEvidence, env string) string {
	var b strings.Builder
	b.WriteString("TEKNİK HATALAR (son 1 sa / 3 sa — Exceptions grupları, en yeni önce):\n")
	if env != "" {
		fmt.Fprintf(&b, "Not: Exceptions grupları ortam boyutu taşımaz — %q filtresi UYGULANMADI.\n", env)
	}
	if ex.Err != "" {
		fmt.Fprintf(&b, "Exceptions grupları OKUNAMADI (%s); grup olup olmadığı bilinmiyor — \"teknik hata yok\" deme.\n", clipRunes(oneLine(ex.Err), 120))
		return b.String()
	}
	if len(ex.Groups) == 0 {
		b.WriteString("Grup yok: son 3 saatte bu serviste Exceptions teknik hata grubu bulunmadı.\n")
		return b.String()
	}
	last := 0
	for _, g := range ex.Groups {
		if exGroupSeenLastHour(g, ex.at) {
			last++
		}
	}
	more := ""
	if ex.More > 0 {
		more = fmt.Sprintf(" (+%d grup daha gösterilmedi", ex.More)
		if ex.MoreAtMin {
			more = fmt.Sprintf(" (+en az %d grup daha gösterilmedi", ex.More)
		}
		more += ")"
	}
	fmt.Fprintf(&b, "KANIT: son 1 saatte görülen grup %d, son 3 saatte görülen grup %d%s\n", last, len(ex.Groups), more)
	for i, g := range ex.Groups {
		when := "yalnız son 3 saatte (son 1 saatte yok)"
		if exGroupSeenLastHour(g, ex.at) {
			when = "son 1 saatte görüldü"
		}
		fmt.Fprintf(&b, "KANIT: %d. %s", i+1, exGroupHead(g))
		if g.Priority != "" {
			fmt.Fprintf(&b, " · %s", g.Priority)
		}
		fmt.Fprintf(&b, " · durum %s · %s", g.State, when)
		if g.LastSeen > 0 && !ex.at.IsZero() {
			ls := time.Unix(0, g.LastSeen).UTC()
			fmt.Fprintf(&b, " · son görülme %s önce (%s UTC)", fmtAgoTR(int64(ex.at.Sub(ls)/time.Second)), ls.Format("01-02 15:04"))
		}
		fmt.Fprintf(&b, " · toplam %s · %s · Exceptions: %s\n", fmtThousands(g.Occurrences), exGroupTrendTR(g), g.Href)
		if i >= exGroupSampleGroups {
			continue
		}
		switch {
		case g.Sample != "":
			fmt.Fprintf(&b, "   ne demek: örnek satır mesajı %q\n", g.Sample)
		case !g.Oracle && g.Message != "":
			fmt.Fprintf(&b, "   ne demek: mesaj %q\n", clipRunes(oneLine(g.Message), exGroupSampleRunes))
		}
		if len(g.Services) > 0 {
			fmt.Fprintf(&b, "   nerede yoğunlaşıyor: servis %s\n", strings.Join(g.Services, ", "))
		}
		if len(g.SampleTraceIDs) > 0 {
			fmt.Fprintf(&b, "   örnek trace: %s\n", strings.Join(g.SampleTraceIDs, ", "))
		}
	}
	return b.String()
}

func renderReportTracesTR(cands []drillCandidate, drills []traceDrill, tracesErr error) string {
	var b strings.Builder
	b.WriteString("TRACE KANITI (deterministik kritik yol — kritik yolda öz süresi en büyük span):\n")
	if len(drills) == 0 {
		if tracesErr != nil {
			fmt.Fprintf(&b, "Trace listesi OKUNAMADI (%s) — kritik yol bilinmiyor; hedef UYDURMA.\n", sourcestate.Classify(tracesErr))
		} else {
			b.WriteString("Bu pencerede kırılacak trace yok.\n")
		}
		return b.String()
	}
	count := map[string]int{}
	var order []string
	ok := 0
	for _, d := range drills {
		fmt.Fprintf(&b, "KANIT: %s: %s\n", d.Label, renderTraceDrillTR(d))
		if d.Err != nil || d.NotFound || !d.Pick.OK {
			continue
		}
		ok++
		if t := d.Peer.Target; t != "" {
			if count[t] == 0 {
				order = append(order, t)
			}
			count[t]++
		}
	}
	for _, t := range order {
		if count[t] >= 2 {
			fmt.Fprintf(&b, "KANIT: ortak hedef %s (%d/%d trace)\n", t, count[t], ok)
		}
	}
	return b.String()
}

func reportTraceIDs(drills []traceDrill) []string {
	ids := make([]string, 0, len(drills))
	for _, d := range drills {
		ids = append(ids, d.TraceID)
	}
	return ids
}

func renderReportLogsTR(d serviceReportData) string {
	var b strings.Builder
	backend := d.LogBackend
	if backend == "" {
		backend = "yok"
	}
	fmt.Fprintf(&b, "LOG KANITI (yalnız bu trace id'leri, trace zarfı ±1 dk; log deposu: %s):\n", backend)
	ids := reportTraceIDs(d.Drills)
	switch {
	case len(ids) == 0:
		b.WriteString("Kırılan trace yok — log okunmadı.\n")
		return b.String()
	case d.LogsNoStore:
		b.WriteString("Log deposu yapılandırılmamış — bu trace'lerin logları okunamadı (depo yok).\n")
		return b.String()
	case d.LogsErr != nil:
		fmt.Fprintf(&b, "Loglar OKUNAMADI (%s) — bu 'log yok' DEMEK DEĞİL; log içeriği UYDURMA.\n", sourcestate.Classify(d.LogsErr))
		return b.String()
	}
	var logs []*logstore.LogRecord
	if d.LogsPage != nil {
		logs = d.LogsPage.Logs
	}
	for i, g := range groupTraceLogs(logs, ids, serviceReportLogsPerTrace) {
		if len(g) == 0 {
			fmt.Fprintf(&b, "KANIT: trace %s: bu trace'e bağlı log kaydı YOK.\n", drillTraceID12(ids[i]))
			continue
		}
		for k, lg := range g {
			if k >= serviceReportEvLogLines {
				fmt.Fprintf(&b, "(trace %s: +%d satır daha kartta)\n", drillTraceID12(ids[i]), len(g)-k)
				break
			}
			b.WriteString(renderTraceLogLineTR(lg) + "\n")
		}
	}
	return b.String()
}

func renderReportProblemsTR(d serviceReportData) string {
	var b strings.Builder
	b.WriteString("AÇIK PROBLEMLER + HİPOTEZ:\n")
	switch {
	case d.ProbErr != nil:
		fmt.Fprintf(&b, "Problem kayıtları OKUNAMADI (%s) — 'açık problem yok' DEMEK DEĞİL; hipotez de okunamadı.\n", sourcestate.Classify(d.ProbErr))
		return b.String()
	case len(d.Problems) == 0:
		b.WriteString("Açık problem yok — kayıtlı kök-neden hipotezi de yok.\n")
		return b.String()
	}
	probs := d.Problems
	if len(probs) > serviceReportProblemsMax {
		probs = probs[:serviceReportProblemsMax]
	}
	blk := renderProblemsEvidenceTR(probs, d.Service, d.Env, d.Now, d.ProbTotal)
	for _, ln := range strings.Split(strings.TrimRight(blk, "\n"), "\n") {
		if strings.HasPrefix(ln, "- ") {
			ln = "KANIT: " + strings.TrimPrefix(ln, "- ")
		}
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// renderServiceReportTR — SAF: modele giden rapor verisi. HÜKÜM EN ÜSTTE,
// sonra sabit bölümler; her bölüm kendi bayt tavanıyla (toplam ≤ ~6 KB).
// OKUNAMADI, "boş" ve "depo yok" her bölümde ayrı cümleler.
func renderServiceReportTR(d serviceReportData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "HÜKÜM (deterministik eşik, model değil): %s — %s.\n", d.Verdict, strings.Join(d.Reasons, "; "))
	scope := fmt.Sprintf("servis %s", d.Service)
	if d.Env != "" {
		scope += " · ortam " + d.Env
	}
	fmt.Fprintf(&b, "SORU ŞEKLİ: servis raporu — %s; pencere son %s, teknik hatalar son 3sa.\n\n", scope, fmtAgoTR(d.RangeS))
	b.WriteString(capSectionTR(renderReportOpsTR(d.Ops, d.Snapshot, d.SnapshotErr, d.NoSpans), 1400) + "\n")
	b.WriteString(capSectionTR(renderReportExceptionsTR(d.Ex, d.Env), 1400) + "\n")
	b.WriteString(capSectionTR(renderReportTracesTR(d.Cands, d.Drills, d.TracesErr), 1200) + "\n")
	b.WriteString(capSectionTR(renderReportLogsTR(d), 1000) + "\n")
	b.WriteString(capSectionTR(renderReportProblemsTR(d), 700) + "\n")
	b.WriteString("KURAL: HÜKÜM deterministik eşikten gelir — DEĞİŞTİRME. KANIT satırlarını aynen alıntıla; OKUNAMADI ≠ yok; log satırı yoksa log içeriği UYDURMA.\n")
	return capSectionTR(b.String(), serviceReportEvidenceMax)
}

// ─── Kart yükü ──────────────────────────────────────────────────────────

// reportLogLine — kartın log satırı (gövde ≤200 rune).
type reportLogLine struct {
	Timestamp int64  `json:"ts"` // unix ns
	Severity  string `json:"severity"`
	Service   string `json:"service"`
	Body      string `json:"body"`
}

// traceDrillView — bir trace kırılımının kart hâli (+ o trace'in logları).
type traceDrillView struct {
	TraceID   string          `json:"traceId"`
	Label     string          `json:"label"`
	Source    string          `json:"source,omitempty"`
	OK        bool            `json:"ok"`
	Text      string          `json:"text"` // renderTraceDrillTR (kanıttaki cümle)
	Span      string          `json:"span,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Target    string          `json:"target,omitempty"`
	Path      string          `json:"path,omitempty"`
	Service   string          `json:"service,omitempty"`
	SelfMs    float64         `json:"selfMs,omitempty"`
	WallMs    float64         `json:"wallMs,omitempty"`
	SharePct  float64         `json:"sharePct,omitempty"`
	HTTPCode  int             `json:"httpCode,omitempty"`
	Error     string          `json:"error,omitempty"`
	BaseP50Ms float64         `json:"baseP50Ms,omitempty"`
	LogsState string          `json:"logsState"` // ok | empty | no_store | error | none
	Logs      []reportLogLine `json:"logs"`
}

// serviceReportOpsPayload — kartın operasyon bölümü.
type serviceReportOpsPayload struct {
	Slow     []reportOp `json:"slow"`
	Erroring []reportOp `json:"erroring"`
	Err      string     `json:"err,omitempty"`
}

// serviceReportREDPayload — kartın servis RED satırı.
type serviceReportREDPayload struct {
	Spans         uint64  `json:"spans"`
	ErrorRate     float64 `json:"errorRate"`
	P99Ms         float64 `json:"p99Ms"`
	BaseErrorRate float64 `json:"baseErrorRate"`
	BaseP99Ms     float64 `json:"baseP99Ms"`
	Unavailable   string  `json:"unavailable,omitempty"`
}

// serviceReportPayload — blocks.TypeServiceReport gövdesi (FE ServiceReportCard,
// lib/types.ts ChatServiceReportPayload).
type serviceReportPayload struct {
	Service         string                   `json:"service"`
	Env             string                   `json:"env,omitempty"`
	RangeS          int64                    `json:"rangeS"`
	Verdict         string                   `json:"verdict"`
	Reasons         []string                 `json:"reasons"`
	RED             *serviceReportREDPayload `json:"red,omitempty"`
	Ops             serviceReportOpsPayload  `json:"ops"`
	Exceptions      []exGroupLine            `json:"exceptions"`
	ExceptionsError string                   `json:"exceptionsError,omitempty"`
	Drills          []traceDrillView         `json:"drills"`
	LogBackend      string                   `json:"logBackend,omitempty"`
	Links           []guidedAnswerLink       `json:"links"`
}

// buildTraceDrillViews — SAF: kırılımlar + gruplanmış loglar → kart satırları.
func buildTraceDrillViews(d serviceReportData) []traceDrillView {
	ids := reportTraceIDs(d.Drills)
	var logs []*logstore.LogRecord
	if d.LogsPage != nil {
		logs = d.LogsPage.Logs
	}
	grouped := groupTraceLogs(logs, ids, serviceReportLogsPerTrace)
	out := make([]traceDrillView, 0, len(d.Drills))
	for i, dr := range d.Drills {
		v := traceDrillView{TraceID: dr.TraceID, Label: dr.Label, Source: traceSourceTR(dr.Source), Text: renderTraceDrillTR(dr), Logs: []reportLogLine{}}
		if dr.Err == nil && !dr.NotFound && dr.Pick.OK {
			sp := dr.Pick.Span
			v.OK = true
			v.Span, v.Kind, v.Service = sp.Name, strings.ToUpper(sp.Kind), sp.ServiceName
			v.Target, v.Path = dr.Peer.Target, dr.Peer.Path
			v.SelfMs, v.WallMs = float64(dr.Pick.SelfNs)/1e6, float64(dr.Pick.WallNs)/1e6
			v.SharePct = dr.Pick.Share * 100
			v.HTTPCode = dr.Fail.HTTPCode
			switch {
			case dr.Fail.ExType != "" && dr.Fail.ExMsg != "":
				v.Error = dr.Fail.ExType + ": " + oneLineSample(dr.Fail.ExMsg, 200)
			case dr.Fail.ExType != "":
				v.Error = dr.Fail.ExType
			case dr.Fail.ExMsg != "":
				v.Error = oneLineSample(dr.Fail.ExMsg, 200)
			}
			if dr.BaseOK {
				v.BaseP50Ms = dr.BaseP50Ms
			}
		}
		switch {
		case d.LogsNoStore:
			v.LogsState = "no_store"
		case d.LogsErr != nil:
			v.LogsState = "error"
		case i < len(grouped) && len(grouped[i]) > 0:
			v.LogsState = "ok"
			for _, lg := range grouped[i] {
				v.Logs = append(v.Logs, reportLogLine{Timestamp: lg.Timestamp, Severity: drillSevTR(lg), Service: lg.ServiceName, Body: oneLineSample(lg.Body, traceDrillLogBodyRunes)})
			}
		default:
			v.LogsState = "empty"
		}
		out = append(out, v)
	}
	return out
}

func buildServiceReportPayload(d serviceReportData, red reportRED, links []guidedAnswerLink) serviceReportPayload {
	p := serviceReportPayload{
		Service: d.Service, Env: d.Env, RangeS: d.RangeS, Verdict: d.Verdict, Reasons: append([]string{}, d.Reasons...),
		Ops:        serviceReportOpsPayload{Slow: append([]reportOp{}, d.Ops.Slow...), Erroring: append([]reportOp{}, d.Ops.Erroring...)},
		Exceptions: append([]exGroupLine{}, d.Ex.Groups...), ExceptionsError: d.Ex.Err,
		Drills: buildTraceDrillViews(d), LogBackend: d.LogBackend, Links: append([]guidedAnswerLink{}, links...),
	}
	if d.Ops.Err != nil {
		p.Ops.Err = string(sourcestate.Classify(d.Ops.Err))
	}
	rp := &serviceReportREDPayload{Spans: red.Cur.Spans, ErrorRate: red.Cur.ErrorRate, P99Ms: red.Cur.P99Ms, BaseErrorRate: red.Base.ErrorRate, BaseP99Ms: red.Base.P99Ms}
	if red.Err != nil {
		rp = &serviceReportREDPayload{Unavailable: string(sourcestate.Classify(red.Err))}
	}
	p.RED = rp
	return p
}

// reportTraceListRows — SAF: kırılan trace'lerin trace_list satırları; örnek
// trace'lerin (liste satırı yok) zarfı kırılımdan doldurulur.
func reportTraceListRows(cands []drillCandidate, drills []traceDrill) []chstore.TraceRow {
	rows := make([]chstore.TraceRow, 0, len(cands))
	for i, c := range cands {
		r := c.Row
		if i < len(drills) {
			d := drills[i]
			if r.StartTime == 0 && d.MinNs > 0 {
				r.StartTime = d.MinNs
				if d.MaxNs > d.MinNs {
					r.DurationMs = float64(d.MaxNs-d.MinNs) / 1e6
				}
			}
			if r.ServiceName == "" {
				r.ServiceName = d.RootSvc
			}
			if r.RootName == "" {
				r.RootName = d.RootName
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// drillLogEnvelope — SAF: log penceresi. Bilinen span zarflarının birleşimi;
// bir trace'in zarfı bilinmiyorsa (span okunamadı, liste satırı da yok)
// sohbet penceresi de katılır.
func drillLogEnvelope(cands []drillCandidate, drills []traceDrill, from, to time.Time) (int64, int64) {
	var lo, hi int64
	unknown := false
	widen := func(a, b int64) {
		if a > 0 && (lo == 0 || a < lo) {
			lo = a
		}
		if b > hi {
			hi = b
		}
	}
	for i, d := range drills {
		a, z := d.MinNs, d.MaxNs
		if a == 0 && i < len(cands) && cands[i].Row.StartTime > 0 {
			a = cands[i].Row.StartTime
			z = a + int64(cands[i].Row.DurationMs*1e6)
		}
		if a == 0 {
			unknown = true
			continue
		}
		widen(a, z)
	}
	if unknown || lo == 0 {
		widen(from.UnixNano(), to.UnixNano())
	}
	return lo, hi
}

// ─── Önbellek ───────────────────────────────────────────────────────────

// reportStep — kaydedilen adım çipi (isabette yeniden yayınlanır).
type reportStep struct {
	Tool string `json:"tool"`
	Args string `json:"args"`
	Text string `json:"text"`
	Err  string `json:"err,omitempty"`
}

// serviceReportCached — 60 sn önbellek gövdesi: deterministik yük + kanıt.
// Anlatım (model cevabı) BURADA YOK.
type serviceReportCached struct {
	Evidence  string               `json:"evidence"`
	Sources   string               `json:"sources"`
	Steps     []reportStep         `json:"steps"`
	TraceList *traceListPayload    `json:"traceList,omitempty"`
	Report    serviceReportPayload `json:"report"`
}

// serviceReportCacheKey — SAF: TÜM girdiler FNV'ye (servis, ortam, dakikaya
// oturtulmuş from/to); uzunluk-yalnız anahtar yok (v0.5.187 kuralı).
func serviceReportCacheKey(service, env string, from, to time.Time) string {
	h := fnv.New64a()
	for _, p := range []string{service, env, fmt.Sprint(from.Unix() / 60), fmt.Sprint(to.Unix() / 60)} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	// v0.10.1410 — "v2": kanıttaki komşular topoloji özetinden (DB/dış
	// çağrılar dahil); eski önbellek gövdesi DB-kör komşu listesini taşıyor.
	return fmt.Sprintf("copilot:guided:svcreport:v2:%x", h.Sum64())
}

// reportRecorder — adım çiplerini yayınlar ve önbellek için kaydeder.
type reportRecorder struct {
	emit  func(string, any)
	steps []reportStep
	ids   []int
}

func (r *reportRecorder) step(tool, args string) int {
	r.steps = append(r.steps, reportStep{Tool: tool, Args: args})
	r.ids = append(r.ids, emitGuidedStep(r.emit, tool, args))
	return len(r.steps) - 1
}

func (r *reportRecorder) done(k int, text string, err error) {
	if k < 0 || k >= len(r.steps) {
		return
	}
	r.steps[k].Text = text
	if err != nil {
		r.steps[k].Err = err.Error()
	}
	emitGuidedStepResult(r.emit, r.ids[k], r.steps[k].Tool, text, err)
}

// replayReportSteps — önbellek isabetinde aynı adım çiplerini aynı kanıtla yayınlar.
func replayReportSteps(emit func(string, any), steps []reportStep) {
	for _, st := range steps {
		i := emitGuidedStep(emit, st.Tool, st.Args)
		var err error
		if st.Err != "" {
			err = errors.New(st.Err)
		}
		emitGuidedStepResult(emit, i, st.Tool, st.Text, err)
	}
}

func (s *Server) serviceReportFromCache(ctx context.Context, key string) (serviceReportCached, bool) {
	var c serviceReportCached
	if s.cache == nil {
		return c, false
	}
	b, ok, _ := s.cache.Get(ctx, key)
	if !ok || len(b) == 0 || json.Unmarshal(b, &c) != nil || c.Evidence == "" {
		return serviceReportCached{}, false
	}
	return c, true
}

func (s *Server) serviceReportToCache(ctx context.Context, key string, c serviceReportCached) {
	if s.cache == nil {
		return
	}
	if b, err := json.Marshal(c); err == nil {
		_ = s.cache.Set(ctx, key, b, serviceReportCacheTTL)
	}
}

// ─── Demet ──────────────────────────────────────────────────────────────

func (s *Server) logBackendName() string {
	if s.logs == nil {
		return ""
	}
	return s.logs.Backend()
}

// guidedServiceReportBundle — service_report rotasının kanıtı (dosya başı).
// Hiçbir okuma hatası demeti düşürmez: kaynak OKUNAMADI diye yazılır.
func (s *Server) guidedServiceReportBundle(ctx context.Context, emit func(string, any), route guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	svc, env := route.Service, route.Env
	if s.store == nil {
		return "", "", errors.New("servis raporu: depo yok")
	}
	key := serviceReportCacheKey(svc, env, from, to)
	if c, ok := s.serviceReportFromCache(ctx, key); ok {
		emitGuidedContextStep(emit, "önbellek: aynı servis raporu (≤60 sn)")
		replayReportSteps(emit, c.Steps)
		if c.TraceList != nil {
			emit("trace_list", *c.TraceList)
		}
		emit(blocks.TypeServiceReport, c.Report)
		return c.Evidence, c.Sources, nil
	}
	bctx, cancel := context.WithTimeout(ctx, serviceReportBudget)
	defer cancel()
	rec := &reportRecorder{emit: emit}
	svcArg := `{"service":` + jsonStr(svc) + `}`

	// Faz 1 — paralel okumalar; çipler önce, sonuçlar sonra.
	kCtx := rec.step("service_context", svcArg)
	kProb := rec.step("list_problems", withEnvArg(`{"service":`+jsonStr(svc)+`,"status":"open"}`, env))
	kOps := rec.step("top_operations", withEnvArg(`{"service":`+jsonStr(svc)+`,"compare":"prior"}`, env))
	kEx := rec.step("exception_groups", fmt.Sprintf(`{"service":%s,"range_s":%d}`, jsonStr(svc), int64(serviceReportExLookback/time.Second)))
	kTr := rec.step("slow_traces", withEnvArg(`{"service":`+jsonStr(svc)+`,"sort":"duration"}`, env))

	p1, c1 := context.WithTimeout(bctx, serviceReportPhase1)
	var (
		wg        sync.WaitGroup
		cx        *aiServiceContext
		probs     []chstore.Problem
		probTotal problemsTotal
		probErr   error
		ops       []chstore.OperationSummary
		opsErr    error
		ex        exceptionGroupsEvidence
		slowRows  []chstore.TraceRow
		errRows   []chstore.TraceRow
		trErr     error
	)
	wg.Add(5)
	go func() {
		defer wg.Done()
		cx = s.buildServiceContext(p1, svc, from, to)
	}()
	go func() {
		defer wg.Done()
		probs, probTotal, probErr = s.guidedProblemsWithTotal(p1, guidedProblemFilter(svc, env, 10))
		if probErr == nil && len(probs) > 0 {
			probs = s.enrichProblemsForRead(p1, probs)
			probs = s.store.EnrichProblemsWithRootCause(p1, probs)
		}
	}()
	go func() {
		defer wg.Done()
		ops, opsErr = s.store.GetOperationSummaryCompared(p1, svc, 0, from, to, false, env)
	}()
	go func() {
		defer wg.Done()
		ex = s.exceptionGroupsEvidence(p1, exScope{Service: svc, From: to.Add(-serviceReportExLookback), To: to})
	}()
	go func() {
		defer wg.Done()
		slowRows, _, _, trErr = s.store.GetTraces(p1, guidedTraceFilter(svc, env, from, to))
		ef := guidedTraceFilter(svc, env, from, to)
		ef.HasError = true
		errRows, _, _, _ = s.store.GetTraces(p1, ef) // en hatalı operasyonun trace'i için; hata = yalnız eksik tamamlama
	}()
	wg.Wait()
	c1()

	snapshot := renderServiceSnapshot(cx)
	noSpans := cx.curErr == nil && cx.Current.Spans == 0
	ctxText := snapshot
	if noSpans {
		ctxText += "Bu pencerede span verisi yok — değişim de okunamıyor.\n"
	}
	rec.done(kCtx, ctxText, cx.curErr)
	if probErr != nil {
		rec.done(kProb, "", probErr)
	} else {
		rec.done(kProb, renderProblemsEvidenceTR(probs, svc, env, evidenceAsOf(to), probTotal), nil)
	}
	view := reportOpsView{Err: opsErr, Read: len(ops)}
	if opsErr == nil {
		view.Slow, view.Erroring = rankOpDeltas(ops)
	}
	rec.done(kOps, renderReportOpsTR(view, "", nil, false), opsErr)
	var exErr error
	if ex.Err != "" {
		exErr = fmt.Errorf("exceptions okunamadı: %s", ex.Err)
	}
	rec.done(kEx, renderReportExceptionsTR(ex, env), exErr)
	if trErr != nil {
		rec.done(kTr, "", trErr)
	} else {
		rec.done(kTr, renderSlowTracesEvidenceTR(slowRows, svc, env, rangeS), nil)
	}

	// Faz 2 — SAF seçim.
	topErrOp := ""
	if len(view.Erroring) > 0 {
		topErrOp = view.Erroring[0].Name
	}
	cands := pickReportTraces(slowRows, errRows, ex, topErrOp)

	// Faz 3 — paralel kırılım (traceDrillFor; goroutine'ler emit etmez).
	drills := make([]traceDrill, len(cands))
	if len(cands) > 0 {
		ks := make([]int, len(cands))
		for i, c := range cands {
			args, _ := json.Marshal(map[string]any{"trace_id": c.Row.TraceID, "pick": c.Label})
			ks[i] = rec.step("trace_drill", string(args))
		}
		p3, c3 := context.WithTimeout(bctx, serviceReportDrillPhase)
		var dwg sync.WaitGroup
		for i, c := range cands {
			dwg.Add(1)
			go func(i int, c drillCandidate) {
				defer dwg.Done()
				drills[i] = s.traceDrillFor(p3, c)
			}(i, c)
		}
		dwg.Wait()
		c3()
		for i, d := range drills {
			rec.done(ks[i], renderTraceDrillTR(d), d.Err)
		}
	}

	// Faz 4 — TEK log okuması (yapılandırılmış depo; env'den tahmin YOK).
	data := serviceReportData{
		Service: svc, Env: env, RangeS: rangeS, Snapshot: snapshot, SnapshotErr: cx.curErr, NoSpans: noSpans,
		Ops: view, Ex: ex, Cands: cands, Drills: drills, TracesErr: trErr, LogBackend: s.logBackendName(),
		LogsNoStore: s.logs == nil, Problems: probs, ProbTotal: probTotal, ProbErr: probErr, Now: evidenceAsOf(to),
	}
	if len(drills) > 0 {
		ids := reportTraceIDs(drills)
		largs, _ := json.Marshal(map[string]any{"trace_ids": ids, "limit": traceDrillLogsLimit})
		kLog := rec.step("trace_drill_logs", string(largs))
		if s.logs != nil {
			lo, hi := drillLogEnvelope(cands, drills, from, to)
			p4, c4 := context.WithTimeout(bctx, serviceReportLogsPhase)
			data.LogsPage, data.LogsErr = s.readDrillLogs(p4, ids, lo, hi, from, to)
			c4()
		}
		rec.done(kLog, renderReportLogsTR(data), data.LogsErr)
	}

	// Faz 5 — SAF hüküm, kanıt metni, kart.
	red := reportRED{
		Cur:  healthverdict.RED{Spans: cx.Current.Spans, ErrorRate: cx.Current.ErrorRate, P99Ms: cx.Current.P99Ms},
		Base: healthverdict.RED{Spans: cx.Baseline.Spans, ErrorRate: cx.Baseline.ErrorRate, P99Ms: cx.Baseline.P99Ms},
		Err:  cx.curErr, OpenProblems: len(probs), ErrTraces: len(errRows),
	}
	data.Verdict, data.Reasons = serviceReportVerdict(red, view, ex, drills)
	evidence := renderServiceReportTR(data)

	links := dedupLinksByHref(guidedAnswerLinks(route, linkWindowBetween(from, to)))
	report := buildServiceReportPayload(data, red, links)
	var tl *traceListPayload
	if len(cands) > 0 {
		href := "/traces?service=" + url.QueryEscape(svc) + "&sort=duration"
		p := endpointTraceListPayload("servis raporu: kırılan trace'ler", reportTraceListRows(cands, drills), from, to, href)
		tl = &p
		emit("trace_list", p)
	}
	emit(blocks.TypeServiceReport, report)

	backend := data.LogBackend
	if backend == "" {
		backend = "yok"
	}
	sources := fmt.Sprintf("servis RED + açık problemler + kök-neden hipotezleri + operasyonlar (önceki eşit pencereyle) + Exceptions grupları (son 3sa) + kırılan trace'lerin kritik yolu (Tempo/ClickHouse) + o trace'lerin logları (log deposu: %s) (son %s)", backend, fmtAgoTR(rangeS))
	if env != "" {
		sources += fmt.Sprintf(", ortam: %s (RED ve Exceptions tüm ortamlar)", env)
	}
	s.serviceReportToCache(ctx, key, serviceReportCached{Evidence: evidence, Sources: sources, Steps: rec.steps, TraceList: tl, Report: report})
	return evidence, sources, nil
}

// serviceReportAskable — SAF: öznesiz rapor isteği "hangi servis?" diye
// sorulur mu. Başka bir kılavuz yolu olan şekiller (problem / hata / vardiya /
// takım / yavaş / neden) kendi filo-geneli yollarında kalır ("açık problemleri
// analiz et" problems, "takımımın servislerini analiz et" my_services).
func serviceReportAskable(msg string, toks []string) bool {
	return !hasProblemSignal(toks) && !hasErrorSignal(toks) && !hasShiftSignal(msg, toks) &&
		!hasTeamSelfSignal(toks) && !hasSlowTraceSignal(msg) && !hasWhySignal(toks)
}
