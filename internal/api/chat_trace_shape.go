package api

// chat_trace_shape.go — v0.10.1373 (operator-reported): CoSRE'nin en yavaş
// trace kırılımı (chat_trace_drill.go) yalnız BASKIN span'i anlatıyordu.
// Operatör sohbetlerinde asıl soru "tekrar eden DB/servis çağrıları" idi
// (N+1 şekli: kök + 40 özdeş SELECT) — baskın span tek bir SELECT'ti, şekil
// görünmüyordu. traceShape aynı span listesi + BuildTraceAnalysis'ten (ek
// okuma YOK) trace'in şeklini çıkarır; renderTraceShapeTR kırılım satırına
// "Şekil: …" ekler.
//
// Paylar ÖZ süreyle (TraceNode.SelfNs) toplanır: iç içe DB/istemci span'leri
// (ör. ORM span'i + JDBC span'i) çift sayılmasın. Paralel çağrılarda toplam
// duvar saatini aşabilir → pay 1'de kırpılır.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	shapeRepeatMax    = 5  // traceShapeStats.Repeats tavanı
	shapeRepeatRender = 3  // satıra yazılan tekrar sayısı
	shapeNameRunes    = 48 // tekrar adı tavanı (rune)
)

// shapeRepeat — aynı adla ≥2 kez geçen span (süreler TAM süre: "ort 19 ms"
// çağrı başına ortalama).
type shapeRepeat struct {
	Name  string
	Count int
	SumNs int64
	AvgNs int64
}

// shapeShare — bir span sınıfının (DB / DB dışı istemci) payı.
type shapeShare struct {
	Count int
	SumNs int64   // öz süre toplamı
	Share float64 // SumNs / WallNs, ≤1
}

// traceShapeStats — tek trace'in şekli. Spans==0 → şekil yok.
type traceShapeStats struct {
	Spans         int
	WallNs        int64
	Repeats       []shapeRepeat
	DB            shapeShare
	Client        shapeShare
	DistinctPeers int
}

// isDBSpan — SAF: db_system kolonu ya da herhangi bir db.* attribute'u.
func isDBSpan(sp chstore.SpanRow) bool {
	if strings.TrimSpace(sp.DBSystem) != "" {
		return true
	}
	for k := range sp.Attributes {
		if strings.HasPrefix(k, "db.") {
			return true
		}
	}
	return false
}

// traceShape — SAF (tablo testli, chat_trace_shape_test.go).
func traceShape(spans []chstore.SpanRow, an chstore.TraceAnalysis) traceShapeStats {
	st := traceShapeStats{Spans: len(spans)}
	if len(spans) == 0 {
		return st
	}
	self := make(map[string]int64, len(an.Nodes))
	for _, n := range an.Nodes {
		self[n.SpanID] = n.SelfNs
	}
	st.WallNs = an.CriticalNs
	if st.WallNs <= 0 {
		var lo, hi int64
		for _, sp := range spans {
			if sp.StartTime > 0 && (lo == 0 || sp.StartTime < lo) {
				lo = sp.StartTime
			}
			if e := sp.StartTime + spanDurNs(sp); e > hi {
				hi = e
			}
		}
		if hi > lo {
			st.WallNs = hi - lo
		}
	}
	type agg struct {
		count int
		sum   int64
	}
	byName := map[string]*agg{}
	peers := map[string]bool{}
	for _, sp := range spans {
		dur := spanDurNs(sp)
		if sp.Name != "" {
			a := byName[sp.Name]
			if a == nil {
				a = &agg{}
				byName[sp.Name] = a
			}
			a.count++
			a.sum += dur
		}
		own, ok := self[sp.SpanID]
		if !ok || sp.SpanID == "" {
			own = dur
		}
		db := isDBSpan(sp)
		client := drillKindRank(sp.Kind) > 0
		switch {
		case db:
			st.DB.Count++
			st.DB.SumNs += own
		case client:
			st.Client.Count++
			st.Client.SumNs += own
		}
		if db || client {
			if p := peerOf(sp); p.Kind != "name" && p.Target != "" {
				peers[p.Target] = true
			}
		}
	}
	st.DistinctPeers = len(peers)
	share := func(ns int64) float64 {
		if st.WallNs <= 0 {
			return 0
		}
		if f := float64(ns) / float64(st.WallNs); f < 1 {
			return f
		}
		return 1
	}
	st.DB.Share, st.Client.Share = share(st.DB.SumNs), share(st.Client.SumNs)
	for name, a := range byName {
		if a.count >= 2 {
			st.Repeats = append(st.Repeats, shapeRepeat{Name: name, Count: a.count, SumNs: a.sum, AvgNs: a.sum / int64(a.count)})
		}
	}
	sort.Slice(st.Repeats, func(i, j int) bool {
		ri, rj := st.Repeats[i], st.Repeats[j]
		if ri.Count != rj.Count {
			return ri.Count > rj.Count
		}
		if ri.SumNs != rj.SumNs {
			return ri.SumNs > rj.SumNs
		}
		return ri.Name < rj.Name
	})
	if len(st.Repeats) > shapeRepeatMax {
		st.Repeats = st.Repeats[:shapeRepeatMax]
	}
	return st
}

// fmtShapeDur — SAF: Türkçe ondalık ("1,8 s", "19 ms", "0,4 ms").
func fmtShapeDur(ns int64) string {
	switch {
	case ns >= 1e9:
		return strings.Replace(fmt.Sprintf("%.1f s", float64(ns)/1e9), ".", ",", 1)
	case ns < 1e6:
		return strings.Replace(fmt.Sprintf("%.1f ms", float64(ns)/1e6), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f ms", float64(ns)/1e6)
}

// renderTraceShapeTR — SAF: "Şekil: 148 span · DB 92 span / 1,8 s (%78) ·
// dış çağrı 12 span / 420 ms (%9) · 3 hedef · tekrar: SELECT … ×46 (ort 19 ms),
// GET /x ×12 (ort 35 ms)". Boş bölüm yazılmaz; tekrar adı ≤48 rune, ≤3 tekrar.
func renderTraceShapeTR(st traceShapeStats) string {
	if st.Spans == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("Şekil: %d span", st.Spans)}
	if st.DB.Count > 0 {
		parts = append(parts, fmt.Sprintf("DB %d span / %s (%%%.0f)", st.DB.Count, fmtShapeDur(st.DB.SumNs), st.DB.Share*100))
	}
	if st.Client.Count > 0 {
		parts = append(parts, fmt.Sprintf("dış çağrı %d span / %s (%%%.0f)", st.Client.Count, fmtShapeDur(st.Client.SumNs), st.Client.Share*100))
	}
	if st.DistinctPeers > 0 {
		parts = append(parts, fmt.Sprintf("%d hedef", st.DistinctPeers))
	}
	if len(st.Repeats) > 0 {
		reps := st.Repeats
		if len(reps) > shapeRepeatRender {
			reps = reps[:shapeRepeatRender]
		}
		items := make([]string, len(reps))
		for i, r := range reps {
			items[i] = fmt.Sprintf("%s ×%d (ort %s)", oneLineSample(r.Name, shapeNameRunes), r.Count, fmtShapeDur(r.AvgNs))
		}
		parts = append(parts, "tekrar: "+strings.Join(items, ", "))
	}
	return strings.Join(parts, " · ")
}
