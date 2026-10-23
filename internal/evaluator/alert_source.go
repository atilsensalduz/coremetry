package evaluator

// alert_source.go — v0.10.1489: alarm kuralının ölçü KAYNAĞI (VictoriaMetrics
// örneklenmemiş HTTP/gRPC sunucu histogramı ↔ span MV'si).
//
// Prod trace'leri kuyruk örnekleniyor (hatalar hep + ~%5 taban): span
// error_rate yukarı yanlı, request_rate %5'lik gürültülü örnek → sahte alarm.
// v0.10.1478 anomali dedektörünü VM'e taşıdı; bu dosya aynı okuyucuyu
// (anomaly.AlertVMReader — aile kataloğu, kapsam keşfi, geri çekilme 1478'in
// kodu) kural değerlendirmesine bağlar.
//
// SÖZLEŞME — tek kaynak: bir kural×servis değerlendirmesinin DEĞERİ ve
// minSamples SAYIMI aynı kaynaktan gelir. VM seçildiyse span prefetch'i o
// çiftte HİÇ okunmaz; VM'in herhangi bir türü bu tikte okunamadıysa çift
// BÜTÜNÜYLE span'e düşer (TestRuleMeasurementNeverMixesSources). Eşik /
// forSec / cooldown / karşılaştırıcı DEĞİŞMEZ — kapılar ruleMeasurement'ı
// okur, kaynağı bilmez (TestRuleMeasurementEquivalence).
//
// Davranış kayması (DECISIONS v0.10.1489): VM penceresi şimdi − 2 dk'da biter
// (vmIngestLag; collector dışa aktarımı + VM gecikmesi), minSamples artık
// GERÇEK istek sayısı (span'de %5 örnek), request_rate gerçek istek/sn
// (span'de servisin tüm span'leri).

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// alertVMPreparer — okuyucunun evaluator'ın kullandığı yüzü (test dikişi).
type alertVMPreparer interface {
	Prepare(ctx context.Context, needs []anomaly.AlertVMNeed, now time.Time) *anomaly.AlertVMTick
}

// SetAlertVM — main.go: süreç-içi tek okuyucu (API önizlemesiyle AYNI örnek).
// nil = her kural span (bugünkü davranış).
func (e *Evaluator) SetAlertVM(r *anomaly.AlertVMReader) {
	if r == nil {
		e.alertVM = nil
		return
	}
	e.alertVM = r
}

// collectAlertVMNeeds — SAF: tikin VM ihtiyacı (span-metrik yolundan geçen
// etkin kurallar; log / watcher / hedefli kural VM'e gitmez).
func collectAlertVMNeeds(rules []chstore.AlertRule) []anomaly.AlertVMNeed {
	var out []anomaly.AlertVMNeed
	for _, r := range evaluableRules(rules) {
		if r.LogQuery != "" || r.WatcherJSON != "" || r.Target != nil || r.WindowSec == 0 {
			continue
		}
		n := anomaly.AlertVMNeed{Metric: r.Metric, WindowSec: int(r.WindowSec)}
		if r.Service != "" {
			n.Services = []string{r.Service}
		}
		out = append(out, n)
	}
	return out
}

// prepareAlertVM — tikin VM ölçüleri; okuyucu yoksa nil (Measure nil-güvenli → span).
func (e *Evaluator) prepareAlertVM(ctx context.Context, rules []chstore.AlertRule, now time.Time) *anomaly.AlertVMTick {
	if e.alertVM == nil {
		return nil
	}
	return e.alertVM.Prepare(ctx, collectAlertVMNeeds(rules), now)
}

// ruleMeasurement — kapıların okuduğu ölçü (kaynaktan bağımsız).
type ruleMeasurement struct {
	value      float64
	evaluate   bool // false → bu tik değerlendirme yok (okuma hatası / boş oran)
	belowFloor bool // minSamples altında → ihlal damgası temizlenir
	src        anomaly.AlertSource
}

// vmRuleMeasurement — SAF: VM ölçüsünü kapı girdisine çevirir; span yolunun
// anlamıyla BİREBİR (minSamples yalnız örnek-duyarlı metrikte; pencerede ölçü
// yoksa absentMeasure).
func vmRuleMeasurement(r chstore.AlertRule, m anomaly.AlertVMMeasure) ruleMeasurement {
	out := ruleMeasurement{src: m.Source}
	if r.MinSamples > 0 && metricNeedsSampleFloor(r.Metric) && m.Count < uint64(r.MinSamples) {
		out.belowFloor = true
		return out
	}
	if !m.Present {
		av, ev := absentMeasure(r.Metric)
		out.value, out.evaluate = av, ev
		return out
	}
	out.value, out.evaluate = m.Value, true
	return out
}

// measureRule — kural×servis ölçüsü: VM seçildiyse VM, değilse span (gerekçeli).
func (e *Evaluator) measureRule(ctx context.Context, r chstore.AlertRule, service string, pre *tickMeasures, vt *anomaly.AlertVMTick) ruleMeasurement {
	m := vt.Measure(service, r.Metric, int(r.WindowSec))
	if m.Source.VM {
		return vmRuleMeasurement(r, m)
	}
	out := e.spanRuleMeasurement(ctx, r, service, pre)
	out.src = m.Source
	return out
}

// spanRuleMeasurement — v0.10.1488'e dek evaluateOne'ın gövdesindeki span
// yolu, değişmeden: önce minSamples sayımı (batched → per-service yedek),
// sonra değer (batched → per-service yedek).
func (e *Evaluator) spanRuleMeasurement(ctx context.Context, r chstore.AlertRule, service string, pre *tickMeasures) ruleMeasurement {
	window := time.Duration(r.WindowSec) * time.Second
	// v0.8.352 (perf P2-A) — windows the 5m MV serves consume the per-tick
	// batched prefetch (see prefetch.go). Sub-5m custom windows keep the
	// per-service raw path below: they're rare (builtins are 5m/10m) and
	// the 5m MV grid can't reconstruct them (useSummaryMV).
	batched := pre != nil && useSummaryMV(window)

	// Sample floor (v0.5.128). For sample-dependent metrics
	// (error_rate / percentiles / avg_ms) a single bad request
	// in a low-traffic window pushes the value to scary levels
	// without any real signal — skip the eval entirely below
	// MinSamples. request_rate / error_count are absolute and
	// inherently sample-aware, so the gate doesn't apply.
	if r.MinSamples > 0 && metricNeedsSampleFloor(r.Metric) {
		var count uint64
		counted := false
		if batched {
			counts, failed, ok := pre.countFor(int(r.WindowSec))
			if failed {
				// Batched count read errored this tick (logged at
				// prefetch) — skip, same as a per-service error.
				return ruleMeasurement{}
			}
			if ok {
				count = counts[service] // absent = no MV buckets = 0 spans
				counted = true
			}
		}
		if !counted {
			// Sub-5m window, or a defensive fallback when the pair was
			// never prefetched (a collectMeasureKeys mismatch — bug).
			var err error
			count, err = e.measureCount(ctx, service, window)
			if err != nil {
				log.Printf("[evaluator] measure-count %s/%s: %v", r.ID, service, err)
				return ruleMeasurement{}
			}
		}
		if count < uint64(r.MinSamples) {
			return ruleMeasurement{belowFloor: true}
		}
	}

	if batched {
		vals, failed, ok := pre.measureFor(r.Metric, int(r.WindowSec))
		if failed {
			// Batched read errored this tick (logged at prefetch) —
			// skip every service on this rule, same blast radius as
			// the old per-service measure() error.
			return ruleMeasurement{}
		}
		if ok {
			v, present := vals[service]
			if !present {
				// Zero traffic in the window. absentMeasure reproduces
				// what the per-service query returned for an empty
				// result (0 / NaN / skip) — see prefetch.go.
				av, evaluate := absentMeasure(r.Metric)
				return ruleMeasurement{value: av, evaluate: evaluate}
			}
			return ruleMeasurement{value: v, evaluate: true}
		}
	}
	value, err := e.measure(ctx, service, r.Metric, window)
	if err != nil {
		log.Printf("[evaluator] measure %s/%s: %v", r.ID, service, err)
		return ruleMeasurement{}
	}
	return ruleMeasurement{value: value, evaluate: true}
}
