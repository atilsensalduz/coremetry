package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1495 regresyon testi — operatör (prod, `p99_ms · <svc> · 5 dk pencere`
// alarm problemi): çizgi 12:00–12:45 arası ~0 ms'e düşüp sonra sıçrıyordu.
// Seyrek örneklenmiş span'lerde penceredeki birkaç kısa span'in p99'u çiziliyor,
// oysa değerlendirici o anlarda minSamples altında ÖLÇMÜYORDU (belowFloor).
// Grafik değerlendiricinin kapısını uygular: taban altı nokta boşluk (null) +
// belowFloor; boş pencere her zaman null (sıfır değil); kaynak satırı her
// cevapta. Servis adları sentetik.

func ptsAt(vals ...any) []chstore.AlertMetricPoint {
	out := []chstore.AlertMetricPoint{}
	for i, v := range vals {
		p := chstore.AlertMetricPoint{T: int64(i + 1)}
		if f, ok := v.(float64); ok {
			x := f
			p.V = &x
		}
		out = append(out, p)
	}
	return out
}

func TestAlertRuleSeriesSampleFloorParity(t *testing.T) {
	orig := alertRuleSeriesStoreOf
	t.Cleanup(func() { alertRuleSeriesStoreOf = orig })
	from := time.Now().Add(-70 * time.Minute).UnixNano()
	call := func(st *fakeAlertRuleSeriesStore, metric string) alertRuleSeriesResponse {
		t.Helper()
		alertRuleSeriesStoreOf = func(*Server) alertRuleSeriesStore { return st }
		srv := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/alert-rules/r1/series?service=orders-api&metric="+metric+"&from="+itoa64(from), nil)
		r.SetPathValue("id", "r1")
		srv.getAlertRuleSeries(w, r)
		if w.Code != 200 {
			t.Fatalf("code=%d %s", w.Code, w.Body.String())
		}
		// Boşluk JSON'da null; sayım alanı (N) JSON'a ÇIKMAZ.
		if strings.Contains(w.Body.String(), `"N"`) {
			t.Fatalf("N sızdı: %s", w.Body.String())
		}
		var b alertRuleSeriesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		if b.Source == nil || b.Source.Evidence == "" {
			t.Fatalf("kaynak satırı her cevapta olmalı: %+v", b.Source)
		}
		return b
	}
	rule := func(metric string, min uint32) *chstore.AlertRule {
		return &chstore.AlertRule{ID: "r1", Metric: metric, WindowSec: 300, MinSamples: min}
	}

	t.Run("prod: seyrek örnek → taban altı noktalar boşluk + belowFloor", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: rule("p99_ms", 20),
			pts:    ptsAt(820.0, 3.0, 2.0, nil, 4100.0),
			counts: ptsAt(400.0, 6.0, 0.0, 0.0, 380.0)}
		b := call(st, "p99_ms")
		want := []struct {
			v     bool
			floor bool
		}{{true, false}, {false, true}, {false, true}, {false, true}, {true, false}}
		for i, w := range want {
			p := b.Points[i]
			if (p.V != nil) != w.v || p.BelowFloor != w.floor {
				t.Errorf("nokta %d: v=%v floor=%v, want v=%v floor=%v", i, p.V, p.BelowFloor, w.v, w.floor)
			}
		}
		if st.countCalls != 1 {
			t.Fatalf("sayım dizisi bir kez okunmalı: %d", st.countCalls)
		}
	})
	t.Run("minSamples 0 → taban yok, sayım okunmaz", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: rule("p99_ms", 0), pts: ptsAt(3.0, nil)}
		b := call(st, "p99_ms")
		if b.Points[0].V == nil || b.Points[0].BelowFloor || b.Points[1].V != nil || st.countCalls != 0 {
			t.Fatalf("%+v calls=%d", b.Points, st.countCalls)
		}
	})
	t.Run("sayım metriği (request_rate) tabana tabi değil — 0 trafik 0 kalır", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: rule("request_rate", 50), pts: ptsAt(0.0, 12.0)}
		b := call(st, "request_rate")
		if b.Points[0].V == nil || *b.Points[0].V != 0 || st.countCalls != 0 {
			t.Fatalf("%+v calls=%d", b.Points, st.countCalls)
		}
	})
	t.Run("sayım okunamazsa dizi tabansız döner (grafik düşmez)", func(t *testing.T) {
		st := &fakeAlertRuleSeriesStore{rule: rule("error_rate", 20), pts: ptsAt(4.0), countErr: errors.New("code: 159")}
		b := call(st, "error_rate")
		if b.Points[0].V == nil || b.Points[0].BelowFloor {
			t.Fatalf("%+v", b.Points)
		}
	})
}

func TestApplyAlertSampleFloor(t *testing.T) {
	pts := ptsAt(5.0, 6.0, 7.0)
	got := chstore.ApplyAlertSampleFloor(pts, map[int64]float64{1: 19, 2: 20}, 20)
	if got[0].V != nil || !got[0].BelowFloor {
		t.Error("19 < 20 → boşluk")
	}
	if got[1].V == nil || got[1].BelowFloor {
		t.Error("20 = taban → kalır")
	}
	if got[2].V == nil || got[2].BelowFloor {
		t.Error("sayımı bilinmeyen nokta dokunulmaz")
	}
	if p := chstore.ApplyAlertSampleFloor(ptsAt(1.0), map[int64]float64{1: 0}, 0); p[0].V == nil {
		t.Error("min 0 → aynen")
	}
}
