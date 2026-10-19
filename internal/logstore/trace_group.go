package logstore

// trace_group.go — v0.10.1370 (operatör: "Teknik hatalar için Exceptions
// sayfasında trace incelemesi gibi loga bakmıyor"): tek çoklu-trace log
// sayfasını trace id'ye göre gruplayan SAF yardımcı. v0.10.1327'den beri
// api.groupTraceLogs (CoSRE trace kırılımı) olarak yaşıyordu; exception
// açıklaması (anomaly) da AYNI gruplamayı kullansın diye buraya taşındı —
// iki kopya sürüklenmez. api.groupTraceLogs artık buna devreder.

import (
	"sort"
	"strings"
)

// GroupByTrace — SAF: logları traceIDs SIRASIYLA gruplar (eşleşme büyük/küçük
// harf duyarsız, logdaki id kırpılır), trace başına ≤perTrace satır (≤0 =
// tavansız), önce şiddet (azalan) sonra zaman (artan). Listede olmayan
// trace'in logu ve nil kayıt atılır. Dönen dilim len(traceIDs) uzunluğunda;
// logu olmayan trace'in grubu nil.
func GroupByTrace(logs []*LogRecord, traceIDs []string, perTrace int) [][]*LogRecord {
	idx := make(map[string]int, len(traceIDs))
	for i, id := range traceIDs {
		idx[strings.ToLower(id)] = i
	}
	out := make([][]*LogRecord, len(traceIDs))
	for _, lg := range logs {
		if lg == nil {
			continue
		}
		if i, ok := idx[strings.ToLower(strings.TrimSpace(lg.TraceID))]; ok {
			out[i] = append(out[i], lg)
		}
	}
	for i := range out {
		g := out[i]
		sort.SliceStable(g, func(a, b int) bool {
			if g[a].Severity != g[b].Severity {
				return g[a].Severity > g[b].Severity
			}
			return g[a].Timestamp < g[b].Timestamp
		})
		if perTrace > 0 && len(g) > perTrace {
			out[i] = g[:perTrace]
		}
	}
	return out
}
