package api

// v0.10.1287 — AI SRE Faz 1b: sohbetin kayıtlı araştırmayı yeniden kullanması
// (get_problem investigation + guided "KAYITLI ARAŞTIRMA" bloğu).

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

func TestGuidedSavedInvestigationTR(t *testing.T) {
	s, _, st := newSRETestEnv(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 14, 5, 0, 0, time.UTC).UnixNano()
	body, err := sre.Encode(sre.Investigation{ProblemID: "p1", Service: "pay", State: sre.StateDone, FinishedAt: at,
		Findings: []sre.Finding{{Kind: "operation", Name: "POST /pay", Level: "crit", Reasons: []string{"hata %9"}}},
		Summary:  &sre.Narration{Text: "Ne oldu: pay hata üretiyor."}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSREInvestigation(ctx, "p1", sre.StateDone, body, at); err != nil {
		t.Fatal(err)
	}
	probs := []chstore.Problem{{ID: "none"}, {ID: "p1", DisplayID: "P-1"}}
	got := s.guidedSavedInvestigationTR(ctx, probs)
	if !strings.Contains(got, "KAYITLI ARAŞTIRMA (") || !strings.Contains(got, "problem P-1") ||
		!strings.Contains(got, "[crit] operasyon POST /pay: hata %9") || !strings.Contains(got, "Ne oldu: pay hata üretiyor.") {
		t.Fatalf("blok: %q", got)
	}
	if s.guidedSavedInvestigationTR(ctx, []chstore.Problem{{ID: "none"}}) != "" {
		t.Fatal("kayıtsız problemde blok yok")
	}
	sum, err := s.mcpInvestigationSummary(ctx, "p1")
	if err != nil || sum == nil || sum.State != sre.StateDone || len(sum.Findings) != 1 {
		t.Fatalf("get_problem özeti: %+v %v", sum, err)
	}
	// Kablolama: kök-neden demeti bloğu ekliyor, Deps okuyucuyu taşıyor.
	for file, needle := range map[string]string{
		"copilot_guided.go": "s.guidedSavedInvestigationTR(ctx, probs)",
		"mcp_deps.go":       "Investigation: s.mcpInvestigationSummary",
	} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), needle) {
			t.Errorf("%s: %q yok", file, needle)
		}
	}
}

func TestSREReadStoreNilSafe(t *testing.T) {
	s := &Server{}
	if s.sreReadStore() != nil {
		t.Fatal("mağazasız sunucu nil okuyucu döndürmeli")
	}
	if got, err := s.mcpInvestigationSummary(context.Background(), "p1"); got != nil || err != nil {
		t.Fatalf("mağazasız: %+v %v", got, err)
	}
	if s.guidedSavedInvestigationTR(context.Background(), []chstore.Problem{{ID: "p1"}}) != "" {
		t.Fatal("mağazasız blok yok")
	}
}
