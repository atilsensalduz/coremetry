package chstore

import "context"

// knownServiceNamesSQL — v0.10.1344: ext-peer çözümünün bilinen-servis
// yedeği (api knownServiceSet, 60 sn bellek). MV okuması (service_summary_5m,
// LowCardinality service_name), zaman sınırlı, LIMIT + max_execution_time
// (CLAUDE.md CH sınırları). Pinli: TestKnownServiceNamesSQLPinned.
const knownServiceNamesSQL = `SELECT DISTINCT service_name FROM service_summary_5m ` +
	`WHERE time_bucket >= now() - INTERVAL 1 DAY LIMIT 20000 SETTINGS max_execution_time = 5`

// KnownServiceNames — son 1 günde span üretmiş servis adları (en çok 20000).
func (s *Store) KnownServiceNames(ctx context.Context) ([]string, error) {
	rows, err := s.telemetryReadConn().Query(ctx, knownServiceNamesSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if n != "" {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}
