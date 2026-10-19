package mcptools

// service_sweep_latency.go — v0.10.1299 (operatör 2026-10-17: "Bu sorgu metric
// grafiği 5 dakika aralıklı olduğu için sağlıklı değil. Aynı veriye Traces
// sayfasından bakınca süre grafiği düzgün gözüküyor. Buna da bir çözüm bul.").
//
// Üç parça, hepsi service_health_sweep'in operasyon bulgusu için:
//
//  1. Ham span doğrulaması — LatencyLevel kapılarını geçen gecikme ADAYI
//     (≤ sweepCorrobMax, paralel, ≤ 3 s) chstore.SlowSpanCorroborate ile
//     doğrulanır. Doğrulanmayan gecikme kolu düşer (satır gerekçesinde "MV p99
//     +%45 ama ham span doğrulamadı"); okuma hatası → "olası" (seviye kalır,
//     söylenir). Hata kolu etkilenmez.
//  2. Grafik — spanmetrics_1m (≤ 6 sa ve kapsıyorsa) p50 + p99 + önceki
//     pencerenin birleşik p99'u (kesikli) + doğrulamanın "yavaş istek" sayısı +
//     hata; adım dakikanın katı, kaba adım state BİRLEŞTİRİR (SQL). Kapsam
//     yoksa operation_summary_5m sparkline'ı (5 dk). Başlık kova boyutunu söyler.
//  3. Log örneği — JSON gövde modele / anlatıma ham basılmaz: message | msg |
//     error | error_code (+ exception/stack ilk satırı), ≤ 200 rune
//     (logBodyPreview). Ham gövde yalnız açılır kanıt tablosunda.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
)

const (
	sweepCorrobMax   = 8
	sweepCapCorrob   = 3500 * time.Millisecond // SQL max_execution_time 3 s + ağ payı
	sweepCapChart    = 3 * time.Second
	sweepChartOpsMax = 3
	sweepFineMaxWin  = 6 * time.Hour
	sweepBodyRunes   = 200
)

// Doğrulama durumu (adayın gecikme kolu).
const (
	corrobConfirmed  = "confirmed"
	corrobRejected   = "rejected"
	corrobUnverified = "unverified" // okuma hatası / bütçe / tavan → "olası"
)

// sweepCorrobResult — bir operasyon adayının doğrulama okuması.
type sweepCorrobResult struct {
	res chstore.SlowSpanCorroboration
	err error
}

// sweepLatencyCands — SAF: doğrulanacak operasyon adayları (gecikme kolu ≠ ok),
// kötü önce, tavanlı. Kalanlar doğrulanmadan "olası" kalır.
func sweepLatencyCands(cands []sweepCand) []int {
	var idx []int
	for i, c := range cands {
		if c.Kind == sweepKindOperation && c.row != nil && c.row.LatLevel != levelOK {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := cands[idx[a]], cands[idx[b]]
		if x, y := healthverdict.LevelRank(ca.row.LatLevel), healthverdict.LevelRank(cb.row.LatLevel); x != y {
			return x < y
		}
		return ca.Impact > cb.Impact
	})
	return idx
}

// sweepCorroborate — gecikme adaylarını ham span'le doğrular, ops bölümünü
// günceller (aday seviyesi, satır seviyesi/gerekçesi) ve KENDİ bölümünü döndürür
// ("corroborate"; aday yoksa nil). Bölüm evre 2'den SONRA birleşir: kanıt
// çipi (ham sorgu) bileşik kanıt tavanında logların yerini almasın.
func sweepCorroborate(ctx context.Context, r sweepReaders, req SweepRequest, o *sweepOutcome, deadline time.Time) *sweepOutcome {
	if o == nil {
		return nil
	}
	idx := sweepLatencyCands(o.cands)
	if len(idx) == 0 {
		return nil
	}
	elapsed := stopwatch()
	results := map[string]sweepCorrobResult{}
	priorFrom := req.From.Add(-req.To.Sub(req.From))
	if r.slowSpans != nil {
		run := idx
		if len(run) > sweepCorrobMax {
			run = run[:sweepCorrobMax]
		}
		dl := hopDeadline(time.Until(deadline)-sweepPhase2Margin, sweepCapCorrob)
		if dl > 0 {
			type done struct {
				name string
				r    sweepCorrobResult
			}
			ch := make(chan done, len(run))
			cctx, cancel := context.WithTimeout(ctx, dl)
			for _, i := range run {
				c := o.cands[i]
				go func(name string, thr float64) {
					res, err := r.slowSpans(cctx, req.Service, name, thr, req.From, req.To, priorFrom)
					ch <- done{name, sweepCorrobResult{res, err}}
				}(c.Name, c.latThrMs)
			}
			for range run {
				d := <-ch
				results[d.name] = d.r
			}
			cancel()
		}
	}
	first := o.cands[idx[0]] // sweepApplyCorrob listeyi süzer — önce yakala
	confirmed, rejected, unverified := sweepApplyCorrob(o, idx, results, req.To.Sub(req.From))
	o.corrobCands, o.corrobConfirmed = len(idx), confirmed
	out := &sweepOutcome{section: SweepSection{Name: sweepHopCorrob, DurationMs: elapsed(),
		Summary: fmt.Sprintf("gecikme adayı %d operasyon: %d ham span ile doğrulandı, %d doğrulanmadı (puanlanmadı), %d okunamadı (olası)",
			len(idx), confirmed, rejected, unverified),
		Note: "MV p99 farkı yalnız olası sinyaldir; bulgu eşiği aşan ham istek sayısıyla (cari ≥ 10 ve ≥ 2× önceki pencere) doğrulanır"}}
	switch {
	case r.slowSpans == nil:
		out.section.State = hopSkipped
	case unverified == 0:
		out.section.State = hopOK
	case confirmed+rejected == 0:
		out.section.State, out.failed = hopError, true
	default:
		out.section.State, out.failed = hopPartial, true
	}
	if len(results) == 0 {
		return out
	}
	// Kanıt: ilk adayın ham sorgusu (çip sorguyu aynen gösterir).
	sql, args := chstore.SlowSpanCorroborateSQL(req.Service, first.Name, int64(first.latThrMs*1e6), priorFrom, req.To,
		chstore.SlowCorrobSampleN(priorFrom, req.To))
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSQL, Query: inlineSQL(sql, args...), Window: evWin(priorFrom, req.To)}
	if res, ok := results[first.Name]; ok && res.err == nil {
		ev.Rows = intPtr(len(res.res.Minutes))
	}
	ev.DurationMs = out.section.DurationMs
	out.evidences = append(out.evidences, cosreHopEvidence{Hop: sweepHopCorrob, As: queryTracesName,
		Request: map[string]any{"service": req.Service, "operation": first.Name, "sort_by": qtSortSlowest}, cosreEvidence: ev})
	return out
}

// sweepApplyCorrob — SAF: doğrulama sonuçlarını adaylara + satırlara uygular.
// Doğrulanmayan ve başka kolu olmayan aday listeden düşer.
func sweepApplyCorrob(o *sweepOutcome, idx []int, results map[string]sweepCorrobResult, curLen time.Duration) (confirmed, rejected, unverified int) {
	if o.corrob == nil {
		o.corrob = map[string]chstore.SlowSpanCorroboration{}
	}
	for _, i := range idx {
		c := &o.cands[i]
		v := c.row
		res, ran := results[c.Name]
		state := corrobUnverified
		switch {
		case !ran:
			v.LatReason = "olası — " + v.LatReason + " (ham span doğrulaması yapılamadı: süre/aday tavanı)"
		case res.err != nil:
			v.LatReason = "olası — " + v.LatReason + " (ham span doğrulaması okunamadı)"
		case healthverdict.Corroborated(res.res.CurSlow, res.res.PriorSlow, curLen.Seconds(), curLen.Seconds()):
			state = corrobConfirmed
			o.corrob[c.Name] = res.res
			v.LatReason += fmt.Sprintf("; ham span: %d istek > %.0f ms (önceki pencere %d), en yavaş %.0f ms",
				res.res.CurSlow, res.res.ThresholdMs, res.res.PriorSlow, res.res.MaxMs)
			if len(res.res.ExemplarID) >= 8 {
				v.LatReason += " (trace " + strings.ToLower(res.res.ExemplarID[:8]) + ")"
			}
		default:
			state = corrobRejected
			v.LatLevel = levelOK
			v.LatReason = fmt.Sprintf("MV p99 %+.0f%% ama ham span doğrulamadı (yavaş istek %d vs %d, eşik %.0f ms)",
				v.P99Rel*100, res.res.CurSlow, res.res.PriorSlow, res.res.ThresholdMs)
		}
		switch state {
		case corrobConfirmed:
			confirmed++
		case corrobRejected:
			rejected++
		default:
			unverified++
		}
		c.applyRow()
		for j := range o.opRows {
			if o.opRows[j].Name == c.Name {
				o.opRows[j].Level = c.Level
				o.opRows[j].Reason = truncateRunes(strings.Join(c.Reasons, "; "), sweepReasonRunes)
				o.opRows[j].Corroboration = state
			}
		}
	}
	kept := o.cands[:0]
	for _, c := range o.cands {
		if c.Level != levelOK {
			kept = append(kept, c)
		}
	}
	o.cands = kept
	return confirmed, rejected, unverified
}

// ── grafik ─────────────────────────────────────────────────────────────

// sweepChartStep — SAF: grafik adımı. ≤ 6 sa → 1 dk tabanlı (spanmetrics_1m),
// ≤ evBlockMaxPoints nokta için dakikanın en küçük katı; aksi 5 dk tabanı.
func sweepChartStep(win time.Duration) (stepSec int, fine bool) {
	sec := int(win / time.Second)
	if sec <= 0 {
		return 60, true
	}
	grain := 300
	if win <= sweepFineMaxWin {
		grain, fine = 60, true
	}
	n := (sec + grain*evBlockMaxPoints - 1) / (grain * evBlockMaxPoints)
	if n < 1 {
		n = 1
	}
	return grain * n, fine
}

// sweepBucketLabel — SAF: "1 dk" / "3 dk" / "90 sn".
func sweepBucketLabel(stepSec int) string {
	if stepSec%60 == 0 {
		return fmt.Sprintf("%d dk", stepSec/60)
	}
	return fmt.Sprintf("%d sn", stepSec)
}

// sweepOpChartSet — v0.10.1391: grafik okumasının sonucu. pts: seçilen
// operasyonların ince serisi; chosen: çizilecek operasyonlar (giriş önce, giden
// çağrı yalnız başka aday yoksa); kinds: okunan span türleri (nil = bilinmiyor).
type sweepOpChartSet struct {
	pts    map[string][]chstore.OpLatencyPoint
	chosen map[string]bool
	kinds  map[string][]string
}

func (s sweepOpChartSet) isChosen(name string) bool { return s.chosen[name] }

// v0.10.1391 — span türü sınıfları (grafik tercihi).
const (
	opClassEntry    = 0 // server | consumer — servisin GELEN trafiği
	opClassOther    = 1 // internal | bilinmiyor
	opClassOutbound = 2 // client | producer — servisin GİDEN çağrısı
)

// sweepCapKinds — tür okuması bütçesi (önek budamalı tek MV okuması).
const sweepCapKinds = 1500 * time.Millisecond

// sweepOpKindClass — SAF (v0.10.1391): bir operasyon adının türleri → sınıf.
// Ad hem giriş hem giden türde görülüyorsa giriş kazanır.
func sweepOpKindClass(kinds []string) int {
	cls := opClassOther
	for _, k := range kinds {
		switch strings.ToLower(k) {
		case "server", "consumer":
			return opClassEntry
		case "client", "producer":
			cls = opClassOutbound
		}
	}
	return cls
}

// sweepOpKindLabel — SAF (v0.10.1391): türlerin anlatım etiketi (giriş önce,
// sonra giden, sonra iç; büyük harf). Bilinmiyorsa "".
func sweepOpKindLabel(kinds []string) string {
	for _, pref := range [][]string{{"server", "consumer"}, {"client", "producer"}, {"internal"}} {
		for _, p := range pref {
			for _, k := range kinds {
				if strings.EqualFold(k, p) {
					return strings.ToUpper(p)
				}
			}
		}
	}
	return ""
}

// sweepOpKindTag — SAF (v0.10.1391): grafik başlığının tür eki — YALNIZ giden
// çağrıda ("giden çağrı · CLIENT"); giriş/iç/bilinmeyende "".
func sweepOpKindTag(kinds []string) string {
	if sweepOpKindClass(kinds) != opClassOutbound {
		return ""
	}
	return "giden çağrı · " + sweepOpKindLabel(kinds)
}

// sweepPickChartOps — SAF (v0.10.1391; operatör: "en azından HTTP metriclerimi
// getirebilirdi. Onun yerine gRPC getirdi"): grafik çizilecek operasyon
// bulguları, bulgu sırasıyla. Giriş (SERVER/CONSUMER) sınıfı önce; giden çağrı
// (CLIENT/PRODUCER) YALNIZ giriş/iç/bilinmeyen aday yoksa çizilir (bulgu yine
// özet tablosunda ve anlatımda). Tür okunamadıysa (kinds nil) herkes "diğer"
// sınıfında — eski davranış. Tavan sweepChartOpsMax.
func sweepPickChartOps(names []string, kinds map[string][]string) []string {
	idx := make([]int, len(names))
	cls := make([]int, len(names))
	anyInbound := false
	for i, n := range names {
		idx[i] = i
		cls[i] = sweepOpKindClass(kinds[n])
		if cls[i] != opClassOutbound {
			anyInbound = true
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return cls[idx[a]] < cls[idx[b]] })
	var out []string
	for _, i := range idx {
		if len(out) >= sweepChartOpsMax {
			break
		}
		if anyInbound && cls[i] == opClassOutbound {
			continue
		}
		out = append(out, names[i])
	}
	return out
}

// sweepApplyOpKinds — v0.10.1391: operasyon bulgularına okunan span türü.
func sweepApplyOpKinds(findings []SweepFinding, kinds map[string][]string) {
	for i := range findings {
		if findings[i].Kind == sweepKindOperation {
			findings[i].SpanKind = sweepOpKindLabel(kinds[findings[i].Name])
		}
	}
}

// sweepStartOpCharts — operasyon bulgularının ince gecikme serisini evre 2 ile
// PARALEL okur; dönen fonksiyon bekler (bütçe sonunda yarım sonuçla döner).
// v0.10.1391: önce adayların span türü (tek MV okuması), sonra seçilen
// (sweepPickChartOps) ≤ sweepChartOpsMax operasyonun serisi — hepsi aynı
// goroutine zincirinde, bekleme fonksiyonu yalnız sonucu toplar.
func sweepStartOpCharts(ctx context.Context, r sweepReaders, req SweepRequest, findings []SweepFinding, deadline time.Time) func() sweepOpChartSet {
	step, fine := sweepChartStep(req.To.Sub(req.From))
	var cands []string
	for _, f := range findings {
		if f.Kind == sweepKindOperation {
			cands = append(cands, f.Name)
		}
	}
	dl := hopDeadline(time.Until(deadline)-sweepPhase2Margin, sweepCapChart)
	if len(cands) == 0 || dl <= 0 {
		set := sweepOpChartSet{pts: map[string][]chstore.OpLatencyPoint{}, chosen: map[string]bool{}}
		for _, n := range sweepPickChartOps(cands, nil) {
			set.chosen[n] = true
		}
		return func() sweepOpChartSet { return set }
	}
	done := make(chan sweepOpChartSet, 1)
	go func() {
		set := sweepOpChartSet{pts: map[string][]chstore.OpLatencyPoint{}, chosen: map[string]bool{}}
		cctx, cancel := context.WithTimeout(ctx, dl)
		defer cancel()
		if r.opKinds != nil {
			kctx, kcancel := context.WithTimeout(cctx, sweepCapKinds)
			if k, err := r.opKinds(kctx, req.Service, cands, req.From, req.To); err == nil {
				set.kinds = k
			}
			kcancel()
		}
		names := sweepPickChartOps(cands, set.kinds)
		for _, n := range names {
			set.chosen[n] = true
		}
		if r.opLatency == nil || !fine || len(names) == 0 {
			done <- set
			return
		}
		type res struct {
			name string
			pts  []chstore.OpLatencyPoint
			err  error
		}
		ch := make(chan res, len(names))
		for _, n := range names {
			go func(n string) {
				pts, err := r.opLatency(cctx, req.Service, n, req.From, req.To, step)
				ch <- res{n, pts, err}
			}(n)
		}
		for range names {
			select {
			case d := <-ch:
				if d.err == nil && len(d.pts) > 0 {
					set.pts[d.name] = d.pts
				}
			case <-cctx.Done():
				done <- set
				return
			}
		}
		done <- set
	}()
	return func() sweepOpChartSet { return <-done }
}

// DropOperationCharts — v0.10.1391: operasyon grafik bloklarını (ops bölümünün
// chart blokları) çıkarır, sayısını döndürür. Guided servis sağlığı, servisin
// HTTP RED'ini VictoriaMetrics'ten (örneklenmemiş) çizdiğinde span türevli
// operasyon grafiği YERİNE geçer — cevaptaki grafik sayısı artmaz. Yeni dilim
// kurar (paylaşılan dizi yerinde değişmez).
func (r *SweepResult) DropOperationCharts() int {
	kept := make([]pivotBlock, 0, len(r.Data.Blocks))
	for _, b := range r.Data.Blocks {
		if b.Hop == sweepHopOps && b.Type == blocks.TypeChart {
			continue
		}
		kept = append(kept, b)
	}
	n := len(r.Data.Blocks) - len(kept)
	r.Data.Blocks = kept
	return n
}

// sweepOpChartTitle — SAF (v0.10.1391): grafik başlığı. Arayüz birim başına bir
// panel çizer ve başlığa " · <birim>" ekler (ms paneli + count paneli); bu yüzden
// başlık GECİKMEYE özgü bir şey ("p50/p99") SÖYLEMEZ — count panelinde yanıltıcıydı
// (operatör ekranı: "… · p50/p99 · 2 dk kova (spanmetrics_1m) · count", 0–7
// örneklenmiş span sayısı). Seri adları (p50, p99, errors, yavaş istek) lejantta;
// sayımların örneklenmiş span olduğu başlıkta açık.
func sweepOpChartTitle(name, kindTag, bucket, source string) string {
	if kindTag != "" {
		name += " (" + kindTag + ")"
	}
	return fmt.Sprintf("%s · %s kova · örneklenmiş span (%s)", name, bucket, source)
}

// sweepOpLatencyChart — SAF: ince seri → grafik. p50 (birincil), p99
// (ikincil), önceki pencerenin birleşik p99'u (kesikli, her noktada aynı
// değer — hizalı eksen), doğrulamanın yavaş-istek sayısı (adım kovasına
// TOPLANIR) ve hata sayısı.
func sweepOpLatencyChart(name, kindTag string, pts []chstore.OpLatencyPoint, stepSec int, baseP99 float64, corr *chstore.SlowSpanCorroboration, win EvBlockWindow) (EvChartPayload, bool) {
	c := EvChartPayload{Title: sweepOpChartTitle(name, kindTag, sweepBucketLabel(stepSec), "spanmetrics_1m"), Window: win, Series: []EvChartSeries{}}
	if len(pts) == 0 {
		return c, false
	}
	p50 := EvChartSeries{Name: "p50", Unit: "ms", Points: [][2]float64{}}
	p99 := EvChartSeries{Name: "p99", Unit: "ms", Style: evStyleSecondary, Points: [][2]float64{}}
	bl := EvChartSeries{Name: "önceki pencere p99", Unit: "ms", Style: evStyleDashed, Points: [][2]float64{}}
	er := EvChartSeries{Name: "errors", Unit: "count", Points: [][2]float64{}}
	for _, p := range pts {
		t := float64(p.TS * 1000)
		p50.Points = append(p50.Points, [2]float64{t, round4(p.P50Ms)})
		p99.Points = append(p99.Points, [2]float64{t, round4(p.P99Ms)})
		er.Points = append(er.Points, [2]float64{t, float64(p.Errors)})
		if baseP99 > 0 {
			bl.Points = append(bl.Points, [2]float64{t, round4(baseP99)})
		}
	}
	c.Series = append(c.Series, sweepTrimSeries(p50), sweepTrimSeries(p99))
	if len(bl.Points) > 0 {
		c.Series = append(c.Series, sweepTrimSeries(bl))
	}
	if s, ok := sweepSlowSeries(corr, pts, stepSec); ok {
		c.Series = append(c.Series, s)
	}
	c.Series = append(c.Series, sweepTrimSeries(er))
	return c, true
}

// sweepSlowSeries — SAF: doğrulamanın dakika sayımları → grafik adımına
// toplanmış "yavaş (> T ms)" serisi (ince serinin zaman damgalarında, boş = 0).
func sweepSlowSeries(corr *chstore.SlowSpanCorroboration, pts []chstore.OpLatencyPoint, stepSec int) (EvChartSeries, bool) {
	if corr == nil || len(corr.Minutes) == 0 || stepSec <= 0 {
		return EvChartSeries{}, false
	}
	byStep := map[int64]uint64{}
	for _, m := range corr.Minutes {
		byStep[m.TS-m.TS%int64(stepSec)] += m.Count
	}
	s := EvChartSeries{Name: fmt.Sprintf("yavaş istek (> %.0f ms)", corr.ThresholdMs), Unit: "count", Points: [][2]float64{}}
	for _, p := range pts {
		s.Points = append(s.Points, [2]float64{float64(p.TS * 1000), float64(byStep[p.TS])})
	}
	return sweepTrimSeries(s), true
}

// sweepSparkChart — 5 dk geri dönüşü (ince MV pencereyi kapsamıyor ya da
// pencere > 6 sa): operation_summary_5m sparkline'ı — p50 (varsa) + p99 +
// önceki pencere p99'u + hata. Başlık kova boyutunu söyler.
func sweepSparkChart(name, kindTag string, op chstore.OperationSummary, from, to time.Time) (EvChartPayload, bool) {
	c := EvChartPayload{Window: EvBlockWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, Series: []EvChartSeries{}}
	n := len(op.P99Sparkline)
	slot := 0.0
	if n > 0 {
		slot = float64(to.Sub(from).Milliseconds()) / float64(n)
	}
	label := "5 dk"
	if n > 0 {
		if sec := int(to.Sub(from).Seconds()) / n; sec > 300 {
			label = sweepBucketLabel((sec + 59) / 60 * 60)
		}
	}
	c.Title = sweepOpChartTitle(name, kindTag, label, "operation_summary_5m") // v0.10.1391
	at := func(i int) float64 { return float64(from.UnixMilli()) + float64(i)*slot }
	if len(op.P50Sparkline) == n && n > 0 {
		s := EvChartSeries{Name: "p50", Unit: "ms", Points: [][2]float64{}}
		for i, v := range op.P50Sparkline {
			s.Points = append(s.Points, [2]float64{at(i), round4(v)})
		}
		c.Series = append(c.Series, sweepTrimSeries(s))
	}
	if n > 0 {
		s := EvChartSeries{Name: "p99", Unit: "ms", Style: evStyleSecondary, Points: [][2]float64{}}
		bl := EvChartSeries{Name: "önceki pencere p99", Unit: "ms", Style: evStyleDashed, Points: [][2]float64{}}
		for i, v := range op.P99Sparkline {
			s.Points = append(s.Points, [2]float64{at(i), round4(v)})
			if op.PriorP99Ms > 0 {
				bl.Points = append(bl.Points, [2]float64{at(i), round4(op.PriorP99Ms)})
			}
		}
		c.Series = append(c.Series, sweepTrimSeries(s))
		if len(bl.Points) > 0 {
			c.Series = append(c.Series, sweepTrimSeries(bl))
		}
	}
	if m := len(op.ErrorsSparkline); m > 0 {
		s := EvChartSeries{Name: "errors", Unit: "count", Points: [][2]float64{}}
		st := float64(to.Sub(from).Milliseconds()) / float64(m)
		for i, v := range op.ErrorsSparkline {
			s.Points = append(s.Points, [2]float64{float64(from.UnixMilli()) + float64(i)*st, float64(v)})
		}
		c.Series = append(c.Series, sweepTrimSeries(s))
	}
	return c, len(c.Series) > 0
}

// ── log örneği ─────────────────────────────────────────────────────────

var (
	logPreviewMsgKeys  = []string{"message", "msg", "error_message", "errorMessage", "error", "err", "reason", "description", "detail", "text", "body"}
	logPreviewCodeKeys = []string{"error_code", "errorCode", "err_code", "code"}
	logPreviewExcKeys  = []string{"exception", "exception.message", "exception.stacktrace", "stacktrace", "stack_trace", "stack"}
	logPreviewSvcKeys  = []string{"service_name", "serviceName", "service"}
)

// LogBodyPreview — SAF: log gövdesinin kısa, insan okur satırı. JSON nesnesi →
// "[error_code] message — exception ilk satırı" (mesaj yoksa error_code +
// servis); JSON değilse boşluklar katlanmış ham metin. ≤ max rune.
func LogBodyPreview(body string, max int) string {
	t := strings.TrimSpace(body)
	if strings.HasPrefix(t, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(t), &m) == nil {
			if s := logPreviewFromMap(m); s != "" {
				return capRunes(strings.Join(strings.Fields(s), " "), max)
			}
		}
	}
	return capRunes(strings.Join(strings.Fields(t), " "), max)
}

// capRunes — SAF: ≤ max rune (kesilirse "…" DAHİL max).
func capRunes(s string, max int) string {
	if max <= 1 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return truncateRunes(s, max-1)
}

func logPreviewFromMap(m map[string]any) string {
	lookup := func(keys []string) string {
		for _, k := range keys {
			for mk, v := range m {
				if !strings.EqualFold(mk, k) {
					continue
				}
				switch x := v.(type) {
				case string:
					if strings.TrimSpace(x) != "" {
						return x
					}
				case float64:
					return fmt.Sprint(x)
				case map[string]any: // {"error": {"message": "…"}}
					if s := logPreviewFromMap(x); s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	firstLine := func(s string) string {
		if i := strings.IndexAny(s, "\r\n"); i >= 0 {
			return strings.TrimSpace(s[:i])
		}
		return strings.TrimSpace(s)
	}
	msg := firstLine(lookup(logPreviewMsgKeys))
	code := lookup(logPreviewCodeKeys)
	exc := firstLine(lookup(logPreviewExcKeys))
	var parts []string
	if code != "" {
		parts = append(parts, "["+code+"]")
	}
	switch {
	case msg != "":
		parts = append(parts, msg)
	case code != "":
		if svc := lookup(logPreviewSvcKeys); svc != "" {
			parts = append(parts, svc)
		}
	}
	if exc != "" && exc != msg {
		if len(parts) > 0 {
			parts = append(parts, "—")
		}
		parts = append(parts, exc)
	}
	return strings.Join(parts, " ")
}

// sweepPreviewRows — SAF: satırların gövdesi önizlemeye çevrilmiş KOPYASI (ham
// satırlar açılır kanıt tablosunda kalır).
func sweepPreviewRows(rows []qlRow) []qlRow {
	out := make([]qlRow, len(rows))
	for i, r := range rows {
		r.Body = LogBodyPreview(r.Body, sweepBodyRunes)
		out[i] = r
	}
	return out
}
