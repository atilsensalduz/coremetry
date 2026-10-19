package chstore

// oracle_op_traces_test.go — v0.10.1388: Oracle-only operasyonun trace id
// sorgusu sınırlı (PK öneki, zaman penceresi, LIMIT, max_execution_time).

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOracleOpTraceIDsSQLBounded(t *testing.T) {
	q := oracleOpTraceIDsSQL(10)
	for _, want := range []string{
		"FROM oracle_error_log",
		"WHERE source_id = ? AND time >= ? AND time < ?",
		"trimBoth(operation_code) = ?",
		"trace_id != ''",
		"LIMIT 10",
		"SETTINGS max_execution_time =",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("sorguda yok: %q\n%s", want, q)
		}
	}
	if strings.Contains(q, "coremetry.") {
		t.Error("tablo nitelenmemeli")
	}
}

func TestOracleTraceIDsForOperationNoQueryOnEmpty(t *testing.T) {
	var s *Store // sorgu atılırsa nil conn panikler
	now := time.Now()
	for _, c := range []struct{ src, op string }{{"", "PAYMENT_CREATE"}, {"src-1", " "}} {
		ids, err := s.OracleTraceIDsForOperation(context.Background(), c.src, c.op, now.Add(-time.Hour), now, 10)
		if err != nil || len(ids) != 0 {
			t.Errorf("%+v: %v %v", c, ids, err)
		}
	}
	if ids, _ := s.OracleTraceIDsForOperation(context.Background(), "src-1", "PAYMENT_CREATE", now, now, 10); len(ids) != 0 {
		t.Error("ters pencere")
	}
}
