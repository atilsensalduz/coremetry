package chstore

// v0.10.1327 (operator-reported: "bsa-loan-legalproceeding-prod neden yavaş"
// SocketTimeoutException dedi ama NEREDE demedi) — trace drill'i baskın span'in
// operasyon taban çizgisini (24 sa p50) SpanOperationRED'den okur. SQL pini:
// MV-first (operation_summary_5m, ham spans DEĞİL), zaman-sınırlı, p99 eski
// konumunda (3. kolon), p50 yeni 4. kolon, max_execution_time.

import (
	"strings"
	"testing"
	"time"
)

func TestSpanOperationREDQueryPinsP50(t *testing.T) {
	from := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	q, args := spanOperationREDQuery("bsa-loan-legalproceeding-prod", "POST", from, to)
	for _, want := range []string{
		"FROM operation_summary_5m",
		"arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6,",
		"arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6",
		"WHERE name = ? AND time_bucket >= ? AND time_bucket < ?",
		" AND service_name = ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("SQL %q taşımalı:\n%s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("taban çizgisi ham spans'tan okunmamalı (MV-first)")
	}
	// p99 kolonu p50'den ÖNCE (Scan sırası: calls, errors, p99, p50).
	if i, j := strings.Index(q, "), 3) / 1e6"), strings.Index(q, "), 1) / 1e6"); i < 0 || j < 0 || i > j {
		t.Errorf("kolon sırası p99 sonra p50 olmalı (p99=%d p50=%d)", i, j)
	}
	if len(args) != 4 || args[0] != "POST" || args[3] != "bsa-loan-legalproceeding-prod" {
		t.Errorf("bind'ler: %v", args)
	}
	q2, args2 := spanOperationREDQuery("", "POST", from, to)
	if strings.Contains(q2, "service_name") || len(args2) != 3 {
		t.Errorf("servissiz sorgu servis süzgeci taşımamalı: %s %v", q2, args2)
	}
}
