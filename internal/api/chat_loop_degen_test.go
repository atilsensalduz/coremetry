package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// v0.10.1324 — operator-reported: gemma4 31b final turda bir önsöz cümlesini
// max_tokens dolana dek yazdı ve operatör onlarca kez aynı satırı gördü.
// Uçtan uca (gerçek copilotChat + gerçek copilot.Service + SSE sahte model):
// akış muhafızı final turu keser, Derin kapalıyken model YENİDEN ÇAĞRILMAZ,
// cevap deterministik kanıt özetidir (araç önizlemesi + künye), adım çipi
// operatöre söyler, akan metin answer ile değiştirilir.
func TestChatLoopDegenerateFinalTurnFallsBackToEvidence(t *testing.T) {
	llm := newSSELoopLLM(t, func(n int, w http.ResponseWriter, r *http.Request) {
		if n == 0 {
			sseChunk(w, map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "c0", "type": "function",
				"function": map[string]any{"name": "compare_periods", "arguments": `{"service":"checkout"}`},
			}}})
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		sseChunk(w, map[string]any{"content": "Operasyonun hatalarını inceledim ve kanıtı topladım; özet aşağıda, kaynaklar en altta yer alıyor.\n"})
		for i := 0; i < 400; i++ {
			if r.Context().Err() != nil {
				return
			}
			sseChunk(w, map[string]any{"content": "Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları (cluster/namespace/problem/filtreler) tool argümanlarında kullan.\n"})
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s := sseLoopServer(t, llm)
	(&fakeTools{}).install(t)
	frames := postLoopChat(t, s, context.Background(), "u-a", traceDrawerBody("Loglarda bu hatanın karşılığı var mı?", anchoredToMs(), "conv-1"))

	if llm.count() != 2 {
		t.Fatalf("model %d kez çağrıldı; Derin kapalıyken yeniden deneme olmamalı (2)", llm.count())
	}
	chip := false
	for _, f := range framesOf(frames, "step") {
		if l, _ := f["label"].(string); l == chatDegenFallbackLabel {
			chip = true
		}
	}
	if !chip {
		t.Error("yineleme düşüşü adım çipi yok")
	}
	ans := framesOf(frames, "answer")
	if len(ans) != 1 {
		t.Fatalf("answer sayısı %d", len(ans))
	}
	text, _ := ans[0]["text"].(string)
	for _, want := range []string{"yinelenen metne düştüğü", "**compare_periods**", "1830", "Kaynak:"} {
		if !strings.Contains(text, want) {
			t.Errorf("deterministik cevapta %q yok:\n%s", want, text)
		}
	}
	if strings.Contains(text, "AKSİNİ SÖYLEMEDİKÇE") {
		t.Errorf("yinelenen satır cevapta kaldı:\n%s", text)
	}
	if d := framesOf(frames, "done"); len(d) != 1 || d[0]["ok"] != true {
		t.Errorf("done: %v (düşüş hata değildir)", d)
	}
}
