package chstore

// rollout_sort_test.go — v0.10.1267 — operatör 2026-10-17: Rollouts sunucu-
// tarafı sıralama (Namespace/Workload/Cluster). Pinler:
//   - ?sort/?dir beyaz listeden SABİT ifadeye eşlenir; tanınmayan değer
//     (SQL parçası dahil) varsayılana düşer — kullanıcı girdisi SQL'e girmez.
//   - Eşitlik kırıcı `started_at DESC` + tablo kimlik kuyruğu (toplam sıra).
//   - ORDER BY LIMIT'ten ÖNCE, v1 ve v2 akışında; varsayılan bugünkü ifade.

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeRolloutSort(t *testing.T) {
	cases := []struct{ sort, dir, wantSort, wantDir string }{
		{"", "", "started", "desc"},
		{"started", "", "started", "desc"},
		{"started", "asc", "started", "asc"},
		{"namespace", "", "namespace", "asc"},
		{"namespace", "desc", "namespace", "desc"},
		{" Workload ", "ASC", "workload", "asc"},
		{"cluster", "sideways", "cluster", "asc"},
		{"cluster_id", "asc", "started", "desc"},                           // ham kolon adı kimlik değil
		{"namespace; DROP TABLE rollout_events", "asc", "started", "desc"}, // enjeksiyon → varsayılan
		{"bogus", "asc", "started", "desc"},                                // geçersiz sort, yönü de sıfırlar
	}
	for _, c := range cases {
		s, d := NormalizeRolloutSort(c.sort, c.dir)
		if s != c.wantSort || d != c.wantDir {
			t.Errorf("NormalizeRolloutSort(%q,%q) = %s.%s, want %s.%s", c.sort, c.dir, s, d, c.wantSort, c.wantDir)
		}
	}
}

func TestRolloutListOrderByWhitelist(t *testing.T) {
	where, _ := rolloutWhere(RolloutFilter{}, time.Unix(100, 0), time.Unix(200, 0))
	whereV2, _ := rolloutV2Where(RolloutFilter{}, time.Unix(100, 0), time.Unix(200, 0))
	cases := []struct {
		sort, dir string
		v1, v2    string // beklenen ORDER BY gövdesi
	}{
		{"", "", "started_at DESC, " + rolloutV1OrderKey, "started_at DESC, " + rolloutV2OrderKey},
		{"started", "asc", "started_at ASC, " + rolloutV1OrderKey, "started_at ASC, " + rolloutV2OrderKey},
		{"namespace", "asc", "namespace ASC, started_at DESC, " + rolloutV1OrderKey, "namespace ASC, started_at DESC, " + rolloutV2OrderKey},
		{"namespace", "desc", "namespace DESC, started_at DESC, " + rolloutV1OrderKey, "namespace DESC, started_at DESC, " + rolloutV2OrderKey},
		{"workload", "asc", "workload ASC, started_at DESC, " + rolloutV1OrderKey, "workload ASC, started_at DESC, " + rolloutV2OrderKey},
		{"cluster", "desc", "cluster_id DESC, started_at DESC, " + rolloutV1OrderKey, "cluster_id DESC, started_at DESC, " + rolloutV2OrderKey},
		{"1; DROP TABLE x --", "asc) --", "started_at DESC, " + rolloutV1OrderKey, "started_at DESC, " + rolloutV2OrderKey},
	}
	for _, c := range cases {
		f := RolloutFilter{Sort: c.sort, Dir: c.dir}
		for _, sq := range []struct{ name, sql, want string }{
			{"v1", rolloutListSQL(where, f), c.v1},
			{"v2", rolloutV2ListSQL(whereV2, f), c.v2},
		} {
			ob := strings.Index(sq.sql, "ORDER BY "+sq.want+" LIMIT ?")
			lim := strings.LastIndex(sq.sql, "LIMIT ?")
			if ob < 0 {
				t.Errorf("%s %s.%s: ORDER BY %q yok:\n%s", sq.name, c.sort, c.dir, sq.want, sq.sql)
				continue
			}
			if ob > lim || strings.Index(sq.sql, "WHERE") > ob {
				t.Errorf("%s %s.%s: ORDER BY WHERE'den sonra, LIMIT'ten önce olmalı:\n%s", sq.name, c.sort, c.dir, sq.sql)
			}
			if strings.Count(sq.sql, "ORDER BY") != 1 || strings.Contains(sq.sql, "DROP") || strings.Contains(sq.sql, "--") {
				t.Errorf("%s %s.%s: kullanıcı girdisi SQL'e sızdı:\n%s", sq.name, c.sort, c.dir, sq.sql)
			}
		}
	}
	// Varsayılan v1 ifadesi v0.10.1267 öncesiyle bayt bayt aynı.
	if !strings.Contains(rolloutListSQL(where, RolloutFilter{}), "ORDER BY started_at DESC, cluster_id, namespace, workload, revision LIMIT ? SETTINGS max_execution_time = 10") {
		t.Fatal("v1 varsayılan sıralama değişmiş")
	}
}
