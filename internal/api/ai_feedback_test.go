package api

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.8.399 — AI answer feedback (thumbs up/down). Pins the pure
// request gate for POST /api/ai/feedback: verdict ∈ {-1, 1},
// exchangeId non-empty and ≤ 64 chars, and — the integration
// contract — every id the chat handler mints (newRandID(16), the
// value emitted in the SSE answer event) must pass the gate, or
// legitimate thumbs clicks would 400.
func TestValidateAIFeedback(t *testing.T) {
	long := strings.Repeat("a", aiFeedbackMaxIDLen+1)
	exact := strings.Repeat("b", aiFeedbackMaxIDLen)
	cases := []struct {
		name       string
		exchangeID string
		verdict    int8
		wantErr    bool
	}{
		{"thumbs up", "0123456789abcdef0123456789abcdef", 1, false},
		{"thumbs down", "0123456789abcdef0123456789abcdef", -1, false},
		{"empty id", "", 1, true},
		{"whitespace id", "   ", 1, true},
		{"id at max length ok", exact, -1, false},
		{"id over max length", long, 1, true},
		{"verdict zero", "0123456789abcdef", 0, true},
		{"verdict two", "0123456789abcdef", 2, true},
		{"verdict minus two", "0123456789abcdef", -2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAIFeedback(tc.exchangeID, tc.verdict)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAIFeedback(%q, %d) err=%v, wantErr=%t",
					tc.exchangeID, tc.verdict, err, tc.wantErr)
			}
		})
	}
}

// v0.8.399 — server-minted exchange ids (the only ids a well-behaved
// client ever posts back) always pass validation and are hex, so the
// answer-event → feedback-POST loop can't reject its own ids.
func TestAIFeedbackAcceptsMintedExchangeIDs(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := newRandID(16)
		if len(id) != 32 {
			t.Fatalf("newRandID(16) len = %d, want 32", len(id))
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("newRandID(16) = %q is not hex: %v", id, err)
		}
		if err := validateAIFeedback(id, 1); err != nil {
			t.Fatalf("minted id %q rejected: %v", id, err)
		}
	}
}

// ── v0.9.1193 (AI Faz 5.1) — 👎 yorumu ─────────────────────────────────
//
// Telin en kritik ayrımı nil ↔ boş dize: alan HİÇ yoksa saklanan yorum
// KORUNUR (eski FE'ler ve düz oy tıkları comment göndermez; tam-satır
// replace, korunmasaydı herhangi bir flip operatörün yazdığı metni
// sessizce silerdi), boş dize açıkça TEMİZLER.
func TestNormalizeFeedbackComment(t *testing.T) {
	strp := func(s string) *string { return &s }

	t.Run("nil → preserve", func(t *testing.T) {
		val, preserve, err := normalizeFeedbackComment(nil)
		if err != nil || !preserve || val != "" {
			t.Fatalf("nil = (%q, %v, %v); beklenen ('', true, nil)", val, preserve, err)
		}
	})
	t.Run("boş dize → açık temizleme, preserve DEĞİL", func(t *testing.T) {
		val, preserve, err := normalizeFeedbackComment(strp(""))
		if err != nil || preserve || val != "" {
			t.Fatalf("'' = (%q, %v, %v); beklenen ('', false, nil)", val, preserve, err)
		}
	})
	t.Run("boşluk kırpılır", func(t *testing.T) {
		val, preserve, _ := normalizeFeedbackComment(strp("  cevap eksikti  "))
		if preserve || val != "cevap eksikti" {
			t.Fatalf("= (%q, %v)", val, preserve)
		}
	})
	t.Run("tavan RUNE cinsinden — Türkçe metin bayt tavanında kırpılmaz", func(t *testing.T) {
		// 2000 rune 'ş' ≈ 4000 bayt: bayt tavanı olsaydı reddedilirdi.
		ok := strings.Repeat("ş", aiFeedbackMaxCommentRunes)
		if _, _, err := normalizeFeedbackComment(strp(ok)); err != nil {
			t.Fatalf("tam tavan geçmeliydi: %v", err)
		}
		if _, _, err := normalizeFeedbackComment(strp(ok + "x")); err == nil {
			t.Fatal("tavan+1 reddedilmeliydi")
		}
	})
}

// ── v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls ─────
//
// Oy artık somut ai_calls satırına bağlanır (callId) ve 👎 hazır neden taşır
// (reason). Geriye uyum: callId'siz eski istemci gövdesi AYNEN kabul edilir
// (exchange düzeyinde oy) ve saklanan bağı/nedeni/yorumu SİLMEZ.
func TestAIFeedbackPayloadWithAndWithoutCallID(t *testing.T) {
	stored := chstore.AIFeedbackStored{Comment: "eski yorum", Reason: "wrong", CallID: "aaaa1111bbbb2222cccc3333"}
	cases := []struct {
		name                    string
		body                    string
		wantErr                 bool
		wantComment, wantReason string
		wantCall                string
	}{
		{"eski istemci: yalnız exchangeId + verdict → hepsi korunur",
			`{"exchangeId":"x1","verdict":1}`, false, "eski yorum", "wrong", "aaaa1111bbbb2222cccc3333"},
		{"yeni istemci: callId + reason",
			`{"exchangeId":"x1","verdict":-1,"callId":"0123456789abcdef01234567","reason":"incomplete"}`, false, "eski yorum", "incomplete", "0123456789abcdef01234567"},
		{"neden temizlenir, callId boş dize korunur",
			`{"exchangeId":"x1","verdict":-1,"callId":"","reason":""}`, false, "eski yorum", "", "aaaa1111bbbb2222cccc3333"},
		{"yorum + neden birlikte",
			`{"exchangeId":"x1","verdict":-1,"comment":"span eksik","reason":"irrelevant"}`, false, "span eksik", "irrelevant", "aaaa1111bbbb2222cccc3333"},
		{"küme dışı neden → 400", `{"exchangeId":"x1","verdict":-1,"reason":"bad"}`, true, "", "", ""},
		{"hex olmayan callId → 400", `{"exchangeId":"x1","verdict":-1,"callId":"<script>"}`, true, "", "", ""},
		{"büyük harf callId → 400", `{"exchangeId":"x1","verdict":-1,"callId":"ABCDEF"}`, true, "", "", ""},
		{"uzun callId → 400", `{"exchangeId":"x1","verdict":-1,"callId":"` + strings.Repeat("a", aiFeedbackMaxIDLen+1) + `"}`, true, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var req aiFeedbackRequest
			if err := json.Unmarshal([]byte(c.body), &req); err != nil {
				t.Fatal(err)
			}
			if err := validateAIFeedback(req.ExchangeID, req.Verdict); err != nil {
				t.Fatalf("taban kapısı: %v", err)
			}
			comment, keepC, err1 := normalizeFeedbackComment(req.Comment)
			call, keepK, err2 := normalizeFeedbackCallID(req.CallID)
			reason, keepR, err3 := normalizeFeedbackReason(req.Reason)
			gotErr := err1 != nil || err2 != nil || err3 != nil
			if gotErr != c.wantErr {
				t.Fatalf("hata %v/%v/%v, beklenen hata=%v", err1, err2, err3, c.wantErr)
			}
			if c.wantErr {
				return
			}
			comment, reason, call = mergeStoredFeedback(stored, comment, reason, call, keepC, keepR, keepK)
			if comment != c.wantComment || reason != c.wantReason || call != c.wantCall {
				t.Errorf("birleşim = (%q, %q, %q); beklenen (%q, %q, %q)", comment, reason, call, c.wantComment, c.wantReason, c.wantCall)
			}
		})
	}
	// Sunucunun bastığı her çağrı kimliği kapıdan geçer (copilot.NewCallID).
	for i := 0; i < 50; i++ {
		id := copilot.NewCallID()
		if v, _, err := normalizeFeedbackCallID(&id); err != nil || v != id {
			t.Fatalf("basılmış kimlik %q reddedildi: %v", id, err)
		}
	}
}
