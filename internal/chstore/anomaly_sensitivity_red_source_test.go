package chstore

// v0.10.1478 — RED kaynağı ayarının gidiş-dönüşü: varsayılan auto, span
// korunur (geri alma yolu), bilinmeyen auto'ya iner, Normalize alanı PUT'ta
// düşürmez, ortam kapsamı kırpılır / tavanı aşan düşer.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestREDSourceSettingRoundTrip(t *testing.T) {
	if d := DefaultAnomalySensitivity(); d.REDSourceMode() != AnomalyREDSourceAuto || d.REDSource != AnomalyREDSourceAuto {
		t.Fatalf("varsayılan auto olmalı: %+v", d.REDSource)
	}
	if (AnomalySensitivityConfig{}).REDSourceMode() != AnomalyREDSourceAuto {
		t.Fatal("alan yok (eski blob) = auto")
	}
	cases := []struct{ in, want string }{{"span", "span"}, {" SPAN ", "span"}, {"auto", "auto"}, {"vm", "auto"}, {"", "auto"}}
	for _, c := range cases {
		got := NormalizeAnomalySensitivity(AnomalySensitivityConfig{REDSource: c.in}).REDSource
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, beklenen %q", c.in, got, c.want)
		}
	}
	in := DefaultAnomalySensitivity()
	in.REDSource, in.REDSourceEnv = "span", "  prod-eu-a  "
	b, err := json.Marshal(NormalizeAnomalySensitivity(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"redSource":"span"`) || !strings.Contains(string(b), `"redSourceEnv":"prod-eu-a"`) {
		t.Fatalf("blob alanları taşımalı: %s", b)
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back = NormalizeAnomalySensitivity(back)
	if back.REDSourceMode() != AnomalyREDSourceSpan || back.REDSourceEnvScope() != "prod-eu-a" {
		t.Fatalf("gidiş-dönüş: %q %q", back.REDSource, back.REDSourceEnv)
	}
	if got := NormalizeAnomalySensitivity(AnomalySensitivityConfig{REDSourceEnv: strings.Repeat("x", 201)}).REDSourceEnv; got != "" {
		t.Errorf("tavanı aşan ortam düşmeli: %q", got)
	}
}

// v0.10.1489 — alarm kuralı kaynağı: varsayılan auto, span korunur (geri
// alma), bilinmeyen auto, Normalize PUT'ta düşürmez, redSource'tan bağımsız.
func TestAlertSourceSettingRoundTrip(t *testing.T) {
	if d := DefaultAnomalySensitivity(); d.AlertSourceMode() != AnomalyREDSourceAuto || d.AlertSource != AnomalyREDSourceAuto {
		t.Fatalf("varsayılan auto olmalı: %q", d.AlertSource)
	}
	if (AnomalySensitivityConfig{}).AlertSourceMode() != AnomalyREDSourceAuto {
		t.Fatal("alan yok (eski blob) = auto")
	}
	for in, want := range map[string]string{"span": "span", " Span ": "span", "auto": "auto", "vm": "auto", "": "auto"} {
		if got := NormalizeAnomalySensitivity(AnomalySensitivityConfig{AlertSource: in}).AlertSource; got != want {
			t.Errorf("Normalize(%q) = %q, beklenen %q", in, got, want)
		}
	}
	in := DefaultAnomalySensitivity()
	in.AlertSource = "span"
	b, err := json.Marshal(NormalizeAnomalySensitivity(in))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"alertSource":"span"`) {
		t.Fatalf("blob alanı taşımalı: %s", b)
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	back = NormalizeAnomalySensitivity(back)
	if back.AlertSourceMode() != AnomalyREDSourceSpan || back.REDSourceMode() != AnomalyREDSourceAuto {
		t.Fatalf("gidiş-dönüş / bağımsızlık: alert=%q red=%q", back.AlertSource, back.REDSource)
	}
}
