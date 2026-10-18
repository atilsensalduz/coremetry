package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// v0.10.1273 — CoSRE adım 4: serbest döngüde token akışı + adım satırları.
//
// Pinlenen sözleşmeler:
//   - düşünce soyucu parça sınırından bağımsız (etiket ortadan bölünse de),
//   - tool-call turu metin yayınlamaz (kısa önsöz hiç görünmez, uzun önsöz
//     Reset ile geri alınır), metin-gömülü çağrının JSON'u asla akmaz,
//   - final turda son Reset'ten sonraki deltaların birleşimi = buffered metin,
//   - akan tur buffered çözümleyiciyle BİREBİR aynı ChatResponse'u verir
//     (tool_call parçaları birleşir, Gemini ek alanı Raw'da korunur),
//   - ctx iptali üst akış isteğini keser.

// splitEvery — s'yi n baytlık parçalara böler (etiket ortası bölünmeleri dâhil).
func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// splitRunes — rune sınırında böler (SSE JSON dizgisi daima geçerli UTF-8).
func splitRunes(s string, n int) []string {
	r := []rune(s)
	var out []string
	for len(r) > n {
		out = append(out, string(r[:n]))
		r = r[n:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// playStripper — parçaları soyucudan geçirir, Reset'leri uygulayarak
// operatörün GÖRDÜĞÜ metni döndürür.
func playStripper(chunks []string) string {
	var st thinkStripper
	var seen strings.Builder
	for _, c := range chunks {
		out, reset := st.Feed(c)
		if reset {
			seen.Reset()
		}
		seen.WriteString(out)
	}
	seen.WriteString(st.Flush())
	return seen.String()
}

func TestThinkStripperChunkBoundaries(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"düşüncesiz", "p95 1830 ms, hata %3.", "p95 1830 ms, hata %3."},
		{"baştaki blok", "<think>önce metrik</think>Cevap: yavaş.", "Cevap: yavaş."},
		{"ortadaki blok öncesini de geri alır", "Taslak <think>x</think>Son.", "Son."},
		{"başıboş kapanış", "çalışma notu</think>Cevap.", "Cevap."},
		{"iki blok", "<think>a</think>A<think>b</think>B", "B"},
		{"kapanmamış blok gizli kalır", "Cevap <think>yarım düşünce", "Cevap "},
		{"benzer ama etiket değil", "a <thinking> b <thin c", "a <thinking> b <thin c"},
		{"küçüktür işareti", "p95 < 200ms ve x<t", "p95 < 200ms ve x<t"},
		{"gemma kanal düşüncesi", "<|channel>thought plan<channel|>Cevap.", "Cevap."},
	}
	for _, tc := range cases {
		for n := 1; n <= len(tc.in); n++ {
			if got := playStripper(splitEvery(tc.in, n)); got != tc.want {
				t.Errorf("%s (parça=%d): %q, beklenen %q", tc.name, n, got, tc.want)
			}
		}
	}
}

// gateRun — bir gate'i parçalarla besler; olayları ve görülen metni döndürür.
type gateRec struct {
	deltas []TextDelta
}

func (r *gateRec) sink(d TextDelta) { r.deltas = append(r.deltas, d) }

// visible — Reset'leri uygulayarak son görünen metin.
func (r *gateRec) visible() string {
	var b strings.Builder
	for _, d := range r.deltas {
		if d.Reset {
			b.Reset()
		}
		b.WriteString(d.Text)
	}
	return b.String()
}

func (r *gateRec) anyText(sub string) bool {
	for _, d := range r.deltas {
		if strings.Contains(d.Text, sub) {
			return true
		}
	}
	return false
}

var offeredT = []string{"query_metrics", "query_logs", "query_traces"}

// longProse — n FARKLI cümle. v0.10.1324: eskiden aynı cümlenin n kopyasıydı;
// yineleme muhafızı (degen_guard.go) o metni haklı olarak dejenere sayar.
func longProse(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "checkout servisinde %d numaralı pencerede p95 gecikmesi yükseldi. ", i+1)
	}
	return b.String()
}

func TestToolTurnGate(t *testing.T) {
	answer := "## Bulgu\n" + longProse(6) + "\n- p95 < 200ms hedefi aşıldı.\n```promql\nsum(rate(x[5m]))\n```\nSon satır."
	t.Run("final tur akar, birleşim = buffered metin", func(t *testing.T) {
		for _, n := range []int{1, 3, 7, 64} {
			r := &gateRec{}
			g := newToolTurnGate(r.sink, true, offeredT)
			for _, c := range splitEvery(answer, n) {
				g.Content(c)
			}
			streamedBeforeFinish := len(r.deltas)
			g.Finish(answer, false)
			if r.visible() != answer {
				t.Fatalf("parça=%d: görünen %q", n, r.visible())
			}
			if streamedBeforeFinish == 0 {
				t.Fatalf("parça=%d: tur bitmeden hiçbir şey akmadı", n)
			}
			for _, d := range r.deltas {
				if d.Reset {
					t.Fatalf("parça=%d: düz cevapta Reset olmamalı", n)
				}
			}
		}
	})
	t.Run("kısa önsöz + yapılandırılmış çağrı: hiçbir şey akmaz", func(t *testing.T) {
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, offeredT)
		g.Content("Önce metriklere bakıyorum.")
		g.ToolCall()
		g.Finish("Önce metriklere bakıyorum.", true)
		if len(r.deltas) != 0 {
			t.Fatalf("tool-call turu delta yayınladı: %+v", r.deltas)
		}
	})
	t.Run("uzun önsöz + yapılandırılmış çağrı: Reset ile geri alınır", func(t *testing.T) {
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, nil) // anthropic şekli
		g.Content(longProse(8))
		if r.visible() == "" {
			t.Fatal("commit eşiği aşıldı ama akmadı")
		}
		g.ToolCall()
		g.Content("devam metni")
		g.Finish(longProse(8)+"devam metni", true)
		if r.visible() != "" {
			t.Fatalf("tool-call turunun metni ekranda kaldı: %q", r.visible())
		}
	})
	t.Run("metin-gömülü Hermes çağrısı JSON'u akmaz", func(t *testing.T) {
		in := longProse(5) + "\n<tool_call>{\"name\":\"query_logs\",\"arguments\":{\"q\":\"x\"}}</tool_call>"
		for _, n := range []int{1, 5, 40} {
			r := &gateRec{}
			g := newToolTurnGate(r.sink, true, offeredT)
			for _, c := range splitEvery(in, n) {
				g.Content(c)
			}
			if r.anyText("tool_call") || r.anyText(`"name"`) {
				t.Fatalf("parça=%d: çağrı metni aktı: %+v", n, r.deltas)
			}
			g.Finish("", true)
			if r.visible() != "" {
				t.Fatalf("parça=%d: tool turu ekranda kaldı: %q", n, r.visible())
			}
		}
	})
	t.Run("çıplak JSON çağrı satırı akmaz", func(t *testing.T) {
		in := longProse(5) + "\n{\"name\":\"query_metrics\",\"arguments\":{\"service\":\"checkout\"}}\n"
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, offeredT)
		for _, c := range splitEvery(in, 2) {
			g.Content(c)
		}
		if r.anyText("query_metrics") {
			t.Fatalf("çağrı satırı aktı")
		}
	})
	t.Run("pythonic çağrı satırı akmaz", func(t *testing.T) {
		in := longProse(5) + "\nquery_logs(service=\"checkout\", level=\"ERROR\")\n"
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, offeredT)
		for _, c := range splitEvery(in, 3) {
			g.Content(c)
		}
		if r.anyText("query_logs") {
			t.Fatalf("pythonic çağrı aktı")
		}
	})
	t.Run("çağrı olmayan JSON örneği akar", func(t *testing.T) {
		in := longProse(5) + "\n{\"service\":\"checkout\",\"p95\":1830}\nbitti\n"
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, offeredT)
		for _, c := range splitEvery(in, 4) {
			g.Content(c)
		}
		if !r.anyText(`"p95":1830`) {
			t.Fatalf("zararsız JSON satırı tutulu kaldı: %q", r.visible())
		}
	})
	t.Run("tavan turu (çağrı yasak) beklemeden akar", func(t *testing.T) {
		r := &gateRec{}
		g := newToolTurnGate(r.sink, false, offeredT)
		g.Content("Kısa")
		if r.visible() != "Kısa" {
			t.Fatalf("passthrough beklenirken %q", r.visible())
		}
	})
	t.Run("düşünce bloğu akmaz, sonrası akar", func(t *testing.T) {
		in := "<think>" + longProse(5) + "</think>\n\n" + longProse(5)
		r := &gateRec{}
		g := newToolTurnGate(r.sink, true, offeredT)
		for _, c := range splitEvery(in, 3) {
			g.Content(c)
		}
		if r.anyText("<think") || r.anyText("</think") {
			t.Fatal("düşünce etiketi aktı")
		}
		final := StripThinking(in)
		g.Finish(final, false)
		if r.visible() != final {
			t.Fatalf("görünen %q, beklenen %q", r.visible(), final)
		}
	})
	t.Run("son işlem metni değiştirdiyse Reset + tam metin", func(t *testing.T) {
		r := &gateRec{}
		g := newToolTurnGate(r.sink, false, nil)
		g.Content("ham cevap")
		g.Finish(MarkSalvagedThinking("kurtarılan"), false)
		if r.visible() != MarkSalvagedThinking("kurtarılan") {
			t.Fatalf("görünen %q", r.visible())
		}
	})
}

// ─── uçtan uca (httptest SSE) ───────────────────────────────────────

func sseServer(t *testing.T, chunks []string, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotBody != nil {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func toolReq() ChatRequest {
	return ChatRequest{
		System:   "sys",
		Messages: []ChatMessage{{Role: "user", Text: "checkout neden yavaş?"}},
		Tools:    []ToolSpec{{Name: "query_metrics"}, {Name: "query_logs"}},
	}
}

func TestStreamOpenAIToolsFinalTurnStreamsInOrder(t *testing.T) {
	parts := append([]string{"<thi", "nk>plan</th", "ink>", "checkout ", "p95 ", "1830 ms; "}, splitRunes(longProse(8), 9)...)
	var chunks []string
	for _, p := range parts {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": p}}}})
		chunks = append(chunks, string(b))
	}
	chunks = append(chunks, `{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`, "[DONE]")
	var body map[string]any
	srv := sseServer(t, chunks, &body)
	r := &gateRec{}
	resp, err := StreamOpenAITools(context.Background(), Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), r.sink)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := body["stream"].(bool); !st {
		t.Fatal("istek stream:true taşımıyor")
	}
	if len(resp.ToolCalls) != 0 || resp.InputTokens != 11 || resp.OutputTokens != 7 {
		t.Fatalf("yanıt: %+v", resp)
	}
	if r.visible() != resp.Text {
		t.Fatalf("delta birleşimi %q ≠ metin %q", r.visible(), resp.Text)
	}
	if r.anyText("plan") || r.anyText("think") {
		t.Fatal("düşünce aktı")
	}
	if len(r.deltas) < 2 {
		t.Fatalf("akış tek parça geldi (%d delta)", len(r.deltas))
	}
}

func TestStreamOpenAIToolsToolCallTurnNoText(t *testing.T) {
	chunks := []string{
		`{"choices":[{"delta":{"content":"Bakıyorum."}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"query_metrics","arguments":""},"extra_content":{"google":{"thought_signature":"sig"}}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"serv"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ice\":\"checkout\"}"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"query_logs","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		"[DONE]",
	}
	srv := sseServer(t, chunks, nil)
	r := &gateRec{}
	resp, err := StreamOpenAITools(context.Background(), Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), r.sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.deltas) != 0 {
		t.Fatalf("tool-call turu delta yayınladı: %+v", r.deltas)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "query_metrics" || string(resp.ToolCalls[0].Input) != `{"service":"checkout"}` ||
		resp.ToolCalls[1].Name != "query_logs" || resp.ToolCalls[0].ID != "c1" {
		t.Fatalf("tool çağrıları: %+v", resp.ToolCalls)
	}
	if !strings.Contains(string(resp.ToolCalls[0].Raw), "thought_signature") {
		t.Fatalf("Gemini ek alanı Raw'da yok: %s", resp.ToolCalls[0].Raw)
	}
}

func TestStreamOpenAIToolsTextEmbeddedCall(t *testing.T) {
	content := "<tool_call>{\"name\":\"query_logs\",\"arguments\":{\"service\":\"checkout\"}}</tool_call>"
	var chunks []string
	for _, p := range splitRunes(content, 5) {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": p}}}})
		chunks = append(chunks, string(b))
	}
	srv := sseServer(t, append(chunks, "[DONE]"), nil)
	r := &gateRec{}
	resp, err := StreamOpenAITools(context.Background(), Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), r.sink)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.ToolCallsFromText || len(resp.ToolCalls) != 1 {
		t.Fatalf("metin-gömülü çağrı ayrıştırılmadı: %+v", resp)
	}
	if len(r.deltas) != 0 {
		t.Fatalf("metin-gömülü çağrı aktı: %+v", r.deltas)
	}
}

func TestStreamOpenAIToolsNonSSEIsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"tek atış"}}]}`))
	}))
	defer srv.Close()
	_, err := StreamOpenAITools(context.Background(), Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), nil)
	fe, ok := err.(*StreamFallbackError)
	if !ok || fe.Verdict != VerdictParseBuffered {
		t.Fatalf("beklenen VerdictParseBuffered, %v", err)
	}
	resp, err := ParseOpenAIToolsOneShot(fe.Body, toolReq())
	if err != nil || resp.Text != "tek atış" {
		t.Fatalf("tek atış çözümü: %+v %v", resp, err)
	}
}

func TestStreamOpenAIToolsAbortStopsUpstream(t *testing.T) {
	upstreamDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"başladı\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done() // istemci kesene dek akış açık
		close(upstreamDone)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := StreamOpenAITools(ctx, Config{BaseURL: srv.URL, HTTPClient: srv.Client()}, toolReq(), nil)
		errc <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("iptal edilen akış hatasız döndü")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("iptal akışı durdurmadı")
	}
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("üst akış isteği iptal edilmedi")
	}
}

func TestAnthropicToolsStreamAccum(t *testing.T) {
	lines := []string{
		`{"type":"message_start","message":{"usage":{"input_tokens":20,"cache_read_input_tokens":5}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG"}}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Metriklere bakıyorum."}}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tu1","name":"query_metrics","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"service\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"checkout\"}"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
	}
	acc := &anthropicToolsStreamAccum{}
	r := &gateRec{}
	g := newToolTurnGate(r.sink, true, nil)
	for _, l := range lines {
		acc.feed("data: "+l, g)
	}
	resp, err := parseAnthropicToolsChat(acc.synthesize())
	if err != nil {
		t.Fatal(err)
	}
	g.Finish(resp.Text, len(resp.ToolCalls) > 0)
	if len(r.deltas) != 0 {
		t.Fatalf("tool_use turu delta yayınladı: %+v", r.deltas)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"service":"checkout"}` || resp.ToolCalls[0].ID != "tu1" {
		t.Fatalf("tool_use: %+v", resp.ToolCalls)
	}
	if !strings.Contains(string(resp.RawContent), `"signature":"SIG"`) || !strings.Contains(string(resp.RawContent), `"thinking":"plan"`) {
		t.Fatalf("imzalı thinking bloğu RawContent'te yok: %s", resp.RawContent)
	}
	if resp.InputTokens != 25 || resp.OutputTokens != 9 || resp.CachedTokens != 5 {
		t.Fatalf("usage: %+v", resp)
	}
}
