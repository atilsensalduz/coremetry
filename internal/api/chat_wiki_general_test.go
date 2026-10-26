package api

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

// v0.10.1549 — wiki "bulunamadı" → genel bilgi cevabı; etiket sunucudan.
func TestWikiGeneralText(t *testing.T) {
	cases := []struct {
		raw    string
		ok     bool
		prefix string
	}{
		{"", false, ""},
		{"   \n ", false, ""},
		{"Java ajanını -javaagent ile ekleyin.", true, wikiGeneralNoteTR + "\n\nJava ajanını"},
	}
	for _, c := range cases {
		got, ok := wikiGeneralText(c.raw)
		if ok != c.ok || !strings.HasPrefix(got, c.prefix) {
			t.Errorf("wikiGeneralText(%q) = %q, %v", c.raw, got, ok)
		}
	}
	// Etiketli cevap ret sayılmaz (hafıza / takip yolu onu "bulunamadı" sanmasın).
	if txt, _ := wikiGeneralText("x"); wikiDeclined(txt) {
		t.Fatal("genel bilgi cevabı wikiDeclined sayılmamalı")
	}
}

// Kapı kapalıyken (copilot yok) ret cevabı bayt bayt aynen kalır.
func TestWikiGeneralFallbackGateClosed(t *testing.T) {
	ans := map[string]any{"text": "Wikide bulunamadı: bu soruyla eşleşen bir wiki sayfası yok."}
	var s Server
	if s.wikiGeneralFallback(t.Context(), "otel java", ans, func(string, any) { t.Fatal("emit olmamalı") }) {
		t.Fatal("kapı kapalıyken fallback koşmamalı")
	}
	if ans["text"] != "Wikide bulunamadı: bu soruyla eşleşen bir wiki sayfası yok." {
		t.Fatalf("ret metni değişti: %v", ans["text"])
	}
}

// Uçtan uca: genel sorular açık + wiki'de eşleşme yok → TEK genel-bilgi
// çağrısı, cevap sunucu etiketiyle başlar, metin modelinki.
func TestWikiTierNotFoundAnswersFromGeneralKnowledge(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}})
	setGeneralQuestions(t, true)
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any {
		return map[string]any{"content": "Java ajanını -javaagent ile ekleyin."}
	})
	s, _ := newLoopTestServer(t, llm, "u-wg")
	fr := postLoopChat(t, s, context.Background(), "u-wg", map[string]any{
		"messages": []map[string]any{{"role": "user", "text": "kafka sertifika yenileme runbook"}},
	})
	text := answerTextOf(t, fr)
	if !strings.HasPrefix(text, wikiGeneralNoteTR) || !strings.Contains(text, "-javaagent") {
		t.Fatalf("cevap: %q", text)
	}
	if n := len(llm.calls()); n != 1 {
		t.Fatalf("LLM çağrı sayısı %d, beklenen 1", n)
	}
}
