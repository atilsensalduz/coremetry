package api

// chat_source_kind_test.go — v0.10.1170: kaynak türü rozeti.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestChatSourceKinds(t *testing.T) {
	cases := []struct{ wikiID, url, want string }{
		{"devops-guid", "https://dev.azure.com/x", "wiki"},
		{wiki.LocalWikiID, "", "upload"},
		{wiki.LocalWikiID, "https://docs.example/a", "url"},
	}
	for _, c := range cases {
		if got := wikiSourceKind(c.wikiID, c.url); got != c.want {
			t.Errorf("wikiSourceKind(%q,%q) = %q, beklenen %q", c.wikiID, c.url, got, c.want)
		}
	}
	if ragSourceKind("url") != "url" || ragSourceKind("upload") != "upload" || ragSourceKind("") != "upload" {
		t.Fatal("ragSourceKind")
	}
	h := wiki.Hit{}
	h.WikiID, h.Title = wiki.LocalWikiID, "kasa"
	if s := wikiHitSource(h); s.Kind != "upload" || s.Doc != "Belge · kasa" {
		t.Fatalf("yüklenen belge kaynağı: %+v", s)
	}
}
