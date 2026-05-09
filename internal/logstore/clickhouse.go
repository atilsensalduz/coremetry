package logstore

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// CHStore adapts the existing ClickHouse logs table to the LogStore
// interface. Pure delegation — chstore.GetLogs already takes a similar
// filter shape.
type CHStore struct {
	store *chstore.Store
}

func NewCH(store *chstore.Store) *CHStore { return &CHStore{store: store} }

func (s *CHStore) Backend() string { return "clickhouse" }

// Ping delegates to the wrapped chstore — the same CH connection the
// rest of the app uses, so no separate liveness contract.
func (s *CHStore) Ping(ctx context.Context) error { return s.store.Ping(ctx) }

func (s *CHStore) Search(ctx context.Context, f Filter) (*Page, error) {
	rows, total, err := s.store.GetLogs(ctx, chstore.LogFilter{
		Service:     f.Service,
		Search:      f.Search,
		From:        f.From,
		To:          f.To,
		SeverityMin: f.SeverityMin,
		TraceID:     f.TraceID,
		SpanID:      f.SpanID,
		Limit:       f.Limit,
		Offset:      f.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*LogRecord, 0, len(rows))
	for _, l := range rows {
		out = append(out, &LogRecord{
			ID:                 int64(l.ID),
			Timestamp:          l.Timestamp,
			Severity:           l.SeverityNumber,
			SeverityText:       l.SeverityText,
			Body:               l.Body,
			ServiceName:        l.ServiceName,
			TraceID:            l.TraceID,
			SpanID:             l.SpanID,
			Attributes:         l.Attributes,
			ResourceAttributes: l.ResourceAttributes,
		})
	}
	return &Page{Total: int(total), Logs: out}, nil
}

// Histogram buckets log volume server-side via the same logs
// table. Whitelisted groupBy options ("service", "severity",
// or "" for total) map to indexed LowCardinality columns so the
// query stays partition-pruned + index-friendly even at billion
// log/day. Unknown groupBy collapses to a single _total series
// rather than failing — operator notices empty break-down and
// can pick a different field.
func (s *CHStore) Histogram(ctx context.Context, f Filter, bucketSec int, groupBy string) ([]LogSeries, error) {
	if bucketSec <= 0 {
		bucketSec = 30
	}
	groupExpr := "'_total'"
	switch groupBy {
	case "service":
		groupExpr = "service_name"
	case "severity":
		groupExpr = "if(severity_text != '', severity_text, toString(severity_num))"
	}

	from, to := f.From, f.To
	if from.IsZero() {
		from = time.Now().Add(-1 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now()
	}
	args := []any{from, to}
	wc := "time >= ? AND time <= ?"
	if f.Service != "" {
		wc += " AND service_name = ?"
		args = append(args, f.Service)
	}
	if f.Search != "" {
		wc += " AND positionCaseInsensitive(body, ?) > 0"
		args = append(args, f.Search)
	}
	if f.SeverityMin > 0 {
		wc += " AND severity_num >= ?"
		args = append(args, f.SeverityMin)
	}
	if f.TraceID != "" {
		wc += " AND trace_id = ?"
		args = append(args, f.TraceID)
	}

	sql := fmt.Sprintf(`
		SELECT %s AS g,
		       toStartOfInterval(time, INTERVAL %d SECOND) AS bucket,
		       count() AS c
		FROM logs
		WHERE %s
		GROUP BY g, bucket
		ORDER BY g, bucket
		LIMIT 50000
		SETTINGS max_execution_time = 30`, groupExpr, bucketSec, wc)

	rows, err := s.store.Conn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]*LogSeries{}
	order := []string{}
	for rows.Next() {
		var g string
		var t time.Time
		var c uint64
		if err := rows.Scan(&g, &t, &c); err != nil {
			return nil, err
		}
		s, ok := byName[g]
		if !ok {
			s = &LogSeries{Name: g}
			byName[g] = s
			order = append(order, g)
		}
		s.Points = append(s.Points, LogPoint{T: t.UnixNano(), V: int64(c)})
	}
	out := make([]LogSeries, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, rows.Err()
}
