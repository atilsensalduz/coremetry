package chstore

// anomaly_behavior_hv_floor_test.go — v0.10.1521: davranış motorunun hacim
// farkında hata tabanı vidaları (highVolumeErrorFloor / highVolumeErrorsPerSec
// / highVolumeMinErrorRatePct). Varsayılanlar v0.10.1518 errorRamp'ın
// sayılarına PİNLİ (iki motorun "yüksek hacim" tanımı aynı).

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestBehaviorHVFloorDefaultsMatchErrorRamp(t *testing.T) {
	d, r := DefaultAnomalyBehavior(), DefaultErrorRamp()
	if !d.HighVolumeErrorFloorOn() || d.HighVolumeErrorFloor == nil {
		t.Fatal("varsayılan AÇIK ve somut olmalı")
	}
	if d.HighVolumeErrorsPerSec != r.MinErrorsPerSec || d.HighVolumeMinErrorRatePct != r.MinRatioPct {
		t.Fatalf("hv %v/%v ≠ errorRamp %v/%v", d.HighVolumeErrorsPerSec, d.HighVolumeMinErrorRatePct, r.MinErrorsPerSec, r.MinRatioPct)
	}
	if d.HighVolumeMinErrorRatePct >= d.MinErrorRatePct {
		t.Fatal("hacim tabanı normal tabandan düşük olmalı (yoksa kapı anlamsız)")
	}
}

func TestNormalizeBehaviorHVFloor(t *testing.T) {
	d := DefaultAnomalyBehavior()
	off := false
	cases := []struct {
		name            string
		in              AnomalyBehaviorConfig
		on              bool
		wantEPS, wantPc float64
	}{
		{"eski blob (alan yok) → açık + varsayılan", AnomalyBehaviorConfig{}, true, d.HighVolumeErrorsPerSec, d.HighVolumeMinErrorRatePct},
		{"geçerli korunur", AnomalyBehaviorConfig{HighVolumeErrorsPerSec: 5, HighVolumeMinErrorRatePct: 0.5}, true, 5, 0.5},
		{"sınırlar meşru (alt)", AnomalyBehaviorConfig{HighVolumeErrorsPerSec: 0.05, HighVolumeMinErrorRatePct: 0.01}, true, 0.05, 0.01},
		{"sınırlar meşru (üst)", AnomalyBehaviorConfig{HighVolumeErrorsPerSec: 100000, HighVolumeMinErrorRatePct: 100}, true, 100000, 100},
		{"aralık dışı → varsayılan", AnomalyBehaviorConfig{HighVolumeErrorsPerSec: 0.01, HighVolumeMinErrorRatePct: 101}, true, d.HighVolumeErrorsPerSec, d.HighVolumeMinErrorRatePct},
		{"NaN → varsayılan", AnomalyBehaviorConfig{HighVolumeErrorsPerSec: math.NaN(), HighVolumeMinErrorRatePct: math.Inf(1)}, true, d.HighVolumeErrorsPerSec, d.HighVolumeMinErrorRatePct},
		{"açıkça kapalı korunur", AnomalyBehaviorConfig{HighVolumeErrorFloor: &off}, false, d.HighVolumeErrorsPerSec, d.HighVolumeMinErrorRatePct},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeAnomalyBehavior(c.in)
			if got.HighVolumeErrorFloor == nil || *got.HighVolumeErrorFloor != c.on {
				t.Fatalf("bayrak %v, want %v", got.HighVolumeErrorFloor, c.on)
			}
			if got.HighVolumeErrorsPerSec != c.wantEPS || got.HighVolumeMinErrorRatePct != c.wantPc {
				t.Fatalf("got %v/%v want %v/%v", got.HighVolumeErrorsPerSec, got.HighVolumeMinErrorRatePct, c.wantEPS, c.wantPc)
			}
		})
	}
}

// JSON etiketleri frontend sözleşmesi; kaydet → oku aynı değer.
func TestBehaviorHVFloorRoundTrip(t *testing.T) {
	off := false
	in := NormalizeAnomalySensitivity(AnomalySensitivityConfig{Behavior: AnomalyBehaviorConfig{
		HighVolumeErrorFloor: &off, HighVolumeErrorsPerSec: 3, HighVolumeMinErrorRatePct: 0.4,
	}})
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"highVolumeErrorFloor":false`, `"highVolumeErrorsPerSec":3`, `"highVolumeMinErrorRatePct":0.4`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("JSON'da %s yok: %s", f, raw)
		}
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	b := NormalizeAnomalySensitivity(back).Behavior
	if b.HighVolumeErrorFloorOn() || b.HighVolumeErrorsPerSec != 3 || b.HighVolumeMinErrorRatePct != 0.4 {
		t.Fatalf("round-trip değiştirdi: %+v", b)
	}
}
