package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// think_parity_test.go — v0.10.1285 — tamponlu yol Gemma <|channel>thought
// bloğunu ayıklamıyordu.
//
// Kök neden: akış soyucusu (think_stream.go, v0.10.1273) Gemma kanal
// bloğunu ve kapanmamış bloğu gizliyordu; tamponlu StripThinking ise
// yalnız "son </think> sonrası" kuralını biliyordu ve parseOpenAIToolsChat
// soyucuyu yalnız metin "</think>" taşıyınca çağırıyordu. Sağlayıcı akmayınca
// (buffered geri düşüş) ya da akışın sonunda Finish uzlaştırması `answer`
// metniyle yayınlananı değiştirince düşünce nihai cevapta görünüyordu.
//
// Düzeltme: iki yol TEK makine (StripThinking → thinkVisible → thinkStripper,
// tek etiket tablosu thinkTagPairs). Bu test aynı vaka tablosunu iki yoldan
// geçirir — akış her bölme noktasında — ve görünen metnin eşit olduğunu
// pinler; yalnız eşitlik değil, beklenen metnin kendisi de tabloda.

var thinkParityCases = []struct {
	name, in, want string
}{
	{"düşüncesiz", "p95 1830 ms, hata %3.", "p95 1830 ms, hata %3."},
	{"baştaki think", "<think>önce metrik</think>Cevap: yavaş.", "Cevap: yavaş."},
	{"ortadaki think öncesini de geri alır", "Taslak <think>x</think>Son.", "Son."},
	{"başıboş </think>", "çalışma notu</think>Cevap.", "Cevap."},
	{"iki think bloğu", "<think>a</think>A<think>b</think>B", "B"},
	{"kapanmamış think gizli", "Cevap <think>yarım düşünce", "Cevap"},
	{"yalnız kapanmamış think", "<think>yarım düşünce", ""},
	{"benzer ama etiket değil", "a <thinking> b <thin c", "a <thinking> b <thin c"},
	{"küçüktür işareti", "p95 < 200ms ve x<t", "p95 < 200ms ve x<t"},
	{"gemma baştaki", "<|channel>thought plan<channel|>Cevap.", "Cevap."},
	{"gemma boşluklu", "<|channel>thought\nservis yavaş mı?\n<channel|>\n\nCevap: evet.", "Cevap: evet."},
	{"gemma ortada — öncesi KALIR", "Özet: <|channel>thought not<channel|>yavaş.", "Özet: yavaş."},
	{"gemma kapanmamış gizli", "Cevap. <|channel>thought yarım", "Cevap."},
	{"yalnız gemma kapanmamış", "<|channel>thought yarım", ""},
	{"iki gemma bloğu", "<|channel>thought a<channel|>A <|channel>thought b<channel|>B", "A B"},
	{"gemma sonra think", "<|channel>thought g<channel|>X<think>t</think>Y", "Y"},
	{"think sonra gemma", "<think>t</think>X<|channel>thought g<channel|>Y", "XY"},
	{"gemma içindeki </think> sayılmaz", "<|channel>thought a</think>b<channel|>C", "C"},
	{"başıboş gemma kapanışı düz metin", "a<channel|>b", "a<channel|>b"},
	{"benzer gemma öneki etiket değil", "<|channel>final x", "<|channel>final x"},
	{"türkçe çok baytlı", "<|channel>thought şüğı<channel|>çözüm: ölçek", "çözüm: ölçek"},
}

func TestThinkStripBufferedStreamParity(t *testing.T) {
	for _, tc := range thinkParityCases {
		buffered := StripThinking(tc.in)
		if buffered != tc.want {
			t.Errorf("%s: buffered StripThinking = %q, beklenen %q", tc.name, buffered, tc.want)
		}
		for n := 1; n <= len(tc.in); n++ {
			// Akış kırpmaz (sondaki boşluk Finish'e kalır); buffered kırpar —
			// karşılaştırma kırpık görünen metin üstünde.
			if got := strings.TrimSpace(playStripper(splitEvery(tc.in, n))); got != buffered {
				t.Errorf("%s (parça=%d): akış %q ≠ buffered %q", tc.name, n, got, buffered)
			}
		}
	}
}

// Nihai-cevap çıkışları: free loop'un tamponlu turu (parseOpenAIToolsChat),
// explain/guided/drawer/intent'in tamponlu yolu (ParseOpenAIChat →
// SalvageAnswer) ve akan turun Finish uzlaştırması — üçü de Gemma
// düşüncesini nihai metne koymamalı.
func TestGemmaThoughtNeverReachesFinalAnswer(t *testing.T) {
	const content = "<|channel>thought checkout p95'e bakmalıyım<channel|>checkout p95 1.8s — DB havuzu dolu."
	const want = "checkout p95 1.8s — DB havuzu dolu."
	body := `{"choices":[{"message":{"content":"` + content + `"},"finish_reason":"stop"}]}`

	t.Run("free loop buffered (tool sunulmuş)", func(t *testing.T) {
		out, err := parseOpenAIToolsChat([]byte(body), labelOpenAI, offeredT)
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != want || len(out.ToolCalls) != 0 {
			t.Fatalf("Text=%q calls=%d, beklenen %q", out.Text, len(out.ToolCalls), want)
		}
	})
	t.Run("free loop buffered (tool yok)", func(t *testing.T) {
		out, err := parseOpenAIToolsChat([]byte(body), labelOpenAI, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != want {
			t.Fatalf("Text=%q, beklenen %q", out.Text, want)
		}
	})
	t.Run("yalnız gemma düşüncesi — işaretli kurtarılır", func(t *testing.T) {
		only := `{"choices":[{"message":{"content":"<|channel>thought yalnız düşünce<channel|>"},"finish_reason":"stop"}]}`
		out, err := parseOpenAIToolsChat([]byte(only), labelOpenAI, nil)
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != MarkSalvagedThinking("yalnız düşünce") {
			t.Fatalf("Text=%q", out.Text)
		}
	})
	t.Run("explain buffered (SalvageAnswer)", func(t *testing.T) {
		resp, err := ParseOpenAIChat([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.Text != want {
			t.Fatalf("Text=%q, beklenen %q", resp.Text, want)
		}
	})
	t.Run("akan tur: Finish düşünceyi geri getirmez", func(t *testing.T) {
		for n := 1; n <= len(content); n += 7 {
			r := &gateRec{}
			g := newToolTurnGate(r.sink, false, nil)
			for _, c := range splitEvery(content, n) {
				g.Content(c)
			}
			out, err := parseOpenAIToolsChat([]byte(body), labelOpenAI, nil)
			if err != nil {
				t.Fatal(err)
			}
			g.Finish(out.Text, false)
			if r.anyText("thought") || r.anyText("bakmalıyım") {
				t.Fatalf("parça=%d: düşünce yayınlandı: %+v", n, r.deltas)
			}
			if r.visible() != want {
				t.Fatalf("parça=%d: görünen %q, beklenen %q", n, r.visible(), want)
			}
		}
	})
	// v0.10.1304 — explain/guided/drawer/intent'in AKAN yolu (StreamOpenAI →
	// copilot.StreamText). Eski kapı yalnız baştaki <think>'i tanıdığı için
	// Gemma bloğu canlıda ham akıyordu; nihai metin doğruydu, delta'lar değil.
	t.Run("explain akışı (StreamOpenAI): delta'lar düşünce taşımaz", func(t *testing.T) {
		for n := 1; n <= len([]rune(content)); n++ {
			var payloads []string
			for _, c := range splitRunes(content, n) {
				raw, _ := json.Marshal(c)
				payloads = append(payloads, `{"choices":[{"delta":{"content":`+string(raw)+`}}]}`)
			}
			payloads = append(payloads, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, `[DONE]`)
			rt := &streamRT{body: sse(payloads...)}
			cfg := Config{BaseURL: "http://llm.invalid/v1", HTTPClient: newStreamClient(rt)}
			var deltas []string
			resp, err := StreamOpenAI(context.Background(), cfg, Request{System: "s", User: "u"},
				func(d string) { deltas = append(deltas, d) })
			if err != nil {
				t.Fatalf("parça=%d: %v", n, err)
			}
			joined := strings.Join(deltas, "")
			if strings.Contains(joined, "thought") || strings.Contains(joined, "bakmalıyım") {
				t.Fatalf("parça=%d: düşünce akışta yayınlandı: %q", n, deltas)
			}
			if joined != want || resp.Text != want {
				t.Fatalf("parça=%d: delta'lar %q, Text %q; beklenen %q", n, joined, resp.Text, want)
			}
		}
	})
}
