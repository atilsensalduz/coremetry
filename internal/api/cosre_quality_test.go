package api

// CoSRE öğrenme Faz 2 — örtük sinyal ucunun girdi sözleşmesi.

import (
	"strings"
	"testing"
)

func TestValidateCosreSignal(t *testing.T) {
	ok := []cosreSignalRequest{
		{ExchangeID: "ab12", Kind: "copy"},
		{ExchangeID: " ab12 ", Kind: "link_click", Detail: "/traces/x"},
		{ExchangeID: "ab12", Kind: "evidence_click"},
		{ExchangeID: "ab12", Kind: "problem_note"},
	}
	for _, r := range ok {
		if _, err := validateCosreSignal(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []cosreSignalRequest{
		{ExchangeID: "", Kind: "copy"},
		{ExchangeID: strings.Repeat("a", aiFeedbackMaxIDLen+1), Kind: "copy"},
		{ExchangeID: "ab", Kind: "rephrase"}, // türetilen sinyal istemciden GELEMEZ
		{ExchangeID: "ab", Kind: "thumbs_up"},
	}
	for _, r := range bad {
		if _, err := validateCosreSignal(r); err == nil {
			t.Errorf("%+v kabul edildi", r)
		}
	}
	r, _ := validateCosreSignal(cosreSignalRequest{ExchangeID: "a", Kind: "copy", Detail: strings.Repeat("ş", 600)})
	if len(r.Detail) > cosreSignalDetailMax {
		t.Errorf("detail tavanı: %d", len(r.Detail))
	}
}

func TestCosreQualityDays(t *testing.T) {
	for in, want := range map[string]int{"": 7, "x": 7, "0": 7, "-3": 7, "1": 1, "14": 14, "99": 30} {
		if got := cosreQualityDays(in); got != want {
			t.Errorf("%q → %d", in, got)
		}
	}
}
