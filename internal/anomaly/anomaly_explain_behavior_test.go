package anomaly

// anomaly_explain_behavior_test.go — v0.10.1521: explain replay'i davranış
// motorunun hükmünü de AYNI adımlarda yazar. v0.10.1518'in gateway vakası
// (1300 istek/sn'de 5xx %0.05 → %0.6): davranış motoru hacim farkında tabanla
// adayı bulur; taban kapalıyken %1 tabanı susturur ve kapı adı raporlanır.

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// gwBehaviorRows — senaryonun span davranış satırları (sahte okuyucu).
func gwBehaviorRows(sc gwScenario) behaviorRowsReader {
	return func(_ context.Context, svc string, from, to time.Time) ([]behaviorRow, error) {
		if svc != gwSvc {
			return nil, nil
		}
		var out []behaviorRow
		for b := from.Unix() - from.Unix()%bucketSeconds; b < to.Unix(); b += bucketSeconds {
			r := sc.at(b)
			spans := uint64(math.Round(r.rate * bucketSeconds))
			out = append(out, behaviorRow{Unix: b, HOW: hourOfWeek(time.Unix(b, 0)), Spans: spans,
				Errs: uint64(math.Round(r.v / 100 * float64(spans))), P99Ms: 80})
		}
		return out, nil
	}
}

func noSpanRows(context.Context, string, string, time.Time, time.Time) (map[int64]spanRow, error) {
	return nil, nil
}

func TestExplainBehaviorReplay(t *testing.T) {
	days, minS, neighbor := seasonalDays, seasonalMinSamples, seasonalNeighborBuckets
	from, to := incidentDay.Add(3*time.Hour), incidentDay.Add(9*time.Hour)
	incident := gwScenario{baseRatio: 0.05, incident: true}
	hvOff := chstore.DefaultAnomalySensitivity()
	hvOff.Behavior.HighVolumeErrorFloor = boolPtrT(false)
	behOff := chstore.DefaultAnomalySensitivity()
	behOff.Behavior.Enabled = boolPtrT(false)

	cases := []struct {
		name      string
		vm        REDVM
		beh       behaviorRowsReader
		sc        gwScenario
		sens      chstore.AnomalySensitivityConfig
		wantCand  bool
		wantSrc   string
		wantBlock string // aday yoksa en sık kapıda geçmeli
	}{
		{"VM: gateway rampası → aday (hacim farkında taban)", gwFake(incident), nil, incident, chstore.DefaultAnomalySensitivity(), true, redSourceVM, ""},
		{"span: aynı rampa VM'siz → aday, kaynak span", nil, gwBehaviorRows(incident), incident, chstore.DefaultAnomalySensitivity(), true, redSourceSpan, ""},
		{"span: hacim farkında taban kapalı → aday yok, %1 tabanı", nil, gwBehaviorRows(incident), incident, hvOff, false, redSourceSpan, "absFloor"},
		{"span: aynı oranlar düşük hacim (×0.01) → aday yok", nil, gwBehaviorRows(gwScenario{baseRatio: 0.05, incident: true, lowVolume: 0.01}),
			gwScenario{}, chstore.DefaultAnomalySensitivity(), false, redSourceSpan, ""},
		{"span: olay yok, sabah trafiği artışı → aday yok", nil, gwBehaviorRows(gwScenario{baseRatio: 0.05}), gwScenario{}, chstore.DefaultAnomalySensitivity(), false, redSourceSpan, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := explainRun(context.Background(), tc.vm, noSpanRows, tc.beh, gwSvc, "error_rate", from, to, tc.sens, days, minS, neighbor, true)
			if (rep.BehaviorFirstAt != 0) != tc.wantCand {
				t.Fatalf("aday = %v, want %v (verdict %q, kapılar %v)", rep.BehaviorFirstAt != 0, tc.wantCand, rep.BehaviorVerdict, rep.BehaviorTopBlockers)
			}
			for _, s := range rep.Steps {
				if s.Behavior == nil {
					t.Fatalf("adımda davranış hükmü yok: %+v", s)
				}
			}
			if tc.wantCand {
				var first *ExplainBehaviorStep
				for _, s := range rep.Steps {
					if s.At == rep.BehaviorFirstAt {
						first = s.Behavior
					}
				}
				if first.Source != tc.wantSrc || first.Direction != "up" || first.Current >= 1 {
					t.Fatalf("ilk aday: %+v", first)
				}
				if !strings.Contains(rep.BehaviorVerdict, "aday üretirdi") {
					t.Fatalf("verdict: %q", rep.BehaviorVerdict)
				}
				return
			}
			if tc.wantBlock != "" && (len(rep.BehaviorTopBlockers) == 0 || !strings.Contains(strings.Join(rep.BehaviorTopBlockers, " | "), tc.wantBlock)) {
				t.Fatalf("kapılar %v, %q içermeli", rep.BehaviorTopBlockers, tc.wantBlock)
			}
		})
	}

	// Motor kapalı / metrik dışı: adım hükmü yok, verdict gerekçeyi söyler.
	rep := explainRun(context.Background(), nil, noSpanRows, gwBehaviorRows(incident), gwSvc, "error_rate", from, to, behOff, days, minS, neighbor, true)
	if rep.Steps[0].Behavior != nil || !strings.Contains(rep.BehaviorVerdict, "kapalı") {
		t.Fatalf("motor kapalı: %+v %q", rep.Steps[0].Behavior, rep.BehaviorVerdict)
	}
}

func TestBehaviorRowsAtMirrorsSQLWhere(t *testing.T) {
	now := incidentDay.Add(10*time.Hour + explainTickOffset)
	upper, cutoff, recentFrom, target := behaviorBinds(now)
	var all []behaviorRow
	for b := cutoff.Add(-time.Hour).Unix(); b < upper.Add(time.Hour).Unix(); b += bucketSeconds {
		all = append(all, behaviorRow{Unix: b, HOW: hourOfWeek(time.Unix(b, 0))})
	}
	got := behaviorRowsAt(all, now)
	want := vmBehaviorBuckets(now) // VM kova kümesi = aynı WHERE'in ikinci ikizi
	if len(got) != len(want) {
		t.Fatalf("satır %d, VM kova kümesi %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Unix != want[i] {
			t.Fatalf("%d. satır %d, kova %d", i, got[i].Unix, want[i])
		}
		in := got[i].Unix >= recentFrom.Unix() || hourOfWeekDistance(got[i].HOW, target) <= behaviorNeighborHours
		if !in || got[i].Unix < cutoff.Unix() || got[i].Unix >= upper.Unix() {
			t.Fatalf("WHERE dışı satır: %+v", got[i])
		}
	}
}

func TestBuildExplainBehaviorQueryShape(t *testing.T) {
	q := buildExplainBehaviorQuery()
	for _, want := range []string{"FROM service_summary_5m", "service_name = ?", "time_bucket >= ?", "time_bucket < ?", "'UTC'", "LIMIT", "max_execution_time"} {
		if !strings.Contains(q, want) {
			t.Errorf("%q yok: %s", want, q)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham spans okunmamalı")
	}
}
