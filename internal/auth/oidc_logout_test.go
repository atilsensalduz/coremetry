package auth

// v0.10.1192 (operatör-bildirimi) — logout IdP oturumunu da kapatmalı:
// end_session_endpoint + id_token_hint + client_id + post_logout_redirect_uri.

import (
	"net/url"
	"testing"
)

func TestBuildOIDCLogoutURL(t *testing.T) {
	got := BuildOIDCLogoutURL("https://kc.example/realms/r/protocol/openid-connect/logout?ui_locales=tr", "eyJ.a.b", "coremetry", "https://cm.example/login")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "kc.example" || q.Get("id_token_hint") != "eyJ.a.b" || q.Get("client_id") != "coremetry" ||
		q.Get("post_logout_redirect_uri") != "https://cm.example/login" || q.Get("ui_locales") != "tr" {
		t.Fatalf("%s", got)
	}
	if u2, _ := url.Parse(BuildOIDCLogoutURL("https://kc/logout", "", "c", "")); u2.Query().Has("id_token_hint") || u2.Query().Has("post_logout_redirect_uri") {
		t.Errorf("boş alan parametre olmamalı: %s", u2)
	}
}

func TestPostLogoutRedirect(t *testing.T) {
	cases := map[[2]string]string{
		{"https://cm.example/", "https://x/api/auth/oidc/callback"}: "https://cm.example/login",
		{"", "https://cm.bank:8443/api/auth/oidc/callback"}:         "https://cm.bank:8443/login",
		{"", ""}: "",
	}
	for in, want := range cases {
		if got := postLogoutRedirect(in[0], in[1]); got != want {
			t.Errorf("%v → %q want %q", in, got, want)
		}
	}
}

func TestLogoutURLDisabled(t *testing.T) {
	var o OIDCService
	if o.LogoutURL("x") != "" {
		t.Error("SSO kapalıyken IdP çıkışı yok")
	}
}
