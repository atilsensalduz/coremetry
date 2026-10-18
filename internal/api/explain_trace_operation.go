package api

// explain_trace_operation.go — v0.10.1329 (operatör-bildirimli): trace'in
// ANA operasyonu, kesin kaynaktan.
//
// Olay: trace 6008dded… kökü POST /BSAWEB/application/directCreditPreApproval,
// kendi Oracle satırı LOAN_…_APPLICATIONTRX_DIRECT_CREDIT_PRE_APPROVAL_REST;
// Trace sayfası çipi ise fonksiyon kodu sözlüğünün (bankanın tamamında son 7
// günde en çok satırlı aday) LOAN_…_APPLICATIONINQ_… adını "(+15)" ile "bu
// trace'in operasyonu" gibi gösteriyordu. Frontend düzeltmesi
// (lib/oracleFunctionOps.ts pickTraceOperation) önce trace'in KENDİ Oracle
// satırlarını okur; bu dosya aynı öncelik sırasının backend ikizi.
//
// Bu sürümde YALNIZ dikiş: traceExplainInput'a MainOperation /
// OtherOperations / OperationSource / RootVersion girer ama prompt'a girmez
// (TestTraceExplainUserGolden bayt-bayt aynı kalır). Sözlük BİLEREK yok:
// 1:n tahmin modele "bu trace'in operasyonu" diye verilmez.

import (
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// traceOperationSpanKeys — span attribute'unda operasyon kodunu taşıyan
// yazımlar, öncelik sırasıyla (FE OPERATION_CODE_KEYS ile aynı liste).
var traceOperationSpanKeys = []string{"operation.code", "OPERATION_CODE", "operation_code"}

// Operasyonun kaynağı — FE TraceOperationPick.source ile aynı yazım.
const (
	traceOpSourceOracle = "oracle"
	traceOpSourceSpan   = "span"
)

func spanOperationCode(sp chstore.SpanRow) string {
	for _, k := range traceOperationSpanKeys {
		if v := strings.TrimSpace(sp.Attributes[k]); v != "" {
			return v
		}
	}
	return ""
}

func isEntrySpanKind(kind string) bool {
	k := strings.ToLower(kind)
	return k == "server" || k == "consumer"
}

// pickMainOperation — SAF. Öncelik:
//
//	(a) oracle — bu trace'in Oracle satırlarındaki tekil OperationCode.
//	    Sıra: satırı kök servisin bir span'ine bağlı olan önce → en çok
//	    satırlı → en erken satır → ad (determinizm). others = BU trace'teki
//	    diğer gerçek operasyonlar.
//	(b) span — kök span'in, sonra giriş (server/consumer) span'lerinin,
//	    sonra herhangi bir span'in operasyon attribute'u (zaman sırasıyla);
//	    others = span'lerdeki diğer tekil değerler.
//	(c) hiçbiri → "", nil, "".
func pickMainOperation(rows []chstore.OracleErrorRow, spans []chstore.SpanRow) (main string, others []string, source string) {
	var rootSvc string
	if len(spans) > 0 {
		rootSvc = traceRootSpan(spans).ServiceName
	}
	rootSvcSpans := map[string]bool{}
	for _, sp := range spans {
		if sp.ServiceName == rootSvc && sp.SpanID != "" {
			rootSvcSpans[sp.SpanID] = true
		}
	}
	type opGroup struct {
		op         string
		rows       int
		first      int64
		rootLinked bool
	}
	groups := map[string]*opGroup{}
	for _, r := range rows {
		op := strings.TrimSpace(r.OperationCode)
		if op == "" {
			continue
		}
		t := r.Time.UnixNano()
		g := groups[op]
		if g == nil {
			g = &opGroup{op: op, first: t}
			groups[op] = g
		}
		g.rows++
		if t < g.first {
			g.first = t
		}
		if r.SpanID != "" && rootSvcSpans[r.SpanID] {
			g.rootLinked = true
		}
	}
	if len(groups) > 0 {
		ranked := make([]*opGroup, 0, len(groups))
		for _, g := range groups {
			ranked = append(ranked, g)
		}
		sort.Slice(ranked, func(i, j int) bool {
			a, b := ranked[i], ranked[j]
			if a.rootLinked != b.rootLinked {
				return a.rootLinked
			}
			if a.rows != b.rows {
				return a.rows > b.rows
			}
			if a.first != b.first {
				return a.first < b.first
			}
			return a.op < b.op
		})
		for _, g := range ranked[1:] {
			others = append(others, g.op)
		}
		return ranked[0].op, others, traceOpSourceOracle
	}
	if len(spans) == 0 {
		return "", nil, ""
	}
	ordered := append([]chstore.SpanRow(nil), spans...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartTime < ordered[j].StartTime })
	pick := spanOperationCode(traceRootSpan(spans))
	if pick == "" {
		for _, sp := range ordered {
			if isEntrySpanKind(sp.Kind) {
				if pick = spanOperationCode(sp); pick != "" {
					break
				}
			}
		}
	}
	if pick == "" {
		for _, sp := range ordered {
			if pick = spanOperationCode(sp); pick != "" {
				break
			}
		}
	}
	if pick == "" {
		return "", nil, ""
	}
	seen := map[string]bool{pick: true}
	for _, sp := range ordered {
		if c := spanOperationCode(sp); c != "" && !seen[c] {
			seen[c] = true
			others = append(others, c)
		}
	}
	return pick, others, traceOpSourceSpan
}
