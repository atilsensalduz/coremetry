package chstore

// summary_operation_hits_test.go — v0.10.1333 (birleşik arama, GET /api/search).
//
// SÖZLEŞME (saf operationHitsSQL): endpoint kaynağı operation_summary_5m'den
// (service_name, name) ÇİFTLERİNİ okur — ListOperationNames'in DISTINCT şekli,
// aynı WHERE (operationNamesQuery: öz-telemetri dışlama + ILIKE), aynı önek
// sıralaması; ham spans ASLA (MV-first), her iki sorguda max_execution_time
// ve sayfada LIMIT. Toplam çift sayısıdır (uniqExact(service_name, name)),
// ad sayısı değil — iki serviste aynı ad iki ayrı sonuçtur.
// Mutasyon: FROM'u spans'a çevirmek, LIMIT/SETTINGS'i silmek, NOT IN'i
// düşürmek ya da önek bind'ini kaybetmek bu testi düşürür.

import (
	"strings"
	"testing"
)

func TestOperationHitsSQLShape(t *testing.T) {
	countQ, pageQ, countArgs, pageArgs := operationHitsSQL("", "checkout", 20)
	for name, q := range map[string]string{"count": countQ, "page": pageQ} {
		if !strings.Contains(q, "FROM operation_summary_5m") || strings.Contains(q, "FROM spans") {
			t.Errorf("%s: MV-first (operation_summary_5m) bekleniyor:\n%s", name, q)
		}
		if !strings.Contains(q, "SETTINGS max_execution_time") {
			t.Errorf("%s: max_execution_time yok:\n%s", name, q)
		}
		if !strings.Contains(q, "service_name NOT IN ?") || !strings.Contains(q, "name ILIKE ?") {
			t.Errorf("%s: servissiz aramada öz-telemetri dışlama + ILIKE bekleniyor:\n%s", name, q)
		}
	}
	if !strings.Contains(countQ, "uniqExact(service_name, name)") {
		t.Errorf("toplam (servis, ad) çifti saymalı:\n%s", countQ)
	}
	if !strings.Contains(pageQ, "SELECT DISTINCT service_name, name") || !strings.Contains(pageQ, "LIMIT ?") {
		t.Errorf("sayfa DISTINCT (service_name, name) + LIMIT olmalı:\n%s", pageQ)
	}
	if !strings.Contains(pageQ, "ORDER BY startsWith(lowerUTF8(name), lowerUTF8(?)) DESC, name, service_name") {
		t.Errorf("jokersiz desende önek önce, sonra ad, sonra servis:\n%s", pageQ)
	}
	if len(countArgs) != 2 || countArgs[1] != "%checkout%" {
		t.Errorf("count args (öz-telemetri listesi, like): %v", countArgs)
	}
	// page: WHERE args + önek bind + limit
	if len(pageArgs) != 4 || pageArgs[2] != "checkout" || pageArgs[3] != 20 {
		t.Errorf("page args (liste, like, önek, limit): %v", pageArgs)
	}
}

func TestOperationHitsSQLServicePushdownAndClamp(t *testing.T) {
	countQ, pageQ, _, pageArgs := operationHitsSQL("payments", "pay*", 500)
	if strings.Contains(countQ, "NOT IN") || !strings.Contains(countQ, "service_name = ?") {
		t.Errorf("servis seçiliyken eşitlik daraltması, dışlama yok:\n%s", countQ)
	}
	if strings.Contains(pageQ, "startsWith") {
		t.Errorf("jokerli desende önek sıralaması yok:\n%s", pageQ)
	}
	if got := pageArgs[len(pageArgs)-1]; got != operationHitsMaxLimit {
		t.Errorf("limit %d'e kırpılmalı, gelen %v", operationHitsMaxLimit, got)
	}
	_, _, _, a0 := operationHitsSQL("svc", "x", 0)
	if got := a0[len(a0)-1]; got != operationHitsMaxLimit {
		t.Errorf("limit ≤0 → varsayılan %d, gelen %v", operationHitsMaxLimit, got)
	}
}
