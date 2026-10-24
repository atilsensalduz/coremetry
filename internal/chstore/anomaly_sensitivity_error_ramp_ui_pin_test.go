package chstore

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestErrorRampUIBoundsMirrorNormalize — Settings › Dedektör hassasiyeti ›
// "Sürekli teknik hata (5xx) yükselişi" formu (frontend/src/pages/settings/
// errorRamp.ts) aralık dışı değeri Kaydet'ten ÖNCE reddeder, çünkü
// NormalizeErrorRamp aralık dışını KELEPÇELEMEZ, sessizce varsayılana döndürür.
// Bu yalnız iki taraftaki sayılar birebir aynıyken doğrudur: biri değişirse bu
// test kırılır.
func TestErrorRampUIBoundsMirrorNormalize(t *testing.T) {
	b, err := os.ReadFile("../../frontend/src/pages/settings/errorRamp.ts")
	if err != nil {
		t.Fatalf("frontend errorRamp.ts okunamadı: %v", err)
	}
	src := string(b)
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("sayı değil: %q", s)
		}
		return v
	}
	bounds := map[string][2]float64{
		"windowBuckets":   {errRampWindowLo, errRampWindowHi},
		"ratioFactor":     {errRampFactorLo, errRampFactorHi},
		"minRatioPct":     {errRampRatioLo, errRampRatioHi},
		"minErrorsPerSec": {errRampEPSLo, errRampEPSHi},
	}
	for k, want := range bounds {
		re := regexp.MustCompile(fmt.Sprintf(`\b%s: \{ min: ([0-9.]+), max: ([0-9.]+)`, k))
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("ERROR_RAMP_BOUNDS.%s bulunamadı", k)
		}
		if got := [2]float64{num(m[1]), num(m[2])}; got != want {
			t.Errorf("%s: UI aralığı %v, Go NormalizeErrorRamp %v", k, got, want)
		}
	}
	d := DefaultErrorRamp()
	defs := map[string]float64{
		"windowBuckets": float64(d.WindowBuckets), "ratioFactor": d.RatioFactor,
		"minRatioPct": d.MinRatioPct, "minErrorsPerSec": d.MinErrorsPerSec,
	}
	dm := regexp.MustCompile(`ERROR_RAMP_DEFAULTS[^=]*= \{([^}]*)\}`).FindStringSubmatch(src)
	if dm == nil {
		t.Fatal("ERROR_RAMP_DEFAULTS bulunamadı")
	}
	for k, want := range defs {
		m := regexp.MustCompile(fmt.Sprintf(`\b%s: ([0-9.]+)`, k)).FindStringSubmatch(dm[1])
		if m == nil || num(m[1]) != want {
			t.Errorf("varsayılan %s: UI %v, Go %v", k, m, want)
		}
	}
}
