package chstore

// rollout_workloads_test.go — v0.10.1420 — operatör (/rollouts Canlı):
// "Workload seçilerek filtrelenebilsin." Seçicinin sunucu araması SAF
// sorgu kurucusu: kaynak tablosu, CH sınırları (pencere + LIMIT n+1 +
// max_execution_time), desen çevirisi, durum/tür süzgecinin seçiciyi
// daraltmaması. Ayrıca liste süzgecinin workload'ı WHERE'e taşıması
// (v1 + v2) — seçimin SUNUCUDA uygulandığının pini (liste 200'de kesilir;
// istemci süzgeci kesik sayfada satır kaçırırdı).

import (
	"strings"
	"testing"
	"time"
)

func TestRolloutWorkloadPattern(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"orders", "%orders%"},
		{"orders-api", "%orders-api%"},
		{"orders*", "orders%"},
		{"*-api", "%-api"},
		{"ord?rs", "ord_rs"},
		{"a_b", `%a\_b%`},
		{"100%", `%100\%%`},
	} {
		if got := rolloutWorkloadPattern(c.in); got != c.want {
			t.Errorf("rolloutWorkloadPattern(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRolloutWorkloadNamesQuery(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	f := RolloutFilter{ClusterID: "c-1", Namespace: "team-a", Workload: "ignored", Status: "completed", Kind: "Deployment",
		ExcludeNamespaces: []string{"kube-*"}}
	for _, c := range []struct {
		name  string
		v2    bool
		table string
	}{
		{"v1", false, "FROM workload_rollouts WHERE"},
		{"v2", true, "FROM rollout_events WHERE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sql, args := rolloutWorkloadNamesQuery(c.v2, f, "orders", from, to, 50)
			for _, want := range []string{c.table, "started_at >= toDateTime64(?", "started_at <= toDateTime64(?",
				"cluster_id = ?", "namespace = ?", "NOT match(namespace, ?)", "workload ILIKE ?",
				"GROUP BY workload ORDER BY workload LIMIT ?", "SETTINGS max_execution_time = 5"} {
				if !strings.Contains(sql, want) {
					t.Errorf("SQL'de %q yok:\n%s", want, sql)
				}
			}
			// Seçici durum/tür/kendi workload süzgeciyle daralmaz; FINAL yok (DISTINCT ad kümesi aynı).
			for _, bad := range []string{"status", "workload_kind", "workload = ?", "FINAL"} {
				if strings.Contains(sql, bad) {
					t.Errorf("SQL'de %q olmamalı:\n%s", bad, sql)
				}
			}
			if got := args[len(args)-1]; got != 51 {
				t.Errorf("LIMIT bağı %v, want 51 (n+1 nöbetçisi)", got)
			}
			if got := args[len(args)-2]; got != "%orders%" {
				t.Errorf("desen bağı %v", got)
			}
			if strings.Count(sql, "?") != len(args) {
				t.Errorf("yer tutucu %d ≠ bağ %d", strings.Count(sql, "?"), len(args))
			}
		})
	}
	// Boş desen: ILIKE yok.
	sql, _ := rolloutWorkloadNamesQuery(false, RolloutFilter{}, " ", from, to, 50)
	if strings.Contains(sql, "ILIKE") {
		t.Errorf("boş desende ILIKE olmamalı:\n%s", sql)
	}
}

// Liste süzgeci: ?workload= WHERE'e eşitlik olarak iner (v1 + v2).
func TestRolloutListWhereCarriesWorkload(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for name, build := range map[string]func(RolloutFilter, time.Time, time.Time) (string, []any){
		"v1": rolloutWhere, "v2": rolloutV2Where,
	} {
		where, args := build(RolloutFilter{Workload: "orders-api"}, from, to)
		if !strings.Contains(where, "workload = ?") {
			t.Errorf("%s: WHERE workload taşımıyor: %s", name, where)
		}
		found := false
		for _, a := range args {
			if a == "orders-api" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: bağlarda orders-api yok: %v", name, args)
		}
		where, _ = build(RolloutFilter{}, from, to)
		if strings.Contains(where, "workload = ?") {
			t.Errorf("%s: boş workload süzgeci WHERE'e girmemeli: %s", name, where)
		}
	}
}
