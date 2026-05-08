// Package evaluator runs alert rules on a fixed interval, opens problems
// when their condition is breached, and resolves problems whose breach is
// no longer present. Built-in rules cover the typical APM signals
// (error rate, P99 latency, request-rate drops).
package evaluator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/notify"
)

const lockKey = "coremetry:lock:evaluator"

type Evaluator struct {
	store    *chstore.Store
	interval time.Duration
	lock     cache.Lock
	notifier *notify.Notifier
}

// New takes a cache.Lock so multiple Coremetry replicas only run the
// evaluation loop once per tick, and a notifier so PROBLEM OPENED
// transitions fan out to email/slack/etc.
func New(store *chstore.Store, interval time.Duration, lock cache.Lock, notifier *notify.Notifier) *Evaluator {
	if interval == 0 {
		interval = time.Minute
	}
	return &Evaluator{store: store, interval: interval, lock: lock, notifier: notifier}
}

// Start runs the evaluation loop until ctx is cancelled. Built-in rules
// are seeded by every replica — that's safe (UpsertAlertRule is idempotent
// on id). Only the actual evaluation pass is leader-gated.
func (e *Evaluator) Start(ctx context.Context) {
	if err := e.seedBuiltinRules(ctx); err != nil {
		log.Printf("[evaluator] seed built-in rules: %v", err)
	}

	t := time.NewTicker(e.interval)
	defer t.Stop()

	e.runIfLeader(ctx) // run once immediately

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.runIfLeader(ctx)
		}
	}
}

// runIfLeader skips the tick when another replica holds the lock. Lease
// is 2× the tick interval so a crashed leader is recovered quickly while
// still leaving headroom for slow runs.
func (e *Evaluator) runIfLeader(ctx context.Context) {
	ok, err := e.lock.TryAcquire(ctx, lockKey, 2*e.interval)
	if err != nil {
		log.Printf("[evaluator] lock: %v — running anyway", err)
		e.evaluateAll(ctx)
		return
	}
	if !ok {
		return // another replica is running this tick
	}
	defer e.lock.Release(ctx, lockKey)
	e.evaluateAll(ctx)
}

// ── Built-in rules ───────────────────────────────────────────────────────────
//
// These ship out of the box — auto-applied to every service detected in
// the last 24 hours. Users can disable them via the UI.

var builtins = []chstore.AlertRule{
	// ── Spans-wide, all-transport ─────────────────────────────────
	{ID: "builtin-error-rate-5pct",  Name: "High error rate (>5% over 5 min)",
		Metric: "error_rate", Comparator: ">", Threshold: 5,  WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-error-rate-15pct", Name: "Critical error rate (>15% over 5 min)",
		Metric: "error_rate", Comparator: ">", Threshold: 15, WindowSec: 5 * 60,
		Severity: "critical", Enabled: true, BuiltIn: true},
	{ID: "builtin-p99-2s",           Name: "High P99 latency (>2s over 5 min)",
		Metric: "p99_ms",     Comparator: ">", Threshold: 2000, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},

	// ── HTTP server-side ──────────────────────────────────────────
	{ID: "builtin-http-5xx-1pct",    Name: "HTTP 5xx rate >1% (5 min)",
		Metric: "http_5xx_rate", Comparator: ">", Threshold: 1,  WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-http-5xx-5pct",    Name: "Critical HTTP 5xx rate >5% (5 min)",
		Metric: "http_5xx_rate", Comparator: ">", Threshold: 5,  WindowSec: 5 * 60,
		Severity: "critical", Enabled: true, BuiltIn: true},
	{ID: "builtin-http-p99-1s",      Name: "HTTP P99 >1s (5 min)",
		Metric: "http_p99_ms",   Comparator: ">", Threshold: 1000, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-http-p99-3s",      Name: "Critical HTTP P99 >3s (5 min)",
		Metric: "http_p99_ms",   Comparator: ">", Threshold: 3000, WindowSec: 5 * 60,
		Severity: "critical", Enabled: true, BuiltIn: true},

	// ── Database (postgres / mysql / mongo / redis / elasticsearch) ─
	{ID: "builtin-db-error-1pct",    Name: "DB error rate >1% (5 min)",
		Metric: "db_error_rate", Comparator: ">", Threshold: 1,  WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-db-p99-500ms",     Name: "DB P99 latency >500ms (5 min)",
		Metric: "db_p99_ms",     Comparator: ">", Threshold: 500, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-db-p99-2s",        Name: "Critical DB P99 latency >2s (5 min)",
		Metric: "db_p99_ms",     Comparator: ">", Threshold: 2000, WindowSec: 5 * 60,
		Severity: "critical", Enabled: true, BuiltIn: true},

	// ── RPC (gRPC / Thrift / etc.) ────────────────────────────────
	{ID: "builtin-rpc-error-5pct",   Name: "RPC error rate >5% (5 min)",
		Metric: "rpc_error_rate", Comparator: ">", Threshold: 5, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-rpc-p99-1s",       Name: "RPC P99 latency >1s (5 min)",
		Metric: "rpc_p99_ms",     Comparator: ">", Threshold: 1000, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},

	// ── Message queue (Kafka producers + consumers) ───────────────
	{ID: "builtin-mq-publish-err-5pct", Name: "MQ publish error rate >5% (5 min)",
		Metric: "mq_publish_error_rate", Comparator: ">", Threshold: 5, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-mq-consume-err-5pct", Name: "MQ consume error rate >5% (5 min)",
		Metric: "mq_consume_error_rate", Comparator: ">", Threshold: 5, WindowSec: 5 * 60,
		Severity: "warning",  Enabled: true, BuiltIn: true},
	{ID: "builtin-mq-consume-p99-30s",  Name: "MQ consume P99 >30s — processing lag (5 min)",
		Metric: "mq_consume_p99_ms",     Comparator: ">", Threshold: 30000, WindowSec: 5 * 60,
		Severity: "critical", Enabled: true, BuiltIn: true},
}

func (e *Evaluator) seedBuiltinRules(ctx context.Context) error {
	existing, err := e.store.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]bool)
	for _, r := range existing {
		have[r.ID] = true
	}
	for _, r := range builtins {
		if have[r.ID] {
			continue
		}
		r.CreatedAt = time.Now().UnixNano()
		if err := e.store.UpsertAlertRule(ctx, r); err != nil {
			log.Printf("[evaluator] seed %s: %v", r.ID, err)
		}
	}
	return nil
}

// ── Evaluation loop ──────────────────────────────────────────────────────────

func (e *Evaluator) evaluateAll(ctx context.Context) {
	rules, err := e.store.ListAlertRules(ctx)
	if err != nil {
		log.Printf("[evaluator] list rules: %v", err)
		return
	}

	// Cache the recent service set so wildcard rules know what to evaluate.
	services, err := e.store.GetServices(ctx, 24*time.Hour, time.Time{}, time.Time{})
	if err != nil {
		log.Printf("[evaluator] services: %v", err)
		return
	}

	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		targets := []string{r.Service}
		if r.Service == "" {
			targets = make([]string, 0, len(services))
			for _, s := range services {
				targets = append(targets, s.Name)
			}
		}
		for _, svc := range targets {
			e.evaluateOne(ctx, r, svc)
		}
	}
}

func (e *Evaluator) evaluateOne(ctx context.Context, r chstore.AlertRule, service string) {
	if service == "" {
		return
	}
	value, err := e.measure(ctx, service, r.Metric, time.Duration(r.WindowSec)*time.Second)
	if err != nil {
		log.Printf("[evaluator] measure %s/%s: %v", r.ID, service, err)
		return
	}

	breached := compare(value, r.Comparator, r.Threshold)

	open, err := e.store.FindOpenProblem(ctx, r.ID, service)
	hasOpen := err == nil && open != nil && open.ID != ""

	switch {
	case breached && !hasOpen:
		// Open a new problem
		p := chstore.Problem{
			ID:          newID(),
			RuleID:      r.ID,
			RuleName:    r.Name,
			Severity:    r.Severity,
			Service:     service,
			Metric:      r.Metric,
			Value:       value,
			Threshold:   r.Threshold,
			Status:      "open",
			Description: describeProblem(r, service, value),
			StartedAt:   time.Now().UnixNano(),
		}
		if err := e.store.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator] open problem: %v", err)
		} else {
			log.Printf("[evaluator] PROBLEM OPENED: %s · %s = %.2f (threshold %.2f)",
				service, r.Metric, value, r.Threshold)
			// Auto-group into an Incident — same-service same-severity
			// problems within 30min get folded under one declared
			// incident so the oncall has a single place to drive
			// response from. Best-effort; failure here doesn't block
			// alerting.
			if _, err := e.store.AttachProblemToIncident(ctx, p); err != nil {
				log.Printf("[evaluator] incident attach: %v", err)
			}
			// Fan out to user channels (email/slack/etc). Fire-and-forget
			// so a flaky SMTP doesn't block the eval loop.
			if e.notifier != nil {
				go e.notifier.SendProblemAlert(context.Background(), p)
			}
		}

	case breached && hasOpen:
		// Refresh the live value on the existing problem
		open.Value = value
		if err := e.store.UpsertProblem(ctx, *open); err != nil {
			log.Printf("[evaluator] refresh problem: %v", err)
		}

	case !breached && hasOpen:
		// Auto-resolve
		now := time.Now().UnixNano()
		open.Status = "resolved"
		open.ResolvedAt = &now
		open.Value = value
		if err := e.store.UpsertProblem(ctx, *open); err != nil {
			log.Printf("[evaluator] resolve problem: %v", err)
		} else {
			log.Printf("[evaluator] PROBLEM RESOLVED: %s · %s", service, r.Metric)
		}
	}
}

// measure runs the per-service metric query for the given window.
func (e *Evaluator) measure(ctx context.Context, service, metric string, window time.Duration) (float64, error) {
	cutoff := time.Now().Add(-window)
	conn := e.store.Conn()

	switch metric {
	case "error_rate":
		var v float64
		err := conn.QueryRow(ctx, `
			SELECT countIf(status_code='error') / nullIf(count(),0) * 100
			FROM spans WHERE service_name = ? AND time >= ?`,
			service, cutoff).Scan(&v)
		if err != nil {
			return 0, err
		}
		return v, nil
	case "error_count":
		var n uint64
		err := conn.QueryRow(ctx, `
			SELECT countIf(status_code='error')
			FROM spans WHERE service_name = ? AND time >= ?`,
			service, cutoff).Scan(&n)
		if err != nil {
			return 0, err
		}
		return float64(n), nil
	case "request_rate":
		var n uint64
		err := conn.QueryRow(ctx, `
			SELECT count() FROM spans WHERE service_name = ? AND time >= ?`,
			service, cutoff).Scan(&n)
		if err != nil {
			return 0, err
		}
		return float64(n) / window.Seconds(), nil
	case "p50_ms", "p95_ms", "p99_ms", "avg_ms":
		var sql string
		switch metric {
		case "avg_ms":
			sql = `SELECT avg(duration) / 1e6 FROM spans WHERE service_name=? AND time>=?`
		default:
			q := metric[1 : len(metric)-3] // "50" / "95" / "99"
			sql = fmt.Sprintf(`SELECT quantile(0.%s)(duration) / 1e6 FROM spans WHERE service_name=? AND time>=?`, q)
		}
		var v float64
		err := conn.QueryRow(ctx, sql, service, cutoff).Scan(&v)
		return v, err
	}

	// Transport-scoped metrics — narrow each query by an indexed
	// LowCardinality column (db_system / rpc_system / kind /
	// http_method) so the (service_name, time) primary key still
	// drives the scan and only relevant rows are aggregated. These
	// power the production-grade DB / RPC / HTTP / MQ alert
	// categories.
	//
	// For *_rate metrics the WHERE narrows the *denominator* (the
	// span population we're measuring against, e.g. all HTTP server
	// spans), and the *_rate's numerator condition counts within
	// that population (e.g. http_status >= 500). Conflating the two
	// — narrowing WHERE by 5xx — would produce 100% trivially.
	if where, numerator, ok := transportFilter(metric); ok {
		op := transportOp(metric)
		var sql string
		switch op {
		case "error_rate":
			sql = `SELECT countIf(` + numerator + `) * 100.0 / nullIf(count(),0)
				FROM spans WHERE service_name=? AND time>=? AND ` + where
		case "p50_ms", "p95_ms", "p99_ms", "avg_ms":
			if op == "avg_ms" {
				sql = `SELECT avg(duration) / 1e6
					FROM spans WHERE service_name=? AND time>=? AND ` + where
			} else {
				q := op[1 : len(op)-3]
				sql = fmt.Sprintf(`SELECT quantile(0.%s)(duration) / 1e6
					FROM spans WHERE service_name=? AND time>=? AND `, q) + where
			}
		case "count":
			sql = `SELECT count() FROM spans WHERE service_name=? AND time>=? AND ` + where
		default:
			return 0, fmt.Errorf("unknown transport op %q in %q", op, metric)
		}
		var v float64
		err := conn.QueryRow(ctx, sql, service, cutoff).Scan(&v)
		return v, err
	}
	return 0, fmt.Errorf("unknown metric %q", metric)
}

// transportFilter returns:
//   - where:     denominator population predicate (WHERE narrows
//     the span set we're measuring against)
//   - numerator: numerator predicate for *_rate metrics (counts the
//     "bad" rows within the population). Unused for latency/count
//     metrics.
//
// All fragments are literal SQL — no user input — so they're safe
// to concatenate.
func transportFilter(metric string) (where, numerator string, ok bool) {
	switch {
	case strings.HasPrefix(metric, "http_5xx_"):
		return "kind='server' AND http_method != ''",
			"http_status >= 500", true
	case strings.HasPrefix(metric, "http_4xx_"):
		return "kind='server' AND http_method != ''",
			"http_status >= 400 AND http_status < 500", true
	case strings.HasPrefix(metric, "http_"):
		return "kind='server' AND http_method != ''",
			"status_code='error'", true
	case strings.HasPrefix(metric, "db_"):
		return "db_system != ''",
			"status_code='error'", true
	case strings.HasPrefix(metric, "rpc_"):
		return "rpc_system != ''",
			"status_code='error'", true
	case strings.HasPrefix(metric, "mq_publish_"):
		return "kind='producer'",
			"status_code='error'", true
	case strings.HasPrefix(metric, "mq_consume_"):
		return "kind='consumer'",
			"status_code='error'", true
	}
	return "", "", false
}

// transportOp pulls the aggregate suffix off a transport metric:
//
//	http_5xx_rate          → error_rate (5xx-narrowed by transportFilter)
//	http_p99_ms            → p99_ms
//	db_error_rate          → error_rate
//	mq_publish_error_rate  → error_rate
func transportOp(metric string) string {
	switch {
	case strings.HasSuffix(metric, "_rate"):
		return "error_rate"
	case strings.HasSuffix(metric, "_p99_ms"):
		return "p99_ms"
	case strings.HasSuffix(metric, "_p95_ms"):
		return "p95_ms"
	case strings.HasSuffix(metric, "_p50_ms"):
		return "p50_ms"
	case strings.HasSuffix(metric, "_avg_ms"):
		return "avg_ms"
	case strings.HasSuffix(metric, "_count"):
		return "count"
	}
	return ""
}

func compare(value float64, op string, threshold float64) bool {
	switch op {
	case ">":  return value >  threshold
	case ">=": return value >= threshold
	case "<":  return value <  threshold
	case "<=": return value <= threshold
	}
	return false
}

func describeProblem(r chstore.AlertRule, service string, value float64) string {
	unit := metricUnit(r.Metric)
	return fmt.Sprintf("%s on %s — observed %.2f%s, threshold %s %.2f%s over %ds window.",
		r.Name, service, value, unit, r.Comparator, r.Threshold, unit, r.WindowSec)
}

func metricUnit(m string) string {
	if strings.HasSuffix(m, "_ms") {
		return "ms"
	}
	if strings.HasSuffix(m, "_rate") {
		// http_5xx_rate, db_error_rate, rpc_error_rate, … all
		// percent — request_rate is the one exception.
		if m == "request_rate" {
			return "/s"
		}
		return "%"
	}
	return ""
}

func newID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
