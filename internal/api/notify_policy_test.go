package api

// notify_policy_test.go — v0.10.1523 (Problem standardı R1): admin uçlarının
// kablosu, replay gün kelepçesi + önbellek anahtarı, D2 görünüm önceliği ikizi.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNotifyPolicyRoutesAdminGated(t *testing.T) {
	for file, want := range map[string][]string{
		"notify_policy.go": {`registerRoutesExtra("notify-policy"`, `"GET /api/admin/notify-policy"`, `"PUT /api/admin/notify-policy"`,
			`s.audit(r, "notify.policy"`, "chstore.ValidateNotifyPolicy("},
		"notify_policy_replay.go": {`registerRoutesExtra("notify-policy-replay"`, `"GET /api/admin/notify-policy/replay"`, "s.serveCached(", "5*time.Minute"},
	} {
		src := readAPISourceNoComments(t, file)
		for _, w := range want {
			if !strings.Contains(src, w) {
				t.Errorf("%s: %q yok", file, w)
			}
		}
		if strings.Count(src, "mux.HandleFunc(") != strings.Count(src, "auth.RequireRole(auth.RoleAdmin") {
			t.Errorf("%s: her uç admin kapılı olmalı", file)
		}
	}
	if strings.Contains(readAPISourceNoComments(t, "api.go"), "notify-policy") {
		t.Error("api.go büyümemeli")
	}
}

func TestNotifyReplayDaysAndKey(t *testing.T) {
	for in, want := range map[string]int{"": 14, "x": 14, "0": 14, "-3": 14, "7": 7, "30": 30, "31": 30, "999": 30} {
		if got := notifyReplayDays(in); got != want {
			t.Errorf("days=%q → %d, istenen %d", in, got, want)
		}
	}
	now := time.Date(2026, 10, 24, 10, 15, 0, 0, time.UTC)
	pol := chstore.DefaultNotifyPolicy()
	k := notifyReplayKey(14, pol, now)
	if k != notifyReplayKey(14, pol, now.Add(20*time.Minute)) {
		t.Error("aynı saat kovası aynı anahtar")
	}
	if k == notifyReplayKey(7, pol, now) || k == notifyReplayKey(14, pol, now.Add(time.Hour)) {
		t.Error("gün / saat anahtarı ayırmalı")
	}
	p2 := pol
	p2.P3 = chstore.NotifyP3Off
	if k == notifyReplayKey(14, p2, now) {
		t.Error("politika değişimi anahtarı ayırmalı")
	}
}

func TestDecodeNotifyPolicyPutKeepsStored(t *testing.T) {
	stored := chstore.DefaultNotifyPolicy()
	stored.QuietHours = &chstore.NotifyQuietHours{From: "22:00", To: "07:00"}
	got, err := decodeNotifyPolicyPut(stored, []byte(`{"mode":"off"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != chstore.NotifyModeOff || got.P3 != stored.P3 || got.QuietHours == nil || got.QuietHours.From != "22:00" {
		t.Fatalf("kayıtlı alanlar korunmadı: %+v", got)
	}
	got.DigestTimes[0] = "x"
	got.QuietHours.From = "y"
	if stored.DigestTimes[0] != "09:00" || stored.QuietHours.From != "22:00" {
		t.Fatal("decode kayıtlı değeri yerinde ezdi")
	}
}

// TestNotifyViewPriorityMatchesInbox — D2 pini: chstore.NotifyViewPriority,
// /inbox'ın forceNonExceptionP3With kuralıyla problem/incident satırlarında
// BİREBİR aynı önceliği verir (biri değişirse bildirim ve ekran ayrışır).
func TestNotifyViewPriorityMatchesInbox(t *testing.T) {
	keep := chstore.DefaultInboxKeepSourcePriority()
	probs := []chstore.Problem{
		{ID: "1", RuleID: "anomaly:a:error_rate", Severity: "critical", Priority: "P1"},
		{ID: "2", RuleID: "anomaly:a:p99_ms", Severity: "critical", Priority: "P1"},
		{ID: "3", RuleID: "builtin-http-p99-5s", Severity: "warning", Priority: "P2"},
		{ID: "4", RuleID: "user-x", Severity: "critical", Priority: "P1"},
		{ID: "5", RuleID: "db-health:oracle@h/d", Severity: "critical", Priority: "P1"},
		{ID: "6", RuleID: "svc-slowdown:crm", Severity: "critical", Priority: "P1"},
		{ID: "7", RuleID: "anomaly:ext:src/OP:ext:error_count", Severity: "critical", Priority: "P2"},
		{ID: "8", RuleID: "anomaly:ext-down:src", Severity: "critical", Priority: "P1"},
		{ID: "9", RuleID: "exception-storm", Severity: "critical", Priority: "P1"},
		{ID: "10", RuleID: "anomaly:a:error_rate", Severity: "warning", Priority: "P3"},
	}
	for _, p := range probs {
		items := []InboxItem{{Kind: "problem", Priority: p.Priority, Severity: p.Severity, Problem: &InboxProblemRef{ID: p.ID, RuleID: p.RuleID}}}
		forceNonExceptionP3With(items, keep)
		if got := chstore.NotifyViewPriority(p, keep); got != items[0].Priority {
			t.Errorf("%s: bildirim %s, ekran %s", p.RuleID, got, items[0].Priority)
		}
	}
	for _, sev := range []string{"critical", "warning"} {
		items := []InboxItem{{Kind: "incident", Priority: "P1", Severity: sev}}
		forceNonExceptionP3With(items, keep)
		p := chstore.Problem{RuleID: "incident:i", Kind: chstore.NotifyKindIncident, Severity: sev, Priority: "P1"}
		if got := chstore.NotifyViewPriority(p, keep); got != items[0].Priority {
			t.Errorf("incident %s: bildirim %s, ekran %s", sev, got, items[0].Priority)
		}
	}
}
