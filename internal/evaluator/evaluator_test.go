package evaluator

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		val, thr float64
		op       string
		want     bool
	}{
		{10, 5, ">", true},
		{5, 10, ">", false},
		{5, 5, ">", false},
		{5, 5, ">=", true},
		{4, 5, ">=", false},
		{4, 5, "<", true},
		{5, 5, "<", false},
		{5, 5, "<=", true},
		{6, 5, "<=", false},
		{5, 5, "==", false}, // unknown op → false (defensive)
		{5, 5, "", false},   // empty op → false
	}
	for _, c := range cases {
		if got := compare(c.val, c.op, c.thr); got != c.want {
			t.Errorf("compare(%v %q %v) = %v, want %v", c.val, c.op, c.thr, got, c.want)
		}
	}
}

func TestTransportFilterRoutesByPrefix(t *testing.T) {
	cases := []struct {
		metric        string
		wantOK        bool
		wantNumerator string
	}{
		{"http_5xx_rate", true, "http_status >= 500"},
		{"http_4xx_rate", true, "http_status >= 400 AND http_status < 500"},
		{"http_error_rate", true, "status_code='error'"},
		{"db_p99_ms", true, "status_code='error'"},
		{"rpc_error_rate", true, "status_code='error'"},
		{"mq_publish_p95_ms", true, "status_code='error'"},
		{"mq_consume_error_rate", true, "status_code='error'"},
		{"request_rate", false, ""}, // not a transport metric
		{"", false, ""},
	}
	for _, c := range cases {
		_, num, ok := transportFilter(c.metric)
		if ok != c.wantOK {
			t.Errorf("transportFilter(%q) ok = %v, want %v", c.metric, ok, c.wantOK)
		}
		if num != c.wantNumerator {
			t.Errorf("transportFilter(%q) numerator = %q, want %q", c.metric, num, c.wantNumerator)
		}
	}
}

func TestTransportOpExtractsSuffix(t *testing.T) {
	cases := map[string]string{
		"http_5xx_rate":    "error_rate",
		"http_p99_ms":      "p99_ms",
		"http_p95_ms":      "p95_ms",
		"http_p50_ms":      "p50_ms",
		"http_avg_ms":      "avg_ms",
		"http_count":       "count",
		"db_p99_ms":        "p99_ms",
		"mq_publish_count": "count",
		"unknown":          "",
		"":                 "",
	}
	for m, want := range cases {
		if got := transportOp(m); got != want {
			t.Errorf("transportOp(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestMetricUnit(t *testing.T) {
	cases := map[string]string{
		"http_p99_ms":     "ms",
		"db_p95_ms":       "ms",
		"http_5xx_rate":   "%",
		"db_error_rate":   "%",
		"request_rate":    "/s",
		"http_count":      "",
		"":                "",
		"weird_metric_xx": "",
	}
	for m, want := range cases {
		if got := metricUnit(m); got != want {
			t.Errorf("metricUnit(%q) = %q, want %q", m, got, want)
		}
	}
}
