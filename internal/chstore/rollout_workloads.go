package chstore

// rollout_workloads.go — v0.10.1420 (operatör, /rollouts Canlı sekmesi:
// "Workload seçilerek filtrelenebilsin").
//
// /rollouts Workload seçicisinin SUNUCU araması: pencerede rollout'u olan
// iş yüklerinin DISTINCT adları, okuma kaynağının tablosundan (v1
// workload_rollouts | v2 rollout_events). Seçici hiçbir zaman tam katalog
// çekmez (CLAUDE.md "Picker = server-side search"); bu uç yazılan desene
// göre en çok RolloutWorkloadNamesMax ad döndürür.
//
// NEDEN /api/entities DEĞİL: envanter ucu cluster ister (Cluster "tümü"
// iken kullanılamaz), ayrı bayrağa bağlı ve ŞU ANKİ envanteri listeler —
// pencerede rollout'u olmayan bir ad seçmek boş liste üretirdi.
//
// Sınırlar (ev kuralı): started_at penceresi + LIMIT + max_execution_time.
// FINAL YOK: ReplacingMergeTree'nin birleşmemiş sürümleri aynı anahtarı
// (cluster_id, namespace, workload, …) taşır — DISTINCT ad kümesi FINAL'siz
// de aynıdır, FINAL yalnız maliyet eklerdi. LIMIT n+1 nöbetçisi "tam
// tavanda" ile "kesildi"yi ayırır.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RolloutWorkloadNamesMax — tek aramanın döndürdüğü en çok ad.
const RolloutWorkloadNamesMax = 200

// rolloutWorkloadPattern — SAF: seçici deseni → ILIKE deseni. Joker
// karakterler ServicePicker sözleşmesi (* → %, ? → _); jokersiz desen
// alt-dizge araması. Desenin LIKE metakarakterleri (\ % _) kaçırılır:
// "a_b" yazan operatör "axb"yi görmesin.
func rolloutWorkloadPattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	like := strings.NewReplacer(`*`, `%`, `?`, `_`).Replace(escapeLike(q))
	if !strings.ContainsAny(q, "*?") {
		like = "%" + like + "%"
	}
	return like
}

// rolloutWorkloadNamesQuery — SAF: SQL + bağlar. Süzgeç yalnız cluster /
// namespace / platform dışlaması + desen okur (durum/tür seçiciyi
// daraltmaz — operatör önce adı seçer). limit çağıranda kelepçeli gelir.
func rolloutWorkloadNamesQuery(v2 bool, f RolloutFilter, q string, from, to time.Time, limit int) (string, []any) {
	base := RolloutFilter{ClusterID: f.ClusterID, Namespace: f.Namespace, ExcludeNamespaces: f.ExcludeNamespaces}
	table := "workload_rollouts"
	where, args := rolloutWhere(base, from, to)
	if v2 {
		table = "rollout_events"
		where, args = rolloutV2Where(base, from, to)
	}
	if like := rolloutWorkloadPattern(q); like != "" {
		where += " AND workload ILIKE ?"
		args = append(args, like)
	}
	args = append(args, limit+1)
	return `SELECT workload FROM ` + table + ` WHERE ` + where + `
		GROUP BY workload ORDER BY workload LIMIT ? SETTINGS max_execution_time = 5`, args
}

// RolloutWorkloadNames — pencerede rollout'u olan workload adları
// (alfabetik). capped: tavandan fazlası vardı.
func (s *Store) RolloutWorkloadNames(ctx context.Context, v2 bool, f RolloutFilter, q string, from, to time.Time, limit int) ([]string, bool, error) {
	limit = clampRolloutLimit(limit, 50, RolloutWorkloadNamesMax)
	sql, args := rolloutWorkloadNamesQuery(v2, f, q, from, to, limit)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, fmt.Errorf("rollout workload names: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, false, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	capped := len(out) > limit
	if capped {
		out = out[:limit]
	}
	return out, capped, nil
}
