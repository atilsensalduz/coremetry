package chstore

// oracle_op_traces.go — v0.10.1388 (CoSRE operasyon topolojisi): Oracle-only
// operasyonun (span'i örneklemeye girmemiş olabilir) en yeni trace id'leri.
// Topoloji demeti bu id'leri Tempo-önce çözer (api/copilot_service_topology.go,
// v0.10.1369/1370 deseni). PK öneki (source_id, time) taraması + operasyon
// eşitliği; LIMIT + max_execution_time.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// oracleOpTraceIDsSQL — SAF: operasyonun ayrık trace id'leri, en yeni önce.
func oracleOpTraceIDsSQL(limit int) string {
	return fmt.Sprintf(`SELECT trace_id, max(time) AS last_seen
		FROM oracle_error_log
		WHERE source_id = ? AND time >= ? AND time < ?
		  AND trimBoth(operation_code) = ? AND trace_id != ''
		GROUP BY trace_id
		ORDER BY last_seen DESC
		LIMIT %d
		SETTINGS max_execution_time = %d`, clampOracleErrorsLimit(limit), oracleErrorsMaxExecSec)
}

// OracleTraceIDsForOperation — kaynağın operasyon satırlarındaki trace id'leri
// (küçük harf, en yeni önce, ≤limit). Boş kaynak/operasyon ya da ters pencere
// → boş dilim, sorgu YOK.
func (s *Store) OracleTraceIDsForOperation(ctx context.Context, sourceID, op string, from, to time.Time, limit int) ([]string, error) {
	op = strings.TrimSpace(op)
	if sourceID == "" || op == "" || !to.After(from) {
		return []string{}, nil
	}
	rows, err := s.conn.Query(ctx, oracleOpTraceIDsSQL(limit), sourceID, from, to, op)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var last time.Time
		if err := rows.Scan(&id, &last); err != nil {
			return nil, err
		}
		if id = normalizeLogID(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
