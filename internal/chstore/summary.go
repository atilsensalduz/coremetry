package chstore

import (
	"context"
	"time"
)

// ServiceSummaryRow is one 5-minute bucket of pre-aggregated stats for a
// single service, sourced from the service_summary_5m materialized view.
// Use for time-bucketed reads that span hours/days — the MV merges
// AggregateFunction states cheaply at query time, no raw spans scan.
type ServiceSummaryRow struct {
	Service     string  `json:"service"`
	BucketStart int64   `json:"bucketStart"`  // unix ns
	SpanCount   uint64  `json:"spanCount"`
	ErrorCount  uint64  `json:"errorCount"`
	AvgMs       float64 `json:"avgMs"`
	P50Ms       float64 `json:"p50Ms"`
	P95Ms       float64 `json:"p95Ms"`
	P99Ms       float64 `json:"p99Ms"`
}

// GetServiceSummary5m reads pre-aggregated 5-minute buckets from the MV.
// Suitable for "show last N hours per-service trend" without paying the
// cost of scanning raw span rows. Buckets that haven't materialised yet
// (under 5 minutes old) will be missing — callers should overlay raw
// spans for the most recent window if they need second-fresh numbers.
func (s *Store) GetServiceSummary5m(ctx context.Context, service string, from, to time.Time) ([]ServiceSummaryRow, error) {
	args := []any{from, to}
	svcFilter := ""
	if service != "" {
		svcFilter = " AND service_name = ?"
		args = append(args, service)
	}
	rows, err := s.conn.Query(ctx, `
		SELECT
		  service_name,
		  toUnixTimestamp64Nano(time_bucket)                AS bucket_ns,
		  countMerge(span_count_state)                      AS spans,
		  countIfMerge(error_count_state)                   AS errors,
		  sumMerge(duration_sum_state) / nullIf(countMerge(span_count_state), 0) / 1e6 AS avg_ms,
		  arrayElement(quantilesMerge(0.5, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms,
		  arrayElement(quantilesMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
		  arrayElement(quantilesMerge(0.5, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p99_ms
		FROM service_summary_5m
		WHERE time_bucket >= ? AND time_bucket <= ?`+svcFilter+`
		GROUP BY service_name, time_bucket
		ORDER BY service_name, time_bucket`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceSummaryRow
	for rows.Next() {
		var r ServiceSummaryRow
		if err := rows.Scan(&r.Service, &r.BucketStart, &r.SpanCount, &r.ErrorCount,
			&r.AvgMs, &r.P50Ms, &r.P95Ms, &r.P99Ms); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
