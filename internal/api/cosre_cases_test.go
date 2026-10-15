package api

// CoSRE öğrenme Faz 3 — onay formu, öneri/nihai karşılaştırması ve modele
// giden geçmiş-vaka bloğu (onaysız asla olgu gibi).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestValidateCaseConfirm(t *testing.T) {
	if _, err := validateCaseConfirm(caseConfirmRequest{FinalCause: " db havuzu ", Category: "capacity"}); err != nil {
		t.Errorf("geçerli: %v", err)
	}
	for _, r := range []caseConfirmRequest{
		{FinalCause: "", Category: "capacity"},
		{FinalCause: "x", Category: "bilinmeyen"},
		{FinalCause: strings.Repeat("a", caseFieldMax+1), Category: "other"},
	} {
		if _, err := validateCaseConfirm(r); err == nil {
			t.Errorf("%+v kabul edildi", r)
		}
	}
}

func TestSameCauseAndVerdictParse(t *testing.T) {
	if !sameCause("DB  havuzu doldu", "db havuzu doldu") || sameCause("", "") || sameCause("a", "b") {
		t.Error("sameCause")
	}
	for in, want := range map[string]int8{"EVET\naynı bileşen": 1, "**Hayır**\nfarklı": 0, "KISMEN\n…": -1, "belki": -1, "Yes.": 1} {
		if got, _ := parseCaseVerdict(in); got != want {
			t.Errorf("%q → %d want %d", in, got, want)
		}
	}
}

func TestFormatPastCases(t *testing.T) {
	rows := []chstore.SimilarCase{
		{IncidentCase: chstore.IncidentCase{CaseID: "ex:self"}},
		{IncidentCase: chstore.IncidentCase{CaseID: "p:1", Status: chstore.CaseConfirmed, ConfirmedBy: "a@b", Service: "svc", Title: "MCA-00029",
			FinalCause: "Kart PIN servisi zaman aşımı", Resolution: "pool artırıldı", ClosedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, MatchedCodes: []string{"MCA-00029"}},
		{IncidentCase: chstore.IncidentCase{CaseID: "p:2", Status: chstore.CaseDraft, Service: "svc", Title: "t", SuggestedCause: "deploy 2.3",
			FinalCause: "SIZMAMALI"}},
	}
	out := formatPastCases(rows, "ex:self", 3)
	for _, want := range []string{"ONAYLI (a@b)", "neden: Kart PIN servisi zaman aşımı", "çözüm: pool artırıldı", "eşleşen kod: MCA-00029",
		"ONAYSIZ TASLAK", "o zamanki öneri (doğrulanmadı): deploy 2.3", "olgu gibi sunma"} {
		if !strings.Contains(out, want) {
			t.Errorf("blok %q içermiyor:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SIZMAMALI") || strings.Count(out, "\n- [") != 2 {
		t.Errorf("taslağın nihai alanı sızmamalı / kendi vakası dışlanmalı:\n%s", out)
	}
	if formatPastCases(nil, "", 3) != "" {
		t.Error("vaka yoksa blok yok")
	}
}
