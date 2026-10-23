package chstore

import (
	"strings"
	"testing"
)

// TestCosreRoutingTurnsSQLBounded — v0.10.1464: raporun tek okuması sınırlı
// (CLAUDE.md CH sınırları): zaman penceresi iki uçtan, LIMIT, max_execution_time,
// DateTime64(3) kolonuna toDateTime64(?, 3) bağı; metinler kırpık, araç
// dizilerinden yalnız uzunluk, mevcut tablo (yeni şema yok).
func TestCosreRoutingTurnsSQLBounded(t *testing.T) {
	q := cosreRoutingTurnsSQL
	for _, w := range []string{
		"FROM cosre_interactions",
		"created_at >= toDateTime64(?, 3, 'UTC')",
		"created_at <  toDateTime64(?, 3, 'UTC')",
		"ORDER BY created_at DESC",
		"LIMIT ?",
		"SETTINGS max_execution_time = 15",
		"substringUTF8(question, 1, 300)",
		"substringUTF8(answer, 1, 200)",
		"length(tool_name)",
	} {
		if !strings.Contains(q, w) {
			t.Errorf("SQL %q taşımalı", w)
		}
	}
	for _, bad := range []string{"tool_output", "tool_args", "JOIN", "coremetry."} {
		if strings.Contains(q, bad) {
			t.Errorf("SQL %q taşımamalı", bad)
		}
	}
	if strings.Count(q, "?") != 3 {
		t.Errorf("bağ sayısı 3 olmalı (from, to, limit)")
	}
}
