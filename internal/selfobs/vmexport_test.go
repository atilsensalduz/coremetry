package selfobs

// v0.10.1422 — operatör: 'Kendi metriclerini de VictoriaMetrics ayarlardaki
// göndersin ve gözüksün'. Dışa aktarıcının üç sözleşmesi: hedef yok/kapalı →
// yazım yok; hata → sayılır ve Export nil döner (SDK her turu loglamasın,
// hiçbir şey beklemesin); VM etkinken collector yolu düşer (tek hedef).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type fakeSink struct {
	active atomic.Bool
	writes atomic.Int64
	err    error
	last   []byte
}

func (f *fakeSink) SelfMetricsActive() bool { return f.active.Load() }
func (f *fakeSink) WriteSelfOTLP(_ context.Context, b []byte) error {
	f.writes.Add(1)
	f.last = b
	return f.err
}

type fakeExporter struct{ exports atomic.Int64 }

func (f *fakeExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}
func (f *fakeExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}
func (f *fakeExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	f.exports.Add(1)
	return nil
}
func (f *fakeExporter) ForceFlush(context.Context) error { return nil }
func (f *fakeExporter) Shutdown(context.Context) error   { return nil }

func sampleRM(t *testing.T) *metricdata.ResourceMetrics {
	t.Helper()
	rd := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(rd))
	c, _ := mp.Meter("t").Int64Counter("worker.job.runs")
	c.Add(context.Background(), 1, metric.WithAttributes(attribute.String("job", "evaluator")))
	var rm metricdata.ResourceMetrics
	if err := rd.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return &rm
}

func TestVMExporterInactiveAndActive(t *testing.T) {
	t.Cleanup(func() { SetVMSink(nil) })
	rm := sampleRM(t)
	e := &vmExporter{}

	SetVMSink(nil)
	if err := e.Export(context.Background(), rm); err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	SetVMSink(sink)
	if err := e.Export(context.Background(), rm); err != nil || sink.writes.Load() != 0 {
		t.Fatalf("inactive sink must not be written (err=%v writes=%d)", err, sink.writes.Load())
	}

	sink.active.Store(true)
	before := VMExportStatusNow().Exported
	if err := e.Export(context.Background(), rm); err != nil || sink.writes.Load() != 1 {
		t.Fatalf("active sink: err=%v writes=%d", err, sink.writes.Load())
	}
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(sink.last, &req); err != nil {
		t.Fatal(err)
	}
	if n := req.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name; n != "worker_job_runs_total" {
		t.Fatalf("name = %q", n)
	}
	st := VMExportStatusNow()
	if !st.Active || st.Exported != before+1 || st.LastOKAt == 0 {
		t.Fatalf("status = %+v", st)
	}
}

func TestVMExporterFailureIsCountedNotReturned(t *testing.T) {
	t.Cleanup(func() { SetVMSink(nil) })
	sink := &fakeSink{err: errors.New("victoriametrics temporarily unavailable: HTTP 503")}
	sink.active.Store(true)
	SetVMSink(sink)
	before := VMExportStatusNow().Failed
	e := &vmExporter{now: func() time.Time { return time.UnixMilli(5000) }}
	done := make(chan error, 1)
	go func() { done <- e.Export(context.Background(), sampleRM(t)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Export must swallow the error (got %v)", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Export blocked")
	}
	st := VMExportStatusNow()
	if st.Failed != before+1 || !strings.Contains(st.LastError, "503") || st.LastErrorAt != 5000 {
		t.Fatalf("status = %+v", st)
	}
}

func TestGatedExporterSingleDestination(t *testing.T) {
	t.Cleanup(func() { SetVMSink(nil) })
	inner := &fakeExporter{}
	g := gatedExporter{inner: inner}
	rm := sampleRM(t)

	SetVMSink(nil)
	_ = g.Export(context.Background(), rm)
	if inner.exports.Load() != 1 {
		t.Fatal("no VM sink → collector path must export")
	}
	sink := &fakeSink{}
	SetVMSink(sink)
	_ = g.Export(context.Background(), rm)
	if inner.exports.Load() != 2 {
		t.Fatal("inactive VM sink → collector path must export")
	}
	sink.active.Store(true)
	_ = g.Export(context.Background(), rm)
	if inner.exports.Load() != 2 {
		t.Fatal("active VM sink → collector path must DROP (double-write)")
	}
}

func TestSplitGRPCMethod(t *testing.T) {
	for in, want := range map[string][2]string{
		"/opentelemetry.proto.collector.trace.v1.TraceService/Export": {"opentelemetry.proto.collector.trace.v1.TraceService", "Export"},
		"svc/M": {"svc", "M"},
		"bare":  {"bare", ""},
	} {
		s, m := SplitGRPCMethod(in)
		if s != want[0] || m != want[1] {
			t.Errorf("%q → (%q,%q)", in, s, m)
		}
	}
}

func TestServiceNameRoles(t *testing.T) {
	for mode, want := range map[string]string{
		"all": "coremetry-monolithic", "": "coremetry-monolithic", "api": "coremetry-api",
		"ingest": "coremetry-ingest", "worker": "coremetry-worker",
		"agent": "coremetry-agent", "sre": "coremetry-sre",
	} {
		if got := serviceName(mode); got != want {
			t.Errorf("serviceName(%q) = %q, want %q", mode, got, want)
		}
	}
}

// Etiket kümesi önbelleği tavanlı: tavan dolunca kayıt sürer, önbellek büyümez.
func TestSetCacheCapped(t *testing.T) {
	var c setCache[int]
	for i := 0; i < setCacheCap+10; i++ {
		s := c.get(i, func() attribute.Set { return attribute.NewSet(attribute.Int("i", i)) })
		if v, _ := s.Value("i"); v.AsInt64() != int64(i) {
			t.Fatalf("wrong set for %d", i)
		}
	}
	if len(c.m) != setCacheCap {
		t.Fatalf("cache size = %d, want cap %d", len(c.m), setCacheCap)
	}
}
