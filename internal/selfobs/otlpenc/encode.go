package otlpenc

import (
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// promotedResource — her veri noktasına etiket olarak kopyalanan resource
// anahtarları (izin listesi) ve VM'deki adları. Liste DAR: telemetry.sdk.*,
// process.* gibi sabit ama işe yaramaz resource alanları her seriye etiket
// olarak yazılmaz (VM'in resource→etiket dönüşümü hepsini yazardı — bu
// yüzden çıkan Resource BOŞ gönderilir). k8s.pod.uid bilinçli dışarıda:
// pod adı zaten pod başına tekil, uid ikinci bir eşdeğer etiket olurdu.
//
// deployment.environment (selfobs'un yazdığı ESKİ anahtar) ve yeni
// deployment.environment.name İKİSİ de `deployment_environment_name`'e
// gider — frontend'in ortam süzgeci (http-env-scope) o etiketi okur.
var promotedResource = []struct{ key, label string }{
	{"service.name", "service_name"},
	{"service.version", "service_version"},
	{"service.instance.id", "service_instance_id"},
	{"deployment.environment.name", "deployment_environment_name"},
	{"deployment.environment", "deployment_environment_name"},
	{"k8s.namespace.name", "k8s_namespace_name"},
	{"k8s.pod.name", "k8s_pod_name"},
	{"k8s.node.name", "k8s_node_name"},
}

// Stats — bir Encode turunun sessiz düşüşleri (sayaçlara gider).
type Stats struct {
	Metrics int // yazılan metrik sayısı
	Skipped int // desteklenmeyen tür (üstel histogram / summary)
}

// Encode — ResourceMetrics → ExportMetricsServiceRequest. Boş girdi nil döner.
func Encode(rm *metricdata.ResourceMetrics) (*colmetricspb.ExportMetricsServiceRequest, Stats) {
	var st Stats
	if rm == nil {
		return nil, st
	}
	res := promotedLabels(rm)
	out := &metricspb.ResourceMetrics{Resource: &resourcepb.Resource{}}
	for _, sm := range rm.ScopeMetrics {
		psm := &metricspb.ScopeMetrics{Scope: &commonpb.InstrumentationScope{
			Name: sm.Scope.Name, Version: sm.Scope.Version,
		}}
		for _, m := range sm.Metrics {
			pm := encodeMetric(m, res)
			if pm == nil {
				st.Skipped++
				continue
			}
			psm.Metrics = append(psm.Metrics, pm)
			st.Metrics++
		}
		if len(psm.Metrics) > 0 {
			out.ScopeMetrics = append(out.ScopeMetrics, psm)
		}
	}
	if len(out.ScopeMetrics) == 0 {
		return nil, st
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{out}}, st
}

// Marshal — Encode + protobuf. (nil, st, nil) = gönderilecek bir şey yok.
func Marshal(rm *metricdata.ResourceMetrics) ([]byte, Stats, error) {
	req, st := Encode(rm)
	if req == nil {
		return nil, st, nil
	}
	b, err := proto.Marshal(req)
	return b, st, err
}

func promotedLabels(rm *metricdata.ResourceMetrics) []*commonpb.KeyValue {
	if rm.Resource == nil {
		return nil
	}
	var out []*commonpb.KeyValue
	seen := map[string]bool{}
	for _, p := range promotedResource {
		v, ok := rm.Resource.Set().Value(attribute.Key(p.key))
		if !ok || seen[p.label] {
			continue
		}
		s := v.String()
		if s == "" {
			continue
		}
		seen[p.label] = true
		out = append(out, strKV(p.label, s))
	}
	return out
}

func encodeMetric(m metricdata.Metrics, res []*commonpb.KeyValue) *metricspb.Metric {
	pm := &metricspb.Metric{Description: m.Description} // Unit BİLEREK boş
	switch d := m.Data.(type) {
	case metricdata.Gauge[int64]:
		pm.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: numberPoints(d.DataPoints, res)}}
		pm.Name = PromMetricName(m.Name, m.Unit, KindGauge)
	case metricdata.Gauge[float64]:
		pm.Data = &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: numberPoints(d.DataPoints, res)}}
		pm.Name = PromMetricName(m.Name, m.Unit, KindGauge)
	case metricdata.Sum[int64]:
		pm.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			DataPoints: numberPoints(d.DataPoints, res), IsMonotonic: d.IsMonotonic,
			AggregationTemporality: temporality(d.Temporality),
		}}
		pm.Name = PromMetricName(m.Name, m.Unit, sumKind(d.IsMonotonic))
	case metricdata.Sum[float64]:
		pm.Data = &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			DataPoints: numberPoints(d.DataPoints, res), IsMonotonic: d.IsMonotonic,
			AggregationTemporality: temporality(d.Temporality),
		}}
		pm.Name = PromMetricName(m.Name, m.Unit, sumKind(d.IsMonotonic))
	case metricdata.Histogram[int64]:
		pm.Data = &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints: histPoints(d.DataPoints, res), AggregationTemporality: temporality(d.Temporality),
		}}
		pm.Name = PromMetricName(m.Name, m.Unit, KindHistogram)
	case metricdata.Histogram[float64]:
		pm.Data = &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints: histPoints(d.DataPoints, res), AggregationTemporality: temporality(d.Temporality),
		}}
		pm.Name = PromMetricName(m.Name, m.Unit, KindHistogram)
	default:
		// Üstel histogram / summary: Coremetry kendi enstrümanlarında
		// üretmiyor; gelirse sayılır ve atlanır (sessiz değil — Stats.Skipped).
		return nil
	}
	if pm.Name == "" {
		return nil
	}
	return pm
}

func sumKind(monotonic bool) Kind {
	if monotonic {
		return KindCounter
	}
	return KindGauge
}

func temporality(t metricdata.Temporality) metricspb.AggregationTemporality {
	if t == metricdata.DeltaTemporality {
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	}
	return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
}

func numberPoints[N int64 | float64](dps []metricdata.DataPoint[N], res []*commonpb.KeyValue) []*metricspb.NumberDataPoint {
	out := make([]*metricspb.NumberDataPoint, 0, len(dps))
	for _, dp := range dps {
		p := &metricspb.NumberDataPoint{
			Attributes:        pointAttrs(dp.Attributes, res),
			StartTimeUnixNano: unixNano(dp.StartTime),
			TimeUnixNano:      unixNano(dp.Time),
		}
		switch v := any(dp.Value).(type) {
		case int64:
			p.Value = &metricspb.NumberDataPoint_AsInt{AsInt: v}
		case float64:
			p.Value = &metricspb.NumberDataPoint_AsDouble{AsDouble: v}
		}
		out = append(out, p)
	}
	return out
}

func histPoints[N int64 | float64](dps []metricdata.HistogramDataPoint[N], res []*commonpb.KeyValue) []*metricspb.HistogramDataPoint {
	out := make([]*metricspb.HistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		sum := float64(dp.Sum)
		p := &metricspb.HistogramDataPoint{
			Attributes:        pointAttrs(dp.Attributes, res),
			StartTimeUnixNano: unixNano(dp.StartTime),
			TimeUnixNano:      unixNano(dp.Time),
			Count:             dp.Count,
			Sum:               &sum,
			BucketCounts:      dp.BucketCounts,
			ExplicitBounds:    dp.Bounds,
		}
		if v, ok := dp.Min.Value(); ok {
			f := float64(v)
			p.Min = &f
		}
		if v, ok := dp.Max.Value(); ok {
			f := float64(v)
			p.Max = &f
		}
		out = append(out, p)
	}
	return out
}

// pointAttrs — veri noktası etiketleri (sanitize) + terfi eden resource
// etiketleri. Çakışmada veri noktası kazanır (daha özgül bilgi).
func pointAttrs(set attribute.Set, res []*commonpb.KeyValue) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, set.Len()+len(res))
	seen := make(map[string]bool, set.Len())
	iter := set.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		k := PromLabel(string(kv.Key))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strKV(k, kv.Value.String()))
	}
	for _, r := range res {
		if !seen[r.Key] {
			out = append(out, r)
		}
	}
	return out
}

func strKV(k, v string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
}

func unixNano(t time.Time) uint64 {
	if t.IsZero() {
		return 0
	}
	return uint64(t.UnixNano()) // #nosec G115 — SDK zaman damgası 1970 sonrası
}
