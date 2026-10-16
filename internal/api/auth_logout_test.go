package api

// v0.10.1192 (operatör-bildirimi) — logout yerel oturumu siler; SSO
// girişinde IdP çıkış adresini döner, yerel girişte dönmez.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogoutResponse(t *testing.T) {
	if r := logoutResponse(""); r["redirect"] != "" || r["status"] != "ok" {
		t.Errorf("yerel: %v", r)
	}
	if r := logoutResponse("https://kc/logout?x=1"); r["redirect"] != "https://kc/logout?x=1" {
		t.Errorf("sso: %v", r)
	}
}

func TestLogoutClearsSessionAndHintCookies(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: oidcIDTokenCookie, Value: "eyJ.a.b"})
	w := httptest.NewRecorder()
	s.logout(w, req)
	cookies := strings.Join(w.Header().Values("Set-Cookie"), "\n")
	if !strings.Contains(cookies, oidcIDTokenCookie+"=;") || !strings.Contains(cookies, "Max-Age=0") {
		t.Errorf("çerezler silinmeli:\n%s", cookies)
	}
	if strings.Contains(w.Body.String(), "redirect") {
		t.Errorf("OIDC yokken yönlendirme dönmemeli: %s", w.Body.String())
	}
}

func TestOIDCLogoutHintSkipsOversizedToken(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.setOIDCLogoutHint(w, strings.Repeat("a", oidcIDTokenCookieMax+1), timeNowPlusHour())
	if len(w.Header().Values("Set-Cookie")) != 0 {
		t.Error("tavanı aşan id_token çereze yazılmamalı")
	}
	w = httptest.NewRecorder()
	s.setOIDCLogoutHint(w, "eyJ.a.b", timeNowPlusHour())
	if c := w.Header().Get("Set-Cookie"); !strings.Contains(c, "Path="+oidcIDTokenPath) || !strings.Contains(c, "HttpOnly") {
		t.Errorf("ipucu çerezi yalnız logout yoluna, HttpOnly: %s", c)
	}
}

func timeNowPlusHour() time.Time { return time.Now().Add(time.Hour) }

// v0.10.1246 — Keycloak çıkışı callback'e parametresiz döner → sessizce /login.
func TestOIDCPostLogoutReturn(t *testing.T) {
	if !isPostLogoutReturn("") || isPostLogoutReturn("code=x&state=y") || isPostLogoutReturn("error=access_denied") {
		t.Fatal("yalnız parametresiz callback çıkış dönüşüdür")
	}
	s := &Server{}
	w := httptest.NewRecorder()
	s.oidcMissingCode(w, httptest.NewRequest("GET", "/api/auth/oidc/callback", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("code=%d loc=%q", w.Code, w.Header().Get("Location"))
	}
}
