package sre

// v0.10.1282 — AI SRE Faz 0a: sre rolü, kuyruk, drain, durum.

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── sahteler ────────────────────────────────────────────────────────────────

type fakeStore struct {
	mu   sync.Mutex
	rows map[string]chstore.SavedView
	open []chstore.Problem
	puts int
}

func newFakeStore() *fakeStore { return &fakeStore{rows: map[string]chstore.SavedView{}} }

func (f *fakeStore) GetSREInvestigation(_ context.Context, pid string) (*chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[chstore.SREInvestigationID(pid)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (f *fakeStore) PutSREInvestigation(_ context.Context, pid, state, body string, createdAt int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	f.rows[chstore.SREInvestigationID(pid)] = chstore.SavedView{ID: chstore.SREInvestigationID(pid),
		Page: chstore.SREInvestigationPage, Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}

func (f *fakeStore) ListSREInvestigations(_ context.Context, states []string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[string]bool{}
	for _, s := range states {
		want[s] = true
	}
	out := []chstore.SavedView{}
	for _, v := range f.rows {
		if want[v.Name] {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out, nil
}

func (f *fakeStore) CountSREQueued(_ context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.rows {
		if v.Name == StateQueued {
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) OpenProblemsSnapshot(_ context.Context) (*chstore.OpenProblems, error) {
	return chstore.NewOpenProblems(f.open), nil
}

func (f *fakeStore) get(t *testing.T, pid string) Investigation {
	t.Helper()
	v, _ := f.GetSREInvestigation(context.Background(), pid)
	if v == nil {
		t.Fatalf("%s satırı yok", pid)
	}
	inv, ok := Decode(*v)
	if !ok {
		t.Fatalf("%s çözülemedi: %s", pid, v.QueryString)
	}
	return inv
}

type fakeLock struct {
	mu   sync.Mutex
	held map[string]bool
}

func newFakeLock() *fakeLock { return &fakeLock{held: map[string]bool{}} }

func (l *fakeLock) TryAcquire(_ context.Context, key string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[key] {
		return false, nil
	}
	l.held[key] = true
	return true, nil
}
func (l *fakeLock) Release(_ context.Context, key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.held, key)
	return nil
}
func (l *fakeLock) Refresh(_ context.Context, key string, _ time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held[key], nil
}

type fakeClock struct {
	mu  sync.Mutex
	t   time.Time
	inc time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(c.inc)
	return c.t
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("zaman aşımı: %s", what)
}

// ── pickCandidates ──────────────────────────────────────────────────────────

func TestPickCandidates(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixNano() }
	mk := func(id, svc, prio, rule, kind, status string, age time.Duration) chstore.Problem {
		return chstore.Problem{ID: id, Service: svc, Priority: prio, RuleID: rule, Kind: kind, Status: status, StartedAt: ago(age)}
	}
	open := []chstore.Problem{
		mk("p2-new", "a", "P2", "r1", "", "open", 1*time.Minute),
		mk("p1-old", "b", "P1", "r1", "", "open", 10*time.Minute),
		mk("p1-new", "c", "P1", "r1", "service", "acknowledged", 2*time.Minute),
		mk("p3", "d", "P3", "r1", "", "open", time.Minute),
		mk("too-old", "e", "P1", "r1", "", "open", 16*time.Minute),
		mk("no-svc", "", "P1", "r1", "", "open", time.Minute),
		mk("db", "db:pg", "P1", "db-health:x", "db", "open", time.Minute),
		mk("storm", "f", "P1", "exception-storm", "", "open", time.Minute),
		mk("self", "g", "P1", "self-disk", "", "open", time.Minute),
		mk("incident", "h", "P1", "incident:1", "", "open", time.Minute),
		mk("resolved", "i", "P1", "r1", "", "resolved", time.Minute),
		mk("future", "j", "P1", "r1", "", "open", -5*time.Minute),
	}
	var got []string
	for _, p := range pickCandidates(open, now, NormalizeSettings(DefaultSettings())) {
		got = append(got, p.ID)
	}
	if want := "p1-new,p1-old,p2-new"; strings.Join(got, ",") != want {
		t.Fatalf("adaylar = %v, beklenen %s", got, want)
	}
	// [] = otomatik kuyruk kapalı; P3 listede → P3 de aday.
	empty := []string{}
	if c := pickCandidates(open, now, NormalizeSettings(Settings{AutoPriorities: &empty})); len(c) != 0 {
		t.Fatalf("boş öncelik listesi aday üretmemeli: %v", c)
	}
	p3 := []string{"p3"}
	c := pickCandidates(open, now, NormalizeSettings(Settings{AutoPriorities: &p3}))
	if len(c) != 1 || c[0].ID != "p3" {
		t.Fatalf("P3 listesi = %v", c)
	}
}

// ── reclaimable ─────────────────────────────────────────────────────────────

func TestReclaimable(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	run := func(claimedAgo time.Duration, attempts int) Investigation {
		return Investigation{ProblemID: "p", State: StateRunning, Attempts: attempts, ClaimedAt: now.Add(-claimedAgo).UnixNano()}
	}
	cases := []struct {
		name          string
		inv           Investigation
		lockHeld      bool
		requeue, fail bool
	}{
		{"taze sahiplenme dokunulmaz", run(time.Minute, 1), false, false, false},
		{"tam 2×kira sınırı dokunulmaz", run(2*itemLease, 1), false, false, false},
		{"bayat + kilit boş → requeue", run(2*itemLease+time.Second, 1), false, true, false},
		{"bayat + kilit tutuluyor → dokunma (canlı pod)", run(time.Hour, 1), true, false, false},
		{"bayat + deneme tavanı → fail", run(time.Hour, 2), false, false, true},
		{"queued satır dokunulmaz", Investigation{State: StateQueued, ClaimedAt: now.Add(-time.Hour).UnixNano()}, false, false, false},
		{"sahiplenme anı yok → dokunulmaz", Investigation{State: StateRunning}, false, false, false},
	}
	for _, c := range cases {
		rq, fl := reclaimable(c.inv, c.lockHeld, now, 2)
		if rq != c.requeue || fl != c.fail {
			t.Errorf("%s: (requeue=%v fail=%v), beklenen (%v %v)", c.name, rq, fl, c.requeue, c.fail)
		}
	}
}

// ── drain ───────────────────────────────────────────────────────────────────

func TestDrainIdleExit(t *testing.T) {
	SetSettings(DefaultSettings())
	clk := &fakeClock{t: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), inc: time.Second}
	done := StartDrain(context.Background(), DrainDeps{Store: newFakeStore(), Lock: newFakeLock(),
		PodID: "sre-1", Mode: "sre", IdleExit: 30 * time.Second, Now: clk.Now, Poll: time.Millisecond})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("boş kuyrukta IdleExit sonrası drain kapanmalı")
	}
	if _, ok := Local(); ok {
		t.Fatal("kapanışta süreç-içi durum temizlenmeli")
	}

	// IdleExit 0 = asla: yalnız ctx iptali kapatır.
	ctx, cancel := context.WithCancel(context.Background())
	done = StartDrain(ctx, DrainDeps{Store: newFakeStore(), Lock: newFakeLock(), PodID: "sre-2",
		IdleExit: 0, Now: clk.Now, Poll: time.Millisecond})
	select {
	case <-done:
		t.Fatal("IdleExit 0 iken drain kendiliğinden kapanmamalı")
	case <-time.After(100 * time.Millisecond):
	}
	if h, ok := Local(); !ok || h.Pod != "sre-2" {
		t.Fatalf("koşan drain süreç-içi durum yayınlamalı: %+v %v", h, ok)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ctx iptali drain'i kapatmalı")
	}
}

func TestDrainSIGTERMRequeues(t *testing.T) {
	SetSettings(DefaultSettings())
	st := newFakeStore()
	cfg := CurrentSettings()
	if _, err := Enqueue(context.Background(), st, EnqueueRequest{ProblemID: "p1", Service: "pay", Priority: "P1", ProblemStartedAt: time.Now().UnixNano()}, cfg); err != nil {
		t.Fatal(err)
	}
	lock := newFakeLock()
	started := make(chan struct{})
	runner := func(ctx context.Context, inv *Investigation, cp func(*Investigation)) {
		inv.setStep(Step{Name: "a", State: StepOK, At: time.Now().UnixNano()})
		cp(inv)
		close(started)
		<-ctx.Done() // uzun adım: kapanış süresini aşar
		inv.setStep(Step{Name: "b", State: StepError, Summary: "iptal"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := StartDrain(ctx, DrainDeps{Store: st, Lock: lock, PodID: "sre-1", Poll: time.Millisecond,
		Runner: runner, ShutdownGrace: 20 * time.Millisecond})
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("iş alınmadı")
	}
	if got := st.get(t, "p1"); got.State != StateRunning || got.ClaimedBy != "sre-1" || got.Attempts != 1 {
		t.Fatalf("sahiplenme: %+v", got)
	}
	cancel() // SIGTERM
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain kapanmadı")
	}
	got := st.get(t, "p1")
	if got.State != StateQueued || got.ClaimedBy != "" || got.Attempts != 0 {
		t.Fatalf("kapanışta iş queued + deneme sayılmadan geri yazılmalı: state=%s by=%q attempts=%d", got.State, got.ClaimedBy, got.Attempts)
	}
	if got.step("a") == nil || got.step("a").State != StepOK {
		t.Fatalf("checkpoint'teki tamamlanan adım korunmalı: %+v", got.Steps)
	}
	if lock.held[itemLockKey("p1")] {
		t.Fatal("kapanışta iş kilidi bırakılmalı")
	}
}

func TestDrainRunsAndFinalizes(t *testing.T) {
	SetSettings(DefaultSettings())
	st := newFakeStore()
	cfg := CurrentSettings()
	for _, id := range []string{"p1", "p2"} {
		if _, err := Enqueue(context.Background(), st, EnqueueRequest{ProblemID: id, Service: "svc-" + id}, cfg); err != nil {
			t.Fatal(err)
		}
	}
	var events []string
	var mu sync.Mutex
	runner := func(_ context.Context, inv *Investigation, cp func(*Investigation)) {
		state := StepOK
		if inv.ProblemID == "p2" {
			state = StepPartial
		}
		inv.setStep(Step{Name: "sweep", State: state})
		cp(inv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := StartDrain(ctx, DrainDeps{Store: st, Lock: newFakeLock(), PodID: "all-1", Mode: "all", Poll: time.Millisecond, Runner: runner,
		Publish: func(kind string, p any) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, kind)
		}})
	waitFor(t, "iki iş bitsin", func() bool {
		a, b := st.get(t, "p1"), st.get(t, "p2")
		return a.State == StateDone && b.State == StatePartial
	})
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(events) < 4 || events[0] != EventKind {
		t.Fatalf("SSE olayları (running + sonuç ×2): %v", events)
	}
}

func TestPickWorkOrder(t *testing.T) {
	invs := []Investigation{
		{ProblemID: "old-p2", State: StateQueued, Priority: "P2", EnqueuedAt: 1},
		{ProblemID: "new-p1", State: StateQueued, Priority: "P1", EnqueuedAt: 5},
		{ProblemID: "manual", State: StateQueued, Kind: KindManual, Priority: "P3", EnqueuedAt: 9},
		{ProblemID: "mine", State: StateQueued, Priority: "P1", EnqueuedAt: 0},
		{ProblemID: "run", State: StateRunning, Priority: "P1"},
	}
	var got []string
	for _, i := range pickWork(invs, map[string]bool{"mine": true}, 3) {
		got = append(got, i.ProblemID)
	}
	if strings.Join(got, ",") != "manual,new-p1,old-p2" {
		t.Fatalf("sıra = %v", got)
	}
	if n := len(pickWork(invs, nil, 1)); n != 1 {
		t.Fatalf("free=1 → 1 iş, %d", n)
	}
	if pickWork(invs, nil, 0) != nil {
		t.Fatal("free=0 → iş yok")
	}
}

func TestFinalState(t *testing.T) {
	for _, c := range []struct {
		steps []string
		want  string
	}{
		{nil, StateError},
		{[]string{StepOK}, StateDone},
		{[]string{StepOK, StepSkipped}, StateDone},
		{[]string{StepOK, StepError}, StatePartial},
		{[]string{StepPartial}, StatePartial},
		{[]string{StepError}, StateError},
	} {
		var ss []Step
		for _, s := range c.steps {
			ss = append(ss, Step{State: s})
		}
		if got := finalState(ss); got != c.want {
			t.Errorf("finalState(%v) = %s, beklenen %s", c.steps, got, c.want)
		}
	}
}

// ── enqueue ─────────────────────────────────────────────────────────────────

func TestEnqueueSemantics(t *testing.T) {
	ctx := context.Background()
	cfg := NormalizeSettings(DefaultSettings())
	st := newFakeStore()
	req := EnqueueRequest{ProblemID: "p1", Service: "pay", Kind: KindAuto}
	if r, err := Enqueue(ctx, st, req, cfg); err != nil || !r.Created || r.State != StateQueued {
		t.Fatalf("ilk enqueue: %+v %v", r, err)
	}
	if r, _ := Enqueue(ctx, st, req, cfg); r.Created {
		t.Fatal("queued kayıt çift kuyruğa girmemeli")
	}
	// bitmiş kayıt: otomatik yeniden koşmaz, elle koşar (Run++).
	inv := st.get(t, "p1")
	inv.State = StateDone
	inv.Steps = []Step{{Name: "sweep", State: StepOK}}
	if err := save(ctx, st, inv); err != nil {
		t.Fatal(err)
	}
	if r, _ := Enqueue(ctx, st, req, cfg); r.Created {
		t.Fatal("otomatik istek bitmiş kaydı yeniden koşmamalı")
	}
	req.Kind = KindManual
	if r, err := Enqueue(ctx, st, req, cfg); err != nil || !r.Created {
		t.Fatalf("elle yeniden araştırma: %+v %v", r, err)
	}
	if inv := st.get(t, "p1"); inv.Run != 2 || len(inv.Steps) != 0 || inv.Kind != KindManual {
		t.Fatalf("yeniden koşu taze olmalı: %+v", inv)
	}
	// kapalı / eksik / dolu
	off := false
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "x", Service: "s"}, NormalizeSettings(Settings{Enabled: &off})); err != ErrDisabled {
		t.Fatalf("kapalı → ErrDisabled, %v", err)
	}
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "x"}, cfg); err != ErrInvalid {
		t.Fatalf("servissiz → ErrInvalid, %v", err)
	}
	small := NormalizeSettings(Settings{MaxQueue: 10})
	for i := 0; i < 9; i++ {
		_, _ = Enqueue(ctx, st, EnqueueRequest{ProblemID: "q" + string(rune('a'+i)), Service: "s"}, small)
	}
	if _, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "overflow", Service: "s"}, small); err != ErrQueueFull {
		t.Fatalf("dolu kuyruk → ErrQueueFull, %v", err)
	}
	// shared: kuyruğa girmez, tavanı da yemez
	if r, err := Enqueue(ctx, st, EnqueueRequest{ProblemID: "sh", Service: "s", SharedFrom: "qa"}, small); err != nil || r.State != StateShared {
		t.Fatalf("shared: %+v %v", r, err)
	}
}

func TestEnqueuerTickCapsAndShares(t *testing.T) {
	SetSettings(Settings{MaxPerTick: 2})
	now := time.Now()
	st := newFakeStore()
	st.open = []chstore.Problem{
		{ID: "a1", Service: "a", Status: "open", Severity: "critical", RuleID: "r", StartedAt: now.Add(-time.Minute).UnixNano(), Priority: "P1"},
		{ID: "a2", Service: "a", Status: "open", Severity: "critical", RuleID: "r2", StartedAt: now.Add(-2 * time.Minute).UnixNano()},
		{ID: "b1", Service: "b", Status: "open", Severity: "critical", RuleID: "r", StartedAt: now.Add(-3 * time.Minute).UnixNano()},
		{ID: "c1", Service: "c", Status: "open", Severity: "critical", RuleID: "r", StartedAt: now.Add(-4 * time.Minute).UnixNano()},
	}
	e := &enqueuer{store: st, now: func() time.Time { return now }, seen: map[string]time.Time{}, cooldown: map[string]cooldownEntry{}}
	// öncelik okuma anında hesaplanır; hepsinin P1/P2 olduğundan emin ol.
	open := chstore.EnrichProblemsWithPriority(append([]chstore.Problem(nil), st.open...))
	for _, p := range open {
		if p.Priority != "P1" && p.Priority != "P2" {
			t.Skipf("öncelik merdiveni %s için %s verdi — test girdisi geçersiz", p.ID, p.Priority)
		}
	}
	e.tick(context.Background())
	queued, _ := st.CountSREQueued(context.Background())
	if queued != 2 {
		t.Fatalf("tik başına en çok 2 queued, %d", queued)
	}
	if inv := st.get(t, "a2"); inv.State != StateShared || inv.SharedFrom != "a1" {
		t.Fatalf("aynı servisin ikinci problemi shared olmalı: %+v", inv)
	}
	e.tick(context.Background())
	queued, _ = st.CountSREQueued(context.Background())
	if queued != 3 {
		t.Fatalf("ikinci tik kalan adayı almalı, queued=%d", queued)
	}
	if st.get(t, "a1").Kind != KindAuto {
		t.Fatal("üretici Kind=auto yazar")
	}
	SetSettings(DefaultSettings())
}

// ── kodlayıcı ───────────────────────────────────────────────────────────────

func TestInvestigationCap64KB(t *testing.T) {
	big := strings.Repeat("x", 11<<10)
	inv := Investigation{ProblemID: "p", Service: "s", State: StateDone}
	for i := 0; i < 8; i++ {
		inv.Steps = append(inv.Steps, Step{Name: "s" + string(rune('a'+i)), State: StepOK,
			Summary: strings.Repeat("ö", 3000), Payload: json.RawMessage(`"` + big + `"`)})
	}
	for i := 0; i < 20; i++ {
		inv.Findings = append(inv.Findings, Finding{Kind: "pod", Name: strings.Repeat("n", 500), Reasons: []string{big}})
	}
	for i := 0; i < 50; i++ {
		inv.Warnings = append(inv.Warnings, big)
	}
	body, err := Encode(inv)
	if err != nil {
		t.Fatalf("küçültülebilir kayıt kodlanmalı: %v", err)
	}
	if len(body) > chstore.SREInvestigationMaxBytes {
		t.Fatalf("gövde %d B > 64 KB", len(body))
	}
	var back Investigation
	if err := json.Unmarshal([]byte(body), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Findings) == 0 || len(back.Findings) > maxFindings || len(back.Steps) != 8 {
		t.Fatalf("küçültme adımları korur, bulguları tavanlar: steps=%d findings=%d", len(back.Steps), len(back.Findings))
	}
	// Tek adım yükü > 12 KB her zaman düşer.
	one := Investigation{ProblemID: "p", Steps: []Step{{Name: "a", Payload: json.RawMessage(`"` + strings.Repeat("y", 13<<10) + `"`)}}}
	body, _ = Encode(one)
	if strings.Contains(body, "yyyy") {
		t.Fatal("12 KB'yi aşan adım yükü düşmeli")
	}
	// Küçültülemeyen kayıt hata verir (yarım yazım yok).
	huge := Investigation{ProblemID: strings.Repeat("p", 70<<10)}
	if _, err := Encode(huge); err == nil {
		t.Fatal("küçültülemeyen kayıt hata vermeli")
	}
}

func TestDecodeRejectsForgedRow(t *testing.T) {
	body, _ := Encode(Investigation{ProblemID: "p1", Service: "s"})
	ok := chstore.SavedView{ID: "sre-p1", Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: body}
	if _, good := Decode(ok); !good {
		t.Fatal("geçerli satır çözülmeli")
	}
	for name, v := range map[string]chstore.SavedView{
		"kimlik uyuşmaz": {ID: "sre-other", Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: body},
		"kişisel sahip":  {ID: "sre-p1", OwnerID: "u1", Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: body},
		"başka sayfa":    {ID: "sre-p1", Page: "traces", Name: StateQueued, QueryString: body},
		"bozuk gövde":    {ID: "sre-p1", Page: chstore.SREInvestigationPage, Name: StateQueued, QueryString: "{"},
	} {
		if _, good := Decode(v); good {
			t.Errorf("%s: sahte satır kayıt sayılmamalı", name)
		}
	}
}

// ── ayar / durum ────────────────────────────────────────────────────────────

func TestNormalizeSettings(t *testing.T) {
	d := NormalizeSettings(Settings{})
	if !d.IsEnabled() || strings.Join(d.Priorities(), ",") != "P1,P2" || d.MaxPerTick != 2 || d.MaxQueue != 200 || d.ItemBudgetSec != 90 {
		t.Fatalf("varsayılanlar: %+v", d)
	}
	weird := []string{" p1", "P1", "P9", "p3"}
	n := NormalizeSettings(Settings{AutoPriorities: &weird, MaxConcurrent: 99, ItemBudgetSec: 1})
	if strings.Join(n.Priorities(), ",") != "P1,P3" || n.MaxConcurrent != 8 || n.ItemBudgetSec != 30 {
		t.Fatalf("normalize: %+v %v", n, n.Priorities())
	}
	empty := []string{}
	raw, _ := json.Marshal(NormalizeSettings(Settings{AutoPriorities: &empty}))
	back, ok := parseSettings(raw)
	if !ok || back.AutoPriorities == nil || len(*back.AutoPriorities) != 0 {
		t.Fatalf("[] gidiş-dönüşte [] kalmalı (nil → otomatik kuyruk sessizce açılırdı): %s", raw)
	}
	if _, ok := parseSettings([]byte("{")); ok {
		t.Fatal("bozuk blob ok=false")
	}
}

func TestParseHeartbeatsAndLauncher(t *testing.T) {
	now := time.Now()
	fresh, _ := json.Marshal(Heartbeat{Pod: "sre-b", Mode: "sre", At: now.UnixNano()})
	fresh2, _ := json.Marshal(Heartbeat{Pod: "sre-a", Mode: "sre", At: now.Add(-30 * time.Second).UnixNano()})
	stale, _ := json.Marshal(Heartbeat{Pod: "sre-c", Mode: "sre", At: now.Add(-2 * time.Minute).UnixNano()})
	pods := ParseHeartbeats([][]byte{fresh, nil, []byte("{"), stale, fresh2}, now)
	if len(pods) != 2 || pods[0].Pod != "sre-a" || pods[1].Pod != "sre-b" {
		t.Fatalf("taze kalp atışları: %+v", pods)
	}
	for _, c := range []struct {
		local bool
		env   string
		pods  []Heartbeat
		want  string
	}{
		{true, "none", nil, LauncherInProcess},
		{false, "deployment", nil, LauncherDeployment},
		{false, " None ", pods, LauncherNone},
		{false, "", pods, LauncherDeployment},
		{false, "", nil, LauncherNone},
		{false, "garbage", []Heartbeat{{Pod: "x", Mode: "all"}}, LauncherNone},
	} {
		if got := ResolveLauncher(c.local, c.env, c.pods); got != c.want {
			t.Errorf("ResolveLauncher(%v,%q,%d) = %s, beklenen %s", c.local, c.env, len(c.pods), got, c.want)
		}
	}
}

func TestAnchorFor(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	start := now.Add(-10 * time.Minute)
	a := anchorFor(start.UnixNano(), now)
	if time.Unix(0, a.From).UTC() != start.Add(-15*time.Minute) || time.Unix(0, a.To).UTC() != now {
		t.Fatalf("taze problem: [start−15m, now]: %v", a)
	}
	old := now.Add(-5 * time.Hour)
	a = anchorFor(old.UnixNano(), now)
	if time.Unix(0, a.To).UTC() != old.Add(30*time.Minute) {
		t.Fatalf("eski problem: bitiş start+30m: %v", a)
	}
	if a := anchorFor(0, now); a.To <= a.From {
		t.Fatalf("başlangıçsız pencere geçerli olmalı: %v", a)
	}
}

// ── korkuluk ────────────────────────────────────────────────────────────────

// TestEnqueueNeverUpsertsProblems — paket problems / hipotez / öncelik
// yazıcılarını ÇAĞIRMAZ: tek yazımı kendi kaydı (PutSREInvestigation).
func TestEnqueueNeverUpsertsProblems(t *testing.T) {
	forbidden := regexp.MustCompile(`^(Upsert|Insert|Resolve|Ack|Acknowledge|Mark|Update|Set|Delete|Append|Save)(Problem|Problems|Hypothesis|RootCause|Incident|Priority)`)
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && forbidden.MatchString(sel.Sel.Name) {
				t.Errorf("%s: %s çağrısı — AI SRE problem/hipotez/öncelik yazamaz", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
	src, _ := os.ReadFile("queue.go")
	if strings.Contains(string(src), "UpsertProblem") && !strings.Contains(string(src), "UpsertProblem yok") {
		t.Error("queue.go UpsertProblem'e atıf yapmamalı")
	}
}
