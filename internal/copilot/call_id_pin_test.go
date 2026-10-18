package copilot

import (
	"context"
	"testing"
)

// v0.10.1312 — AI SRE anlatımının 👍/👎 rayı: arka plan Explain çağrısı
// ai_calls kimliğini çağrıdan ÖNCE sabitler (WithCallID) ve kayıt o kimlikle
// yazılır (recordNarration → callIDFor); exchange kimliği meta'dan satıra ve
// call-sink notuna biner. Sabitlenmemiş ctx eski davranışı korur: her çağrı
// taze kimlik.
func TestExplainHonoursPinnedCallID(t *testing.T) {
	body := `{"choices":[{"message":{"content":"Ne oldu: x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7}}`
	s, done := newOpenAITestService(t, body)
	defer done()
	capt := &shieldCapture{}
	s.SetRecorder(capt)

	const xid = "0123456789abcdef0123456789abcdef"
	callID := NewCallID()
	var note CallNote
	ctx := WithCallSink(WithCallID(WithMeta(context.Background(),
		CallMeta{Surface: "sre-investigation", UserID: "system", ExchangeID: xid}), callID),
		func(n CallNote) { note = n })
	if got := CallIDFromContext(ctx); got != callID {
		t.Fatalf("CallIDFromContext = %q, want %q", got, callID)
	}
	if _, err := s.Explain(ctx, "SYS", "u"); err != nil {
		t.Fatal(err)
	}
	rec := capt.last(t)
	if rec.ID != callID || rec.ExchangeID != xid || rec.Surface != "sre-investigation" {
		t.Fatalf("kayıt sabitlenen kimlikle yazılmalı: id=%q xid=%q surface=%q", rec.ID, rec.ExchangeID, rec.Surface)
	}
	if note.CallID != callID || note.ExchangeID != xid {
		t.Fatalf("call-sink notu aynı kimliği taşımalı: %+v", note)
	}

	// Sabitlenmemiş ctx: taze, boş olmayan, öncekinden farklı kimlik.
	capt2 := &shieldCapture{}
	s.SetRecorder(capt2)
	if _, err := s.Explain(WithMeta(context.Background(), CallMeta{Surface: "t"}), "SYS", "u"); err != nil {
		t.Fatal(err)
	}
	if r := capt2.last(t); r.ID == "" || r.ID == callID {
		t.Fatalf("sabitlenmemiş çağrı taze kimlik almalı: %q", r.ID)
	}
	if CallIDFromContext(context.Background()) != "" || CallIDFromContext(WithCallID(context.Background(), "")) != "" {
		t.Fatal("boş ctx / boş kimlik \"\" döner")
	}
}
