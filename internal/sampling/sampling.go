// Package sampling decides whether to keep or drop an incoming span
// based on a hybrid rule set:
//
//   1. Always-keep rules: spans matching these are always kept,
//      regardless of probabilistic decision. Default rules:
//        - status_code = "error"
//        - parent_span_id == "" (i.e. root span)
//      The rules are intentional: errors are 100x more valuable
//      than successful spans, and root spans are a cheap RPS
//      anchor that lets services-list / service-map maths still
//      work at a low ratio.
//
//   2. Probabilistic ratio: for spans that didn't match an
//      always-keep, we hash trace_id and keep iff the hash falls
//      below ratio * 2^32. Same trace_id → same decision, so the
//      spans of one trace are kept or dropped together — partial
//      traces are a pain to analyse and we avoid producing them.
//
// At billion-span/day scale a Default=0.1 + always-keep-errors +
// always-keep-roots cuts ingest volume ~90% on a healthy service
// while preserving 100% of failures and the root-span RPS index.
// That's the difference between affordable and not at scale.
//
// Decisions are pure functions of the Span struct + config — no
// state, no buffering, safe to call from many goroutines. This is
// "head sampling" in OTel terms; full tail-sampling (buffer until
// trace is "done", then decide based on aggregate properties)
// requires a per-trace buffer with TTL, deferred for a follow-up.
package sampling

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"log"
	"strings"
	"sync"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

// settingsKey is the system_settings entry that persists the live
// sampling config. Admin UI writes here; LoadPersisted picks it
// up on boot. JSON-encoded SamplingConfig.
const settingsKey = "sampling"

// Sampler is the application-side decision engine. Construct one
// per process from cfg; safe for concurrent use.
type Sampler struct {
	mu               sync.RWMutex
	defaultRatio     float64
	services         map[string]uint32 // ratio * 2^32, precomputed
	defaultThreshold uint32
	keepErrors       bool
	keepRoots        bool
}

// New builds a Sampler from config. Default ratios outside [0,1]
// are clamped; nil maps treated as empty.
func New(cfg config.SamplingConfig) *Sampler {
	keepErr := true
	if cfg.AlwaysKeepErrors != nil {
		keepErr = *cfg.AlwaysKeepErrors
	}
	keepRoot := true
	if cfg.AlwaysKeepRoots != nil {
		keepRoot = *cfg.AlwaysKeepRoots
	}
	d := clamp01(cfg.Default)
	if cfg.Default == 0 && len(cfg.Services) == 0 && cfg.AlwaysKeepErrors == nil && cfg.AlwaysKeepRoots == nil {
		// Empty config = sampling disabled (keep everything). The
		// "Default=0 means drop probabilistic" semantics only apply
		// once the operator has actually set something.
		d = 1.0
	}
	s := &Sampler{
		defaultRatio:     d,
		defaultThreshold: ratioToThreshold(d),
		services:         map[string]uint32{},
		keepErrors:       keepErr,
		keepRoots:        keepRoot,
	}
	for svc, r := range cfg.Services {
		s.services[svc] = ratioToThreshold(clamp01(r))
	}
	return s
}

// Decide returns true if the span should be kept. Inlinable hot
// path — no allocations, no map fallback besides the per-service
// lookup. Callers usually wrap an `if !s.Decide(span) { drop }`.
func (s *Sampler) Decide(span *chstore.Span) bool {
	if span == nil {
		return false
	}
	if s.keepErrors && isError(span.StatusCode) {
		return true
	}
	if s.keepRoots && span.ParentID == "" {
		return true
	}
	s.mu.RLock()
	threshold, ok := s.services[span.ServiceName]
	s.mu.RUnlock()
	if !ok {
		threshold = s.defaultThreshold
	}
	if threshold == 0 {
		return false
	}
	if threshold >= 0xffffffff {
		return true
	}
	return traceHash(span.TraceID) < threshold
}

// Reload swaps in a new config atomically. Lets the admin UI
// adjust ratios without a process restart.
func (s *Sampler) Reload(cfg config.SamplingConfig) {
	next := New(cfg)
	s.mu.Lock()
	s.defaultRatio = next.defaultRatio
	s.defaultThreshold = next.defaultThreshold
	s.services = next.services
	s.keepErrors = next.keepErrors
	s.keepRoots = next.keepRoots
	s.mu.Unlock()
}

// LoadPersisted reads system_settings["sampling"] and applies it
// to the live Sampler. Called once at boot after the env-var /
// config.yaml-derived state is in place; if a persisted value
// exists it wins, since the admin UI is the canonical source of
// truth for runtime tuning.
func (s *Sampler) LoadPersisted(ctx context.Context, store *chstore.Store) error {
	raw, err := store.GetSetting(ctx, settingsKey)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var cfg config.SamplingConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Printf("[sampling] decode persisted: %v (using config.yaml defaults)", err)
		return nil
	}
	s.Reload(cfg)
	log.Printf("[sampling] loaded persisted config (default=%.2f, %d service overrides)",
		cfg.Default, len(cfg.Services))
	return nil
}

// SavePersisted serialises cfg to system_settings + hot-reloads
// the in-memory Sampler. Admin UI calls this on its "Save" button.
func (s *Sampler) SavePersisted(ctx context.Context, store *chstore.Store, cfg config.SamplingConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := store.PutSetting(ctx, settingsKey, raw); err != nil {
		return err
	}
	s.Reload(cfg)
	return nil
}

// Snapshot returns the live config for /api/sampling readback.
func (s *Sampler) Snapshot() config.SamplingConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := config.SamplingConfig{
		Default:  s.defaultRatio,
		Services: map[string]float64{},
	}
	for svc, t := range s.services {
		out.Services[svc] = float64(t) / float64(0xffffffff)
	}
	keepErr, keepRoot := s.keepErrors, s.keepRoots
	out.AlwaysKeepErrors = &keepErr
	out.AlwaysKeepRoots = &keepRoot
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func ratioToThreshold(ratio float64) uint32 {
	if ratio <= 0 {
		return 0
	}
	if ratio >= 1 {
		return 0xffffffff
	}
	return uint32(ratio * float64(0xffffffff))
}

// traceHash maps a trace_id (hex string, 32 chars usually) to a
// 32-bit value. FNV-1a is non-cryptographic, fast, and good enough
// for uniform partition selection — same trace_id always lands at
// the same bucket so the keep/drop decision is consistent.
func traceHash(traceID string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(traceID))
	return h.Sum32()
}

func isError(statusCode string) bool {
	switch strings.ToLower(statusCode) {
	case "error", "status_code_error":
		return true
	}
	return false
}
