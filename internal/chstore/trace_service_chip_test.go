package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.1155 — Operator-reported (prod): /traces'te servis seçili +
// `function_code = FN00042` çipi → 0 trace; aynı servis seçiliyken tablonun
// function_code kolonu FN00042 gösteriyor. CH'de doğrulandı: servisin
// span'lerinde attr_function_code boş, kod aynı trace'te BAŞKA servisin
// span'inde. Servis WHERE'i ve çip aynı span'de AND'leniyordu; kolon ise
// trace'in herhangi bir span'inden okunuyor (TraceExtras). Servis daraltması
// varken çip trace'i SEÇER: zaman sınırlı + tavanlı `trace_id GLOBAL IN`
// yarı-birleşimi, servis WHERE'i PK önekini korur.
func TestServiceChipSemiJoin(t *testing.T) {
	from := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	to := from.Add(6 * time.Hour)
	chip := []FilterExpr{{Key: "function_code", Op: "=", Values: []string{"FN00042"}}}
	chipSQL, _, err := chip[0].SQLWithPromoted(promotedCols())
	if err != nil || chipSQL == "" {
		t.Fatalf("chip SQL: %q %v", chipSQL, err)
	}

	nameChip := FilterExpr{Key: "name", Op: "=", Values: []string{"POST /login"}}
	chan2 := FilterExpr{Key: "channel_code", Op: "=", Values: []string{"X100"}}
	cases := []struct {
		name     string
		f        TraceFilter
		semiJoin bool
		nSemi    int
		rest     int
	}{
		{"servis + çip (operatörün vakası)", TraceFilter{Service: "app", From: from, To: to, Filters: chip}, true, 1, 0},
		{"servis + arama + çip", TraceFilter{Service: "app", Search: "POST", From: from, To: to, Filters: chip}, true, 1, 0},
		{"RequireServices + çip", TraceFilter{RequireServices: []string{"a", "b"}, From: from, To: to, Filters: chip}, true, 1, 0},
		{"iki özel çip — ayrı yarı-birleşim", TraceFilter{Service: "app", From: from, To: to, Filters: []FilterExpr{chip[0], chan2}}, true, 2, 0},
		{"kimlik + özel çip — kimlik span düzeyi", TraceFilter{Service: "app", From: from, To: to, Filters: []FilterExpr{nameChip, chip[0]}}, true, 1, 1},
		{"servis + kimlik çipi (name) — eski şekil", TraceFilter{Service: "app", From: from, To: to, Filters: []FilterExpr{nameChip}}, false, 0, 1},
		{"servis + resource çipi — eski şekil", TraceFilter{Service: "app", From: from, To: to, Filters: []FilterExpr{{Key: "k8s.pod.name", Op: "=", Values: []string{"p"}}}}, false, 0, 1},
		{"gruplu kök + servis", TraceFilter{Service: "app", From: from, To: to,
			FilterRoot: &FilterGroup{Join: "AND", Filters: chip}}, true, 1, 0},
		{"servissiz çip — eski şekil", TraceFilter{From: from, To: to, Filters: chip}, false, 0, 1},
		{"servis, çip yok", TraceFilter{Service: "app", From: from, To: to}, false, 0, 0},
		{"span-düzeyi prob — eski şekil", TraceFilter{Service: "app", From: from, To: to, Filters: chip, forceFiltersInWhere: true}, false, 0, 1},
		{"zaman sınırı yok — sınırsız alt sorgu yok", TraceFilter{Service: "app", Filters: chip}, false, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, got := splitServiceChips(tc.f); got != tc.semiJoin {
				t.Fatalf("splitServiceChips ok = %v, want %v", got, tc.semiJoin)
			}
			wc := buildGetTracesWhere(tc.f, "cluster")
			var semis []string
			topLevelChip := false
			for _, c := range wc.conds {
				if strings.HasPrefix(c, "trace_id GLOBAL IN (") {
					semis = append(semis, c)
				}
				if c == chipSQL {
					topLevelChip = true
				}
			}
			if len(semis) != tc.nSemi {
				t.Fatalf("yarı-birleşim sayısı %d, want %d: %v", len(semis), tc.nSemi, wc.conds)
			}
			if tc.f.Search == "" {
				if got := len(spanChipFilter(tc.f).Filters); got != tc.rest {
					t.Errorf("span düzeyi kalan çip %d, want %d", got, tc.rest)
				}
			}
			if n := strings.Count(wc.sql(), "?"); n != len(wc.args) {
				t.Errorf("yer tutucu %d ≠ argüman %d", n, len(wc.args))
			}
			if !tc.semiJoin {
				return
			}
			semi := semis[0]
			if topLevelChip {
				t.Fatalf("çip hâlâ span-düzeyi WHERE'de: %v", wc.conds)
			}
			for _, want := range []string{"FROM spans", "time >= ?", "time <= ?", chipSQL, "LIMIT ?"} {
				if !strings.Contains(semi, want) {
					t.Errorf("yarı-birleşimde %q yok: %s", want, semi)
				}
			}
			if strings.Contains(semi, "service_name") {
				t.Errorf("alt sorgu servise daraltılmamalı (çip başka serviste): %s", semi)
			}
			if tc.f.Service != "" && !containsCond(wc.conds, "service_name = ?") {
				t.Errorf("servis PK öneki kayboldu: %v", wc.conds)
			}
			// Özel çip HAVING'e gitmemeli (servis WHERE'i öteki servisleri
			// önceden düşürür, countIf hiç eşleşmez).
			if parts, _ := traceLevelFilterHaving(tc.f); tc.f.Search != "" {
				for _, p := range parts {
					if strings.Contains(p, chipSQL) {
						t.Errorf("özel çip HAVING'de: %s", p)
					}
				}
			}
		})
	}
}

func containsCond(conds []string, want string) bool {
	for _, c := range conds {
		if c == want {
			return true
		}
	}
	return false
}
