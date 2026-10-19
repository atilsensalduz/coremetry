package api

// chat_cluster_compare.go — v0.10.1374 (operator-reported, CoSRE küme kıyası):
// bir endpoint iki bölgeden servis ediliyor, DB çoğunlukla birinde; kullanıcı
// "hem eu-a, eu-b hem tr-c kümelerinde çağrılıyor, tekrar eden DB/servis
// çağrılarına bak" diye soruyor. v0.10.1373 çok ortamlı soruyu tüm ortamlarda
// okuyup bunu söylüyordu, KIYASLAMIYORDU. Bu dosya endpoint ön-getirmesine
// (opPrefetchForTarget) üçüncü bir kanıt ekler:
//
//   - ortam başına RED: TEK ham sorgu GROUP BY deploy_env (chstore
//     RouteREDByEnv / PathREDByEnv; v0.8.385 env ham geri dönüşü, MV değişikliği
//     yok), pencere sohbet penceresi ≤6 sa, ≤6 ortam;
//   - ortam başına en yavaş ≤5 trace (paralel GetTraces, CountMode skip) ve
//     ≤4 ortamda en yavaş ≤2 trace'in ŞEKLİ (traceDrillCore → traceShape: DB
//     payı, tekrar eden çağrılar) — log okuması YOK;
//   - deterministik "KÜME KIYASI" bloğu (renderClusterCompareTR); hipotez
//     cümlesi yalnız HESAPLANMIŞ koşulda (tekrar sayıları ±%20 içinde VE çağrı
//     başına DB ortalaması oranı ≥2) — asla tahmin değil.
//
// Blok seed()'in ÜÇÜNCÜ sentetik tool çifti (cluster_compare); summary()
// değişmez. Emit yalnız ön-getirme goroutine'inden (start/finish); okumalar
// goroutine'lerde TOPLANIR. FE kartı ertelendi (yalnız metin bloğu).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const (
	opClusterToolID      = "cosre-cluster-compare"
	opClusterTool        = "cluster_compare"
	clusterCompareBudget = 10 * time.Second // RED + trace aramaları + kırılımlar
	clusterWindowMax     = 6 * time.Hour    // ham RED penceresi tavanı (operatör varsayılanı)
	clusterTraceLimit    = 5                // ortam başına en yavaş trace
	clusterDrillEnvMax   = 4                // kırılan ortam tavanı (RED ≤envSetMax)
	clusterRepeatTol     = 0.20             // hipotez: tekrar sayıları ±%20 içinde
	clusterDBRatioMin    = 2.0              // hipotez: çağrı başına DB ortalaması oranı ≥2
)

// clusterDrill — kırılan tek trace (liste satırı + kırılım).
type clusterDrill struct {
	Row chstore.TraceRow
	D   traceDrill
}

// clusterEnvEvidence — tek ortamın kanıtı. rows/drills JSON'a girmez.
type clusterEnvEvidence struct {
	Env      string        `json:"env"`
	RED      chstore.OpRED `json:"red"`
	REDErr   string        `json:"redError,omitempty"`
	Traces   []opTraceRow  `json:"traces,omitempty"`
	TraceErr string        `json:"tracesError,omitempty"`
	Drilled  bool          `json:"drilled,omitempty"`
	rows     []chstore.TraceRow
	drills   []clusterDrill
}

// clusterCompareEvidence — küme kıyası paketi (opPrefetch.ClusterCompare).
type clusterCompareEvidence struct {
	Label   string    `json:"label"`
	Service string    `json:"service,omitempty"`
	RangeS  int64     `json:"rangeS"`
	Clamped bool      `json:"clamped,omitempty"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	// REDNote — RED neden ölçülmedi (dolu → her ortam satırı "RED ölçülemedi").
	REDNote    string               `json:"redNote,omitempty"`
	Envs       []clusterEnvEvidence `json:"envs"`
	Hypothesis string               `json:"hypothesis,omitempty"`
}

// ─── Saf yardımcılar ────────────────────────────────────────────────────

// endpointEnvSet — SAF (v0.10.1374, B1/B2): endpoint/yol hedefinin ortam
// kümesi. Yol metinden çıkarılıp sarmalayıcı atıldıktan sonra extractEnvSet
// (canlı ortam kataloğu, sınırlı, mesaj sırası, <2 → nil): "/eu-a/orders"
// gibi yol parçası ortam sayılmaz.
func endpointEnvSet(raw, path string, envs []string) []string {
	if len(envs) < 2 {
		return nil
	}
	rest := raw
	if path != "" {
		rest = strings.Replace(raw, path, " ", 1)
	}
	return extractEnvSet(normalizeGuidedMsg(stripMarkdownWrapText(rest)), envs)
}

// wantsClusterCompare — SAF: ≥2 ortam anılmış endpoint/rota hedefi.
func wantsClusterCompare(t opTarget) bool {
	return len(t.Envs) >= 2 && (t.Endpoint || t.HasRoute)
}

// clusterWindow — SAF: sohbet penceresi ≤clusterWindowMax'a kırpılır (son 6 sa).
func clusterWindow(from, to time.Time) (time.Time, bool) {
	if lo := to.Add(-clusterWindowMax); from.Before(lo) {
		return lo, true
	}
	return from, false
}

// clusterLabel — SAF: "GET /shop/orders/detail" (fiil + rota | yol | span adı).
func clusterLabel(t opTarget) string {
	route := t.Route
	switch {
	case t.SearchPath != "":
		route = t.SearchPath
	case route == "":
		_, route = splitSpanVerb(t.Name)
		if route == "" {
			route = t.Name
		}
	}
	return strings.TrimSpace(t.Method + " " + route)
}

// clusterREDPlan — SAF: ortam başına RED hangi sorguyla ölçülür ("route" |
// "path") ya da neden ölçülmez (note). Ham sorgu yalnız TEK serviste koşar
// (spans birincil anahtar öneki service_name); yol-yalnız hedefte yalnız
// somut yol (elips/joker yok — url.path birebir eşleşmeli).
func clusterREDPlan(t opTarget, svc string) (mode, note string) {
	switch {
	case svc == "":
		return "", "servis tek adaya çözülmedi; servissiz ham RED sorgusu koşmaz — trace'lere bakıldı"
	case t.SearchPath != "":
		if pathSearchText(t.SearchPath) != t.SearchPath {
			return "", "yol elips/joker içeriyor, url.path birebir eşlenemez — trace'lere bakıldı"
		}
		return "path", ""
	case t.HasRoute || t.HasSpan:
		return "route", ""
	}
	return "", "hedefte rota ya da span adı yok — trace'lere bakıldı"
}

// clusterTraceFilter — SAF: ortamın en yavaş trace araması. Hedef süzgeci
// opPrefetchForTarget'ınkiyle aynı öncelik (yol-yalnız → Search, span adı →
// name, rota → http.route); süre azalan, ≤clusterTraceLimit, sayım yok.
func clusterTraceFilter(t opTarget, svc, env string, from, to time.Time) chstore.TraceFilter {
	search := ""
	if t.SearchPath != "" {
		search = pathSearchText(t.SearchPath)
	}
	f := traceSearchFilter(svc, env, search, false, from, to)
	switch {
	case search != "":
	case t.HasSpan:
		f.Filters = []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{t.Name}}}
	case t.HasRoute:
		f.Filters = opRouteFilter(t.Route)
	}
	f.Limit = clusterTraceLimit
	return f
}

// clusterSlowKeys — SAF: ortam yavaşlık anahtarı (ms). Adayların HEPSİNİN RED'i
// ölçülmüşse p95, değilse en yavaş trace süresi — iki ölçü karıştırılmaz.
func clusterSlowKeys(envs []clusterEnvEvidence, cand []int) map[int]float64 {
	allRED := len(cand) > 0
	for _, i := range cand {
		if r := envs[i].RED; envs[i].REDErr != "" || !r.Measured || r.Calls == 0 || r.P95Ms <= 0 {
			allRED = false
		}
	}
	keys := make(map[int]float64, len(cand))
	for _, i := range cand {
		if allRED {
			keys[i] = envs[i].RED.P95Ms
			continue
		}
		for _, r := range envs[i].rows {
			keys[i] = math.Max(keys[i], r.DurationMs)
		}
	}
	return keys
}

// clusterDrillEnvs — SAF: kırılacak ortamlar (trace'i olanlar). ≤clusterDrillEnvMax
// ise hepsi; fazlaysa en yavaş 3 + en hızlı 1 (kıyas çifti korunur). Ortam
// sırasıyla döner.
func clusterDrillEnvs(envs []clusterEnvEvidence) []int {
	var cand []int
	for i, e := range envs {
		if e.TraceErr == "" && len(e.rows) > 0 {
			cand = append(cand, i)
		}
	}
	if len(cand) <= clusterDrillEnvMax {
		return cand
	}
	keys := clusterSlowKeys(envs, cand)
	bySlow := append([]int(nil), cand...)
	sort.SliceStable(bySlow, func(a, b int) bool { return keys[bySlow[a]] > keys[bySlow[b]] })
	pick := append(append([]int(nil), bySlow[:clusterDrillEnvMax-1]...), bySlow[len(bySlow)-1])
	sort.Ints(pick)
	return pick
}

// clusterHypothesisTR — SAF: hipotez cümlesi YALNIZ hesaplanmış koşulda.
// En yavaş ve en hızlı ortamın (clusterSlowKeys) en yavaş trace şekli: yavaş
// ortamın en çok tekrar eden çağrısı hızlı ortamda da var, tekrar sayıları
// ±%20 içinde VE çağrı başına DB ortalaması (DB öz süre / DB span) oranı ≥2.
// Koşul tutmazsa "" — fark başka bir şeyle açıklanıyor olabilir, cümle kurulmaz.
func clusterHypothesisTR(ev clusterCompareEvidence) string {
	var cand []int
	for i, e := range ev.Envs {
		if len(e.drills) == 0 {
			continue
		}
		if sh := e.drills[0].D.Shape; e.drills[0].D.Err == nil && sh.Spans > 0 && sh.DB.Count > 0 && sh.DB.SumNs > 0 && len(sh.Repeats) > 0 {
			cand = append(cand, i)
		}
	}
	if len(cand) < 2 {
		return ""
	}
	keys := clusterSlowKeys(ev.Envs, cand)
	slow, fast := cand[0], cand[0]
	for _, i := range cand[1:] {
		if keys[i] > keys[slow] {
			slow = i
		}
		if keys[i] < keys[fast] {
			fast = i
		}
	}
	if slow == fast || keys[slow] <= keys[fast] {
		return ""
	}
	ss, fs := ev.Envs[slow].drills[0].D.Shape, ev.Envs[fast].drills[0].D.Shape
	rep := ss.Repeats[0]
	other := -1
	for _, r := range fs.Repeats {
		if r.Name == rep.Name {
			other = r.Count
			break
		}
	}
	if other <= 0 {
		return ""
	}
	hi := math.Max(float64(rep.Count), float64(other))
	if math.Abs(float64(rep.Count-other)) > clusterRepeatTol*hi {
		return ""
	}
	slowAvg := ss.DB.SumNs / int64(ss.DB.Count)
	fastAvg := fs.DB.SumNs / int64(fs.DB.Count)
	if fastAvg <= 0 {
		return ""
	}
	ratio := float64(slowAvg) / float64(fastAvg)
	if ratio < clusterDBRatioMin {
		return ""
	}
	se, fe := ev.Envs[slow].Env, ev.Envs[fast].Env
	return fmt.Sprintf("Hipotez (hesaplanmış): tekrar sayısı benzer — %s: %s ×%d, %s ×%d; çağrı başına DB ortalaması %s %s, %s %s (×%s) → fark, tekrar sayısı × bölgeler arası gecikme ile uyumlu (DB/kaynak hızlı küme %s tarafında olabilir).",
		oneLineSample(rep.Name, shapeNameRunes), se, rep.Count, fe, other,
		se, fmtShapeDur(slowAvg), fe, fmtShapeDur(fastAvg), fmtTRRatio(ratio), fe)
}

// fmtTRCount — SAF: Türkçe binlik ayraç ("1.234").
func fmtTRCount(n uint64) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

// fmtTRRatio — SAF: "4,4".
func fmtTRRatio(f float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1)
}

// fmtTRErrRate — SAF: "%0,3"; hata var ama %0,05 altı → "%<0,1".
func fmtTRErrRate(r chstore.OpRED) string {
	pct := r.ErrorRate() * 100
	if r.Errors > 0 && pct < 0.05 {
		return "%<0,1"
	}
	return "%" + strings.Replace(fmt.Sprintf("%.1f", pct), ".", ",", 1)
}

// clusterREDRow — SAF: ortamın RED satırı (en yavaş işareti hariç).
func clusterREDRow(ev clusterCompareEvidence, e clusterEnvEvidence) string {
	switch {
	case e.REDErr != "":
		return fmt.Sprintf("- %s: RED OKUNAMADI (%s)", e.Env, e.REDErr)
	case ev.REDNote != "":
		return fmt.Sprintf("- %s: RED ölçülemedi", e.Env)
	case !e.RED.Measured || e.RED.Calls == 0:
		return fmt.Sprintf("- %s: veri yok (bu pencerede)", e.Env)
	}
	r := e.RED
	return fmt.Sprintf("- %s: %s çağrı · hata %s · p50 %s · p95 %s", e.Env, fmtTRCount(r.Calls), fmtTRErrRate(r),
		fmtShapeDur(int64(r.P50Ms*1e6)), fmtShapeDur(int64(r.P95Ms*1e6)))
}

// renderClusterCompareTR — SAF: deterministik KÜME KIYASI bloğu.
func renderClusterCompareTR(ev clusterCompareEvidence) string {
	var b strings.Builder
	svc := ev.Service
	if svc == "" {
		svc = "servis belirsiz"
	}
	fmt.Fprintf(&b, "KÜME KIYASI — %s · %s · son %s (%s – %s UTC)", ev.Label, svc, fmtAgoTR(ev.RangeS),
		ev.From.UTC().Format("15:04"), ev.To.UTC().Format("15:04"))
	if ev.Clamped {
		b.WriteString(" — sohbet penceresi 6 sa ile sınırlandı")
	}
	b.WriteString("\n")
	if ev.REDNote != "" {
		fmt.Fprintf(&b, "RED ölçülemedi: %s.\n", ev.REDNote)
	}
	// En yavaş işareti: RED'i ölçülmüş (≥1 çağrı) ≥2 ortamda en yüksek p95,
	// en düşük p95'ten büyükse; oran en hızlı ortama göre.
	slow, fast := -1, -1
	for i, e := range ev.Envs {
		if e.REDErr != "" || ev.REDNote != "" || !e.RED.Measured || e.RED.Calls == 0 {
			continue
		}
		if slow < 0 || e.RED.P95Ms > ev.Envs[slow].RED.P95Ms {
			slow = i
		}
		if fast < 0 || e.RED.P95Ms < ev.Envs[fast].RED.P95Ms {
			fast = i
		}
	}
	for i, e := range ev.Envs {
		b.WriteString(clusterREDRow(ev, e))
		if i == slow && fast >= 0 && slow != fast && e.RED.P95Ms > ev.Envs[fast].RED.P95Ms {
			if f := ev.Envs[fast].RED.P95Ms; f > 0 {
				fmt.Fprintf(&b, " ← en yavaş (p95 ×%s / %s)", fmtTRRatio(e.RED.P95Ms/f), ev.Envs[fast].Env)
			} else {
				b.WriteString(" ← en yavaş")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("En yavaş trace'ler (küme · trace · süre · şekil):\n")
	for _, e := range ev.Envs {
		switch {
		case e.TraceErr != "":
			fmt.Fprintf(&b, "- %s · trace OKUNAMADI (%s)\n", e.Env, e.TraceErr)
			continue
		case len(e.rows) == 0 && len(e.Traces) == 0:
			fmt.Fprintf(&b, "- %s · bu pencerede trace yok\n", e.Env)
			continue
		case !e.Drilled:
			slowest := 0.0
			for _, t := range e.Traces {
				slowest = math.Max(slowest, t.DurationMs)
			}
			fmt.Fprintf(&b, "- %s · %d trace, en yavaş %s · kırılım atlandı (en çok %d küme kırılır)\n", e.Env, len(e.Traces),
				fmtShapeDur(int64(slowest*1e6)), clusterDrillEnvMax)
			continue
		}
		for _, cd := range e.drills {
			fmt.Fprintf(&b, "- %s · %s · %s", e.Env, cd.Row.TraceID, fmtShapeDur(int64(cd.Row.DurationMs*1e6)))
			if cd.Row.HasError {
				b.WriteString(" · hatalı")
			}
			d := cd.D
			switch {
			case d.Err != nil:
				fmt.Fprintf(&b, " · span'ler OKUNAMADI (%s)", sourcestate.Classify(d.Err))
			case d.NotFound:
				b.WriteString(" · trace deposunda BULUNAMADI")
			case d.Shape.Spans > 0:
				b.WriteString(" · " + strings.TrimPrefix(renderTraceShapeTR(d.Shape), "Şekil: "))
			}
			b.WriteString("\n")
		}
	}
	if h := clusterHypothesisTR(ev); h != "" {
		b.WriteString(h + "\n")
	}
	b.WriteString("KURAL: Küme satırları HESAPLANMIŞ ölçümdür — sayıları aynen aktar; OKUNAMADI / veri yok / ölçülemedi yazan küme için sayı UYDURMA; Hipotez satırı yoksa bölgeler arası gecikme iddiasını kesin dille kurma.\n")
	return b.String()
}

// clusterPromptTR — v0.10.1374: serbest döngü prompt'una ek YALNIZ küme kıyası
// yapıldıysa; yoksa "" (prompt bayt bayt eski).
func (p *opPrefetch) clusterPromptTR() string {
	if p == nil || p.ClusterCompare == nil {
		return ""
	}
	return copilot.OperationClusterCompareAddendum() + "\n\n"
}

// ─── Sunucu yarısı ──────────────────────────────────────────────────────

// clusterCompareRun — başlatılmış küme kıyası (emit'siz okuma goroutine'i).
type clusterCompareRun struct {
	step int
	args string
	ch   chan *clusterCompareEvidence
}

// startClusterCompare — adım çipi {route, envs, range_s} ön-getirme
// goroutine'inden yayınlanır; okumalar ayrı goroutine'de başlar (endpoint
// kırılımıyla PARALEL — ikisi sırayla koşsa ön-getirmenin 15 sn tavanını
// aşabilirdi). Hedef kıyas istemiyorsa nil.
func (s *Server) startClusterCompare(pctx context.Context, emit func(string, any), t opTarget, svc string, from, to time.Time) *clusterCompareRun {
	if s == nil || s.store == nil || !wantsClusterCompare(t) {
		return nil
	}
	envs := t.Envs
	if len(envs) > envSetMax {
		envs = envs[:envSetMax]
	}
	cfrom, _ := clusterWindow(from, to)
	args, _ := json.Marshal(map[string]any{"route": clusterLabel(t), "envs": envs, "range_s": int64(to.Sub(cfrom) / time.Second)})
	run := &clusterCompareRun{args: string(args), ch: make(chan *clusterCompareEvidence, 1)}
	run.step = emitGuidedStep(emit, opClusterTool, run.args)
	t.Envs = envs
	go func() { run.ch <- s.clusterCompareEvidence(pctx, t, svc, from, to) }()
	return run
}

// finish — kanıtı toplar, pakete yazar, çip sonucunu yayınlar. nil → no-op.
func (run *clusterCompareRun) finish(emit func(string, any), p *opPrefetch) {
	if run == nil || p == nil {
		return
	}
	ev := <-run.ch
	p.ClusterCompare = ev
	p.clusterText = renderClusterCompareTR(*ev)
	p.clusterArgsJSON = run.args
	emitGuidedStepResult(emit, run.step, opClusterTool, p.clusterText, nil)
}

// clusterCompareEvidence — okumalar (emit ETMEZ): RED (tek sorgu) ve ortam
// başına trace araması paralel, sonra ≤clusterDrillEnvMax ortamda ≤2 trace'in
// kırılımı paralel; hepsi clusterCompareBudget içinde.
func (s *Server) clusterCompareEvidence(ctx context.Context, t opTarget, svc string, from, to time.Time) *clusterCompareEvidence {
	cfrom, clamped := clusterWindow(from, to)
	ev := &clusterCompareEvidence{Label: clusterLabel(t), Service: svc, RangeS: int64(to.Sub(cfrom) / time.Second), Clamped: clamped, From: cfrom, To: to}
	ev.Envs = make([]clusterEnvEvidence, len(t.Envs))
	for i, e := range t.Envs {
		ev.Envs[i].Env = e
	}
	mode, note := clusterREDPlan(t, svc)
	ev.REDNote = note
	cctx, cancel := context.WithTimeout(ctx, clusterCompareBudget)
	defer cancel()

	var wg sync.WaitGroup
	var red map[string]chstore.OpRED
	var redErr error
	if mode != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if mode == "path" {
				red, redErr = s.store.PathREDByEnv(cctx, svc, t.SearchPath, t.Envs, cfrom, to)
				return
			}
			name := ""
			if t.HasSpan {
				name = t.Name
			}
			route := ""
			if t.HasRoute {
				route = t.Route
			}
			red, redErr = s.store.RouteREDByEnv(cctx, svc, route, name, t.Envs, cfrom, to)
		}()
	}
	for i := range ev.Envs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows, _, _, err := s.store.GetTraces(cctx, clusterTraceFilter(t, svc, ev.Envs[i].Env, cfrom, to))
			if err != nil {
				ev.Envs[i].TraceErr = string(sourcestate.Classify(err))
				return
			}
			ev.Envs[i].rows = rows
		}(i)
	}
	wg.Wait()
	for i := range ev.Envs {
		e := &ev.Envs[i]
		switch {
		case redErr != nil:
			e.REDErr = string(sourcestate.Classify(redErr))
		case red != nil:
			e.RED = red[e.Env] // yoksa sıfır: "veri yok (bu pencerede)"
		}
		for _, r := range e.rows {
			e.Traces = append(e.Traces, opTraceRow{TraceID: r.TraceID, Service: r.ServiceName, DurationMs: r.DurationMs, Error: r.HasError})
		}
	}

	type job struct {
		env int
		c   drillCandidate
	}
	var jobs []job
	for _, i := range clusterDrillEnvs(ev.Envs) {
		ev.Envs[i].Drilled = true
		for _, c := range pickDrillTraces(ev.Envs[i].rows) {
			jobs = append(jobs, job{i, c})
		}
	}
	res := make([]traceDrill, len(jobs))
	for k, j := range jobs {
		wg.Add(1)
		go func(k int, c drillCandidate) {
			defer wg.Done()
			res[k] = s.traceDrillCore(cctx, c)
		}(k, j.c)
	}
	wg.Wait()
	for k, j := range jobs {
		ev.Envs[j.env].drills = append(ev.Envs[j.env].drills, clusterDrill{Row: j.c.Row, D: res[k]})
	}
	ev.Hypothesis = clusterHypothesisTR(*ev)
	return ev
}
