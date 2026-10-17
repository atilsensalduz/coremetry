package anomaly

// v0.10.1275 — CoSRE adım 6: sinyaller arası pivot (trace → pod → JVM → log) + playbook.
//
// pivot_from_trace ve get_problem_root_cause'un playbook'u problem
// soruşturmasıyla AYNI saf dallanmayı okur (investigationPlan); ikinci bir
// "hangi sinyale bakılır" tablosu doğmasın.

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSymptomPlanParity(t *testing.T) {
	for _, tc := range []struct {
		symptom string
		problem chstore.Problem
	}{
		{"latency", chstore.Problem{Metric: "p99"}},
		{"error", chstore.Problem{Metric: "error_rate"}},
		{"down", chstore.Problem{Metric: "down"}},
		{"saturation", chstore.Problem{Metric: "heap"}},
		{" Latency ", chstore.Problem{Metric: "p99_ms"}},
	} {
		got := SymptomPlan(tc.symptom)
		want := PlanFamilies(tc.problem)
		if len(got) == 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: SymptomPlan %v ≠ PlanFamilies %v", tc.symptom, got, want)
		}
		var plan []string
		for _, f := range investigationPlan(tc.problem) {
			plan = append(plan, string(f))
		}
		if !reflect.DeepEqual(plan, want) {
			t.Errorf("%q: PlanFamilies investigationPlan'dan ayrıştı: %v vs %v", tc.symptom, want, plan)
		}
	}
	if SymptomPlan("bilinmeyen") != nil {
		t.Fatal("bilinmeyen belirti plan üretmemeli")
	}
	if got := SymptomPlan("latency"); !reflect.DeepEqual(got, []string{"saturation", "operations", "business"}) {
		t.Fatalf("gecikme planı değişti: %v", got)
	}
	if got := SymptomPlan("error"); !reflect.DeepEqual(got, []string{"exceptions", "logs", "business"}) {
		t.Fatalf("hata planı değişti: %v", got)
	}
}

func TestGatherDeepEvidenceOnDemandNilStore(t *testing.T) {
	d := GatherDeepEvidenceOnDemand(context.Background(), nil, nil, chstore.Problem{Metric: "p99"}, time.Now().Add(-time.Hour), time.Now())
	if len(d.Checked) != 0 {
		t.Fatalf("store yokken okuma yapılmamalı: %+v", d)
	}
	d = GatherDeepEvidenceOnDemand(context.Background(), nil, nil, chstore.Problem{Metric: "request_rate"}, time.Now(), time.Now())
	if len(d.Checked) != 0 {
		t.Fatal("plansız problem için okuma yok")
	}
}
