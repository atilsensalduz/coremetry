package sampling

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

// TailSampler buffers each incoming span by trace_id for a fixed
// decision window, then commits or discards the whole trace based
// on aggregate properties only knowable post-buffering:
//
//   - any error → keep (catches cascading downstream failures the
//     head sampler can't see when only one span has the error
//     status)
//   - root duration > SlowMs → keep (the "slow trace" case head
//     sampling fundamentally cannot model — head sees the first
//     span at trace start, not the root finish-time)
//   - otherwise: hash(trace_id) < ratio (consistent across late-
//     arrivals so trace partitioning is stable)
//
// Late-arriving spans for a trace that already decided are routed
// through a small post-decision map (`decided`) so a span that
// arrives 31s after the rest of the trace still follows the same
// keep/drop verdict. The post-decision map self-GCs at 5min.
//
// Memory bound: MaxTraces × ~5 spans × ~500B = ~500MB at the
// 200K-trace default. On overflow we evict the oldest bucket and
// flush whatever it had — better to be slightly inaccurate than
// to OOM the whole process.
type TailSampler struct {
	cfg            config.TailSamplingConfig
	defaultRatio   float64
	keepErrors     bool
	keepRoots      bool

	mu        sync.Mutex
	buckets   map[string]*traceBucket
	decided   map[string]decision
	bucketCt  atomic.Int64

	flush     func(*chstore.Span) bool   // delegate to consumer.Add
	stopOnce  sync.Once
	stopCh    chan struct{}

	// counters surfaced via /api/settings/sampling.
	flushed    atomic.Uint64
	dropped    atomic.Uint64
	evicted    atomic.Uint64
}

type traceBucket struct {
	spans     []*chstore.Span
	firstSeen time.Time
	hasError  bool
	rootDur   int64
}

type decision struct {
	keep bool
	at   time.Time
}

// NewTailSampler constructs a tail sampler. Caller still owns
// starting Run() — that loop is what makes deferred decisions
// actually fire.
func NewTailSampler(cfg config.TailSamplingConfig, defaultRatio float64, keepErrors, keepRoots bool, flush func(*chstore.Span) bool) *TailSampler {
	if cfg.WindowSec <= 0 {
		cfg.WindowSec = 30
	}
	if cfg.SlowMs <= 0 {
		cfg.SlowMs = 1000
	}
	if cfg.MaxTraces <= 0 {
		cfg.MaxTraces = 200_000
	}
	return &TailSampler{
		cfg:          cfg,
		defaultRatio: defaultRatio,
		keepErrors:   keepErrors,
		keepRoots:    keepRoots,
		buckets:      make(map[string]*traceBucket, 1024),
		decided:      make(map[string]decision, 1024),
		flush:        flush,
		stopCh:       make(chan struct{}),
	}
}

// Enabled reports whether tail sampling should consume spans.
// Returns false when the operator has disabled it; callers fall
// back to head-only sampling.
func (t *TailSampler) Enabled() bool {
	return t != nil && t.cfg.Enabled
}

// Add buffers the span. Always returns true — the OTLP ingester
// treats tail-sampling acceptance as "successful, will appear
// later via flush" rather than a synchronous accept/reject. If
// the trace has already been decided, the span is flushed (or
// dropped) immediately; otherwise it joins the bucket and the
// sweeper deals with it later.
func (t *TailSampler) Add(sp *chstore.Span) {
	if sp == nil {
		return
	}
	t.mu.Lock()

	// Fast path: trace already decided, route the late span through
	// the cached verdict.
	if d, ok := t.decided[sp.TraceID]; ok {
		t.mu.Unlock()
		if d.keep {
			t.flushSpan(sp)
		} else {
			t.dropped.Add(1)
		}
		return
	}

	b, ok := t.buckets[sp.TraceID]
	if !ok {
		// Memory-cap enforcement: if we're at the bucket cap, evict
		// the oldest bucket (decide it now, accepting whatever
		// state it has). Better than refusing the new span, which
		// would tear holes in active traces.
		if int64(len(t.buckets)) >= int64(t.cfg.MaxTraces) {
			t.evictOldestLocked()
		}
		b = &traceBucket{firstSeen: time.Now()}
		t.buckets[sp.TraceID] = b
		t.bucketCt.Store(int64(len(t.buckets)))
	}
	b.spans = append(b.spans, sp)
	if isError(sp.StatusCode) {
		b.hasError = true
	}
	if sp.ParentID == "" && sp.Duration > b.rootDur {
		b.rootDur = sp.Duration
	}
	t.mu.Unlock()
}

// Run is the sweeper. Every second, scan for buckets whose
// firstSeen is older than the decision window and finalise them.
// Also GCs the post-decision map at 5min so memory doesn't grow
// without bound on a steady-state load.
func (t *TailSampler) Run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.stopCh:
			return
		case <-tick.C:
			t.sweep()
		}
	}
}

// Stop signals the sweeper to exit. Safe to call multiple times.
func (t *TailSampler) Stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
}

func (t *TailSampler) sweep() {
	due := time.Now().Add(-time.Duration(t.cfg.WindowSec) * time.Second)
	gcBefore := time.Now().Add(-5 * time.Minute)

	t.mu.Lock()
	overdue := make([]string, 0, 64)
	for id, b := range t.buckets {
		if b.firstSeen.Before(due) {
			overdue = append(overdue, id)
		}
	}
	for _, id := range overdue {
		t.decideLocked(id)
	}
	for id, d := range t.decided {
		if d.at.Before(gcBefore) {
			delete(t.decided, id)
		}
	}
	t.bucketCt.Store(int64(len(t.buckets)))
	t.mu.Unlock()
}

// evictOldestLocked picks the bucket with the earliest firstSeen
// and decides it now. Caller holds t.mu.
func (t *TailSampler) evictOldestLocked() {
	var oldestID string
	var oldestAt time.Time
	for id, b := range t.buckets {
		if oldestID == "" || b.firstSeen.Before(oldestAt) {
			oldestID = id
			oldestAt = b.firstSeen
		}
	}
	if oldestID != "" {
		t.evicted.Add(1)
		t.decideLocked(oldestID)
	}
}

// decideLocked applies the tail policy to a single trace and
// emits its spans through the flush callback. Caller holds t.mu.
func (t *TailSampler) decideLocked(id string) {
	b, ok := t.buckets[id]
	if !ok {
		return
	}
	delete(t.buckets, id)

	keep := false
	switch {
	case t.keepErrors && b.hasError:
		keep = true
	case b.rootDur >= int64(t.cfg.SlowMs)*int64(time.Millisecond):
		// Root duration exceeded the slow-trace threshold — this
		// is the case head sampling cannot see.
		keep = true
	case t.keepRoots && hasRoot(b.spans):
		// Keep at least one span per request so RPS counting on
		// the spans table stays accurate. Aligns with the head
		// sampler semantics; only one root per trace by definition.
		keep = traceHash(id) < ratioToThreshold(t.defaultRatio) // probabilistic; root preference is the head sampler's job
		_ = keep // we still want to keep the root span explicitly:
		keep = true
	default:
		keep = traceHash(id) < ratioToThreshold(t.defaultRatio)
	}

	t.decided[id] = decision{keep: keep, at: time.Now()}

	if keep {
		for _, sp := range b.spans {
			t.flushSpan(sp)
		}
	} else {
		t.dropped.Add(uint64(len(b.spans)))
	}
}

func (t *TailSampler) flushSpan(sp *chstore.Span) {
	if t.flush == nil {
		return
	}
	if !t.flush(sp) {
		// Consumer buffer full — count it as dropped at the
		// downstream layer, not at sampling.
		log.Printf("[tail] consumer buffer full, dropping kept span trace=%s", sp.TraceID)
		return
	}
	t.flushed.Add(1)
}

func hasRoot(spans []*chstore.Span) bool {
	for _, sp := range spans {
		if sp.ParentID == "" {
			return true
		}
	}
	return false
}

// Stats is what /api/settings/sampling shows for the tail stage.
type TailStats struct {
	Enabled    bool   `json:"enabled"`
	WindowSec  int    `json:"windowSec"`
	SlowMs     int    `json:"slowMs"`
	MaxTraces  int    `json:"maxTraces"`
	OpenTraces int64  `json:"openTraces"`
	FlushedSpans uint64 `json:"flushedSpans"`
	DroppedSpans uint64 `json:"droppedSpans"`
	EvictedTraces uint64 `json:"evictedTraces"`
}

func (t *TailSampler) Stats() TailStats {
	if t == nil {
		return TailStats{}
	}
	return TailStats{
		Enabled:       t.cfg.Enabled,
		WindowSec:     t.cfg.WindowSec,
		SlowMs:        t.cfg.SlowMs,
		MaxTraces:     t.cfg.MaxTraces,
		OpenTraces:    t.bucketCt.Load(),
		FlushedSpans:  t.flushed.Load(),
		DroppedSpans:  t.dropped.Load(),
		EvictedTraces: t.evicted.Load(),
	}
}
