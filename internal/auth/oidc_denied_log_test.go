package auth

// v0.10.1381 (operatör-bildirimli, prod): "[oidc] callback failed:
// class=email_domain_denied" geldi ama hangi alan adının reddedildiği ve
// sunucunun hangi izinli listeyle çalıştığı logdan okunamıyordu. Red satırı
// artık alan adını ve etkin liste uzunluğunu yazar; tam e-posta yazılmaz.

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDeniedDomainLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a.b@sub.example.test", "sub.example.test"},
		{"A@Example.TEST", "example.test"},
		{"no-at-sign", "<yok>"},
		{"x@exa mple.test", "<geçersiz>"},
		{"x@örnek.test", "<geçersiz>"},
		{"x@" + strings.Repeat("a", 101), "<geçersiz>"},
	}
	for _, c := range cases {
		if got := deniedDomainLabel(c.in); got != c.want {
			t.Errorf("%q → %q want %q", c.in, got, c.want)
		}
	}
}

func TestLogDeniedEmailDomain(t *testing.T) {
	deniedLogMu.Lock()
	deniedLog = map[string]time.Time{}
	deniedLogMu.Unlock()
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	logDeniedEmailDomain("jane.doe@sub.example.test", []string{"example.test", " "}, now, logf)
	if len(lines) != 1 || !strings.Contains(lines[0], "domain=sub.example.test allowed=1") {
		t.Fatalf("satır: %v", lines)
	}
	if strings.Contains(lines[0], "jane.doe") {
		t.Fatal("tam e-posta loga girmemeli")
	}
	// Aynı alan adı saat içinde tekrar yazılmaz; saat geçince yazılır.
	logDeniedEmailDomain("other@sub.example.test", nil, now.Add(10*time.Minute), logf)
	if len(lines) != 1 {
		t.Fatalf("saatte bir kuralı: %v", lines)
	}
	logDeniedEmailDomain("other@sub.example.test", nil, now.Add(61*time.Minute), logf)
	if len(lines) != 2 || !strings.Contains(lines[1], "allowed=0") {
		t.Fatalf("saat sonrası: %v", lines)
	}
	// Harita sınırlı: deniedLogMax farklı alan adından sonra sıfırlanır.
	for i := 0; i < deniedLogMax+10; i++ {
		logDeniedEmailDomain(fmt.Sprintf("u@d%d.example.test", i), nil, now, func(string, ...any) {})
	}
	deniedLogMu.Lock()
	n := len(deniedLog)
	deniedLogMu.Unlock()
	if n > deniedLogMax {
		t.Fatalf("harita sınırsız büyüdü: %d", n)
	}
}

// Kaynak pini: üç red yolu da satırı yazar.
func TestDeniedDomainLoggedOnAllPaths(t *testing.T) {
	for _, f := range []string{"oidc.go", "oidc_email_resolve.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if !strings.Contains(src, "logDeniedEmailDomain(") {
			t.Errorf("%s red satırını yazmıyor", f)
		}
	}
}
