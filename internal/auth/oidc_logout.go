package auth

// oidc_logout.go — v0.10.1192 (operatör-bildirimi): "Kullanıcı logout'a
// basınca logout olması gerekir." Coremetry yalnız kendi çerezini siliyordu;
// IdP (Keycloak) oturumu açık kaldığı için sonraki SSO girişi sessizce
// yeniden içeri alıyordu. OIDC RP-Initiated Logout: keşif belgesindeki
// end_session_endpoint'e id_token_hint + client_id + post_logout_redirect_uri
// ile yönlendirilir. post_logout_redirect_uri IdP istemcisinde
// "Valid post logout redirect URIs" altında tanımlı olmalı (yoksa IdP hata
// sayfası gösterir) — değer: <public URL>/login.

import (
	"net/url"
	"strings"
)

// LogoutURL — IdP oturumunu kapatan adres; SSO kapalıysa ya da IdP
// end_session_endpoint ilan etmiyorsa "" (yalnız yerel çıkış).
func (o *OIDCService) LogoutURL(idToken string) string {
	c := o.client()
	if c == nil || c.provider == nil {
		return ""
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := c.provider.Claims(&meta); err != nil || meta.EndSession == "" {
		return ""
	}
	return BuildOIDCLogoutURL(meta.EndSession, idToken, c.cfg.ClientID, postLogoutRedirect(o.publicURL, c.cfg.RedirectURL))
}

// postLogoutRedirect — SAF: <public URL>/login; public URL yoksa callback
// adresinin kökeni (redirect_url zorunlu alan, her kurulumda dolu).
func postLogoutRedirect(publicURL, redirectURL string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if base == "" {
		if u, err := url.Parse(redirectURL); err == nil && u.Scheme != "" && u.Host != "" {
			base = u.Scheme + "://" + u.Host
		}
	}
	if base == "" {
		return ""
	}
	return base + "/login"
}

// BuildOIDCLogoutURL — SAF: end_session adresine sorgu parametreleri
// eklenir (mevcut sorgu korunur). id_token yoksa client_id ile gidilir:
// Keycloak onay sayfası gösterir ama oturumu yine kapatır.
func BuildOIDCLogoutURL(endSession, idToken, clientID, postLogout string) string {
	u, err := url.Parse(endSession)
	if err != nil {
		return ""
	}
	q := u.Query()
	if idToken != "" {
		q.Set("id_token_hint", idToken)
	}
	if clientID != "" {
		q.Set("client_id", clientID)
	}
	if postLogout != "" {
		q.Set("post_logout_redirect_uri", postLogout)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
