package api

// auth_oidc_ldap_link_test.go — v0.10.1403 (operatör, giriş sayfası: "SSO
// sign-in failed: Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz;
// parola/LDAP ile girin veya IdP'de Trust Email açılsın" → "Coremetry tarafını
// yap" + "LDAP'ı bir süre sonra hiç kullanmayacağım, sadece merkezi login
// Keycloak üzerinden olacak").
//
// NE ÇİVİLİYOR: saf eşleşme tablosu (oidcLdapLinkMatch — kullanıcı adı claim'i,
// sağlayıcı, ldap_username ASCII, büyük-küçük harf, e-posta, tek satır);
// oidcLoginUser uçtan uca: bağlama sağlayıcıyı oidc'ye taşır (tüm alanlar
// korunur, tek upsert, audit satırı, log e-posta/kullanıcı adı taşımaz); admin
// yalnız ayrı açık-seçimle; bayrak kapalıyken bugünkü mesaj; taşınmış viewer
// ikinci girişte kullanıcı adı olmadan girer; taşınmış admin kanıtı her girişte
// yeniden verir; yerel hesap ASLA bağlanmaz; PUT→GET + audit eski→yeni.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	linkMail  = "first.last@corp.example.test"
	linkUname = "n0000001"
)

func linkUser(role, provider string) chstore.User {
	return chstore.User{ID: "u1", Email: linkMail, Role: role, AuthProvider: provider, LdapUsername: linkUname,
		Team: "payments", CustomRole: "cr-1", PasswordHash: "h", FullName: "First Last", Org: "Corp",
		CreatedAt: 11, LastLoginAt: 22}
}

func TestOIDCLdapLinkMatch(t *testing.T) {
	cl := func(u string) *auth.OIDCClaims {
		return &auth.OIDCClaims{Email: linkMail, ViaTrust: true, Username: u}
	}
	base := linkUser(auth.RoleViewer, "ldap")
	one := []chstore.User{base}
	mod := func(f func(*chstore.User)) chstore.User { u := base; f(&u); return u }
	other := mod(func(u *chstore.User) { u.ID = "u2" })
	cases := []struct {
		name   string
		u      chstore.User
		claims *auth.OIDCClaims
		email  string
		rows   []chstore.User
		ok     bool
		reason string
	}{
		{"eşleşme", base, cl(linkUname), linkMail, one, true, ""},
		{"büyük harf claim (N0000001) eşleşir", base, cl("N0000001"), linkMail, one, true, ""},
		{"büyük harf ldap_username eşleşir", mod(func(u *chstore.User) { u.LdapUsername = "N0000001" }), cl(linkUname), linkMail, one, true, ""},
		{"taşınmış (oidc + ldap_username) eşleşir", mod(func(u *chstore.User) { u.AuthProvider = "oidc" }), cl(linkUname), linkMail, one, true, ""},
		{"claim yok", base, cl(""), linkMail, one, false, ldapLinkNoUsernameClaim},
		{"claims nil", base, nil, linkMail, one, false, ldapLinkNoUsernameClaim},
		{"claim ASCII dışı", base, cl("n000000İ"), linkMail, one, false, ldapLinkNoUsernameClaim},
		{"claim UPN biçimi", base, cl("n0000001@corp.example.test"), linkMail, one, false, ldapLinkNoUsernameClaim},
		{"yerel hesap ASLA", mod(func(u *chstore.User) { u.AuthProvider = "local" }), cl(linkUname), linkMail, one, false, ldapLinkProviderNotLdap},
		{"sağlayıcı boş (depoda local) ASLA", mod(func(u *chstore.User) { u.AuthProvider = "" }), cl(linkUname), linkMail, one, false, ldapLinkProviderNotLdap},
		{"ldap_username boş (hiç LDAP girişi yok)", mod(func(u *chstore.User) { u.LdapUsername = "" }), cl(linkUname), linkMail, one, false, ldapLinkLdapUsernameMissing},
		{"ldap_username ASCII dışı", mod(func(u *chstore.User) { u.LdapUsername = "n000000İ" }), cl("n000000i"), linkMail, one, false, ldapLinkLdapUsernameMissing},
		{"KELVIN SIGN katlaması eşleşmez", mod(func(u *chstore.User) { u.LdapUsername = "kn0000001" }), cl("Kn0000001"), linkMail, one, false, ldapLinkNoUsernameClaim},
		{"kullanıcı adı farklı", base, cl("n0000002"), linkMail, one, false, ldapLinkUsernameMismatch},
		{"e-posta farklı", base, cl(linkUname), "other.user@corp.example.test", one, false, ldapLinkEmailMismatch},
		{"e-posta boş", base, cl(linkUname), "", one, false, ldapLinkEmailMismatch},
		{"iki aktif satır", base, cl(linkUname), linkMail, []chstore.User{base, other}, false, ldapLinkAmbiguous},
		{"satır yok (sütun yok / devre dışı)", base, cl(linkUname), linkMail, nil, false, ldapLinkAmbiguous},
		{"tek satır ama başka hesap", base, cl(linkUname), linkMail, []chstore.User{other}, false, ldapLinkAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := oidcLdapLinkMatch(c.u, c.claims, c.email, c.rows)
			if ok != c.ok || reason != c.reason {
				t.Fatalf("(%v, %q); beklenen (%v, %q)", ok, reason, c.ok, c.reason)
			}
		})
	}
}

// liveLinkEnv — trustUnverifiedEmail + linkLdapByUsername açık canlı ortam.
func liveLinkEnv(t *testing.T, link, allowAdmin bool) *oidcTestEnv {
	t.Helper()
	e := newOIDCTestEnv(t)
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body(oidcTestSecret)), &m)
	m["trustUnverifiedEmail"] = true
	m["linkLdapByUsername"] = link
	m["linkLdapByUsernameAllowAdmin"] = allowAdmin
	b, _ := json.Marshal(m)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(b), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	_ = e.audits()
	if e.s.oidc.LinkLdapByUsername() != link || e.s.oidc.LinkLdapAllowAdmin() != (link && allowAdmin) {
		t.Fatal("bağlama ayarı canlı istemciye uygulanmadı")
	}
	return e
}

// resetLinkLog — red satırı seyreltmesi paket düzeyinde; testler birbirini susturmasın.
func resetLinkLog(t *testing.T) {
	t.Helper()
	ldapLinkLogMu.Lock()
	ldapLinkLog = map[string]time.Time{}
	ldapLinkLogMu.Unlock()
}

type linkFixture struct {
	e  *oidcTestEnv
	st *claimFakeStore
	lk *ldapLookupFake
}

func newLinkFixture(t *testing.T, link, allowAdmin bool, existing chstore.User) *linkFixture {
	t.Helper()
	e := liveLinkEnv(t, link, allowAdmin)
	st := &claimFakeStore{users: map[string]*chstore.User{existing.Email: &existing}, admins: 2}
	lk := &ldapLookupFake{users: map[string][]chstore.User{strings.ToLower(existing.LdapUsername): {existing}}}
	prevL, prevS := oidcLdapLookupFor, oidcUserStoreFor
	oidcLdapLookupFor = func(*Server) oidcLdapUserLookup { return lk }
	oidcUserStoreFor = func(*Server) oidcUserStore { return st }
	t.Cleanup(func() { oidcLdapLookupFor, oidcUserStoreFor = prevL, prevS })
	resetLinkLog(t)
	return &linkFixture{e: e, st: st, lk: lk}
}

func (f *linkFixture) login(username string) (*chstore.User, error) {
	req := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
	return f.e.s.oidcLoginUser(req, linkMail, &auth.OIDCClaims{Email: linkMail, ViaTrust: true,
		EmailSource: auth.OIDCEmailSourceIDToken, Subject: "sub-0001", Username: username})
}

func wantUnverifiedPrivileged(t *testing.T, u *chstore.User, err error) {
	t.Helper()
	var pe *oidcLoginPageError
	if u != nil || !errors.As(err, &pe) || pe.Class != oidcClassEmailUnverifiedPrivileged ||
		!strings.Contains(err.Error(), "Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz") {
		t.Fatalf("bugünkü red bekleniyordu: u=%v err=%v", u, err)
	}
}

func TestOIDCLoginUserLinksLdapAccount(t *testing.T) {
	f := newLinkFixture(t, true, false, linkUser(auth.RoleViewer, "ldap"))
	buf := captureAPILog(t)
	got, err := f.login("N0000001")
	if err != nil || got == nil || got.AuthProvider != "oidc" {
		t.Fatalf("bağlama bekleniyordu: u=%v err=%v", got, err)
	}
	want := linkUser(auth.RoleViewer, "oidc")
	if len(f.st.ups) != 1 || f.st.ups[0].AuthProvider != "oidc" || f.st.ups[0].ID != want.ID ||
		f.st.ups[0].Team != want.Team || f.st.ups[0].CustomRole != want.CustomRole || f.st.ups[0].PasswordHash != want.PasswordHash ||
		f.st.ups[0].LdapUsername != want.LdapUsername || f.st.ups[0].FullName != want.FullName || f.st.ups[0].Org != want.Org ||
		f.st.ups[0].CreatedAt != want.CreatedAt || f.st.ups[0].LastLoginAt != want.LastLoginAt || f.st.ups[0].Role != want.Role {
		t.Fatalf("tek upsert + tüm alanlar taşınmalı: %+v", f.st.ups)
	}
	if len(f.lk.got) != 1 || f.lk.got[0] != linkUname {
		t.Fatalf("ldap_username okuması küçük harfle tek sefer: %v", f.lk.got)
	}
	au := f.e.audits()
	if len(au) != 1 || au[0].Action != "auth.oidc.link_ldap_account" || au[0].TargetKind != "user" || au[0].TargetID != "u1" {
		t.Fatalf("audit satırı: %+v", au)
	}
	var d map[string]string
	if err := json.Unmarshal([]byte(au[0].Details), &d); err != nil || d["from"] != "ldap" || d["to"] != "oidc" ||
		d["via"] != "username+email" || d["sub"] != "sub-0001" || d["issuer"] != f.e.idp.URL {
		t.Fatalf("audit ayrıntısı: %v %s", err, au[0].Details)
	}
	out := buf.String()
	if !strings.Contains(out, "[oidc] ldap account linked via username user id=u1") ||
		strings.Contains(out, "@") || strings.Contains(strings.ToLower(out), linkUname) {
		t.Fatalf("log satırı (e-posta/kullanıcı adı yok): %s", out)
	}

	// İkinci giriş: satır artık oidc viewer — kullanıcı adı olmadan girer, yazma yok.
	f.st.users[linkMail] = &f.st.ups[0]
	got, err = f.login("")
	if err != nil || got == nil || got.AuthProvider != "oidc" || len(f.st.ups) != 1 || len(f.e.audits()) != 0 {
		t.Fatalf("ikinci giriş: u=%v err=%v ups=%d", got, err, len(f.st.ups))
	}
}

func TestOIDCLoginUserLdapLinkRefusals(t *testing.T) {
	cases := []struct {
		name       string
		link       bool
		allowAdmin bool
		existing   chstore.User
		username   string
		reason     string // "" = sebep satırı beklenmez (bayrak kapalı)
	}{
		{"bayrak kapalı → bugünkü mesaj", false, false, linkUser(auth.RoleViewer, "ldap"), linkUname, ""},
		{"admin + açık-seçim yok → red", true, false, linkUser(auth.RoleAdmin, "ldap"), linkUname, ldapLinkAdminRefused},
		{"kullanıcı adı yok → red", true, false, linkUser(auth.RoleViewer, "ldap"), "", ldapLinkNoUsernameClaim},
		{"kullanıcı adı farklı → red", true, false, linkUser(auth.RoleViewer, "ldap"), "n0000002", ldapLinkUsernameMismatch},
		{"yerel hesap (ldap_username dolu olsa da) → red", true, true, linkUser(auth.RoleViewer, "local"), linkUname, ldapLinkProviderNotLdap},
		{"LDAP girişi hiç olmamış → red", true, false, func() chstore.User { u := linkUser(auth.RoleViewer, "ldap"); u.LdapUsername = ""; return u }(), linkUname, ldapLinkLdapUsernameMissing},
		{"taşınmış admin + kullanıcı adı yok → red", true, true, linkUser(auth.RoleAdmin, "oidc"), "", ldapLinkNoUsernameClaim},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newLinkFixture(t, c.link, c.allowAdmin, c.existing)
			buf := captureAPILog(t)
			got, err := f.login(c.username)
			wantUnverifiedPrivileged(t, got, err)
			if len(f.st.ups) != 0 || len(f.e.audits()) != 0 {
				t.Fatalf("red yazmamalı/audit'lememeli: %+v", f.st.ups)
			}
			out := buf.String()
			if !strings.Contains(out, "class=email_unverified_privileged user id=u1") || strings.Contains(out, "@") ||
				strings.Contains(strings.ToLower(out), linkUname) {
				t.Fatalf("red logu: %s", out)
			}
			if c.reason == "" {
				if strings.Contains(out, "link refused") || len(f.lk.got) != 0 {
					t.Fatalf("bayrak kapalıyken bağlama denenmemeli: %s %v", out, f.lk.got)
				}
				return
			}
			if !strings.Contains(out, "[oidc] ldap account link refused: reason="+c.reason+" user id=u1") {
				t.Fatalf("sebep satırı %q yok: %s", c.reason, out)
			}
		})
	}
}

func TestOIDCLoginUserLdapLinkAdminOptIn(t *testing.T) {
	f := newLinkFixture(t, true, true, linkUser(auth.RoleAdmin, "ldap"))
	got, err := f.login(linkUname)
	if err != nil || got == nil || got.AuthProvider != "oidc" || got.Role != auth.RoleAdmin || len(f.st.ups) != 1 {
		t.Fatalf("admin açık-seçimle bağlanmalı: u=%v err=%v", got, err)
	}
	// Taşınmış admin: sonraki girişte kanıt yeniden istenir; geçerse yazma/audit yok.
	f.st.users[linkMail] = &f.st.ups[0]
	f.lk.users[linkUname] = []chstore.User{f.st.ups[0]}
	_ = f.e.audits()
	got, err = f.login(linkUname)
	if err != nil || got == nil || got.Role != auth.RoleAdmin || len(f.st.ups) != 1 || len(f.e.audits()) != 0 {
		t.Fatalf("taşınmış admin kanıtla girmeli, yazmasız: u=%v err=%v ups=%d", got, err, len(f.st.ups))
	}
}

type failingLdapLookup struct{}

func (failingLdapLookup) GetActiveUsersByLdapUsername(context.Context, string) ([]chstore.User, error) {
	return nil, errors.New("ch down")
}

func TestOIDCLoginUserLdapLinkStoreErrors(t *testing.T) {
	f := newLinkFixture(t, true, false, linkUser(auth.RoleViewer, "ldap"))
	oidcLdapLookupFor = func(*Server) oidcLdapUserLookup { return failingLdapLookup{} }
	buf := captureAPILog(t)
	got, err := f.login(linkUname)
	wantUnverifiedPrivileged(t, got, err)
	if !strings.Contains(buf.String(), "reason="+ldapLinkStoreError) || strings.Contains(buf.String(), "ch down") {
		t.Fatalf("okuma hatası: %s", buf.String())
	}
	// Yazma hatası: giriş reddedilir, audit yok.
	f2 := newLinkFixture(t, true, false, linkUser(auth.RoleViewer, "ldap"))
	f2.st.upErr = errors.New("insert failed")
	got, err = f2.login(linkUname)
	wantUnverifiedPrivileged(t, got, err)
	if len(f2.e.audits()) != 0 {
		t.Fatal("başarısız yazma audit'lenmemeli")
	}
}

// Red satırı sebep başına dakikada bir.
func TestLogTrustLinkRefusedDeduped(t *testing.T) {
	resetLinkLog(t)
	var n int
	logf := func(string, ...any) { n++ }
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	logTrustLinkRefused(ldapLinkEmailMismatch, "u1", now, logf)
	logTrustLinkRefused(ldapLinkEmailMismatch, "u2", now.Add(10*time.Second), logf)
	logTrustLinkRefused(ldapLinkUsernameMismatch, "u3", now.Add(10*time.Second), logf)
	if n != 2 {
		t.Fatalf("sebep başına tek satır bekleniyordu: %d", n)
	}
	logTrustLinkRefused(ldapLinkEmailMismatch, "u4", now.Add(ldapLinkLogEvery+time.Second), logf)
	if n != 3 {
		t.Fatalf("süre dolunca yeniden loglanmalı: %d", n)
	}
}

func TestOIDCSettingsLinkLdapRoundTripAndAudit(t *testing.T) {
	e := liveLinkEnv(t, true, true)
	g := e.do(t, "GET", "/api/settings/oidc", "", auth.RoleAdmin)
	var snap auth.OIDCSnapshot
	if err := json.Unmarshal(g.Body.Bytes(), &snap); err != nil || !snap.LinkLdapByUsername || !snap.LinkLdapByUsernameAllowAdmin {
		t.Fatalf("GET: %v %s", err, g.Body)
	}
	// Güven kapatılınca bağlama da kapanır; audit eski→yeni.
	var m map[string]any
	_ = json.Unmarshal([]byte(e.body("")), &m)
	m["trustUnverifiedEmail"] = false
	m["linkLdapByUsername"] = true
	m["linkLdapByUsernameAllowAdmin"] = true
	b, _ := json.Marshal(m)
	if w := e.do(t, "PUT", "/api/settings/oidc", string(b), auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	au := e.audits()
	if len(au) != 1 || au[0].Action != "settings.oidc.update" {
		t.Fatalf("audit: %+v", au)
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(au[0].Details), &raw)
	d := map[string]map[string]bool{}
	for _, k := range []string{"linkLdapByUsername", "linkLdapByUsernameAllowAdmin"} {
		var v map[string]bool
		if err := json.Unmarshal(raw[k], &v); err != nil {
			t.Fatalf("audit alanı %s yok: %s", k, au[0].Details)
		}
		d[k] = v
	}
	if !d["linkLdapByUsername"]["old"] || d["linkLdapByUsername"]["new"] ||
		!d["linkLdapByUsernameAllowAdmin"]["old"] || d["linkLdapByUsernameAllowAdmin"]["new"] {
		t.Fatalf("audit eski→yeni yanlış: %s", au[0].Details)
	}
	if e.s.oidc.LinkLdapByUsername() || e.s.oidc.LinkLdapAllowAdmin() {
		t.Fatal("güven kapalıyken bağlama canlı kaldı")
	}
}
