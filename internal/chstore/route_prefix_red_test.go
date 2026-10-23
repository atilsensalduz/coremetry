package chstore

// v0.10.1482 — önek kipi SQL pini: MV-first, sınırlı, giriş-noktası yüklemi.

import (
	"strings"
	"testing"
	"time"
)

func TestRoutePrefixQueries(t *testing.T) {
	to := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	q, args := routePrefixRowsQuery("orders-api", "/api/orders/", from, to, 11)
	for _, w := range []string{"FROM spanmetrics_1m", "service_name = ? AND (http_route = ? OR startsWith(http_route, ?))",
		"kind NOT IN ('client', 'producer')", "time_bucket >= ? AND time_bucket < ?", "GROUP BY service_name, http_route", "LIMIT ?", "max_execution_time"} {
		if !strings.Contains(q, w) {
			t.Errorf("rows SQL %q içermeli:\n%s", w, q)
		}
	}
	if len(args) != 6 || args[0] != "orders-api" || args[1] != "/api/orders" || args[2] != "/api/orders/" || args[5] != 11 {
		t.Errorf("bind: %v", args)
	}
	if _, a := routePrefixRowsQuery("", "/api", from, to, 999); a[len(a)-1] != RoutePrefixMaxRows || len(a) != 5 {
		t.Errorf("tavan / servissiz: %v", a)
	}
	tq, targs := routePrefixTotalsQuery("", "/api", from.Add(-time.Hour), from, to)
	for _, w := range []string{"if(time_bucket >= ?, 'cur', 'prev')", "uniqExact(service_name, http_route)", "GROUP BY win", "LIMIT 2", "max_execution_time"} {
		if !strings.Contains(tq, w) {
			t.Errorf("totals SQL %q içermeli:\n%s", w, tq)
		}
	}
	if len(targs) != 5 || targs[0] != from || targs[3] != from.Add(-time.Hour) || targs[4] != to {
		t.Errorf("totals bind: %v", targs)
	}
}
