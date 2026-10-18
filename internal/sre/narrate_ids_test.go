package sre

// v0.10.1312 — AI SRE özetinin 👍/👎 rayı. Faz 5'in "incelemeye değer" listesi
// (ListSREReviewDownvotes: ai_feedback.call_id ⋈ ai_calls, yüzey sre-*) hep
// boş geliyordu: anlatım hangi ai_calls satırının özeti ürettiğini ve bir
// exchange kimliğini saklamıyordu, FE özete oy rayı çizemiyordu. Pin:
//   - anlatım çağrısı ctx'e WithCallID + meta.ExchangeID ile gider ve AYNI
//     iki kimlik Narration'a yazılır (gerçek kayıt bu kimlikle iner —
//     copilot TestExplainHonoursPinnedCallID);
//   - kimlik biçimleri ai_feedback kapısından geçer (hex, ≤64);
//   - her anlatım taze kimlik (yeniden araştırmanın oyu eskisine binmez);
//   - kapı kapalı / hata / tekrar (Kind=replay) → kimlik yok (buton yok);
//   - JSON sözleşmesi FE'nin okuduğu alan adları (callId / exchangeId).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func isLowerHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != ""
}

func TestNarrationStoresCallAndExchangeID(t *testing.T) {
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: checkout hata üretiyor."}
	inv := newInv()
	runInvestigation(context.Background(), okReaders(&calls{}, n), inv, func(*Investigation) {}, func() time.Time { return t0 })
	if n.calls != 1 || inv.Summary == nil || inv.Summary.Gated {
		t.Fatalf("tek anlatım çağrısı: calls=%d %+v", n.calls, inv.Summary)
	}
	sum := inv.Summary
	if sum.CallID == "" || sum.CallID != n.callID {
		t.Fatalf("Narration.CallID ctx'te sabitlenenle aynı olmalı: kayıt %q, çağrı %q", sum.CallID, n.callID)
	}
	if sum.ExchangeID == "" || sum.ExchangeID != n.exchangeID {
		t.Fatalf("Narration.ExchangeID meta'daki exchange ile aynı olmalı: kayıt %q, çağrı %q", sum.ExchangeID, n.exchangeID)
	}
	if n.surface != NarrationSurface {
		t.Fatalf("yüzey %q — ListSREReviewDownvotes sre-* süzer", n.surface)
	}
	for name, id := range map[string]string{"callId": sum.CallID, "exchangeId": sum.ExchangeID} {
		// ai_feedback kapısı: exchangeId ≤64, callId küçük harf hex ≤64.
		if !isLowerHex(id) || len(id) > 64 {
			t.Errorf("%s %q ai_feedback kapısından geçmez", name, id)
		}
		if strings.Contains(id, ":") {
			t.Errorf("%s %q kök ayırıcısı taşımamalı", name, id)
		}
	}
	if sum.CallID == sum.ExchangeID {
		t.Fatal("çağrı ve exchange kimliği ayrı basılır")
	}

	// JSON: FE'nin okuduğu alan adları; kayıt (saved_views gövdesi) gidiş-dönüşte korur.
	raw, err := json.Marshal(sum)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"callId":"`+sum.CallID+`"`) || !strings.Contains(string(raw), `"exchangeId":"`+sum.ExchangeID+`"`) {
		t.Fatalf("JSON alanları: %s", raw)
	}
	body, err := Encode(*inv)
	if err != nil {
		t.Fatal(err)
	}
	back, ok := Decode(chstore.SavedView{ID: chstore.SREInvestigationID(inv.ProblemID), Page: chstore.SREInvestigationPage, Name: inv.State, QueryString: body})
	if !ok || back.Summary == nil || back.Summary.CallID != sum.CallID || back.Summary.ExchangeID != sum.ExchangeID {
		t.Fatalf("kayıt gidiş-dönüşü kimlikleri korumalı: %+v", back.Summary)
	}

	// Yeniden anlatım taze kimlik basar.
	inv2 := newInv()
	if st := narrate(context.Background(), okReaders(&calls{}, n), inv2, chstore.Problem{Service: "checkout"}, time.Now); st.State != StepOK {
		t.Fatalf("ikinci anlatım: %+v", st)
	}
	if inv2.Summary.CallID == sum.CallID || inv2.Summary.ExchangeID == sum.ExchangeID {
		t.Fatalf("her anlatım taze kimlik almalı: %+v vs %+v", inv2.Summary, sum)
	}
}

func TestNarrationWithoutLLMCallHasNoIDs(t *testing.T) {
	// Kapı kapalı → çağrı yok, kimlik yok.
	gated := newInv()
	narrate(context.Background(), okReaders(&calls{}, &fakeNarrator{active: true, auto: false}), gated, chstore.Problem{}, time.Now)
	if gated.Summary == nil || !gated.Summary.Gated || gated.Summary.CallID != "" || gated.Summary.ExchangeID != "" {
		t.Fatalf("kapalı kapı kimlik taşımamalı: %+v", gated.Summary)
	}
	// Sağlayıcı hatası → özet yok (önceki özet varsa o kalır, kimlikleriyle).
	prev := &Narration{Text: "eski", CallID: "aa", ExchangeID: "bb", At: 1}
	failed := newInv()
	failed.Summary = prev
	n := &fakeNarrator{active: true, auto: true, err: errors.New("boom")}
	if st := narrate(context.Background(), okReaders(&calls{}, n), failed, chstore.Problem{}, time.Now); st.State != StepError || failed.Summary != prev {
		t.Fatalf("hata önceki özeti değiştirmemeli: %+v %+v", st, failed.Summary)
	}
	// Tekrar (Kind=replay) → LLM yok, kimlik yok.
	rep := newInv()
	rep.Kind = KindReplay
	rep.AsOf = t0.UnixNano()
	rn := &fakeNarrator{active: true, auto: true, answer: "x"}
	runInvestigation(context.Background(), okReaders(&calls{}, rn), rep, func(*Investigation) {}, func() time.Time { return t0 })
	if rn.calls != 0 || rep.Summary == nil || rep.Summary.CallID != "" || rep.Summary.ExchangeID != "" {
		t.Fatalf("tekrar oylanabilir özet üretmemeli: calls=%d %+v", rn.calls, rep.Summary)
	}
}
