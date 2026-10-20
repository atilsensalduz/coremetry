package selfobs

// v0.10.1422 — operatör: "Coremetry api, worker ve ingester için yap". Rol
// başına enstrümanların adı, birimi ve SINIRLI etiket kümesi. Bu paketteki
// tek test olarak meterSink'i ManualReader'a bağlar (enstrümanlar ilk
// kayıtta sync.Once ile kurulur).

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRoleInstruments(t *testing.T) {
	rd := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd))
	prev := meterSink
	meterSink = mp.Meter("coremetry")
	t.Cleanup(func() { meterSink = prev })

	RecordHTTPServer("GET", "/api/services", 200, 20*time.Millisecond)
	RecordHTTPServer("POST", "", 404, time.Millisecond)
	RecordRPCServer("/opentelemetry.proto.collector.trace.v1.TraceService/Export", 0, 3*time.Millisecond)
	ObserveJob("evaluator", time.Now().Add(-time.Second), nil)
	ObserveJob("correlator", time.Now(), errors.New("boom"))
	ObserveSREInvestigation("done", time.Minute)

	var rm metricdata.ResourceMetrics
	if err := rd.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	type info struct {
		unit  string
		attrs [][]string
	}
	got := map[string]info{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			in := info{unit: m.Unit}
			add := func(keys []string) { sort.Strings(keys); in.attrs = append(in.attrs, keys) }
			switch d := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					var ks []string
					for _, kv := range dp.Attributes.ToSlice() {
						ks = append(ks, string(kv.Key))
					}
					add(ks)
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					var ks []string
					for _, kv := range dp.Attributes.ToSlice() {
						ks = append(ks, string(kv.Key))
					}
					add(ks)
				}
			}
			got[m.Name] = in
		}
	}
	want := map[string]string{
		"http.server.request.duration": "s",
		"rpc.server.duration":          "ms",
		"worker.job.duration":          "s",
		"worker.job.runs":              "",
		"sre.investigation.duration":   "s",
		"sre.investigations":           "",
	}
	for n, u := range want {
		in, ok := got[n]
		if !ok {
			t.Errorf("missing %s (have %v)", n, got)
			continue
		}
		if in.unit != u {
			t.Errorf("%s unit = %q, want %q", n, in.unit, u)
		}
	}
	http := got["http.server.request.duration"].attrs
	if len(http) != 2 {
		t.Fatalf("http points = %v", http)
	}
	// Eşleşmeyen istek route etiketi TAŞIMAZ (kardinalite).
	for _, ks := range http {
		for _, k := range ks {
			switch k {
			case "http.request.method", "http.response.status_code", "http.route":
			default:
				t.Errorf("unexpected http label %q", k)
			}
		}
	}
	rpc := got["rpc.server.duration"].attrs
	if len(rpc) != 1 || len(rpc[0]) != 4 {
		t.Fatalf("rpc labels = %v", rpc)
	}
	if runs := got["worker.job.runs"].attrs; len(runs) != 2 {
		t.Fatalf("job runs points = %v (ok + error)", runs)
	}
}
