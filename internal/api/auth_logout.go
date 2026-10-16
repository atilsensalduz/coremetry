package api

// auth_logout.go — v0.10.1192 (operatör-bildirimi): logout IdP oturumunu da
// kapatır (auth/oidc_logout.go). OIDC girişinde id_token, yalnız logout
// ucuna giden HttpOnly bir çereze yazılır; logout yerel oturumu siler ve
// SSO girişiyse istemciye end_session adresini `redirect` olarak döner
// (fetch 302 izleyemez; SPA tam sayfa yönlenir). Yerel/LDAP girişte
// davranış aynen: {"status":"ok"}.

import (
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
)

const (
	oidcIDTokenCookie = "coremetry_oidc_idt"
	oidcIDTokenPath   = "/api/auth/logout"
	// Tarayıcı çerez tavanı ~4 KB; büyük id_token (çok rollü Keycloak)
	// saklanmaz — logout yine çalışır (client_id ile, IdP onay sorar).
	oidcIDTokenCookieMax = 3800
)

// setOIDCLogoutHint — callback'te oturum çereziyle aynı ömürde.
func (s *Server) setOIDCLogoutHint(w http.ResponseWriter, rawIDToken string, exp time.Time) {
	if rawIDToken == "" || len(rawIDToken) > oidcIDTokenCookieMax {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcIDTokenCookie, Value: rawIDToken, Path: oidcIDTokenPath,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: exp,
		MaxAge: int(time.Until(exp).Seconds()),
	})
}

// logoutResponse — SAF: SSO etkinse IdP çıkış adresi döner; "" = yerel çıkış.
func logoutResponse(ssoLogoutURL string) map[string]string {
	out := map[string]string{"status": "ok"}
	if ssoLogoutURL != "" {
		out["redirect"] = ssoLogoutURL
	}
	return out
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	idt := ""
	if c, err := r.Cookie(oidcIDTokenCookie); err == nil {
		idt = c.Value
		http.SetCookie(w, &http.Cookie{Name: oidcIDTokenCookie, Value: "", Path: oidcIDTokenPath, HttpOnly: true, MaxAge: -1})
	}
	ssoURL := ""
	// Yalnız SSO ile girilmiş oturumda IdP'ye gidilir (çerez = OIDC girişi);
	// yerel/LDAP kullanıcısı Keycloak çıkış sayfasına gönderilmez.
	if idt != "" && s.oidc != nil && s.oidc.Enabled() {
		ssoURL = s.oidc.LogoutURL(idt)
	}
	writeJSON(w, logoutResponse(ssoURL))
}

// oidcMissingCode — v0.10.1246: callback'e code/state gelmedi. Hiç sorgu
// parametresi yoksa bu IdP çıkışından (post_logout_redirect_uri = callback)
// dönüştür: sessizce /login (hata mesajı yok). Aksi eski davranış (oidcFail).
func (s *Server) oidcMissingCode(w http.ResponseWriter, r *http.Request) {
	if isPostLogoutReturn(r.URL.RawQuery) {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	s.oidcFail(w, r, "missing code or state")
}

// isPostLogoutReturn — SAF: parametresiz callback (Keycloak çıkış dönüşü).
func isPostLogoutReturn(rawQuery string) bool { return strings.TrimSpace(rawQuery) == "" }
