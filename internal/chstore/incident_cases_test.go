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

// v0.10.1475 — /ai "Onay bekleyen vakalar": liste yalnız taslak, sınırlı
// (pencere + LIMIT/OFFSET tavanı + max_execution_time), kaynak süzgeci
// isteğe bağlı; sayaç 2×2 durum × kaynak.
func TestPendingCasesSQLBounds(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	q, args := pendingCasesSQL(PendingCaseQuery{Source: "problem", From: from, Limit: 25, Offset: 50})
	for _, want := range []string{"FROM incident_cases FINAL", "status = ? AND opened_at >= ? AND source = ?",
		"ORDER BY opened_at DESC, case_id", "LIMIT ? OFFSET ?", "SETTINGS max_execution_time = 5"} {
		if !strings.Contains(q, want) {
			t.Errorf("sorgu %q içermiyor:\n%s", want, q)
		}
	}
	if n := strings.Count(q, "?"); n != len(args) {
		t.Fatalf("placeholder %d, arg %d", n, len(args))
	}
	if args[0] != CaseDraft || args[2] != "problem" || args[3] != 25 || args[4] != 50 {
		t.Errorf("arg sırası: %v", args)
	}
	// Kaynaksız: iki kaynak; tavanlar kırpılır (crafted limit/offset).
	q, args = pendingCasesSQL(PendingCaseQuery{From: from, Limit: 100000, Offset: 1 << 30})
	if strings.Contains(q, "source = ?") || strings.Count(q, "?") != len(args) {
		t.Errorf("kaynaksız sorgu: %s %v", q, args)
	}
	if args[len(args)-2] != PendingCaseListMax || args[len(args)-1] != PendingCaseOffsetMax {
		t.Errorf("tavan: %v", args)
	}
	if _, a := pendingCasesSQL(PendingCaseQuery{From: from, Limit: 0, Offset: -3}); a[len(a)-2] != PendingCaseListMax || a[len(a)-1] != 0 {
		t.Errorf("sıfır/negatif: %v", a)
	}
	// Onaylı vaka bu listeye hiç girmez: status sabit taslak.
	if strings.Contains(q, "'confirmed'") || strings.Contains(q, "status !=") {
		t.Error("liste onaylıyı okumamalı")
	}
}

func TestIncidentCaseCountsSQL(t *testing.T) {
	q, args := incidentCaseCountsSQL(time.Unix(0, 0))
	for _, want := range []string{"FROM incident_cases FINAL", "opened_at >= ?", "GROUP BY status, source", "LIMIT 50", "max_execution_time = 5"} {
		if !strings.Contains(q, want) {
			t.Errorf("sayaç sorgusu %q içermiyor", want)
		}
	}
	if strings.Count(q, "?") != len(args) {
		t.Error("placeholder/arg")
	}
}
