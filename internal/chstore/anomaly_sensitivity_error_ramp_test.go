package chstore

import (
	"encoding/json"
	"testing"
)

// TestErrorRampNormalize — eksik bölüm varsayılan (AÇIK), aralık dışı
// kelepçelenir, açık false korunur, Normalize alanı PUT'ta düşürmez.
func TestErrorRampNormalize(t *testing.T) {
	d := DefaultErrorRamp()
	cases := []struct {
		name string
		in   ErrorRampConfig
		want ErrorRampConfig
	}{
		{"eski blob (bölüm yok)", ErrorRampConfig{}, d},
		{"aralık dışı", ErrorRampConfig{WindowBuckets: 1, RatioFactor: 1.0, MinRatioPct: -1, MinErrorsPerSec: 0}, d},
		{"geçerli", ErrorRampConfig{Enabled: boolPtr(false), WindowBuckets: 12, RatioFactor: 3, MinRatioPct: 0.5, MinErrorsPerSec: 5},
			ErrorRampConfig{Enabled: boolPtr(false), WindowBuckets: 12, RatioFactor: 3, MinRatioPct: 0.5, MinErrorsPerSec: 5}},
	}
	for _, c := range cases {
		got := NormalizeErrorRamp(c.in)
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(c.want)
		if string(a) != string(b) {
			t.Errorf("%s: %s, beklenen %s", c.name, a, b)
		}
	}
	if !(ErrorRampConfig{}).On() {
		t.Error("nil bayrak AÇIK okunmalı")
	}
	in := DefaultAnomalySensitivity()
	in.ErrorRamp = ErrorRampConfig{Enabled: boolPtr(false), WindowBuckets: 9}
	out := NormalizeAnomalySensitivity(in)
	if out.ErrorRamp.On() || out.ErrorRamp.WindowBuckets != 9 {
		t.Errorf("Normalize errorRamp'i taşımalı: %+v", out.ErrorRamp)
	}
	var blob AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"dwellBuckets":3}`), &blob); err != nil {
		t.Fatal(err)
	}
	if n := NormalizeAnomalySensitivity(blob); !n.ErrorRamp.On() || n.ErrorRamp.WindowBuckets != 6 {
		t.Errorf("eski blob varsayılanı almalı: %+v", n.ErrorRamp)
	}
}
