package vmetrics

// v0.10.1271 — CoSRE adım 2: çalışan MetricsQL kaydedicisi (kanıt çipi).

import (
	"context"
	"testing"
)

func TestQueryRecorder(t *testing.T) {
	recordQuery(context.Background(), "up") // kaydedicisiz: no-op, panik yok
	rec := &QueryRecorder{}
	ctx := WithQueryRecorder(context.Background(), rec)
	recordQuery(ctx, "a")
	recordQuery(ctx, "b")
	got := rec.Queries()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("queries = %v", got)
	}
	got[0] = "x"
	if rec.Queries()[0] != "a" {
		t.Fatal("Queries must return a copy")
	}
	var nilRec *QueryRecorder
	if nilRec.Queries() != nil {
		t.Fatal("nil recorder must return nil")
	}
}
