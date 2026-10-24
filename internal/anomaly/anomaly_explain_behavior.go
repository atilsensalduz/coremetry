package anomaly

// anomaly_explain_behavior.go — v0.10.1521: explain replay'ine (GET
// /api/settings/anomaly-red-source/explain) davranış motorunun hükmü. Aynı
// servis × aynı metrik × aynı 5 dk adımlarda canlı motorun yolu birebir:
// kaynak seçimi (VM'in behaviorRowsFor'u hazırsa VM, değilse span —
// splitBehaviorRowsBySource), kova kümesi (buildBehaviorBucketsQuery WHERE'inin
// Go ikizi behaviorRowsAt / vmBehaviorBuckets), takvim budaması
// (dropCalendarBaselineRows), karar (evalBehaviorWhy = evalBehavior + durduran
// kapı). Davranış motoru Problem AÇMAZ: aday anomaly_events'e behavior_change
// olarak düşer ve terfi kapısından geçer — replay "aday / yok" der.
//
// Replay EDİLMEYENLER (cevapta not): batch p99 yük kapısının aktif-olay
// muafiyeti (canlı okuma ister), fırtına tavanı (filo sıralaması ister).
//
// Okuma: span MV'sine servis-filtreli TEK sorgu (ilk adımın 28 gün öncesinden
// son adıma; ORDER BY öneki service_name → budama; LIMIT + max_execution_time).

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ExplainBehaviorStep — bir adımda davranış motorunun hükmü.
type ExplainBehaviorStep struct {
	Source    string  `json:"source"`           // "vm" | "span"
	Action    string  `json:"action"`           // "candidate" | "none" | "skip"
	Signal    string  `json:"signal,omitempty"` // "regime" | "seasonal"
	Direction string  `json:"direction,omitempty"`
	Ratio     float64 `json:"ratio,omitempty"`
	Z         float64 `json:"z,omitempty"`
	Baseline  float64 `json:"baseline,omitempty"`
	Current   float64 `json:"current,omitempty"`
	Blocker   string  `json:"blocker,omitempty"`
	// ErrorsPerSec — son dilimin hata/sn'si (error_rate); HighVolume —
	// hacim farkında taban rejim penceresinde devrede mi.
	ErrorsPerSec float64 `json:"errorsPerSec,omitempty"`
	HighVolume   bool    `json:"highVolume,omitempty"`
}

// behaviorRowsReader — servis-filtreli davranış satırları (test seam'i).
type behaviorRowsReader func(ctx context.Context, service string, from, to time.Time) ([]behaviorRow, error)

// behaviorReplayable — SAF: motor açık ve metrik motorun metriği mi.
func behaviorReplayable(metric string, sens chstore.AnomalySensitivityConfig) bool {
	if !sens.Behavior.IsEnabled() {
		return false
	}
	for _, m := range behaviorMetrics {
		if m == metric {
			return true
		}
	}
	return false
}

// explainBehavior — replay'in davranış durumu.
type explainBehavior struct {
	on       bool
	reason   string // on=false gerekçesi (not)
	service  string
	metric   string
	sens     chstore.AnomalySensitivityConfig
	rows     []behaviorRow // span, zamana göre artan
	readErr  string
	blockers map[string]int
	sources  map[string]int
}

func newExplainBehavior(ctx context.Context, beh behaviorRowsReader, on bool, service, metric string, steps []time.Time, sens chstore.AnomalySensitivityConfig) *explainBehavior {
	x := &explainBehavior{on: on, service: service, metric: metric, sens: sens, blockers: map[string]int{}, sources: map[string]int{}}
	switch {
	case !sens.Behavior.IsEnabled():
		x.on, x.reason = false, "davranış motoru kapalı (behavior.enabled=false)"
		return x
	case !on || len(steps) == 0:
		x.on, x.reason = false, fmt.Sprintf("davranış motoru %s metriğini değerlendirmez", metric)
		return x
	case beh == nil:
		return x
	}
	_, cutoff, _, _ := behaviorBinds(steps[0])
	upper, _, _, _ := behaviorBinds(steps[len(steps)-1])
	rows, err := beh(ctx, service, cutoff, upper)
	if err != nil {
		x.readErr = err.Error()
	}
	x.rows = rows
	return x
}

// behaviorRowsAt — SAF: buildBehaviorBucketsQuery'nin WHERE'i tek servis
// için (aynı bağlar behaviorBinds; kova eşleşmesi dairesel ±komşu ya da son
// pencere). rows zamana göre artan; çıktı da.
func behaviorRowsAt(rows []behaviorRow, now time.Time) []behaviorRow {
	upper, cutoff, recentFrom, target := behaviorBinds(now)
	var out []behaviorRow
	for _, r := range rows {
		if r.Unix < cutoff.Unix() || r.Unix >= upper.Unix() {
			continue
		}
		if r.Unix >= recentFrom.Unix() || hourOfWeekDistance(r.HOW, target) <= behaviorNeighborHours {
			out = append(out, r)
		}
	}
	return out
}

// step — adımın davranış hükmü; canlı tikle aynı sıra (kaynak → takvim →
// batch → karar).
func (x *explainBehavior) step(st *ExplainStep, now time.Time, view *vmREDView) {
	if !x.on {
		return
	}
	bs := &ExplainBehaviorStep{Source: redSourceSpan}
	rows := behaviorRowsAt(x.rows, now)
	recentCutoff := lastCompleteBucketStart(now).Add(-behaviorRecentHours * time.Hour).Unix()
	if vr, _, ok := view.behaviorRowsFor(x.service); ok {
		bs.Source, rows = redSourceVM, vr
		recentCutoff = time.Unix(view.upper, 0).Add(-behaviorRecentHours * time.Hour).Unix()
	}
	x.sources[bs.Source]++
	rows = dropCalendarBaselineRows(map[string][]behaviorRow{x.service: rows}, x.sens.Calendar, recentCutoff)[x.service]
	st.Behavior = bs
	if batchLoadSkipped(x.sens, x.service, x.metric) {
		bs.Action, bs.Blocker = "skip", behBlockBatchSkip
		x.blockers[bs.Blocker]++
		return
	}
	b := x.sens.Behavior
	baseline, recent := splitBehaviorSeries(rows, x.metric, recentCutoff)
	if n := len(recent); n > 0 && x.metric == "error_rate" {
		bs.ErrorsPerSec = round2(float64(recent[n-1].Errs) / 300.0)
		if n >= b.DwellRegime {
			bs.HighVolume = behaviorHighVolumeErrors(x.metric, recent[n-b.DwellRegime:], b)
		}
	}
	c, ok, why := evalBehaviorWhy(x.service, x.metric, baseline, recent, policyFor(x.metric, x.sens), b)
	if !ok {
		bs.Action, bs.Blocker = "none", why
		x.blockers[why]++
		return
	}
	bs.Action, bs.Signal, bs.Direction = "candidate", c.Signal, c.Direction
	bs.Ratio, bs.Z, bs.Baseline, bs.Current = round2(c.Ratio), round2(c.Z), round2(c.Baseline), round2(c.Current)
}

// finish — rapor özeti + notlar.
func (x *explainBehavior) finish(rep *ExplainReport) {
	if !x.on {
		rep.BehaviorVerdict = x.reason
		return
	}
	rep.BehaviorReadError = x.readErr
	for _, s := range rep.Steps {
		if s.Behavior != nil && s.Behavior.Action == "candidate" {
			rep.BehaviorSteps++
			if rep.BehaviorFirstAt == 0 {
				rep.BehaviorFirstAt = s.At
			}
		}
	}
	rep.BehaviorTopBlockers = topBlockers(x.blockers, 4)
	rep.BehaviorVerdict = explainBehaviorVerdict(rep, x.sources)
	rep.Notes = append(rep.Notes, "davranış motoru Problem açmaz: aday behavior_change olayı olur ve terfi kapısından geçer; batch p99 aktif-olay muafiyeti ve fırtına tavanı replay edilmez")
}

// explainBehaviorVerdict — SAF: tek cümle.
func explainBehaviorVerdict(rep *ExplainReport, sources map[string]int) string {
	src := fmt.Sprintf("kaynak: vm %d adım · span %d adım", sources[redSourceVM], sources[redSourceSpan])
	if rep.BehaviorFirstAt != 0 {
		var first *ExplainBehaviorStep
		for _, s := range rep.Steps {
			if s.At == rep.BehaviorFirstAt {
				first = s.Behavior
				break
			}
		}
		return fmt.Sprintf("davranış motoru %s'te aday üretirdi (%s, %s); %d adımda aday — %s",
			time.Unix(rep.BehaviorFirstAt, 0).UTC().Format(time.RFC3339), behaviorSignalTR(first.Signal), first.Source, rep.BehaviorSteps, src)
	}
	if len(rep.BehaviorTopBlockers) > 0 {
		return "davranış motoru aday üretmezdi — en sık kapı: " + rep.BehaviorTopBlockers[0] + " — " + src
	}
	return "davranış motoru aday üretmezdi — " + src
}

// storeBehaviorRows — servis-filtreli davranış satırları (MV, ORDER BY öneki).
func storeBehaviorRows(st *chstore.Store) behaviorRowsReader {
	return func(ctx context.Context, service string, from, to time.Time) ([]behaviorRow, error) {
		rows, err := st.TelemetryReadConn().Query(ctx, buildExplainBehaviorQuery(), service, from, to)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []behaviorRow
		for rows.Next() {
			var t uint32
			var how int32
			var spans, errs uint64
			var p99 float64
			if err := rows.Scan(&t, &how, &spans, &errs, &p99); err != nil {
				return nil, err
			}
			out = append(out, behaviorRow{Unix: int64(t), HOW: int(how), Spans: spans, Errs: errs, P99Ms: p99})
		}
		return out, rows.Err()
	}
}

// buildExplainBehaviorQuery — SAF: buildBehaviorBucketsQuery'nin tek servis
// ikizi (aynı kolonlar, aynı UTC haftanın-saati ifadesi; kova eşleşmesi Go'da
// adım başına). 28 g + 12 sa ≈ 8 200 kova → LIMIT 12 000.
// Bağlar: servis, başlangıç, üst sınır.
func buildExplainBehaviorQuery() string {
	const howExpr = "((toDayOfWeek(time_bucket, 0, 'UTC') - 1) * 24 + toHour(time_bucket, 'UTC'))"
	return fmt.Sprintf(`
		SELECT toUnixTimestamp(time_bucket) AS t,
		       toInt32(%s) AS how,
		       countMerge(span_count_state) AS spans,
		       countMerge(error_count_state) AS errs,
		       quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state)[3] / 1e6 AS p99_ms
		FROM service_summary_5m
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		GROUP BY t, how
		ORDER BY t
		LIMIT 12000
		SETTINGS max_execution_time = 20`, howExpr)
}
