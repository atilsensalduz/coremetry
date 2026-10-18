package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// anthropic_think_parity_test.go — v0.10.1308 — Anthropic yolunun NİHAİ
// metni düşünceyi soymuyordu.
//
// Kök neden: finishAnthropic (akış) content'i yalnız trim'liyor, buffered
// ParseAnthropic text bloklarını ham birleştiriyordu. Gemma'yı Messages
// şekliyle sunan bir vekilde canlı delta'lar 1304'ten beri temizdi
// (live_think_gate.go) ama `answer` çerçevesi `<think>…</think>` /
// `<|channel>thought…<channel|>` bloğunu geri getiriyordu.
//
// Düzeltme: iki Anthropic çıkışı da openai ile AYNI zincirden geçer
// (SalvageAnswer → StripThinking → thinkStripper). Bu test paylaşılan parite
// tablosunu (think_parity_test.go thinkParityCases) dört yoldan geçirir ve
// Anthropic sonuçlarının openai sonuçlarına EŞİT olduğunu pinler — yalnız
// düşünce taşıyan vakalarda (kurtarılmış + işaretli) dahil.

func anthropicTextBody(text string) []byte {
	raw, _ := json.Marshal(text)
	return []byte(`{"content":[{"type":"text","text":` + string(raw) + `}],"stop_reason":"end_turn"}`)
}

func openAIContentBody(content string) []byte {
	raw, _ := json.Marshal(content)
	return []byte(`{"choices":[{"message":{"content":` + string(raw) + `},"finish_reason":"stop"}]}`)
}

// anthropicTextSSE — metni n-rune'luk text_delta olaylarına böler.
func anthropicTextSSE(text string, n int) string {
	payloads := []string{`{"type":"message_start","message":{"usage":{"input_tokens":3}}}`}
	for _, c := range splitRunes(text, n) {
		raw, _ := json.Marshal(c)
		payloads = append(payloads, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(raw)+`}}`)
	}
	payloads = append(payloads,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`)
	return sse(payloads...)
}

func streamAnthropicText(t *testing.T, body string) (Response, []string, error) {
	t.Helper()
	rt := &streamRT{body: body}
	cfg := Config{APIKey: "k", HTTPClient: newStreamClient(rt)}
	var deltas []string
	resp, err := StreamAnthropic(context.Background(), cfg, Request{System: "s", User: "u"},
		func(d string) { deltas = append(deltas, d) })
	return resp, deltas, err
}

// openAIFinal — openai buffered yolunun nihai metni ("" = kurtarılacak bir
// şey yok; openai orada EmptyAnswerError döner).
func openAIFinal(t *testing.T, content string) string {
	t.Helper()
	resp, err := ParseOpenAIChat(openAIContentBody(content))
	if err != nil {
		return ""
	}
	return resp.Text
}

func TestAnthropicFinalTextMatchesOpenAIParityTable(t *testing.T) {
	for _, tc := range thinkParityCases {
		want := openAIFinal(t, tc.in)
		// Tablo beklentisi openai'de de tutmalı; boş beklentili vakalar
		// (yalnız kapanmamış blok) openai'de işaretli kurtarılır.
		if tc.want != "" && want != tc.want {
			t.Fatalf("%s: openai nihai metni %q, tablo %q", tc.name, want, tc.want)
		}
		if tc.want == "" && !IsSalvagedThinking(want) {
			t.Fatalf("%s: openai yalnız-düşünce vakası işaretli değil: %q", tc.name, want)
		}

		// openai akan yol — referansın kendi içinde tutarlı olduğunu da pinle.
		for n := 1; n <= len([]rune(tc.in)); n++ {
			a := &openAIStreamAccum{}
			for _, c := range splitRunes(tc.in, n) {
				raw, _ := json.Marshal(c)
				a.feed(`data: {"choices":[{"delta":{"content":` + string(raw) + `}}]}`)
			}
			a.flushGate()
			if final, _, _ := a.finishOpenAI(); final != want {
				t.Fatalf("%s (parça=%d): openai akış %q ≠ buffered %q", tc.name, n, final, want)
			}
		}

		// Anthropic buffered.
		resp, err := ParseAnthropic(anthropicTextBody(tc.in))
		if err != nil {
			t.Fatalf("%s: ParseAnthropic: %v", tc.name, err)
		}
		if resp.Text != want {
			t.Errorf("%s: ParseAnthropic.Text = %q, openai %q", tc.name, resp.Text, want)
		}

		// Anthropic akış — her bölme noktasında.
		for n := 1; n <= len([]rune(tc.in)); n++ {
			sresp, deltas, err := streamAnthropicText(t, anthropicTextSSE(tc.in, n))
			if err != nil {
				t.Fatalf("%s (parça=%d): StreamAnthropic: %v", tc.name, n, err)
			}
			if sresp.Text != want {
				t.Errorf("%s (parça=%d): StreamAnthropic.Text = %q, openai %q", tc.name, n, sresp.Text, want)
			}
			// Hiçbir şey akmadıysa (yalnız-düşünce) kurtarılan cevap TEK
			// trailing delta olarak gider — openai ile aynı sözleşme.
			if IsSalvagedThinking(want) && strings.Join(deltas, "") != want {
				t.Errorf("%s (parça=%d): delta'lar %q; kurtarılan cevap tek parça gelmeliydi", tc.name, n, deltas)
			}
		}
	}
}

func TestAnthropicFinalTextStripsThinking(t *testing.T) {
	cases := []struct {
		name, text, want string
		thoughts         []string // nihai metinde ASLA görünmemeli
		salvaged         bool
	}{
		{
			name:     "gemma bloğu",
			text:     "<|channel>thought checkout p95'e bakmalıyım<channel|>checkout p95 1.8s — DB havuzu dolu.",
			want:     "checkout p95 1.8s — DB havuzu dolu.",
			thoughts: []string{"thought", "bakmalıyım", "<channel|>"},
		},
		{
			name:     "ortadaki <think>",
			text:     "Taslak cevap <think>hayır, önce redis</think>Sorun redis bağlantı havuzu.",
			want:     "Sorun redis bağlantı havuzu.",
			thoughts: []string{"<think>", "önce redis", "Taslak"},
		},
		{
			name:     "yalnız düşünce — işaretli kurtarılır",
			text:     "<think>checkout span'i Oracle satır kilidinde bekliyor</think>",
			want:     MarkSalvagedThinking("checkout span'i Oracle satır kilidinde bekliyor"),
			thoughts: []string{"<think>", "</think>"},
			salvaged: true,
		},
		{
			name:     "yalnız gemma düşüncesi — işaretli kurtarılır",
			text:     "<|channel>thought yalnız düşünce<channel|>",
			want:     MarkSalvagedThinking("yalnız düşünce"),
			thoughts: []string{"<|channel>", "<channel|>"},
			salvaged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := openAIFinal(t, tc.text); got != tc.want {
				t.Fatalf("openai referansı %q, beklenen %q", got, tc.want)
			}
			check := func(path, got string) {
				t.Helper()
				if got != tc.want {
					t.Errorf("%s: %q, beklenen %q", path, got, tc.want)
				}
				if IsSalvagedThinking(got) != tc.salvaged {
					t.Errorf("%s: işaret=%v, beklenen %v", path, IsSalvagedThinking(got), tc.salvaged)
				}
				body := strings.TrimPrefix(got, SalvagedThinkingPrefix)
				for _, th := range tc.thoughts {
					if strings.Contains(body, th) {
						t.Errorf("%s: nihai metin düşünce taşıyor (%q): %q", path, th, got)
					}
				}
			}
			resp, err := ParseAnthropic(anthropicTextBody(tc.text))
			if err != nil {
				t.Fatal(err)
			}
			check("buffered", resp.Text)

			// VerdictParseBuffered: vekil stream:true'yu yuttu — Service gövdeyi
			// ParseAnthropic'e verir; StreamAnthropic'in elindeki gövde de odur.
			for n := 1; n <= len([]rune(tc.text)); n += 3 {
				sresp, _, err := streamAnthropicText(t, anthropicTextSSE(tc.text, n))
				if err != nil {
					t.Fatalf("parça=%d: %v", n, err)
				}
				check("akış", sresp.Text)
			}
		})
	}
}

// Ayrılmış düşünce kanalı: Anthropic thinking bloğu / thinking_delta, openai
// reasoning_content'in karşılığı — text boşken İŞARETLİ kurtarılır ve üç yol
// aynı metni verir. (Eskiden buffered yol thinking bloğunu atıp boş dönüyordu,
// akış işaretli kurtarıyordu.)
func TestAnthropicThinkingChannelOnlyIsSalvagedLikeOpenAI(t *testing.T) {
	const note = "checkout p95 yükseliyor; DB havuzuna bakmalı"
	want := MarkSalvagedThinking(note)

	oresp, err := ParseOpenAIChat([]byte(`{"choices":[{"message":{"content":"","reasoning_content":"` + note + `"},"finish_reason":"stop"}]}`))
	if err != nil || oresp.Text != want {
		t.Fatalf("openai referansı (%q, %v), beklenen %q", oresp.Text, err, want)
	}

	resp, err := ParseAnthropic([]byte(`{"content":[{"type":"thinking","thinking":"` + note + `","signature":"x"},{"type":"text","text":""}]}`))
	if err != nil || resp.Text != want {
		t.Errorf("buffered = (%q, %v), beklenen %q", resp.Text, err, want)
	}

	body := sse(
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"`+note+`"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
	)
	sresp, deltas, err := streamAnthropicText(t, body)
	if err != nil || sresp.Text != want {
		t.Errorf("akış = (%q, %v), beklenen %q", sresp.Text, err, want)
	}
	if strings.Join(deltas, "") != want {
		t.Errorf("akış delta'ları %q; kurtarılan cevap tek trailing parça olmalıydı", deltas)
	}

	// text doluysa thinking kanalı cevaba KARIŞMAZ (işaretsiz, yalnız text).
	resp, err = ParseAnthropic([]byte(`{"content":[{"type":"thinking","thinking":"` + note + `"},{"type":"text","text":"Cevap."}]}`))
	if err != nil || resp.Text != "Cevap." {
		t.Errorf("text dolu: (%q, %v), beklenen \"Cevap.\"", resp.Text, err)
	}
}
