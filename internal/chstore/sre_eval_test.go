package chstore

// v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü. Koşu
// satırları sistem sayfası; tüm okumalar FINAL + sınırlı + süreli; vaka
// okuması yalnız istenen durum / kaynak / pencere; 👎 inceleme okuması sre-*
// yüzeyine ve pencereli ai_calls alt sorgusuna bağlı.

import (
	"strings"
	"testing"
	"time"
)

func TestSREEvalSQL(t *testing.T) {
	for name, q := range map[string]string{"runs": listSREEvalRunsSQL, "replay": listSREReplayRowsSQL, "recent": listRecentSREInvestigationsSQL} {
		for _, want := range []string{"FROM saved_views FINAL", "page = ?", "owner_id = ''", "LIMIT ?", "SETTINGS max_execution_time = 5"} {
			if !strings.Contains(q, want) {
				t.Errorf("%s SQL %q taşımalı:\n%s", name, want, q)
			}
		}
	}
	if !strings.Contains(listSREEvalRunsSQL, "startsWith(id, 'sre-eval-')") || !strings.Contains(listSREEvalRunsSQL, "ORDER BY created_at DESC") {
		t.Error("koşu listesi sre-eval- önekli, en yeni önce")
	}
	if !strings.Contains(listRecentSREInvestigationsSQL, "NOT startsWith(id, 'sre-replay-')") {
		t.Error("canlı örneklem tekrar satırlarını dışlamalı")
	}
	if SREReplayKey("e1", "p-1") != "replay-e1-p-1" || SREInvestigationID(SREReplayKey("e1", "p")) != "sre-replay-e1-p" || SREEvalID("e1") != "sre-eval-e1" {
		t.Error("anahtar biçimleri")
	}
	if !IsSystemSavedViewPage(SREEvalPage) {
		t.Error("sre-eval sistem sayfası olmalı — genel /api/views ucu koşu satırı yazamamalı")
	}
	for in, want := range map[int]int{0: 200, 5: 5, 201: 200} {
		if clampSREEvalLimit(in) != want {
			t.Errorf("clampSREEvalLimit(%d)", in)
		}
	}
}

func TestListIncidentCasesSQL(t *testing.T) {
	from, to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	q, args := listIncidentCasesSQL(IncidentCaseQuery{Status: CaseConfirmed, Source: "problem", From: from, To: to, Limit: 5000})
	for _, want := range []string{"FROM incident_cases FINAL", "status = ?", "source = ?", "opened_at >= ? AND opened_at < ?", "ORDER BY opened_at DESC", "LIMIT ?", "max_execution_time = 5"} {
		if !strings.Contains(q, want) {
			t.Errorf("%q yok:\n%s", want, q)
		}
	}
	if len(args) != 5 || args[0] != CaseConfirmed || args[1] != "problem" || args[4] != IncidentCaseListMax {
		t.Fatalf("argümanlar: %v", args)
	}
	if strings.Count(q, "?") != len(args) {
		t.Fatalf("yer tutucu %d ≠ argüman %d", strings.Count(q, "?"), len(args))
	}
}

func TestSREReviewDownvotesSQL(t *testing.T) {
	for _, cc := range []bool{true, false} {
		q := sreReviewDownvotesSQL(cc)
		for _, want := range []string{"f.verdict = -1", "FROM ai_feedback AS f FINAL", "LIMIT ?", "max_execution_time = 10"} {
			if !strings.Contains(q, want) {
				t.Errorf("callCols=%v %q yok", cc, want)
			}
		}
		n := strings.Count(q, "?")
		if (cc && n != 5) || (!cc && n != 3) {
			t.Errorf("callCols=%v yer tutucu %d", cc, n)
		}
		if strings.Count(q, ",") < 8 {
			t.Errorf("9 kolon taranır")
		}
	}
	q := sreReviewDownvotesSQL(true)
	if !strings.Contains(q, "c.id = f.call_id") || !strings.Contains(q, "startsWith(surface, 'sre-')") || !strings.Contains(q, "FROM ai_calls") {
		t.Errorf("adım 8 call_id birleşimi + sre-* yüzey süzgeci:\n%s", q)
	}
}
