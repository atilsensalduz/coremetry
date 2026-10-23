package chstore

// exception_sample_stacks_test.go — v0.10.1486: örnek stack okumasının
// ham-spans sınırları (servis + zaman + LIMIT + max_execution_time, kırpılmış
// metin, örnek başına dar zaman dilimi).

import (
	"context"
	"strings"
	"testing"
)

func TestExceptionSampleStacksQueryBounds(t *testing.T) {
	for _, hasCols := range []bool{true, false} {
		q := exceptionSampleStacksQuery(3, hasCols)
		for _, want := range []string{
			"FROM spans", "service_name = ?", "time >= ? AND time <= ?",
			"(trace_id = ? AND span_id = ? AND time >= ? AND time <= ?) OR",
			"substring(", "LIMIT 1 BY trace_id, span_id", "LIMIT 3\n", "SETTINGS max_execution_time = 5",
		} {
			if !strings.Contains(q, want) {
				t.Errorf("hasCols=%v: sorguda %q yok:\n%s", hasCols, want, q)
			}
		}
		if n := strings.Count(q, "?"); n != 3+4*3 {
			t.Errorf("hasCols=%v: %d yer tutucu, want %d", hasCols, n, 3+4*3)
		}
		if strings.Contains(q, "coremetry.") {
			t.Error("tablo nitelemesiz olmalı")
		}
	}
}

// Geçersiz işaretçiler sorguya hiç gitmez (conn nil: sorgu koşsaydı panik).
func TestExceptionSampleStacksSkipsInvalidRefs(t *testing.T) {
	s := &Store{}
	m, err := s.ExceptionSampleStacks(context.Background(), "orders-api", []ExceptionSpanRef{{TraceID: "a"}, {SpanID: "b", TimeNs: 1}})
	if err != nil || len(m) != 0 {
		t.Fatalf("m=%v err=%v", m, err)
	}
	if m, err := s.ExceptionSampleStacks(context.Background(), "", []ExceptionSpanRef{{TraceID: "a", SpanID: "b", TimeNs: 1}}); err != nil || len(m) != 0 {
		t.Fatalf("servissiz okuma: m=%v err=%v", m, err)
	}
}
