package chstore

// v0.10.1299 — operatör 2026-10-17: 5 dk p99 gürültüsü yanlış bulgu üretiyordu.
// Üç okuyucunun SQL disiplin pinleri (CLAUDE.md: time-bounded WHERE + LIMIT +
// max_execution_time) + saf katlama yardımcıları:
//   - SlowSpanCorroborateSQL — ham span doğrulaması (spans; ≤ 3 s)
//   - opLatencySeriesSQL     — operasyon grafiği (spanmetrics_1m, state birleştirme)
//   - rollupREDSQL WindowTotals — pencere birleşik kuantil (GROUPING SETS)
// SQL'ler clickhouse local'de sentetik tablolarla koşturuldu (geliştirme sırasında).

import (
	"strings"
	"testing"
	"time"
)

func TestSlowSpanCorroborateSQL(t *testing.T) {
	from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_007_200, 0)
	sql, args := SlowSpanCorroborateSQL("svc", "SELECT db0p.WGT_TKART", 150_000_000, from, to, 1)
	for _, want := range []string{
		"FROM spans", "service_name = ?", "name = ?", "time >= ?", "time < ?", "duration >= ?",
		"toStartOfMinute(time)", "argMax(trace_id, duration)", "LIMIT 1500", "max_execution_time = 3",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermeli:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "cityHash64") {
		t.Error("≤ 6 sa aralıkta örnekleme olmamalı")
	}
	if len(args) != 5 || args[0] != "svc" || args[4] != int64(150_000_000) {
		t.Fatalf("args %v", args)
	}
	sql, _ = SlowSpanCorroborateSQL("svc", "op", 1, from, from.Add(12*time.Hour), SlowCorrobSampleN(from, from.Add(12*time.Hour)))
	if !strings.Contains(sql, "cityHash64(trace_id) % 2 = 0") {
		t.Errorf("12 sa → 1/2 örnekleme:\n%s", sql)
	}
	for _, c := range []struct {
		d    time.Duration
		want int
	}{{time.Hour, 1}, {6 * time.Hour, 1}, {7 * time.Hour, 2}, {12 * time.Hour, 2}, {13 * time.Hour, 3}} {
		if got := SlowCorrobSampleN(from, from.Add(c.d)); got != c.want {
			t.Errorf("SampleN(%s) = %d, want %d", c.d, got, c.want)
		}
	}
}

func TestFoldSlowMinutes(t *testing.T) {
	curFrom := time.Unix(1_700_003_600, 0)
	rows := []slowMinuteRow{
		{m: 1_700_000_000, n: 3, maxNs: 900e6, exemplar: "old"},
		{m: 1_700_003_600, n: 5, maxNs: 400e6, exemplar: "a"},
		{m: 1_700_003_660, n: 7, maxNs: 800e6, exemplar: "b"},
	}
	got := foldSlowMinutes(rows, curFrom, 2, 150)
	if got.CurSlow != 24 || got.PriorSlow != 6 || got.MaxMs != 800 || got.ExemplarID != "b" || len(got.Minutes) != 2 || got.Minutes[1].Count != 14 {
		t.Fatalf("%+v", got)
	}
}

func TestOpLatencySeriesSQL(t *testing.T) {
	sql := opLatencySeriesSQL("spanmetrics_1m", 180)
	for _, want := range []string{
		"FROM spanmetrics_1m", "INTERVAL 180 SECOND", "quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state)",
		"service_name = ? AND name = ? AND time_bucket >= ? AND time_bucket < ?", "LIMIT 2000", "max_execution_time = 10",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL %q içermeli:\n%s", want, sql)
		}
	}
	now := time.Unix(1_800_000_000, 0)
	cov := now.Add(-48 * time.Hour)
	if !opLatencyCovered(now.Add(-3*time.Hour), cov, now) || opLatencyCovered(now.Add(-72*time.Hour), cov, now) ||
		opLatencyCovered(now.Add(-31*24*time.Hour), now.Add(-60*24*time.Hour), now) {
		t.Fatal("kapsam: coverage başı ve 30 g TTL")
	}
}

func TestRollupREDSQLWindowTotals(t *testing.T) {
	from, to := time.Unix(1000, 0), time.Unix(2000, 0)
	for _, mode := range []string{"tdigest", "buckets"} {
		sql, _, err := rollupREDSQL(rtestPlan(mode), RollupSeriesFilter{Service: "s", GroupBy: "function_code", WindowTotals: true}, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sql, "GROUP BY GROUPING SETS ((tb, grp), (grp))") || !strings.Contains(sql, "LIMIT 250000") {
			t.Errorf("%s: pencere toplamı GROUPING SETS ile gelmeli:\n%s", mode, sql)
		}
		plain, _, _ := rollupREDSQL(rtestPlan(mode), RollupSeriesFilter{Service: "s", GroupBy: "function_code"}, from, to)
		if strings.Contains(plain, "GROUPING SETS") {
			t.Errorf("%s: WindowTotals yokken SQL eskisiyle aynı kalmalı", mode)
		}
	}
	sql, _, _ := rollupREDSQL(rtestPlan("tdigest"), RollupSeriesFilter{Service: "s", WindowTotals: true}, from, to)
	if !strings.Contains(sql, "GROUP BY GROUPING SETS ((tb), ())") {
		t.Errorf("grupsuz toplam:\n%s", sql)
	}
}
