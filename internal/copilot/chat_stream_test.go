package copilot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// v0.10.1273 — CoSRE adım 4: serbest döngüde token akışı + adım satırları.
// ChatWithToolsStream POLİTİKASI: gerçek SSE akar; akmayan uç (stream:true'ya
// 400 ya da SSE olmayan gövde) buffered'a düşer ve final metin TEK delta
// olur; "tool'lu akış yok" kararı kendi önekiyle önbelleklenir ve anlatım
// akışının (StreamText) kararına SIZMAZ.

type toolStreamLLM struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newToolStreamLLM(t *testing.T, handle func(w http.ResponseWriter, stream bool)) *toolStreamLLM {
	t.Helper()
	l := &toolStreamLLM{}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		l.bodies = append(l.bodies, string(b))
		l.mu.Unlock()
		handle(w, requestWantsStream(t, b))
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *toolStreamLLM) streamFlags(t *testing.T) []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]bool, 0, len(l.bodies))
	for _, b := range l.bodies {
		out = append(out, requestWantsStream(t, []byte(b)))
	}
	return out
}

var streamTestTools = []ToolSpec{{Name: "query_metrics"}, {Name: "query_logs"}}

func collectDeltas() (*[]TextDelta, func(TextDelta)) {
	var ds []TextDelta
	return &ds, func(d TextDelta) { ds = append(ds, d) }
}

func joinVisible(ds []TextDelta) string {
	var b strings.Builder
	for _, d := range ds {
		if d.Reset {
			b.Reset()
		}
		b.WriteString(d.Text)
	}
	return b.String()
}

func TestChatWithToolsStreamOpenAISSE(t *testing.T) {
	// v0.10.1324 — FARKLI cümleler (aynı cümlenin kopyaları yineleme muhafızını haklı olarak tetikler).
	var ab strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&ab, "checkout p95 %d ms; ödeme geçidi zaman aşımı. ", 1830+i)
	}
	answer := ab.String()
	llm := newToolStreamLLM(t, func(w http.ResponseWriter, _ bool) {
		w.Header().Set("Content-Type", "text/event-stream")
		r := []rune(answer)
		for i := 0; i < len(r); i += 7 {
			j := min(i+7, len(r))
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", string(r[i:j]))
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s := New("openai", "", "test-model")
	s.Configure("openai", "", "test-model", llm.srv.URL, false, true)
	ds, sink := collectDeltas()
	turn, err := s.ChatWithToolsStream(context.Background(), "sys", []ChatMessage{{Role: "user", Text: "q"}}, streamTestTools, sink)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != answer || joinVisible(*ds) != turn.Text {
		t.Fatalf("metin %q / akan %q", turn.Text, joinVisible(*ds))
	}
	if len(*ds) < 3 {
		t.Fatalf("canlı akış yok: %d delta", len(*ds))
	}
	if f := llm.streamFlags(t); len(f) != 1 || !f[0] {
		t.Fatalf("tek stream:true istek bekleniyordu: %v", f)
	}
}

func TestChatWithToolsStreamFallbackSingleDelta(t *testing.T) {
	cases := []struct {
		name   string
		handle func(w http.ResponseWriter, stream bool)
		// wantFlags — istek dizisinin stream bayrakları (iki tur).
		wantFlags []bool
	}{
		{
			name: "stream:true'ya 400 → buffered, karar önbellekte",
			handle: func(w http.ResponseWriter, stream bool) {
				w.Header().Set("Content-Type", "application/json")
				if stream {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"stream not supported with tools"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"buffered cevap"}}]}`))
			},
			wantFlags: []bool{true, false, false},
		},
		{
			name: "bayrağı yutan uç (200 JSON) → tek atış çözülür, çift fatura yok",
			handle: func(w http.ResponseWriter, _ bool) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"buffered cevap"}}]}`))
			},
			wantFlags: []bool{true, false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := newToolStreamLLM(t, tc.handle)
			s := New("openai", "", "test-model")
			s.Configure("openai", "", "test-model", llm.srv.URL, false, true)
			for round := 0; round < 2; round++ {
				ds, sink := collectDeltas()
				turn, err := s.ChatWithToolsStream(context.Background(), "sys", []ChatMessage{{Role: "user", Text: "q"}}, streamTestTools, sink)
				if err != nil {
					t.Fatal(err)
				}
				if turn.Text != "buffered cevap" || len(*ds) != 1 || (*ds)[0].Text != "buffered cevap" {
					t.Fatalf("tur %d: metin %q, deltalar %+v (tek delta bekleniyordu)", round, turn.Text, *ds)
				}
			}
			if got := llm.streamFlags(t); fmt.Sprint(got) != fmt.Sprint(tc.wantFlags) {
				t.Fatalf("stream bayrakları %v, beklenen %v", got, tc.wantFlags)
			}
			if !s.streamKnownUnsupported(ProviderOpenAI+toolsVerdictSuffix, llm.srv.URL, "test-model") {
				t.Fatal("tool'lu akış kararı önbelleğe yazılmadı")
			}
			if s.streamKnownUnsupported(ProviderOpenAI, llm.srv.URL, "test-model") {
				t.Fatal("tool'lu akış kararı anlatım akışına sızdı")
			}
		})
	}
}

func TestChatWithToolsStreamToolTurnNoDelta(t *testing.T) {
	llm := newToolStreamLLM(t, func(w http.ResponseWriter, _ bool) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Önce metriklere bakayım.\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"query_metrics\",\"arguments\":\"{}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	s := New("openai", "", "test-model")
	s.Configure("openai", "", "test-model", llm.srv.URL, false, true)
	ds, sink := collectDeltas()
	turn, err := s.ChatWithToolsStream(context.Background(), "sys", []ChatMessage{{Role: "user", Text: "q"}}, streamTestTools, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 1 || len(*ds) != 0 {
		t.Fatalf("tool turu: çağrı=%d delta=%+v", len(turn.ToolCalls), *ds)
	}
}
