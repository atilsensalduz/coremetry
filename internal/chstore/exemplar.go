package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Exemplar lookup — given a chart point the user clicked on
// (service + optional operation + time bucket + slow/error
// flavour), pick a single representative span and return its
// trace_id so the UI can drill straight from the metric line
// into the actual trace that produced it.
//
// Why this matters: looking at a P99 latency spike at 14:32 is
// not actionable. Looking at *the* trace that landed at that
// percentile in that 30-second bucket is. This is the standard
// Datadog / Honeycomb / Grafana exemplar pattern.
//
// Picking strategy:
//   - kind = "slow":   ORDER BY duration DESC LIMIT 1 — the
//     single slowest span matching the filter. Best mapped to
//     a P99 / max chart click.
//   - kind = "error":  same, restricted to status_code='error'.
//     The "open the loudest failing trace" button.
//   - kind = "any":    duration-desc on the bucket. Most users
//     want the slow-or-error one, but "any" is here for
//     count/rate charts where the chart datum is just "stuff
//     happened".
//
// Performance: leans on spans.ORDER BY (service_name, time)
// primary key. Adding service_name + time predicates lets CH
// prune partitions and skip-index granules; LIMIT 1 short-
// circuits past the first match. Sub-100ms even on
// billion-span tables.

type ExemplarKind string

const (
	ExemplarSlow  ExemplarKind = "slow"
	ExemplarError ExemplarKind = "error"
	ExemplarAny   ExemplarKind = "any"
)

type ExemplarReq struct {
	Service   string       // service_name (required)
	Operation string       // span name (optional — empty = any op)
	From      time.Time    // bucket start (inclusive)
	To        time.Time    // bucket end (inclusive)
	Kind      ExemplarKind // slow | error | any
}

type Exemplar struct {
	TraceID    string `json:"traceId"`
	SpanID     string `json:"spanId"`
	Service    string `json:"service"`
	Name       string `json:"name"`
	DurationNs int64  `json:"durationNs"`
	StatusCode string `json:"statusCode"`
	TimeUnixNs int64  `json:"timeUnixNs"`
}

func (s *Store) FindExemplar(ctx context.Context, req ExemplarReq) (*Exemplar, error) {
	if strings.TrimSpace(req.Service) == "" {
		return nil, fmt.Errorf("service is required")
	}
	if req.From.IsZero() || req.To.IsZero() {
		return nil, fmt.Errorf("from/to are required")
	}

	var conds []string
	args := []any{req.Service, req.From, req.To}
	conds = append(conds,
		"service_name = ?",
		"time >= ?",
		"time <= ?",
	)
	if op := strings.TrimSpace(req.Operation); op != "" {
		conds = append(conds, "name = ?")
		args = append(args, op)
	}

	switch req.Kind {
	case ExemplarError:
		conds = append(conds, "status_code = 'error'")
	case ExemplarSlow, ExemplarAny, "":
		// no additional predicate
	default:
		return nil, fmt.Errorf("unknown exemplar kind %q", req.Kind)
	}

	sql := fmt.Sprintf(`
		SELECT trace_id, span_id, service_name, name,
		       duration, status_code,
		       toUnixTimestamp64Nano(time) AS time_ns
		FROM spans
		WHERE %s
		ORDER BY duration DESC
		LIMIT 1`, strings.Join(conds, " AND "))

	row := s.conn.QueryRow(ctx, sql, args...)
	var e Exemplar
	var timeNs int64
	if err := row.Scan(&e.TraceID, &e.SpanID, &e.Service, &e.Name,
		&e.DurationNs, &e.StatusCode, &timeNs); err != nil {
		// Empty result set surfaces here as scan error — same
		// pattern used elsewhere in chstore (anomaly_event.go,
		// incident.go). Treat as a clean "not found".
		return nil, nil
	}
	e.TimeUnixNs = timeNs
	return &e, nil
}
