package chstore

// fleet_errors.go — v0.10.1255 (operatör: CoSRE karşılamasında P1 yerine
// "son 1 saatteki exception / hata sayısı"). Filo geneli hatalı span sayısı,
// service_summary_5m MV'sinden (MV-first; ham spans taraması yok).

import (
	"context"
	"time"
)

// fleetErrorCountSQL — SAF (pinli): pencere sınırlı + LIMIT + süre tavanı.
func fleetErrorCountSQL() string {
	return `SELECT countIfMerge(error_count_state) AS errs
		FROM service_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?
		LIMIT 1
		SETTINGS max_execution_time = 10`
}

// FleetErrorCount — [from, to) içinde filo geneli hatalı span sayısı.
func (s *Store) FleetErrorCount(ctx context.Context, from, to time.Time) (uint64, error) {
	var n uint64
	err := s.telemetryReadConn().QueryRow(ctx, fleetErrorCountSQL(), alignBucketStart(from), to).Scan(&n)
	return n, err
}
