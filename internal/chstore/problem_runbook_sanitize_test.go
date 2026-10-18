package chstore

import "testing"

// v0.10.1341 — okuma-anı runbook süzmesi: geçersiz (javascript:) kural
// runbook'u servis kataloğu runbook'una düşer; geçersiz servis runbook'u
// "" olur; self-health kuralları değişmez.
func TestApplyProblemRunbooksSanitises(t *testing.T) {
	rules := []AlertRule{
		{ID: "bad-rule", RunbookURL: "javascript:alert(1)"},
		{ID: "good-rule", RunbookURL: " https://wiki.example.com/rb "},
		{ID: "evil-rule", RunbookURL: "//evil.com"},
	}
	mds := map[string]ServiceMetadata{
		"payments": {RunbookURL: "/runbook?id=pay"},
		"orders":   {RunbookURL: "javascript:alert(2)"},
	}
	problems := []Problem{
		{RuleID: "bad-rule", Service: "payments"},
		{RuleID: "good-rule", Service: "payments"},
		{RuleID: "bad-rule", Service: "orders"},
		{RuleID: "none", Service: "orders"},
		{RuleID: "evil-rule", Service: "unknown"},
		{RuleID: "self-spool-depth", Service: "orders"},
	}
	want := []string{
		"/runbook?id=pay",
		"https://wiki.example.com/rb",
		"",
		"",
		"",
		"/admin/stats",
	}
	got := applyProblemRunbooks(problems, rules, mds)
	for i, w := range want {
		if got[i].RunbookURL != w {
			t.Errorf("problem %d (%s/%s): RunbookURL = %q, want %q", i, problems[i].RuleID, problems[i].Service, got[i].RunbookURL, w)
		}
	}
}
