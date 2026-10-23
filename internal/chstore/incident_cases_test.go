package chstore

// CoSRE öğrenme Faz 3 — similar_incidents puan sorgusu: ağırlıklar, onaylı
// önce sırası, gürültü dışlaması ve placeholder/arg hizası.

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSimilarCasesSQL(t *testing.T) {
	q, args := similarCasesSQL("svc", []string{"MCA-00029"}, time.Unix(0, 0), 3)
	for _, want := range []string{
		"(service = ?) * 3 + length(matched) * 2",
		"exp(-dateDiff('second', closed_at, now64(3)) / 86400.0 / 30)",
		"ORDER BY (status = 'confirmed') DESC, score DESC",
		"category != 'noise'",
		"FROM incident_cases FINAL",
		"max_execution_time",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("sorgu %q içermiyor", want)
		}
	}
	if n := strings.Count(q, "?"); n != len(args) {
		t.Errorf("placeholder %d, arg %d", n, len(args))
	}
	if _, a := similarCasesSQL("", nil, time.Unix(0, 0), 3); a[0] == nil {
		t.Error("nil kod dizisi boş diziye çevrilmeli")
	}
}

func TestIncidentCaseArgsAlign(t *testing.T) {
	cols := strings.Count(incidentCaseCols, ",") + 1
	if n := len(incidentCaseArgs(IncidentCase{CaseID: "p:1"})); n != cols {
		t.Fatalf("kolon %d, arg %d", cols, n)
	}
	if CaseIDFor("exception", "f") != "ex:f" || CaseIDFor("problem", "p") != "p:p" {
		t.Error("vaka kimliği")
	}
}

func TestTopErrorGroups(t *testing.T) {
	s := []RollupSeries{
		{Group: "B", Points: []RollupPoint{{Errors: 5}}},
		{Group: "A", Points: []RollupPoint{{Errors: 5}}},
		{Group: "C", Points: []RollupPoint{{Errors: 0}}},
		{Group: "D", Points: []RollupPoint{{Errors: 9}}},
	}
	got := topErrorGroups(s, 2)
	if len(got) != 2 || got[0] != "D" || got[1] != "A" {
		t.Fatalf("%v", got)
	}
}

// TestSameCauseMoved — v0.10.1450: onay ucu (cosre_correct) ve değerlendirme
// (EvalCase.AcceptedSuggestion) aynı SAF kuralı okur; api'de ikinci yazım yok.
func TestSameCauseMoved(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"DB  havuzu doldu", "db havuzu doldu", true},
		{" Deploy v2.1 ", "deploy v2.1", true},
		{"", "", false},
		{"   ", " ", false},
		{"a", "b", false},
		{"orders-api deploy", "orders-api deploy geri alındı", false},
	} {
		if got := SameCause(c.a, c.b); got != c.want {
			t.Errorf("SameCause(%q, %q) = %v, %v beklenir", c.a, c.b, got, c.want)
		}
	}
	src, err := os.ReadFile("../api/cosre_cases.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "func sameCause(") {
		t.Fatal("api/cosre_cases.go kendi sameCause'unu taşımamalı (chstore.SameCause tek yazım)")
	}
}
