package notify

// lifecycle_test.go — v0.10.1532 (Problem standardı R4). Yaşam döngüsü kararı
// (AÇILDI / GÜNCELLEME / YENİDEN BAŞLADI / ÇÖZÜLDÜ), konu kimliği ve tek-konu
// e-posta başlıkları.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNextLifecycle(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	ns := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	open := func(prio, sev string) chstore.Problem {
		return chstore.Problem{ID: "p", RuleID: "r", Service: "s", Status: "open", Priority: prio, Severity: sev}
	}
	resolved := chstore.Problem{ID: "p", RuleID: "r", Service: "s", Status: "resolved", Priority: "P2", Severity: "warning"}
	openT := &chstore.NotifyThread{Signature: "k", Topic: "eski manşet", OpenedAt: ns(2 * time.Hour), State: chstore.NotifyThreadOpen, Priority: "P2", Severity: "warning"}
	resT := func(ago time.Duration) *chstore.NotifyThread {
		return &chstore.NotifyThread{Signature: "k", Topic: "eski manşet", OpenedAt: ns(48 * time.Hour), State: chstore.NotifyThreadResolved, ResolvedAt: ns(ago), Priority: "P2"}
	}
	cases := []struct {
		name      string
		prev      *chstore.NotifyThread
		p         chstore.Problem
		life      string
		persist   bool
		keepTopic bool // konu (manşet + açılış anı) korunur mu
	}{
		{"ilk açılış", nil, open("P2", "warning"), LifeOpened, true, false},
		{"açıkken tekrar, değişiklik yok", openT, open("P2", "warning"), LifeUpdate, false, true},
		{"açıkken öncelik yükseldi", openT, open("P1", "warning"), LifeUpdate, true, true},
		{"açıkken ciddiyet yükseldi", openT, open("P2", "critical"), LifeUpdate, true, true},
		{"açıkken öncelik düştü (salınım) yazılmaz", openT, open("P3", "info"), LifeUpdate, false, true},
		{"çözülme", openT, resolved, LifeResolved, true, true},
		{"ikinci çözülme yazılmaz", resT(time.Hour), resolved, LifeResolved, false, true},
		{"konusuz çözülme", nil, resolved, LifeResolved, true, false},
		{"24 sa içinde yeniden açılış → aynı konu", resT(3 * time.Hour), open("P2", "warning"), LifeReopened, true, true},
		{"24 sa sonra yeniden açılış → yeni konu", resT(30 * time.Hour), open("P2", "warning"), LifeOpened, true, false},
	}
	for _, c := range cases {
		life, next, persist := nextLifecycle(c.prev, "k", "yeni manşet", c.p, now)
		if life != c.life || persist != c.persist {
			t.Errorf("%s: %s persist=%v, beklenen %s persist=%v", c.name, life, persist, c.life, c.persist)
			continue
		}
		if c.keepTopic && (next.Topic != "eski manşet" || next.OpenedAt != c.prev.OpenedAt) {
			t.Errorf("%s: konu korunmalı: %+v", c.name, next)
		}
		if !c.keepTopic && (next.Topic != "yeni manşet" || next.OpenedAt != now.UnixNano()) {
			t.Errorf("%s: yeni konu açılmalı: %+v", c.name, next)
		}
	}
}

func TestThreadKey(t *testing.T) {
	if threadKey(chstore.Problem{ID: "x", RuleID: "rule-1", Service: "svc"}) != "p:rule-1|svc" {
		t.Error("kural problemi öğretme imzasını kullanmalı")
	}
	if threadKey(chstore.Problem{ID: "inc-1", RuleID: "incident:inc-1", Kind: chstore.NotifyKindIncident}) != "id:inc-1" {
		t.Error("imzasız kayıt kimlikle anahtarlanmalı")
	}
}

func TestThreadMessageIDs(t *testing.T) {
	th := chstore.NotifyThread{Signature: "p:r|svc\r\nBcc: x@evil", OpenedAt: 1700000000e9}
	root := threadMessageRoot(th)
	if strings.ContainsAny(root, "\r\n ") || !strings.HasPrefix(root, "<cm-thread-") || !strings.HasSuffix(root, "@coremetry>") {
		t.Fatalf("kök kimlik özetli ve başlık-güvenli olmalı: %q", root)
	}
	now := time.Unix(1700000500, 0)
	if threadMessageID(th, LifeOpened, now) != root {
		t.Error("AÇILDI mesajı kökün kendisi olmalı")
	}
	upd := threadMessageID(th, LifeUpdate, now)
	if upd == root || !strings.HasSuffix(upd, root[1:]) {
		t.Errorf("diğer mesajlar kökten türeyen tekil kimlik almalı: %q", upd)
	}
	other := chstore.NotifyThread{Signature: th.Signature, OpenedAt: 1700090000e9}
	if threadMessageRoot(other) == root {
		t.Error("aynı imzanın yeni konusu (başka açılış anı) ayrı kök almalı")
	}
}

func TestTRThreadHeadersAndSubject(t *testing.T) {
	th := chstore.NotifyThread{Signature: "p:r|svc", Topic: "svc: teknik hata arttı", OpenedAt: 1700000000e9}
	p := chstore.Problem{Priority: "P1", Severity: "critical"}
	mk := func(life string) *alertView { return &alertView{Life: life, Thread: th, Now: time.Unix(1700000600, 0)} }
	// Konu satırı yaşam döngüsü boyunca SABİT (Outlook konuşması).
	for _, life := range []string{LifeOpened, LifeUpdate, LifeReopened, LifeResolved} {
		if got := trEmailSubject(mk(life)); got != "Coremetry · svc: teknik hata arttı" {
			t.Errorf("%s: konu %q", life, got)
		}
	}
	open := strings.Join(trThreadHeaders(mk(LifeOpened), p), "\n")
	if strings.Contains(open, "In-Reply-To") || !strings.Contains(open, "Importance: high") {
		t.Errorf("AÇILDI: kök mesaj, P1 önemli: %s", open)
	}
	res := strings.Join(trThreadHeaders(mk(LifeResolved), p), "\n")
	root := threadMessageRoot(th)
	if !strings.Contains(res, "In-Reply-To: "+root) || !strings.Contains(res, "References: "+root) {
		t.Errorf("ÇÖZÜLDÜ köke bağlanmalı: %s", res)
	}
	if strings.Contains(res, "Importance") {
		t.Error("çözülme mesajı 'önemli' işaretlenmez")
	}
}

func TestComposeAltEmailHeadersSanitized(t *testing.T) {
	raw, err := composeAltEmailH("Coremetry <cm@x>", []string{"a@x"}, "Coremetry · s", "p", "<b>h</b>",
		[]string{"Message-ID: <a@b>", "Thread-Topic: x\r\nBcc: evil@x"})
	if err != nil {
		t.Fatal(err)
	}
	head := string(raw[:strings.Index(string(raw), "\r\n\r\n")])
	if !strings.Contains(head, "Message-ID: <a@b>\r\n") {
		t.Errorf("ek başlık yazılmalı: %s", head)
	}
	if strings.Contains(head, "\r\nBcc:") {
		t.Error("CR/LF ek başlıktan ayıklanmalı (CWE-93)")
	}
}

// Konu kaydı: aynı imzanın ikinci bildirimi önbellekten okunur, durum
// değişmedikçe yazılmaz; depo yokken de karar verilir.
func TestAdvanceThreadNoStore(t *testing.T) {
	n := &Notifier{}
	p := chstore.Problem{ID: "p", RuleID: "r", Service: "s", Status: "open", Priority: "P2"}
	now := time.Now()
	if life, _ := n.advanceThread(context.Background(), p, "m", now); life != LifeOpened {
		t.Fatalf("ilk: %s", life)
	}
	if life, _ := n.advanceThread(context.Background(), p, "m", now.Add(time.Minute)); life != LifeUpdate {
		t.Fatalf("ikinci: %s", life)
	}
	p.Status = "resolved"
	if life, _ := n.advanceThread(context.Background(), p, "m", now.Add(2*time.Minute)); life != LifeResolved {
		t.Fatalf("çözülme: %s", life)
	}
	p.Status = "open"
	if life, _ := n.advanceThread(context.Background(), p, "m", now.Add(3*time.Minute)); life != LifeReopened {
		t.Fatalf("yeniden: %s", life)
	}
}
