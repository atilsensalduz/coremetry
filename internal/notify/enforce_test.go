package notify

// enforce_test.go — v0.10.1533 (Problem standardı R5). enforce kapısı: shadow /
// off kipinde hiçbir şey değişmez; enforce'ta çözülme yalnız açılışı gitmiş
// konuya gider; gerçek gönderimden sonra tekrar penceresi (D3) tutar.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func enforceNotifier() *Notifier {
	n := &Notifier{}
	n.shadow.traffic = func(context.Context, chstore.Problem, time.Time) *chstore.StdTraffic { return nil }
	n.shadow.verdict = func(context.Context) ([]chstore.ProblemVerdict, error) { return nil, nil }
	n.shadow.sens = func() chstore.AnomalySensitivityConfig { return chstore.AnomalySensitivityConfig{} }
	return n
}

func withPolicyMode(t *testing.T, mode string) {
	t.Helper()
	pol := chstore.DefaultNotifyPolicy()
	pol.Mode = mode
	chstore.SetNotifyPolicy(pol)
	t.Cleanup(func() { chstore.SetNotifyPolicy(chstore.DefaultNotifyPolicy()) })
}

func excP1Problem(status string) chstore.Problem {
	g := chstore.ExceptionGroup{Fingerprint: "fp1", Service: "svc", Type: "java.lang.NullPointerException", Occurrences: 900,
		FirstSeen: time.Now().Add(-10 * time.Minute).UnixNano(), LastSeen: time.Now().UnixNano()}
	p := exceptionGroupProblem(g, chstore.ExStateNew, "P1", "patlama")
	p.Status = status
	return p
}

func TestEnforceGateModes(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{chstore.NotifyModeShadow, chstore.NotifyModeOff} {
		withPolicyMode(t, mode)
		n := enforceNotifier()
		for _, st := range []string{"open", "resolved"} {
			if ok, hold := n.enforceGate(ctx, excP1Problem(st)); !ok || hold != "" {
				t.Errorf("%s kipi %s: gönderim değişmemeli (%v %q)", mode, st, ok, hold)
			}
		}
	}
}

func TestEnforceGateResolveAndDedupe(t *testing.T) {
	withPolicyMode(t, chstore.NotifyModeEnforce)
	ctx := context.Background()
	n := enforceNotifier()
	// Konu kaydı "yok" (depo okundu, satır yok) — süreç-içi önbellekte nil.
	n.threads.m = map[string]*chstore.NotifyThread{threadKey(excP1Problem("open")): nil}
	if ok, hold := n.enforceGate(ctx, excP1Problem("resolved")); ok || hold != "resolve:not_opened" {
		t.Fatalf("açılışı gitmemiş konunun çözülmesi gitmemeli: %v %q", ok, hold)
	}
	if ok, hold := n.enforceGate(ctx, excP1Problem("open")); !ok {
		t.Fatalf("P1 exception açılışı gitmeli: %q", hold)
	}
	if ok, hold := n.enforceGate(ctx, excP1Problem("open")); ok || hold != chstore.NotifyHoldDedupe {
		t.Fatalf("aynı imzanın tekrarı tekrar penceresinde tutulmalı: %v %q", ok, hold)
	}
	if ok, _ := n.enforceGate(ctx, excP1Problem("resolved")); !ok {
		t.Fatal("açılışı gitmiş konunun çözülmesi gitmeli")
	}
	// Konu okunamazsa (depo yok / hata) çözülme GİDER — kayıp, fazla mailden kötü.
	if ok, _ := enforceNotifier().enforceGate(ctx, excP1Problem("resolved")); !ok {
		t.Fatal("bilinmeyen konuda çözülme gitmeli (güvenli yön)")
	}
	// İmzasız kayıt (olay) politika dışı.
	if ok, _ := n.enforceGate(ctx, chstore.Problem{ID: "inc", RuleID: "incident:inc", Kind: chstore.NotifyKindIncident, Status: "open"}); !ok {
		t.Fatal("olay bildirimi enforce'tan etkilenmemeli")
	}
}

// Kablolama: kapı konudan (prepareAlertView) ÖNCE ve gölgeden SONRA.
func TestEnforceGateWiring(t *testing.T) {
	src := mustRead(t, "notify.go")
	shadow := strings.Index(src, "n.shadowNotifyPolicy(ctx, p)")
	gate := strings.Index(src, "n.enforceGate(ctx, p)")
	view := strings.Index(src, "ctx = n.prepareAlertView(ctx, p)")
	if shadow < 0 || gate < 0 || view < 0 || !(shadow < gate && gate < view) {
		t.Errorf("sıra gölge → enforce → konu olmalı (%d %d %d)", shadow, gate, view)
	}
	if !strings.Contains(mustRead(t, "exception_notifier.go"), "e.n.enforceGate(ctx, ep)") {
		t.Error("P1 exception anonsu da enforce kapısından geçmeli")
	}
}

func TestDigestHelpers(t *testing.T) {
	if got := digestTitle("2026-10-25 09:00", 7); got != "Coremetry · P3 özeti — 25.10 09:00 (7 kalem)" {
		t.Errorf("başlık: %q", got)
	}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	it := chstore.DigestItem{Headline: "svc: trafik düştü", Impact: "-%62 istek", Confidence: chstore.ConfidenceMedium,
		StartedAt: time.Date(2026, 10, 25, 9, 48, 0, 0, time.UTC).UnixNano(), Count: 3}
	if got := digestLine(it, now, time.UTC); got != "• svc: trafik düştü · -%62 istek · güven ORTA · 09:48'den beri (×3)" {
		t.Errorf("satır: %q", got)
	}
	std := chstore.ProblemStandard{Headline: "h", Confidence: chstore.StdConfidence{Level: chstore.ConfidenceLow},
		Impact: chstore.StdImpact{Primary: chstore.StdMeasure{Text: "x"}}}
	d := digestItemOf(chstore.Problem{ID: "p", Service: "s", Severity: "info", StartedAt: 5}, std, "P3", "p:r|s", now)
	if d.Signature != "p:r|s" || d.Priority != "P3" || d.Headline != "h" || d.Confidence != "low" || d.Impact != "x" {
		t.Errorf("kalem: %+v", d)
	}
	if (errDigestUnsupported("webhook")).Error() != "özet desteklenmiyor: webhook" {
		t.Error("desteklenmeyen kanal metni")
	}
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
