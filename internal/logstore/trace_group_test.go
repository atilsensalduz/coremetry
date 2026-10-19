package logstore

// trace_group_test.go — v0.10.1370 (operatör-bildirimli: "Teknik hatalar için
// Exceptions sayfasında trace incelemesi gibi loga bakmıyor"): GroupByTrace,
// CoSRE trace kırılımı ile exception açıklamasının ORTAK gruplaması. Pinler:
// traceIDs sırası, büyük/küçük harf duyarsız + kırpılmış eşleşme, trace başına
// tavan (≤0 = tavansız), şiddet azalan → zaman artan, listede olmayan / nil
// kayıt atılır, logu olmayan trace'in grubu boş.

import (
	"strings"
	"testing"
)

func TestGroupByTrace(t *testing.T) {
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccc"
	var logs []*LogRecord
	for i := 0; i < 10; i++ {
		sev := uint8(9)
		if i%3 == 0 {
			sev = 17
		}
		logs = append(logs, &LogRecord{TraceID: a, Severity: sev, Timestamp: int64(100 - i)})
	}
	logs = append(logs,
		&LogRecord{TraceID: "  " + strings.ToUpper(b) + " ", Severity: 13, Timestamp: 5},
		&LogRecord{TraceID: "ffffffffffffffffffffffffffffffff", Severity: 21},
		nil)

	cases := []struct {
		name     string
		ids      []string
		perTrace int
		wantLens []int
	}{
		{"tavan 4", []string{a, b, c}, 4, []int{4, 1, 0}},
		{"tavansız", []string{a, b}, 0, []int{10, 1}},
		{"sıra traceIDs'ten", []string{c, b, a}, 8, []int{0, 1, 8}},
		{"büyük harfli istek id'si", []string{strings.ToUpper(a)}, 2, []int{2}},
		{"boş liste", nil, 8, []int{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := GroupByTrace(logs, tc.ids, tc.perTrace)
			if len(g) != len(tc.wantLens) {
				t.Fatalf("grup sayısı %d, beklenen %d", len(g), len(tc.wantLens))
			}
			for i, want := range tc.wantLens {
				if len(g[i]) != want {
					t.Fatalf("grup %d: %d satır, beklenen %d", i, len(g[i]), want)
				}
				for k := 1; k < len(g[i]); k++ {
					p, q := g[i][k-1], g[i][k]
					if p.Severity < q.Severity || (p.Severity == q.Severity && p.Timestamp > q.Timestamp) {
						t.Fatalf("grup %d sıra: şiddet azalan, sonra zaman artan olmalı (%d)", i, k)
					}
				}
			}
		})
	}
	// Tavanlı grupta önce yüksek şiddet: a'nın 4 ERROR'u (i=0,3,6,9) ilk 4.
	g := GroupByTrace(logs, []string{a}, 4)
	for _, lg := range g[0] {
		if lg.Severity != 17 {
			t.Fatalf("tavan önce yüksek şiddeti tutmalı: %d", lg.Severity)
		}
	}
	if g[0][0].Timestamp != 91 {
		t.Fatalf("eşit şiddette en eski önce: %d", g[0][0].Timestamp)
	}
}
