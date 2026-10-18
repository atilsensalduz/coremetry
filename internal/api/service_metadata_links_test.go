package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1341 — servis kataloğu link alanları yazarken doğrulanır; hata
// alanı adlandırır (customLinks indeksiyle), repository git uzak
// biçimlerini de kabul eder.
func TestValidateServiceMetadataLinks(t *testing.T) {
	cases := []struct {
		name    string
		m       chstore.ServiceMetadata
		wantErr string // "" = geçerli; aksi hata öneki
	}{
		{"all empty", chstore.ServiceMetadata{}, ""},
		{"valid mix", chstore.ServiceMetadata{
			RunbookURL: " /runbook?id=abc ", OncallURL: "https://pagerduty.example.com/s/1",
			CustomLinks: []chstore.CustomLink{{Label: "dash", URL: "/services/payments"}, {Label: "wiki", URL: "https://wiki.example.com"}},
			Repository:  "https://github.com/org/payments",
		}, ""},
		{"runbook javascript", chstore.ServiceMetadata{RunbookURL: "javascript:alert(1)"}, "runbookUrl: "},
		{"oncall protocol-relative", chstore.ServiceMetadata{OncallURL: "//evil.com"}, "oncallUrl: "},
		{"customLinks index", chstore.ServiceMetadata{CustomLinks: []chstore.CustomLink{
			{URL: "https://ok.example.com"}, {URL: "/ok"}, {URL: `/\evil.com`},
		}}, "customLinks[2].url: "},
		{"customLinks empty url ok", chstore.ServiceMetadata{CustomLinks: []chstore.CustomLink{{Label: "x", URL: "  "}}}, ""},
		{"repo scp .git", chstore.ServiceMetadata{Repository: "git@github.com:org/repo.git"}, ""},
		{"repo scp", chstore.ServiceMetadata{Repository: "git@gitlab.internal:group/sub/repo"}, ""},
		{"repo ssh url", chstore.ServiceMetadata{Repository: "ssh://git@host/org/repo"}, ""},
		{"repo ssh port", chstore.ServiceMetadata{Repository: "ssh://git@host:2222/org/repo.git"}, ""},
		{"repo javascript", chstore.ServiceMetadata{Repository: "javascript:alert(1)"}, "repository: "},
		{"repo data", chstore.ServiceMetadata{Repository: "data:text/html,x"}, "repository: "},
		{"repo vbscript", chstore.ServiceMetadata{Repository: "vbscript:msgbox(1)"}, "repository: "},
		{"repo whitespace", chstore.ServiceMetadata{Repository: "git@host:org/my repo.git"}, "repository: "},
		{"repo ctrl char", chstore.ServiceMetadata{Repository: "git@host:org/repo\x00.git"}, "repository: "},
		{"repo ssh password", chstore.ServiceMetadata{Repository: "ssh://git:secret@host/org/repo"}, "repository: "},
		{"repo ssh no host", chstore.ServiceMetadata{Repository: "ssh:///org/repo"}, "repository: "},
		{"repo bare path", chstore.ServiceMetadata{Repository: "org/repo"}, "repository: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := c.m
			err := validateServiceMetadataLinks(&m)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), c.wantErr) {
				t.Fatalf("err = %v, want prefix %q", err, c.wantErr)
			}
		})
	}
}

// v0.10.1341 — geçerli değerler yerinde kırpılır (saklanan değer temiz).
func TestValidateServiceMetadataLinksTrims(t *testing.T) {
	m := chstore.ServiceMetadata{
		RunbookURL: "  /runbook?id=abc\t", OncallURL: " https://x.example.com ",
		Repository:  " git@host:org/repo.git ",
		CustomLinks: []chstore.CustomLink{{URL: " /ok "}},
	}
	if err := validateServiceMetadataLinks(&m); err != nil {
		t.Fatal(err)
	}
	if m.RunbookURL != "/runbook?id=abc" || m.OncallURL != "https://x.example.com" ||
		m.Repository != "git@host:org/repo.git" || m.CustomLinks[0].URL != "/ok" {
		t.Errorf("not trimmed: %+v", m)
	}
}

// v0.10.1341 — decodeServiceMetadata: bozuk JSON "invalid body: …", kötü
// link alan adlı hata; putServiceMetadata ikisini de 400 {error} yazar.
func TestDecodeServiceMetadata(t *testing.T) {
	var m chstore.ServiceMetadata
	r := httptest.NewRequest("PUT", "/api/services/x/metadata", strings.NewReader("{"))
	if err := decodeServiceMetadata(r, &m); err == nil || !strings.HasPrefix(err.Error(), "invalid body: ") {
		t.Errorf("bad json err = %v", err)
	}
	r = httptest.NewRequest("PUT", "/api/services/x/metadata", strings.NewReader(`{"runbookUrl":"javascript:alert(1)"}`))
	if err := decodeServiceMetadata(r, &m); err == nil || !strings.HasPrefix(err.Error(), "runbookUrl: ") {
		t.Errorf("bad link err = %v", err)
	}
	r = httptest.NewRequest("PUT", "/api/services/x/metadata", strings.NewReader(`{"runbookUrl":"/runbook?id=a"}`))
	if err := decodeServiceMetadata(r, &m); err != nil || m.RunbookURL != "/runbook?id=a" {
		t.Errorf("good body err = %v m=%+v", err, m)
	}
}
