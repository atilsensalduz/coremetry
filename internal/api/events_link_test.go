package api

// v0.10.1315 — olay linki XSS: /api/operator-events `link` alanı yazılırken
// süzülür. validateEventLink SAF; createEvent ihlalde store'a dokunmadan 400
// döner (Server{} — store nil — ile çağrılabilmesi bunun kanıtı).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateEventLink(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"https://ci.example/build/1", "https://ci.example/build/1", false},
		{"  https://ci.example/x  ", "https://ci.example/x", false},
		{"HTTP://ci.example", "HTTP://ci.example", false},
		{"/problems?problem=p1", "/problems?problem=p1", false},
		{"javascript:alert(1)", "", true},
		{"JavaScript:alert(1)", "", true},
		{"data:text/html,<script>alert(1)</script>", "", true},
		{"vbscript:msgbox(1)", "", true},
		{"//evil.com", "", true},
		{`/\evil.com`, "", true},
		{"/%2F%2Fevil.com", "", true},
		{"https://user:pw@h/", "", true},
		{"http://", "", true},
		{"ftp://x", "", true},
	}
	for _, c := range cases {
		got, err := validateEventLink(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("validateEventLink(%q) = %q, %v; want %q, err=%v", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestCreateEventRejectsUnsafeLink(t *testing.T) {
	body := `{"kind":"deploy","label":"x","link":"javascript:alert(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/api/operator-events", strings.NewReader(body))
	rec := httptest.NewRecorder()
	(&Server{}).createEvent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body not JSON: %q", rec.Body.String())
	}
	if resp["error"] != errEventLink.Error() {
		t.Fatalf("error = %q, want %q", resp["error"], errEventLink.Error())
	}
}
