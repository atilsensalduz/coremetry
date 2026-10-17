package sre

// drain.go — v0.10.1282 — kuyruk tüketicisi. COREMETRY_MODE=sre pod'unda
// (chart: always-on `coremetry-sre` Deployment, IdleExit 0) ya da all modunda
// süreç içinde koşar; worker/api/ingest rollerinde ASLA (main.go `if mode.sre`,
// TestSREStartsOnlyUnderSREMode).
//
// Sözleşme:
//   - 5 s yoklama; iş başına Redis kilidi "coremetry:lock:sre-item:<id>"
//     (3 dk kira, 30 s yenileme) → çok replika HA-güvenli: bir işi tek pod alır.
//   - Pod başına MaxConcurrent eşzamanlı iş, iş başına ItemBudgetSec bütçe.
//   - Her adımdan sonra kayıt yazılır (checkpoint); resume 10 dk'dan taze ok
//     adımı atlar.
//   - Çökme: kilidi boş + ClaimedAt > 2×kira olan running satır yeniden
//     kuyruğa; deneme > MaxAttempts → error "N denemede tamamlanamadı".
//   - SIGTERM: yeni iş alınmaz, uçuştakilere 20 s; bitmeyen iş queued olarak
//     geri yazılır (deneme sayılmaz) ve kilit bırakılır.
//   - IdleExit > 0: kuyruk boş ve uçuşta iş yokken bu süre geçince drain
//     kapanır (gelecekteki Job başlatıcı için korunan yol; Deployment 0 =
//     asla). all modunda her zaman 0.
//   - Kalp atışı "coremetry:sre:hb:<pod>" 10 s'de bir (TTL 60 s) + Local().

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	itemLockPrefix    = "coremetry:lock:sre-item:"
	itemLease         = 3 * time.Minute
	itemLeaseRefresh  = 30 * time.Second
	defaultPoll       = 5 * time.Second
	heartbeatEvery    = 10 * time.Second
	defaultGrace      = 20 * time.Second
	stepFreshForReuse = 10 * time.Minute
)

func itemLockKey(problemID string) string { return itemLockPrefix + problemID }

// Runner — bir araştırmayı yürütür; adım bittikçe checkpoint çağırır.
type Runner func(ctx context.Context, inv *Investigation, checkpoint func(*Investigation))

// DrainDeps — drain'in bağımlılıkları.
type DrainDeps struct {
	Deps     mcptools.Deps // okuma katmanı (srv.MCPDeps())
	Store    QueueStore
	Cache    cache.Cache
	Lock     cache.Lock
	PodID    string
	Version  string
	Mode     string        // "sre" | "all"
	IdleExit time.Duration // 0 = asla çıkma
	// Publish — SSE (bus.Publish; köprü api pod'larına taşır). nil olabilir.
	Publish func(kind string, payload any)
	// Copilot — v0.10.1287 (Faz 1b): araştırmanın TEK anlatım çağrısı
	// (*copilot.Service; arka plan AI kapıları narrate.go). nil = anlatım yok
	// ("AI özeti yok (otomatik AI kapalı)").
	Copilot Narrator
	// Eval — v0.10.1301 (Faz 5): değerlendirme koşusu satırı (sre-eval). nil =
	// tekrar işleri koşar ama özetleri koşu satırına birleştirilmez (okuma ucu
	// tekrar satırlarından uzlaştırır).
	Eval EvalRunStore

	// Test dikişleri.
	Now           func() time.Time
	Poll          time.Duration
	Runner        Runner
	ShutdownGrace time.Duration
}

type flight struct {
	cancel   context.CancelFunc
	shutdown atomic.Bool
}

type drainer struct {
	d         DrainDeps
	mu        sync.Mutex
	inflight  map[string]*flight
	wg        sync.WaitGroup
	drained   atomic.Int64
	startedAt time.Time
	lastErrAt time.Time
}

// StartDrain — drain'i başlatır; dönen kanal drain bittiğinde (ctx iptali +
// düzenli kapanış ya da idle exit) kapanır.
func StartDrain(ctx context.Context, d DrainDeps) <-chan struct{} {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Poll <= 0 {
		d.Poll = defaultPoll
	}
	if d.ShutdownGrace <= 0 {
		d.ShutdownGrace = defaultGrace
	}
	if d.Runner == nil {
		// v0.10.1287 — Faz 1b orkestratörü (plan → iki dalga → tek anlatım).
		rd := readersFrom(d.Deps, d.Copilot, d.Cache, d.Now)
		now := d.Now
		d.Runner = func(rctx context.Context, inv *Investigation, cp func(*Investigation)) {
			runInvestigation(rctx, rd, inv, cp, now)
		}
	}
	if d.Mode == "" {
		d.Mode = "sre"
	}
	dr := &drainer{d: d, inflight: map[string]*flight{}, startedAt: d.Now()}
	done := make(chan struct{})
	log.Printf("[sre] drain started (pod=%s idleExit=%s maxConc=%d launcher=%s)",
		d.PodID, d.IdleExit, CurrentSettings().MaxConcurrent, launcherForMode(d.Mode))
	go func() {
		defer close(done)
		dr.run(ctx)
	}()
	return done
}

func launcherForMode(mode string) string {
	if mode == "all" {
		return LauncherInProcess
	}
	return LauncherDeployment
}

func (dr *drainer) inFlightCount() int {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	return len(dr.inflight)
}

func (dr *drainer) inFlightIDs() map[string]bool {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	out := make(map[string]bool, len(dr.inflight))
	for id := range dr.inflight {
		out[id] = true
	}
	return out
}

func (dr *drainer) run(ctx context.Context) {
	poll := time.NewTicker(dr.d.Poll)
	defer poll.Stop()
	hb := time.NewTicker(heartbeatEvery)
	defer hb.Stop()
	dr.heartbeat(ctx)
	lastBusy := dr.d.Now()
	for {
		select {
		case <-ctx.Done():
			dr.shutdown()
			return
		case <-hb.C:
			dr.heartbeat(ctx)
		case <-poll.C:
			busy := dr.tick(ctx)
			now := dr.d.Now()
			if busy || dr.inFlightCount() > 0 {
				lastBusy = now
				continue
			}
			if dr.d.IdleExit > 0 && now.Sub(lastBusy) >= dr.d.IdleExit {
				log.Printf("[sre] idle exit: kuyruk %s boş, drain kapanıyor", dr.d.IdleExit)
				dr.shutdown()
				return
			}
		}
	}
}

// heartbeat — Redis kalp atışı + süreç-içi durum.
func (dr *drainer) heartbeat(ctx context.Context) {
	h := Heartbeat{Pod: dr.d.PodID, Version: dr.d.Version, Mode: dr.d.Mode,
		StartedAt: dr.startedAt.UnixNano(), At: dr.d.Now().UnixNano(),
		InFlight: dr.inFlightCount(), Drained: dr.drained.Load(), IdleExitSec: int(dr.d.IdleExit / time.Second)}
	local.Store(&h)
	if dr.d.Cache == nil {
		return
	}
	raw, err := jsonMarshal(h)
	if err != nil {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := dr.d.Cache.Set(cctx, HeartbeatPrefix+dr.d.PodID, raw, HeartbeatTTL); err != nil {
		dr.logErr("heartbeat", err)
	}
}

func (dr *drainer) logErr(what string, err error) {
	now := dr.d.Now()
	dr.mu.Lock()
	quiet := now.Sub(dr.lastErrAt) < time.Minute
	if !quiet {
		dr.lastErrAt = now
	}
	dr.mu.Unlock()
	if !quiet {
		log.Printf("[sre] %s: %v", what, err)
	}
}

// tick — bir yoklama. busy: kuyrukta iş var (ya da durum okunamadı — okuma
// hatasında idle exit tetiklenmesin).
func (dr *drainer) tick(ctx context.Context) bool {
	cfg := CurrentSettings()
	rows, err := dr.d.Store.ListSREInvestigations(ctx, []string{StateQueued, StateRunning}, 0)
	if err != nil {
		dr.logErr("kuyruk okunamadı", err)
		return true
	}
	now := dr.d.Now()
	mine := dr.inFlightIDs()
	invs := make([]Investigation, 0, len(rows))
	for _, r := range rows {
		if inv, ok := Decode(r); ok {
			invs = append(invs, inv)
		}
	}
	busy := false
	for _, inv := range invs {
		if inv.State == StateQueued {
			busy = true
		}
		if inv.State == StateRunning && !mine[inv.QueueKey()] && staleClaim(inv, now) {
			dr.reclaim(ctx, inv, cfg)
		}
	}
	if !cfg.IsEnabled() || ctx.Err() != nil {
		return busy
	}
	free := cfg.MaxConcurrent - len(mine)
	for _, inv := range pickWork(invs, mine, free) {
		dr.claim(ctx, inv, cfg)
	}
	return busy
}

// staleClaim — SAF: running satırın sahiplenmesi 2×kiradan eski mi.
func staleClaim(inv Investigation, now time.Time) bool {
	return inv.ClaimedAt > 0 && now.Sub(time.Unix(0, inv.ClaimedAt)) > 2*itemLease
}

// reclaimable — SAF: kilidi boşalmış running satır ne olur. lockHeld = başka
// bir pod kilidi tutuyor (canlı) → dokunma. Taze sahiplenme → dokunma.
// Deneme tavanı dolmuşsa fail, değilse requeue.
func reclaimable(inv Investigation, lockHeld bool, now time.Time, maxAttempts int) (requeue, fail bool) {
	if inv.State != StateRunning || lockHeld || !staleClaim(inv, now) {
		return false, false
	}
	if inv.Attempts >= maxAttempts {
		return false, true
	}
	return true, false
}

func (dr *drainer) reclaim(ctx context.Context, inv Investigation, cfg Settings) {
	key := itemLockKey(inv.QueueKey())
	ok, err := dr.d.Lock.TryAcquire(ctx, key, itemLease)
	if err != nil {
		dr.logErr("reclaim kilidi", err)
		return
	}
	defer func() { _ = dr.d.Lock.Release(context.WithoutCancel(ctx), key) }()
	requeue, fail := reclaimable(inv, !ok, dr.d.Now(), cfg.MaxAttempts)
	prevPod := inv.ClaimedBy
	switch {
	case fail:
		inv.State = StateError
		inv.Error = fmt.Sprintf("%d denemede tamamlanamadı", inv.Attempts)
		inv.FinishedAt = dr.d.Now().UnixNano()
	case requeue:
		inv.State = StateQueued
		inv.ClaimedBy = ""
	default:
		return
	}
	if err := save(context.WithoutCancel(ctx), dr.d.Store, inv); err != nil {
		dr.logErr("reclaim yazımı", err)
		return
	}
	log.Printf("[sre] %s sahipsiz kaldı (pod=%s) → %s", inv.QueueKey(), prevPod, inv.State)
	dr.publish(inv)
	if fail {
		dr.recordEval(inv) // v0.10.1301 — tavanı dolan tekrar işi koşu satırında "error"
	}
}

// pickWork — SAF: alınacak queued işler. Uçuştakiler hariç; sıra OrderQueued;
// en çok free.
func pickWork(invs []Investigation, inflight map[string]bool, free int) []Investigation {
	if free <= 0 {
		return nil
	}
	var q []Investigation
	for _, inv := range invs {
		if !inflight[inv.QueueKey()] {
			q = append(q, inv)
		}
	}
	q = OrderQueued(q)
	if len(q) > free {
		q = q[:free]
	}
	return q
}

// OrderQueued — SAF: yalnız queued kayıtlar, drain'in alış sırasıyla: elle
// istek (manual / verify) → P1 → P2 → diğer → değerlendirme tekrarı (replay,
// v0.10.1301 — canlı işlerin HEPSİNDEN sonra); eşitlikte en eski kuyruk
// girişi önce. Durum ucu "N. sırada" bilgisini aynı sıradan okur.
func OrderQueued(invs []Investigation) []Investigation {
	var q []Investigation
	for _, inv := range invs {
		if inv.State == StateQueued {
			q = append(q, inv)
		}
	}
	rank := func(inv Investigation) int {
		if inv.Kind == KindManual || inv.Kind == KindVerify { // v0.10.1288 — doğrulama da operatör isteği
			return 0
		}
		if inv.Kind == KindReplay { // v0.10.1301 — geçmiş vaka tekrarı en düşük öncelik
			return 4
		}
		switch inv.Priority {
		case "P1":
			return 1
		case "P2":
			return 2
		}
		return 3
	}
	sort.SliceStable(q, func(i, j int) bool {
		ri, rj := rank(q[i]), rank(q[j])
		if ri != rj {
			return ri < rj
		}
		return q[i].EnqueuedAt < q[j].EnqueuedAt
	})
	return q
}

func (dr *drainer) publish(inv Investigation) {
	if dr.d.Publish != nil {
		payload := map[string]any{"problemId": inv.ProblemID, "state": inv.State}
		if inv.Kind == KindReplay && inv.EvalRun != "" {
			payload["evalRun"] = inv.EvalRun // v0.10.1301 — okuma ucu koşu önbelleğini düşürür
		}
		dr.d.Publish(EventKind, payload)
	}
}

// claim — işi kilitle, durumu running yaz, yürütücüyü başlat.
func (dr *drainer) claim(ctx context.Context, inv Investigation, cfg Settings) {
	qk := inv.QueueKey()
	key := itemLockKey(qk)
	ok, err := dr.d.Lock.TryAcquire(ctx, key, itemLease)
	if err != nil || !ok {
		if err != nil {
			dr.logErr("iş kilidi", err)
		}
		return
	}
	release := func() { _ = dr.d.Lock.Release(context.WithoutCancel(ctx), key) }
	// Kilit altında yeniden oku: liste ile kilit arasında başka bir pod işi
	// bitirmiş olabilir.
	cur, found, err := Load(ctx, dr.d.Store, qk)
	if err != nil || !found || cur.State != StateQueued {
		release()
		return
	}
	now := dr.d.Now()
	cur.State = StateRunning
	cur.ClaimedBy = dr.d.PodID
	cur.ClaimedAt = now.UnixNano()
	cur.StartedAt = now.UnixNano()
	cur.Attempts++
	if err := save(ctx, dr.d.Store, cur); err != nil {
		dr.logErr("sahiplenme yazımı", err)
		release()
		return
	}
	dr.publish(cur)
	// Uçuştaki iş SIGTERM'den doğrudan ÖLMEZ: kapanış ona süre tanır, süre
	// dolunca kendisi iptal eder (flight.shutdown).
	fctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	fl := &flight{cancel: cancel}
	dr.mu.Lock()
	dr.inflight[qk] = fl
	dr.mu.Unlock()
	dr.wg.Add(1)
	go dr.execute(fctx, fl, cur, cfg, key)
}

func (dr *drainer) execute(fctx context.Context, fl *flight, inv Investigation, cfg Settings, key string) {
	defer dr.wg.Done()
	defer fl.cancel()
	defer func() {
		dr.mu.Lock()
		delete(dr.inflight, inv.QueueKey())
		dr.mu.Unlock()
	}()
	wctx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 5*time.Second)
	}
	defer func() {
		c, cancel := wctx()
		defer cancel()
		_ = dr.d.Lock.Release(c, key)
	}()
	// Kira yenileyici.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(itemLeaseRefresh)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if ok, err := dr.d.Lock.Refresh(fctx, key, itemLease); err == nil && !ok {
					log.Printf("[sre] %s: iş kilidi kaybedildi", inv.QueueKey())
				}
			}
		}
	}()
	bctx, bcancel := context.WithTimeout(fctx, time.Duration(cfg.ItemBudgetSec)*time.Second)
	checkpoint := func(cur *Investigation) {
		c, cancel := wctx()
		defer cancel()
		if err := save(c, dr.d.Store, *cur); err != nil {
			dr.logErr("checkpoint", err)
		}
	}
	work := inv
	dr.d.Runner(bctx, &work, checkpoint)
	bcancel()
	c, cancel := wctx()
	defer cancel()
	if fl.shutdown.Load() {
		// Kapanış süresi doldu: işi deneme saymadan kuyruğa geri ver; tamamlanan
		// adımlar checkpoint'te, sonraki pod onları atlar.
		work.State = StateQueued
		work.ClaimedBy = ""
		if work.Attempts > 0 {
			work.Attempts--
		}
		if err := save(c, dr.d.Store, work); err != nil {
			dr.logErr("kapanış requeue", err)
		}
		dr.publish(work)
		return
	}
	work.State = finalState(work.Steps)
	work.FinishedAt = dr.d.Now().UnixNano()
	if work.State == StateError && work.Error == "" {
		work.Error = "hiçbir adım tamamlanamadı"
	}
	if err := save(c, dr.d.Store, work); err != nil {
		dr.logErr("sonuç yazımı", err)
		return
	}
	dr.drained.Add(1)
	dr.recordEval(work) // v0.10.1301 — tekrar işinin özeti koşu satırına (kilitli birleştirme)
	dr.publish(work)
}

// finalState — SAF: adımlardan koşu durumu. Hepsi ok → done; en az biri ok
// ya da partial → partial; hiçbiri → error. Adım yoksa error.
func finalState(steps []Step) string {
	if len(steps) == 0 {
		return StateError
	}
	ok, some := 0, 0
	for _, s := range steps {
		switch s.State {
		case StepOK, StepSkipped:
			ok++
			some++
		case StepPartial:
			some++
		}
	}
	switch {
	case ok == len(steps):
		return StateDone
	case some > 0:
		return StatePartial
	}
	return StateError
}

// shutdown — yeni iş alınmaz (run döngüsü çıktı); uçuştakilere süre tanınır,
// dolunca iptal edilip queued olarak geri yazılırlar.
func (dr *drainer) shutdown() {
	finished := make(chan struct{})
	go func() { dr.wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(dr.d.ShutdownGrace):
		dr.mu.Lock()
		for _, fl := range dr.inflight {
			fl.shutdown.Store(true)
			fl.cancel()
		}
		dr.mu.Unlock()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			log.Printf("[sre] kapanış: uçuştaki işler 5 s içinde bitmedi")
		}
	}
	local.Store(nil)
	if dr.d.Cache != nil {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = dr.d.Cache.Del(c, HeartbeatPrefix+dr.d.PodID)
	}
	log.Printf("[sre] drain stopped (drained=%d)", dr.drained.Load())
}
