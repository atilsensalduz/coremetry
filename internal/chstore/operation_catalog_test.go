package chstore

// CoSRE operasyon tespiti Faz 1 — katalog doldurma SQL'i: ham spans
// taranmaz (MV), sınırlar ve yer tutucu sayısı pinli.

import (
	"strings"
	"testing"
)

func TestOperationCatalogSQL(t *testing.T) {
	if !strings.Contains(spanOperationCatalogSQL, "FROM operation_summary_5m") || strings.Contains(spanOperationCatalogSQL, "FROM spans") {
		t.Error("span kataloğu MV'den okumalı")
	}
	for _, q := range []string{spanOperationCatalogSQL, oracleOperationCatalogSQL(0), oracleOperationCatalogSQL(2)} {
		if !strings.Contains(q, "LIMIT") || !strings.Contains(q, "max_execution_time") || !strings.Contains(q, "WHERE time") && !strings.Contains(q, "WHERE time_bucket") {
			t.Errorf("sınır eksik:\n%s", q)
		}
	}
	if n := strings.Count(oracleOperationCatalogSQL(2), "?"); n != 3 {
		t.Errorf("2 kaynak + pencere = 3 yer tutucu, %d", n)
	}
	if strings.Contains(oracleOperationCatalogSQL(0), "source_id IN") {
		t.Error("kod kaynağı yoksa yalnız attribute ifadesi")
	}
}

// Endpoint ekseni spanmetrics_1m'in GERÇEK kolonlarını okur (time_bucket,
// calls_state, error_state) — "ts" yazımı prod'da UNKNOWN_IDENTIFIER olurdu
// ve endpoint ekseni sessizce boş kalırdı.
func TestEndpointCatalogSQLColumns(t *testing.T) {
	for _, want := range []string{"FROM spanmetrics_1m", "WHERE time_bucket >= ?", "max(time_bucket)", "countMerge(calls_state)", "countMerge(error_state)", "LIMIT", "max_execution_time"} {
		if !strings.Contains(endpointCatalogSQL, want) {
			t.Errorf("endpointCatalogSQL %q içermiyor", want)
		}
	}
	if strings.Contains(endpointCatalogSQL, " ts") || strings.Contains(endpointCatalogSQL, "(ts)") {
		t.Error("spanmetrics_1m'de ts kolonu yok")
	}
}
