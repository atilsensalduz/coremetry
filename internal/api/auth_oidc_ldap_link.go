package api

// auth_oidc_ldap_link.go — v0.10.1403 (operatör, giriş sayfası: "SSO sign-in
// failed: Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz; parola/LDAP ile
// girin veya IdP'de Trust Email açılsın" → "Coremetry tarafını yap" + "LDAP'ı
// bir süre sonra hiç kullanmayacağım, sadece merkezi login Keycloak üzerinden
// olacak").
//
// v0.10.1120'den beri güven anahtarıyla (trustUnverifiedEmail, email_verified=
// false) gelen giriş yerel/LDAP ya da admin hesabını BAĞLAMAZ: IdP e-postayı
// doğrulamadığı için e-posta çarpışması hesap ele geçirmeye dönüşebilir. LDAP
// emekliye ayrılırken mevcut LDAP kullanıcıları Keycloak'a geçemiyordu.
//
// Bu dosya TEK istisnayı kurar — ayrı açık-seçim linkLdapByUsername (varsayılan
// KAPALI; yalnız trustUnverifiedEmail açıkken anlamlı). Bağlama YALNIZ şu
// koşulların HEPSİ tutarsa (oidcLdapLinkMatch, SAF):
//
//  1. imzası/issuer'ı/nonce'u doğrulanmış id_token'daki (yoksa sub'ı
//     eşleşmiş UserInfo'daki) usernameClaim değeri (varsayılan
//     preferred_username; yalnız yazdırılabilir ASCII, `@` yok) var;
//  2. hesap LDAP kaynaklı (ya da daha önce bu yolla oidc'ye taşınmış ve
//     ldap_username'i duran) — yerel/parola hesabı ASLA (ldap_username boş);
//  3. users.ldap_username (YALNIZ başarılı LDAP bind'ı yazar) ASCII ve claim
//     ile büyük-küçük harf duyarsız EŞİT;
//  4. satırın e-postası id_token e-postasıyla (küçük harf) birebir EŞİT;
//  5. o ldap_username'e sahip TAM BİR aktif satır var ve o bu satır.
//
// Admin hesabı ek olarak ayrı açık-seçim linkLdapByUsernameAllowAdmin ister;
// yoksa aynı giriş sayfası metniyle red. Bağlamada hesap auth_provider=oidc'ye
// taşınır (whole-row: tüm alanlar taşınır), /me önbelleği ve yetki önbelleği
// boşaltılır, "auth.oidc.link_ldap_account" audit'lenir. Taşınmış admin her
// sonraki girişte de bu kanıtı yeniden ister (oidcTrustLinkAllowed admin'i hâlâ
// reddeder — derinlemesine savunma). Log satırları e-posta / kullanıcı adı
// TAŞIMAZ (yalnız kullanıcı id'si ve sabit sebep); red satırı sebep başına
// dakikada en çok bir kez.
//
// ÖN KOŞUL: Keycloak'ta kullanıcı adı ve e-posta kullanıcı tarafından
// DEĞİŞTİRİLEMEMELİ (LDAP federasyonu read-only, "Edit username" kapalı,
// self-registration / brokered IdP yok). Ayar açıklaması bunu uyarır.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// Bağlama red sebepleri — SABİT küme (log anahtarı; değer taşımaz).
const (
	ldapLinkNoUsernameClaim     = "no_username_claim"
	ldapLinkProviderNotLdap     = "provider_not_ldap"
	ldapLinkLdapUsernameMissing = "ldap_username_missing"
	ldapLinkUsernameMismatch    = "username_mismatch"
	ldapLinkEmailMismatch       = "email_mismatch"
	ldapLinkAmbiguous           = "ldap_username_ambiguous"
	ldapLinkAdminRefused        = "admin_refused"
	ldapLinkStoreError          = "store_error"
)

// ldapLinkASCII — yazdırılabilir ASCII, `@` yok, boş değil (Unicode katlaması
// ile başka hesaba çarpışma olmasın; UPN/e-posta biçimi kullanıcı adı değil). SAF.
func ldapLinkASCII(v string) bool {
	if v == "" || len(v) > 256 || strings.Contains(v, "@") {
		return false
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return false
		}
	}
	return true
}

// oidcLdapLinkMatch — v0.10.1403: güven anahtarıyla gelen giriş u hesabına
// bağlanabilir mi (dosya başlığındaki 1-5). email callback'in küçük harfli
// e-postası; rows = GetActiveUsersByLdapUsername(claims.Username) (≤2). Admin
// kararı ÇAĞIRANDA (ayrı açık-seçim). SAF.
func oidcLdapLinkMatch(u chstore.User, claims *auth.OIDCClaims, email string, rows []chstore.User) (bool, string) {
	if claims == nil || !ldapLinkASCII(claims.Username) {
		return false, ldapLinkNoUsernameClaim
	}
	// oidc: daha önce bu yolla taşınmış hesap (ldap_username duruyor) — taşınmış
	// admin her girişte kanıtı yeniden verir. local / boş (depoda local) ASLA.
	if u.AuthProvider != "ldap" && u.AuthProvider != "oidc" {
		return false, ldapLinkProviderNotLdap
	}
	if !ldapLinkASCII(u.LdapUsername) {
		return false, ldapLinkLdapUsernameMissing
	}
	if !strings.EqualFold(u.LdapUsername, claims.Username) {
		return false, ldapLinkUsernameMismatch
	}
	if email == "" || u.Email != email {
		return false, ldapLinkEmailMismatch
	}
	if len(rows) != 1 || rows[0].ID != u.ID {
		return false, ldapLinkAmbiguous
	}
	return true, ""
}

// ldapLinkLogEvery — aynı sebep için red satırları arası en az süre.
const ldapLinkLogEvery = time.Minute

var (
	ldapLinkLogMu sync.Mutex
	ldapLinkLog   = map[string]time.Time{} // sabit sebep kümesi → ≤8 anahtar
)

// logTrustLinkRefused — sebep başına dakikada bir satır; e-posta / kullanıcı
// adı YOK, yalnız kullanıcı id'si.
func logTrustLinkRefused(reason, userID string, now time.Time, logf func(string, ...any)) {
	ldapLinkLogMu.Lock()
	if prev, ok := ldapLinkLog[reason]; ok && now.Sub(prev) < ldapLinkLogEvery {
		ldapLinkLogMu.Unlock()
		return
	}
	ldapLinkLog[reason] = now
	ldapLinkLogMu.Unlock()
	logf("[oidc] ldap account link refused: reason=%s user id=%s", reason, userID)
}

// oidcTrustLdapLink — v0.10.1403: oidcLoginUser'ın ViaTrust + bağlanamaz-hesap
// dalı. linked=false → çağıran bugünkü email_unverified_privileged reddini
// verir (aynı sayfa metni). linked=true → dönen kullanıcıyla giriş sürer.
func (s *Server) oidcTrustLdapLink(r *http.Request, st oidcUserStore, u chstore.User, email string, claims *auth.OIDCClaims) (*chstore.User, bool) {
	if s.oidc == nil || !s.oidc.LinkLdapByUsername() {
		return nil, false
	}
	var rows []chstore.User
	if claims != nil && ldapLinkASCII(claims.Username) {
		var err error
		rows, err = oidcLdapLookupFor(s).GetActiveUsersByLdapUsername(r.Context(), strings.ToLower(claims.Username))
		if err != nil {
			logTrustLinkRefused(ldapLinkStoreError, u.ID, time.Now(), log.Printf)
			return nil, false
		}
	}
	ok, reason := oidcLdapLinkMatch(u, claims, email, rows)
	if !ok {
		logTrustLinkRefused(reason, u.ID, time.Now(), log.Printf)
		return nil, false
	}
	if u.Role == auth.RoleAdmin && !s.oidc.LinkLdapAllowAdmin() {
		logTrustLinkRefused(ldapLinkAdminRefused, u.ID, time.Now(), log.Printf)
		return nil, false
	}
	if u.AuthProvider == "oidc" {
		// Daha önce taşınmış (yalnız admin buraya düşer): yazma yok, kanıt tamam.
		return &u, true
	}
	nu, err := s.convertLdapAccountToOIDC(r.Context(), st, u)
	if err != nil {
		logTrustLinkRefused(ldapLinkStoreError, u.ID, time.Now(), log.Printf)
		return nil, false
	}
	details, _ := json.Marshal(map[string]any{"from": "ldap", "to": "oidc",
		"issuer": s.oidc.IssuerURL(), "sub": claims.Subject, "via": "username+email"})
	s.auditAs(&auth.Claims{UserID: nu.ID, Email: nu.Email, Role: nu.Role}, clientIP(r),
		"auth.oidc.link_ldap_account", "user", nu.ID, string(details))
	log.Printf("[oidc] ldap account linked via username user id=%s", nu.ID)
	return &nu, true
}

// convertLdapAccountToOIDC — auth_provider ldap → oidc. ReplacingMergeTree
// whole-row replace: satırın TÜM alanları (ldap_username, şifre özeti, takım,
// özel rol, foto, son giriş …) aynen taşınır; yalnız sağlayıcı değişir.
func (s *Server) convertLdapAccountToOIDC(ctx context.Context, st oidcUserStore, u chstore.User) (chstore.User, error) {
	nu := u
	nu.AuthProvider = "oidc"
	if err := st.UpsertUser(ctx, nu); err != nil {
		return u, err
	}
	if s.meUsers != nil {
		s.meUsers.clear()
	}
	if s.auth != nil {
		s.auth.InvalidateAuthz(nu.ID)
	}
	return nu, nil
}
