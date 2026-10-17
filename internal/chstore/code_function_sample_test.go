package chstore

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)

import (
	"strings"
	"testing"
	"time"
)

// TestCodeFunctionSampleSQL — ham spans okumasının sınır disiplini (CLAUDE.md
// sert kısıtı): zaman-sınırlı WHERE, iç LIMIT, dış LIMIT, max_execution_time,
// değerler bind arg; pencere ≤ 1 sa, ≤ 3 operasyon.
func TestCodeFunctionSampleSQL(t *testing.T) {
	to := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	from := to.Add(-6 * time.Hour)
	sql, args := codeFunctionSampleSQL("checkout", []string{"GET /a", "POST /b", "x", "y"}, from, to)
	for _, want := range []string{
		"FROM spans", "service_name = ?", "time >= ?", "time < ?", "name IN (?, ?, ?)",
		"LIMIT 5000", "LIMIT 10", "SETTINGS max_execution_time = 3",
		"has(attr_keys, 'code.function')", "code.namespace",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL'de yok: %q\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "checkout") || strings.Contains(sql, "GET /a") {
		t.Error("değer SQL'e gömülmüş — bind arg olmalı")
	}
	if strings.Count(sql, "?") != len(args) {
		t.Fatalf("placeholder %d ≠ arg %d", strings.Count(sql, "?"), len(args))
	}
	if args[0] != "checkout" || args[2] != to || args[1] != to.Add(-time.Hour) {
		t.Errorf("pencere 1 saate kırpılmadı / sıra bozuk: %v", args[:3])
	}
	if len(args) != 3+3+1 || args[3] != "GET /a" || args[5] != "x" {
		t.Errorf("operasyon tavanı 3 olmalı: %v", args)
	}
	if args[6] != uint64(1000)*1_000_000 {
		t.Errorf("yavaş eşiği ns: %v", args[6])
	}
}
