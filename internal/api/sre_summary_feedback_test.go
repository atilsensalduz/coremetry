package api

// v0.10.1312 — AI SRE özetinin 👍/👎 rayı, API tarafı: GET
// /api/problems/{id}/investigation özetin ai_calls kimliğini (callId) ve
// exchange kimliğini (exchangeId) FE'ye taşır; FE ikisini POST
// /api/ai/feedback'e gönderir, kapı (validateAIFeedback +
// normalizeFeedbackCallID) anlatımın bastığı biçimleri kabul eder. Kapı
// kapalı / tekrar özetinde alanlar HİÇ gelmez (omitempty) → FE rayı çizmez.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/sre"
)

func TestProblemInvestigationExposesSummaryIDs(t *testing.T) {
	_, mux, st := newSRETestEnv(t)
	callID, xid := copilot.NewCallID(), newRandID(16)
	inv := sre.Investigation{ProblemID: "p1", Service: "pay", Kind: sre.KindAuto, State: sre.StateDone, EnqueuedAt: 1,
		Summary: &sre.Narration{Text: "Ne oldu: x", Model: "m", CallID: callID, ExchangeID: xid, At: 2}}
	body, err := sre.Encode(inv)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSREInvestigation(context.Background(), "p1", inv.State, body, 1); err != nil {
		t.Fatal(err)
	}
	w := sreDo(mux, "GET", "/api/problems/p1/investigation", "", auth.RoleViewer)
	if w.Code != 200 {
		t.Fatalf("GET = %d %s", w.Code, w.Body)
	}
	var raw struct {
		Investigation struct {
			Summary map[string]any `json:"summary"`
		} `json:"investigation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Investigation.Summary["callId"] != callID || raw.Investigation.Summary["exchangeId"] != xid {
		t.Fatalf("özet kimlikleri cevapta olmalı: %s", w.Body)
	}
	// Oy kapısı anlatımın bastığı biçimleri kabul eder.
	if err := validateAIFeedback(xid, -1); err != nil {
		t.Fatalf("exchangeId kapıda reddedildi: %v", err)
	}
	if v, keep, err := normalizeFeedbackCallID(&callID); err != nil || keep || v != callID {
		t.Fatalf("callId kapıda: %q %v %v", v, keep, err)
	}

	// Kapı kapalı özet: alanlar yok.
	gated := inv
	gated.ProblemID = "p2"
	gated.Summary = &sre.Narration{Text: sre.NarrationGatedText, At: 2, Gated: true, Reason: "kapalı"}
	gb, _ := sre.Encode(gated)
	_ = st.PutSREInvestigation(context.Background(), "p2", gated.State, gb, 1)
	w = sreDo(mux, "GET", "/api/problems/p2/investigation", "", auth.RoleViewer)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"callId"`) || strings.Contains(w.Body.String(), `"exchangeId"`) {
		t.Fatalf("kapalı özet kimlik taşımamalı: %d %s", w.Code, w.Body)
	}
}
