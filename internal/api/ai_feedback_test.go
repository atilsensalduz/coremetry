package api

import (
	"encoding/hex"
	"os"
	"regexp"
	"testing"
)

// v0.8.399 → v0.10.1393 — server-minted exchange ids (the only ids a
// well-behaved client ever posts back — CoSRE örtük sinyali, RAG
// küratörlüğü, evalset dışa aktarımı) are 32-char hex and fit the
// aiFeedbackMaxIDLen gate those handlers share.
func TestMintedExchangeIDsFitIDGate(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := newRandID(16)
		if len(id) != 32 {
			t.Fatalf("newRandID(16) len = %d, want 32", len(id))
		}
		if _, err := hex.DecodeString(id); err != nil {
			t.Fatalf("newRandID(16) = %q is not hex: %v", id, err)
		}
		if len(id) > aiFeedbackMaxIDLen {
			t.Fatalf("minted id %q exceeds aiFeedbackMaxIDLen=%d", id, aiFeedbackMaxIDLen)
		}
	}
}

// v0.10.1393 — operatör: "Faydalı faydasız kaldıralım." 👍/👎 rayı hiçbir AI
// yüzeyinde yok; yazma ucu da kayıtlı DEĞİL. Kayıtsız rota sessizce SPA'ya
// düşer (200 + boş sayfa, /api-route), yani geri eklenen bir kayıt bir FE
// rayını yeniden canlandırmanın ilk adımı olurdu — burada kızarır. Geçmiş
// oyların okuması (GET /api/ai/feedback/negative) serbest.
func TestAIFeedbackWriteRouteRemoved(t *testing.T) {
	b, err := os.ReadFile("ai_routes.go")
	if err != nil {
		t.Fatalf("ai_routes.go okunamadı: %v", err)
	}
	src := apiLineComment.ReplaceAllString(string(b), "")
	if regexp.MustCompile(`"POST\s+/api/ai/feedback"`).MatchString(src) {
		t.Error("POST /api/ai/feedback yeniden kaydedilmiş — 👍/👎 yazma ucu v0.10.1393'te kaldırıldı")
	}
	if !regexp.MustCompile(`"GET\s+/api/ai/feedback/negative"`).MatchString(src) {
		t.Error("GET /api/ai/feedback/negative kaybolmuş — geçmiş 👎'ler /ai'da okunmalı")
	}
}
