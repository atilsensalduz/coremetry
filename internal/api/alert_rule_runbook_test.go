package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1341 — alert kuralı runbookUrl yazma kapısı: boş/http(s)/uygulama
// yolu geçer (kırpılmış değer kurala yazılır); javascript:, "//evil",
// "/\evil", ftp:// → 400 JSON {error}, store'a dokunulmadan (&Server{}).
func TestAcceptRuleRunbook(t *testing.T) {
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"empty", "", "", true},
		{"blank", "   ", "", true},
		{"trimmed https", "  https://wiki.example.com/rb/payments  ", "https://wiki.example.com/rb/payments", true},
		{"app path", "/runbook?id=abc", "/runbook?id=abc", true},
		{"javascript", "javascript:alert(1)", "", false},
		{"protocol-relative", "//evil.com", "", false},
		{"backslash", `/\evil.com`, "", false},
		{"ftp", "ftp://files.example.com/rb", "", false},
		{"data", "data:text/html,<script>alert(1)</script>", "", false},
	}
	s := &Server{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rule := chstore.AlertRule{ID: "r1", RunbookURL: c.in}
			got := s.acceptRuleRunbook(rec, &rule)
			if got != c.ok {
				t.Fatalf("acceptRuleRunbook(%q) = %v, want %v", c.in, got, c.ok)
			}
			if c.ok {
				if rule.RunbookURL != c.want {
					t.Errorf("RunbookURL = %q, want %q", rule.RunbookURL, c.want)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("accepted rule wrote a body: %q", rec.Body.String())
				}
				return
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body not JSON: %v (%q)", err, rec.Body.String())
			}
			if !strings.HasPrefix(body["error"], "runbookUrl must be") {
				t.Errorf("error = %q", body["error"])
			}
		})
	}
}
