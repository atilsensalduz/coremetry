package api

// v0.10.1197 (operatör-bildirimi) — SSO hatası giriş sayfasında kod + ipucu
// taşır; Keycloak akışı başarılıyken nedenin ekrandan okunabilmesi için.

import (
	"strings"
	"testing"
)

func TestOIDCLoginFailMessage(t *testing.T) {
	m := oidcLoginFailMessage("token_exchange")
	if !strings.HasPrefix(m, oidcLoginFailedMsg) || !strings.Contains(m, "(kod: token_exchange)") || !strings.Contains(m, "client secret") {
		t.Errorf("%s", m)
	}
	if m := oidcLoginFailMessage("unknown"); m != oidcLoginFailedMsg+" (kod: unknown)" {
		t.Errorf("bilinmeyen sınıf ipucusuz: %s", m)
	}
}
