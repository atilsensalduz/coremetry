package otlpenc

// v0.10.1422 — operatör: 'Kendi metriclerini de VictoriaMetrics ayarlardaki
// göndersin ve gözüksün'. VM'e giden gövdenin ŞEKLİ: frontend'in metrik kipi
// http_server_request_duration_seconds_* + service_name okur; ad/etiket
// burada yanlışsa hiçbir şey patlamaz, servis sayfası "veri yok" der.

import (
	"context"
	"sort"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestPromMetricName(t *testing.T) {
	for _, c := range []struct {
		name, unit string
		k          Kind
		want       string
	}{
		{"http.server.request.duration", "s", KindHistogram, "http_server_request_duration_seconds"},
		{"rpc.server.duration", "ms", KindHistogram, "rpc_server_duration_milliseconds"},
		{"op_baseline_refresh_duration_seconds", "s", KindHistogram, "op_baseline_refresh_duration_seconds"},
		{"ldap_sync_errors_total", "", KindCounter, "ldap_sync_errors_total"},
		{"worker.job.runs", "", KindCounter, "worker_job_runs_total"},
		{"process.cpu.time", "s", KindCounter, "process_cpu_time_seconds_total"},
		{"foo_total", "s", KindCounter, "foo_seconds_total"},
		{"go.memory.used", "By", KindGauge, "go_memory_used_bytes"},
		{"cpu.utilization", "1", KindGauge, "cpu_utilization_ratio"},
		{"requests", "1", KindCounter, "requests_total"},
		{"queue.depth", "{item}", KindGauge, "queue_depth"},
		{"9lives", "", KindGauge, "_9lives"},
		{"", "s", KindGauge, ""},
	} {
		got := PromMetricName(c.name, c.unit, c.k)
		if got != c.want {
			t.Errorf("PromMetricName(%q,%q,%d) = %q, want %q", c.name, c.unit, c.k, got, c.want)
		}
		// İdempotent: VM'in usePrometheusNaming'i (birim boş) adı değiştirmez.
		if again := PromMetricName(got, "", c.k); again != got {
			t.Errorf("not idempotent: %q → %q", got, again)
		}
	}
}

func collect(t *testing.T, record func(m metric.Meter)) *metricdata.ResourceMetrics {
	t.Helper()
	res := resource.NewSchemaless(
		attribute.String("service.name", "coremetry-api"),
		attribute.String("service.version", "v0.0.1"),
		attribute.String("service.instance.id", "pod-a"),
		attribute.String("deployment.environment", "test-env"),
		attribute.String("k8s.pod.name", "coremetry-api-0"),
		attribute.String("k8s.pod.uid", "uid-1"),
		attribute.String("telemetry.sdk.language", "go"),
	)
	rd := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res), sdkmetric.WithReader(rd))
	record(mp.Meter("coremetry"))
	var rm metricdata.ResourceMetrics
	if err := rd.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return &rm
}

func labelKeys(kvs []*commonpb.KeyValue) []string {
	out := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		out = append(out, kv.GetKey())
	}
	sort.Strings(out)
	return out
}

func TestEncodeShape(t *testing.T) {
	rm := collect(t, func(m metric.Meter) {
		h, _ := m.Float64Histogram("http.server.request.duration", metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.1, 1))
		h.Record(context.Background(), 0.02, metric.WithAttributes(
			attribute.String("http.request.method", "GET"),
			attribute.String("http.route", "/api/services"),
			attribute.Int("http.response.status_code", 200)))
		c, _ := m.Int64Counter("ldap_sync_errors_total")
		c.Add(context.Background(), 2)
		g, _ := m.Float64Gauge("go.memory.used", metric.WithUnit("By"))
		g.Record(context.Background(), 1024)
	})
	body, st, err := Marshal(rm)
	if err != nil || body == nil {
		t.Fatalf("marshal: %v body=%v", err, body)
	}
	if st.Metrics != 3 || st.Skipped != 0 {
		t.Fatalf("stats = %+v", st)
	}
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.ResourceMetrics) != 1 {
		t.Fatalf("resource metrics = %d", len(req.ResourceMetrics))
	}
	if n := len(req.ResourceMetrics[0].GetResource().GetAttributes()); n != 0 {
		t.Fatalf("resource must be EMPTY (VM would label every series with it), got %d attrs", n)
	}
	byName := map[string]*metricspb.Metric{}
	for _, sm := range req.ResourceMetrics[0].ScopeMetrics {
		for _, m := range sm.Metrics {
			byName[m.Name] = m
			if m.Unit != "" {
				t.Errorf("%s unit = %q, want empty (usePrometheusNaming would re-suffix)", m.Name, m.Unit)
			}
		}
	}
	hm := byName["http_server_request_duration_seconds"]
	if hm == nil {
		t.Fatalf("histogram name missing: %v", keys(byName))
	}
	hist := hm.GetHistogram()
	if hist.AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		t.Fatalf("temporality = %v", hist.AggregationTemporality)
	}
	dp := hist.DataPoints[0]
	got := labelKeys(dp.Attributes)
	// lib/httpMetrics.ts HTTP_LABELS (service/route/method/status) + ortam
	// süzgeci (deployment_environment_name) + izinli resource etiketleri.
	want := []string{"deployment_environment_name", "http_request_method", "http_response_status_code",
		"http_route", "k8s_pod_name", "service_instance_id", "service_name", "service_version"}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels = %v, want %v", got, want)
		}
	}
	for _, kv := range dp.Attributes {
		if kv.Key == "service_name" && kv.Value.GetStringValue() != "coremetry-api" {
			t.Fatalf("service_name = %q", kv.Value.GetStringValue())
		}
		if kv.Key == "http_response_status_code" && kv.Value.GetStringValue() != "200" {
			t.Fatalf("status = %q", kv.Value.GetStringValue())
		}
	}
	if dp.Count != 1 || dp.GetSum() != 0.02 || len(dp.ExplicitBounds) != 4 || len(dp.BucketCounts) != 5 {
		t.Fatalf("histogram point = %+v", dp)
	}
	if byName["ldap_sync_errors_total"] == nil {
		t.Fatalf("counter double-suffixed? %v", keys(byName))
	}
	if !byName["ldap_sync_errors_total"].GetSum().IsMonotonic {
		t.Fatal("counter lost monotonic flag")
	}
	if byName["go_memory_used_bytes"] == nil {
		t.Fatalf("gauge unit suffix missing: %v", keys(byName))
	}
}

func TestEncodeEmpty(t *testing.T) {
	if b, _, err := Marshal(nil); b != nil || err != nil {
		t.Fatal("nil input must produce nothing")
	}
	if b, _, err := Marshal(&metricdata.ResourceMetrics{}); b != nil || err != nil {
		t.Fatal("empty input must produce nothing")
	}
}

func TestEncodeSkipsExponential(t *testing.T) {
	rm := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: []metricdata.Metrics{
		{Name: "x", Data: metricdata.ExponentialHistogram[float64]{}},
	}}}}
	req, st := Encode(rm)
	if req != nil || st.Skipped != 1 {
		t.Fatalf("req=%v stats=%+v", req, st)
	}
}

func keys(m map[string]*metricspb.Metric) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
