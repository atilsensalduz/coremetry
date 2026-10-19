package chstore

// v0.10.1391 — operatör: "Prod sağlığı güzel ama VictoriaMetrics getirmedi, en
// azından HTTP metriclerimi getirebilirdi. Onun yerine gRPC getirdi." Tarama
// grafiği seçimi için operasyon türü okuması: MV (spanmetrics_1m), sınırlı.

import (
	"strings"
	"testing"
)

func TestOpKindsSQLBounded(t *testing.T) {
	q := opKindsSQL("spanmetrics_1m")
	for _, w := range []string{"FROM spanmetrics_1m", "service_name = ?", "name IN (?)", "time_bucket >= ?", "time_bucket < ?",
		"GROUP BY name", "LIMIT 16", "max_execution_time = 3", "groupUniqArray(kind)"} {
		if !strings.Contains(q, w) {
			t.Errorf("opKindsSQL %q içermeli:\n%s", w, q)
		}
	}
	for _, bad := range []string{"FROM spans", "coremetry."} {
		if strings.Contains(q, bad) {
			t.Errorf("opKindsSQL %q içermemeli (MV-first, nitelenmemiş tablo)", bad)
		}
	}
}
