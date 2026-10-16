package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// v0.10.1238 — Config.Headers (profilin ek başlıkları, ör. x-cluster) her
// openai-uyumlu isteğe iner; çekirdek auth başlığını ezemez; her taşıyıcı
// çağrı yeri applyHeaders çağırır.
func TestCustomHeadersReachOpenAIRequest(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, APIKey: "k", HTTPClient: srv.Client(),
		Headers: map[string]string{"x-cluster": "test-v2", "Authorization": "HACK"}}
	if _, err := DoOpenAI(context.Background(), cfg, Request{Model: "m", System: "S", User: "U"}); err != nil {
		t.Fatal(err)
	}
	if v := got.Get("X-Cluster"); v != "test-v2" {
		t.Fatalf("x-cluster inmedi: %q", v)
	}
	if v := got.Get("Authorization"); v != "Bearer k" {
		t.Fatalf("çekirdek Authorization ezildi: %q", v)
	}
	for f, want := range map[string]int{"openai.go": 1, "anthropic.go": 1, "github.go": 1, "stream.go": 2, "tools.go": 2, "embed.go": 1} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "applyHeaders(httpReq.Header, cfg.Headers)"); n != want {
			t.Errorf("%s: applyHeaders %d kez, beklenen %d", f, n, want)
		}
	}
}

func TestValidateHeaders(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]string
		ok   bool
	}{
		{"nil", nil, true},
		{"x-cluster", map[string]string{"x-cluster": "test-v2"}, true},
		{"boş değer", map[string]string{"X-Tenant": ""}, true},
		{"ayrılmış authorization", map[string]string{"authorization": "x"}, false},
		{"ayrılmış api-key", map[string]string{"API-KEY": "x"}, false},
		{"ayrılmış content-length", map[string]string{"Content-Length": "1"}, false},
		{"boşluklu ad", map[string]string{"x cluster": "a"}, false},
		{"boş ad", map[string]string{"": "a"}, false},
		{"iki nokta ad", map[string]string{"x:y": "a"}, false},
		{"CRLF enjeksiyonu", map[string]string{"X-A": "a\r\nX-Evil: 1"}, false},
		{"büyük/küçük tekrar", map[string]string{"x-a": "1", "X-A": "2"}, false},
	}
	for _, c := range cases {
		if err := ValidateHeaders(c.in); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, ok beklenen %v", c.name, err, c.ok)
		}
	}
	many := map[string]string{}
	for i := 0; i <= MaxHeaders; i++ {
		many["X-H"+strings.Repeat("a", i+1)] = "v"
	}
	if ValidateHeaders(many) == nil {
		t.Errorf("%d+1 başlık kabul edildi", MaxHeaders)
	}
}
