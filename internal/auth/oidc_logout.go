package auth

// oidc_logout.go — v0.10.1192 (operatör-bildirimi): "Kullanıcı logout'a
// basınca logout olması gerekir." Coremetry yalnız kendi çerezini siliyordu;
// IdP (Keycloak) oturumu açık kaldığı için sonraki SSO girişi sessizce
// yeniden içeri alıyordu. OIDC RP-Initiated Logout: keşif belgesindeki
// end_session_endpoint'e id_token_hint + client_id + post_logout_redirect_uri
// ile yönlendirilir. v0.10.1246: post_logout_redirect_uri = giriş için zaten
// kayıtlı callback adresi (kurum dokümanı 3.5); IdP'de ek tanım gerekmez.

import (
	"net/url"
	"strings"
)

// LogoutURL — IdP oturumunu kapatan adres; SSO kapalıysa ya da IdP
// end_session_endpoint ilan etmiyorsa "" (yalnız yerel çıkış).
func (o *OIDCService) LogoutURL(idToken string) string {
	c := o.client()
	// v0.10.1243 — operatör: logout sonrası SSO sessizce geri alıyordu. Adres
	// provider.Claims()'ten okunuyordu; ağsız ProviderConfig sağlayıcısında
	// Claims her zaman hata döner → hep "" (yalnız yerel çıkış). Kaynak artık
	// kendi keşfimiz (OIDCDiscovery.EndSessionEndpoint).
	if c == nil || c.disc == nil || c.disc.EndSessionEndpoint == "" {
		return ""
	}
	return BuildOIDCLogoutURL(c.disc.EndSessionEndpoint, idToken, c.cfg.ClientID, postLogoutRedirect(o.publicURL, c.cfg.RedirectURL))
}

// postLogoutRedirect — SAF. v0.10.1246 (operatör, prod Keycloak: "Invalid
// redirect uri"): <public URL>/login IdP istemcisinde KAYITLI DEĞİLDİ.
// Kurum dokümanı (3.5 Logout) çıkış dönüşü olarak giriş için zaten kayıtlı
// redirect URI'yi ister — yani callback adresi; ek IdP tanımı gerekmez.
// Callback parametresiz gelişi sessizce /login'e çevirir (api auth_logout.go).
// publicURL artık kullanılmıyor; imza çağıranlar için korunur.
func postLogoutRedirect(_ string, redirectURL string) string {
	u, err := url.Parse(strings.TrimSpace(redirectURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.String()
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
