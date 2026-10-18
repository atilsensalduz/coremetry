package safelink

// v0.10.1315 — olay linki XSS: safelink tablo sözleşmesi.
//
// Keep in lockstep: bu tablo frontend/src/lib/safeAppPath.ratchet.test.ts
// (isSafeAppPath + appLinkKind tabloları, "appLinkKind — Go safelink aynası (v0.10.1315)"
// bloğu) ile kilitli adımda. Bir satır burada eklenir/değişirse orada da.

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		in   string
		want Kind
	}{
		// safeAppPath.ratchet.test.ts isSafeAppPath tablosu (v0.10.1314).
		{`/\evil.com`, None},
		{`\\evil.com`, None},
		{"//evil.com", None},
		{"/%5Cevil.com", None},
		{"/%2F%2Fevil.com", None},
		{`/x\y`, None},
		{"/x%0Ay", None},
		{"/ x", None},
		{"https://a", External}, // isSafeAppPath false; appLinkKind external
		{"", None},
		{"/%E0%A4%A", None},
		{"/", Internal},
		{"/traces?service=a#x", Internal},
		{"/problems?problem=%2Fx", Internal},
		// appLinkKind tablosu (v0.10.1314).
		{"javascript:alert(1)", None},
		{"ftp://x", None},
		{"/problems?problem=p1", Internal},
		{"https://ci.example/build/1", External},
		{"HTTP://ci.example", External},
		// v0.10.1315 ekleri.
		{"JavaScript:alert(1)", None},
		{"data:text/html,<script>alert(1)</script>", None},
		{"vbscript:msgbox(1)", None},
		{"http:evil", None},
		{"http://", None},
		{"https://user:pw@h/", None},
		{" https://x", External},
		{"https://x/a\x01b", None},
		{"/a\x7fb", None},
		{"/a b", None},
		{"/%E0%A4", None}, // geçerli kodlama, geçersiz UTF-8 (decodeURIComponent fırlatır)
		{"https://x/" + strings.Repeat("a", MaxLen-len("https://x/")+1), None},
		{"/runbook?id=abc&problemId=p1", Internal},
		{"\xff/x", None},
	}
	for _, c := range cases {
		if got := Classify(c.in); got != c.want {
			t.Errorf("Classify(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMaxLenBoundary(t *testing.T) {
	ok := "https://x/" + strings.Repeat("a", MaxLen-len("https://x/"))
	if len(ok) != MaxLen || Classify(ok) != External {
		t.Fatalf("%d-byte URL must be External", MaxLen)
	}
	if Classify(ok+"a") != None {
		t.Fatalf("%d-byte URL must be None", MaxLen+1)
	}
}

func TestSafeLinkAndSanitize(t *testing.T) {
	if s, ok := SafeLink("  https://x/y  "); !ok || s != "https://x/y" {
		t.Fatalf("SafeLink trims: got %q %v", s, ok)
	}
	if s, ok := SafeLink(""); ok || s != "" {
		t.Fatalf("SafeLink empty: got %q %v", s, ok)
	}
	if s, ok := SafeLink("javascript:alert(1)"); ok || s != "" {
		t.Fatalf("SafeLink javascript: got %q %v", s, ok)
	}
	if got := Sanitize("javascript:alert(1)"); got != "" {
		t.Fatalf("Sanitize javascript: got %q", got)
	}
	if got := Sanitize(" /problems?problem=p1 "); got != "/problems?problem=p1" {
		t.Fatalf("Sanitize internal: got %q", got)
	}
}
