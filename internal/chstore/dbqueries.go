package chstore

import (
	"context"
	"fmt"
	"time"
)

// DBQueryStat is one row in the database query analyzer — a
// single normalized statement aggregated across every span
// that issued it for the given service in the time window.
//
// Normalisation collapses literal-only differences ("WHERE
// id = 1" vs "WHERE id = 2") so a single hot query surfaces
// as one row rather than thousands of near-duplicates. The
// sample statement keeps a real example so the operator can
// see what literals were involved without losing the
// aggregation benefit.
type DBQueryStat struct {
	// Normalised statement — literals replaced with "?". Used
	// as the GROUP BY key in CH and as the row label in the UI.
	Statement string `json:"statement"`
	// One real (non-normalised) example of the query so the
	// operator sees actual values, not just placeholders.
	SampleStatement string `json:"sampleStatement"`
	DBSystem        string `json:"dbSystem"`
	// Span counts + latency stats for the bucket.
	Count      int     `json:"count"`
	AvgMs      float64 `json:"avgMs"`
	P95Ms      float64 `json:"p95Ms"`
	P99Ms      float64 `json:"p99Ms"`
	MaxMs      float64 `json:"maxMs"`
	ErrorCount int     `json:"errorCount"`
	// TotalMs = count × avgMs — the aggregate wall-clock cost
	// of this query class in the window. Sorting by total ms
	// surfaces the queries actually worth optimising (a 50ms
	// query running 10k times is a bigger problem than a 500ms
	// one running once, but the second one beats it on max).
	TotalMs float64 `json:"totalMs"`
}

// GetTopDBQueries returns the top-N normalized DB statements
// for the given service in the time window, ordered by total
// wall-clock time spent in them (count × avgMs).
//
// Performance posture: the query reads only spans where
// db_statement != '' (a small slice of total span volume),
// applies regex normalisation in CH (no Go-side post-pass),
// groups in-store, and the result is bounded by `limit`. At
// billion-span scale it lands in <2s with the (service_name,
// time) primary key handling the partition pruning.
//
// The two replaceRegexpAll passes:
//
//   1. Replace single-quoted string literals with "?". A
//      bracketed character class with negation handles
//      embedded apostrophes badly, but the simple form covers
//      the vast majority of ORM-emitted SQL — and pathological
//      cases just produce an extra normalisation cluster
//      rather than an incorrect result.
//   2. Replace integer / decimal numeric literals with "?".
//      Boundary anchors (\\b) prevent munging column names
//      that happen to end in digits ("col1" stays intact).
//
// IN-list collapse and parameter-binding placeholders ($1 / ?N)
// are left as-is — they're not literals, they're already
// normalised forms.
func (s *Store) GetTopDBQueries(
	ctx context.Context, service string, from, to time.Time, limit int,
) ([]DBQueryStat, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const sql = `
		SELECT
			replaceRegexpAll(
				replaceRegexpAll(db_statement, '''[^'']*''', '?'),
				'\\b[0-9]+(\\.[0-9]+)?\\b', '?'
			)                                          AS norm_stmt,
			any(db_statement)                          AS sample_stmt,
			any(db_system)                             AS db_system,
			count()                                    AS cnt,
			avg(duration / 1e6)                        AS avg_ms,
			quantile(0.95)(duration / 1e6)             AS p95_ms,
			quantile(0.99)(duration / 1e6)             AS p99_ms,
			max(duration / 1e6)                        AS max_ms,
			countIf(status_code = 'error')             AS err_cnt
		FROM spans
		WHERE service_name = ?
		  AND time >= ? AND time <= ?
		  AND db_statement != ''
		GROUP BY norm_stmt
		ORDER BY (cnt * avg_ms) DESC
		LIMIT ?
		SETTINGS max_execution_time = 30`
	rows, err := s.conn.Query(ctx, sql, service, from, to, limit)
	if err != nil {
		return nil, fmt.Errorf("query db queries: %w", err)
	}
	defer rows.Close()

	out := []DBQueryStat{}
	for rows.Next() {
		var r DBQueryStat
		var cnt uint64
		var errCnt uint64
		if err := rows.Scan(&r.Statement, &r.SampleStatement, &r.DBSystem,
			&cnt, &r.AvgMs, &r.P95Ms, &r.P99Ms, &r.MaxMs, &errCnt); err != nil {
			return nil, err
		}
		r.Count = int(cnt)
		r.ErrorCount = int(errCnt)
		r.TotalMs = float64(cnt) * r.AvgMs
		out = append(out, r)
	}
	return out, rows.Err()
}
