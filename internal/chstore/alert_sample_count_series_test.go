package chstore

import (
	"strings"
	"testing"
)

// v0.10.1495 — problem grafiğinin minSamples sayımı değerlendiricinin
// measureCount'unun ikizi: ≥ 5 dk pencere service_summary_5m span_count_state,
// altı ham spans count(); her okuma LIMIT + max_execution_time taşır; pencere
// ölçeklemesi değerlendiricinin (v0.8.315) sayım ölçeklemesi.
func TestAlertSampleCountPlan(t *testing.T) {
	for _, tc := range []struct {
		raw          bool
		window       int
		table, inner string
	}{
		{false, 300, "FROM service_summary_5m", "countMerge(span_count_state)"},
		{true, 120, "FROM spans", "count() AS e"},
	} {
		p := alertSampleCountPlan(tc.raw)
		sp, err := AlertMetricSeriesSpec("p99_ms", tc.window)
		if err != nil {
			t.Fatal(err)
		}
		sql := alertMetricSeriesSQL(p, sp)
		for _, want := range []string{tc.table, tc.inner, "LIMIT 2000", "max_execution_time", "WHERE service_name = ?"} {
			if !strings.Contains(sql, want) {
				t.Errorf("raw=%v: SQL %q içermiyor:\n%s", tc.raw, want, sql)
			}
		}
		if p.scan != seriesScanCountScaled {
			t.Errorf("raw=%v: sayım pencereye ölçeklenmeli", tc.raw)
		}
	}
}
