package chstore

// trace_chip_rank.go — v0.10.1158 — servis + birden çok özel çip: EN KÜÇÜK
// küme önce.
//
// Operator-reported (prod, 2026-10-13): servis seçili + `error.type =
// java.net.SocketTimeoutException` + `server.address = <bağımlılık>`, 12 saat
// → liste yalnız son ~30 dk'nın trace'lerini gösteriyor; 4 saat önceki 6
// trace şeritte var, listede yok.
//
// Kök neden: v0.10.1155 her özel çipi AYRI bir `trace_id GLOBAL IN (…)`
// yarı-birleşimine koyuyordu ve her biri en yeni traceChipSetMax span'le
// tavanlıydı. Geniş çipin (server.address = her çağrı) kümesi tavana çarpıp
// son ~30 dk'ya kesiliyor, AND kesişimi eski trace'leri SESSİZCE düşürüyordu.
//
// Düzeltme (trace_error_tracelevel.go'nun "küçük taraf küme olur" deseni):
// çipler TEK iç içe zincir olur, en küçük küme en içte:
//
//	trace_id GLOBAL IN (
//	  SELECT trace_id FROM spans WHERE <pencere> AND <geniş çip>
//	    AND trace_id GLOBAL IN (
//	      SELECT trace_id FROM spans WHERE <pencere> AND <küçük çip>
//	      ORDER BY time DESC LIMIT M)
//	  ORDER BY time DESC LIMIT M)
//
// Dış seviye geniş çipi yalnız içteki birkaç trace'te arar (idx_trace
// bloom) — tavana çarpmaz. Hangisinin küçük olduğunu VERİ söyler: çip başına
// tavanlı sayım (traceErrCountSQL, M+1'de durur), paralel. Operatör / anahtar
// sezgisi yanılır ("=" hem 15 hem milyonlarca satır olabilir). En küçük küme
// bile tavandaysa cevap RankedWithin ile ilan edilir — sessiz kesme yok.

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

// orderSemiChips — SAF: çipleri tavanlı sayıma göre artan sıralar (en küçük
// küme önce = zincirin en içi). Eşitlikte giriş sırası korunur; sayım yoksa
// (prob koşmadı / hata) sıra aynen.
func orderSemiChips(semi []FilterExpr, counts []uint64) []FilterExpr {
	out := append([]FilterExpr(nil), semi...)
	if len(counts) != len(semi) {
		return out
	}
	idx := make([]int, len(semi))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return counts[idx[a]] < counts[idx[b]] })
	for i, j := range idx {
		out[i] = semi[j]
	}
	return out
}

// semiChipWindow — SAF: yarı-birleşim seviyelerinin penceresi (kök / eşleşen
// span listenin penceresinin kenarından taşabilir; satır onarımıyla aynı pay).
func semiChipWindow(f TraceFilter) (time.Time, time.Time) {
	return f.From.Add(-traceRowRepairLead), f.To.Add(traceRowRepairLead)
}

// semiJoinChain — SAF: tek iç içe yarı-birleşim koşulu. semi[0] en içte
// (en küçük küme), her dış seviye bir sonraki çipi içteki kümeye üyelikle
// arar. Her seviye zaman sınırlı + LIMIT'li (ev kuralı: spans sorgusu
// tavansız olmaz). Derlenemeyen çip atlanır (ApplyFilters loglar).
func semiJoinChain(f TraceFilter, semi []FilterExpr, pm map[string]string) (string, []any) {
	from, to := semiChipWindow(f)
	inner, innerArgs := "", []any(nil)
	for _, fe := range semi {
		var lvl whereClause
		lvl.add("time >= ?", from)
		lvl.add("time <= ?", to)
		ApplyFiltersWith(&lvl, []FilterExpr{fe}, pm)
		if len(lvl.conds) < 3 {
			continue
		}
		if inner != "" {
			lvl.add("trace_id GLOBAL IN ("+inner+")", innerArgs...)
		}
		inner = "SELECT trace_id FROM spans " + lvl.sql() + " ORDER BY time DESC LIMIT ?"
		innerArgs = append(lvl.args, traceChipSetMax)
	}
	if inner == "" {
		return "", nil
	}
	return "trace_id GLOBAL IN (" + inner + ")", innerArgs
}

// rankServiceChips — servis + özel çip yarı-birleşiminde çipleri tavanlı
// sayımla sıralar (en küçük önce) ve en küçük küme bile tavandaysa bunu
// RankedWithin ile ilan eder. Store girişlerinde (GetTraces,
// TraceErrorHistogram) bir kez koşar; iç buildGetTracesWhere çağrıları f
// kopyasıyla sırayı taşır. Prob hatası listeyi düşürmez: sıra aynen kalır.
func (s *Store) rankServiceChips(ctx context.Context, f TraceFilter) TraceFilter {
	semi, rest, ok := splitServiceChips(f)
	if !ok {
		return f
	}
	pm := promotedCols()
	if f.NoPromoted {
		pm = nil
	}
	from, to := semiChipWindow(f)
	counts := make([]uint64, len(semi))
	errs := make([]error, len(semi))
	t0 := time.Now()
	var wg sync.WaitGroup
	for i, fe := range semi {
		var wc whereClause
		wc.add("time >= ?", from)
		wc.add("time <= ?", to)
		ApplyFiltersWith(&wc, []FilterExpr{fe}, pm)
		wg.Add(1)
		go func(i int, wc whereClause) {
			defer wg.Done()
			counts[i], errs[i] = s.traceErrCount(ctx, wc, traceChipSetMax+1)
		}(i, wc)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			log.Printf("[traces] çip seçicilik probu (%s): %v — sıra aynen", semi[i].Key, err)
			counts = nil
			break
		}
	}
	ordered := orderSemiChips(semi, counts)
	f.Filters, f.FilterRoot = append(ordered, rest...), nil
	if counts != nil {
		minN := counts[0]
		for _, n := range counts {
			if n < minN {
				minN = n
			}
		}
		f.Explain.note("semi-chip order=%v counts=%v (%s, v0.10.1158)", chipKeys(ordered), counts, time.Since(t0).Round(time.Millisecond))
		if minN > traceChipSetMax && f.RankedWithin != nil {
			*f.RankedWithin = traceChipSetMax
		}
	}
	return f
}

// chipKeys — explain notu için çip anahtarları.
func chipKeys(fs []FilterExpr) []string {
	out := make([]string, len(fs))
	for i, fe := range fs {
		out[i] = fe.Key
	}
	return out
}
