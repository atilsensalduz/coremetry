package auth

// oidc_email_trust_test.go — v0.10.1120 (operatör, prod: "[oidc] callback
// failed: class=email_unverified"; AD/LDAP federasyonlu kurumsal IdP
// email_verified=false gönderiyor).
//
// NE ÇİVİLİYOR: saf karar tablosu (false+güven+alan adı → kabul; false+güven+
// boş liste → email_unverified; false+güvensiz → email_unverified; claim yok
// / true → değişmedi; alan adı dışı hâlâ reddedilir); doğrulama 400 metni;
// elle yazılmış tutarsız blob SSO'yu düşürmez, bayrak kapalı sayılır;
// uçtan uca Exchange (sahte IdP); WARNING satırı tam e-posta taşımaz ve alan
// adı başına seyreltilir.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDecideOIDCEmail(t *testing.T) {
	doms := []string{"example.test"}
	cases := []struct {
		name     string
		claim    string // ham JSON; "" = claim yok
		email    string
		domains  []string
		trust    bool
		class    string
		viaTrust bool
	}{
		{"false + güven + alan adları → kabul", `false`, "a@example.test", doms, true, "", true},
		{`"false" dizgisi + güven + alan adları → kabul`, `"false"`, "a@EXAMPLE.test", doms, true, "", true},
		{"false + güven + boş liste → red", `false`, "a@example.test", nil, true, "email_unverified", false},
		{"false + güven + yalnız boş giriş → red", `false`, "a@example.test", []string{" "}, true, "email_unverified", false},
		{"false + güven yok → red", `false`, "a@example.test", doms, false, "email_unverified", false},
		{"false + güven yok + boş liste → red", `false`, "a@example.test", nil, false, "email_unverified", false},
		{"claim yok → kabul (değişmedi)", ``, "a@example.test", doms, false, "", false},
		{"claim null → kabul (değişmedi)", `null`, "a@other.test", nil, false, "", false},
		{"true → kabul", `true`, "a@example.test", doms, false, "", false},
		{"true + güven → kabul, anahtar sayesinde DEĞİL", `true`, "a@example.test", doms, true, "", false},
		{"false + güven + alan adı dışı → red", `false`, "a@evil.test", doms, true, "email_domain_denied", false},
		{"true + alan adı dışı → red", `true`, "a@evil.test", doms, false, "email_domain_denied", false},
		{"false + güven + e-posta yok → red", `false`, "", doms, true, "email_missing", false},
		// Güvenlik incelemesi: ASCII dışı e-posta YALNIZ güven anahtarıyla gelen girişte red.
		{"ASCII dışı yerel kısım + güven → email_invalid", `false`, "İdmin@example.test", doms, true, "email_invalid", false},
		{"KELVIN SIGN alan adı + güven → email_invalid", `false`, "a@examplK.test", doms, true, "email_invalid", false},
		{"Türkçe karakter + doğrulanmış → kabul (değişmedi)", `true`, "çağrı.öz@example.test", doms, true, "", false},
		{"Türkçe karakter + claim yok + boş liste → kabul (değişmedi)", ``, "ü@other.test", nil, false, "", false},
		{"Türkçe karakter + doğrulanmış + güven yok → kabul", `true`, "şule@example.test", doms, false, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideOIDCEmail(json.RawMessage(c.claim), c.email, c.domains, c.trust)
			if d.Class != c.class || d.ViaTrust != c.viaTrust {
				t.Fatalf("karar = %+v; beklenen class=%q viaTrust=%v", d, c.class, c.viaTrust)
			}
		})
	}
}

func TestValidateTrustUnverifiedEmailNeedsDomains(t *testing.T) {
	s := NormalizeOIDCSettings(validInput("https://idp.example.test"), testPublicURL)
	s.TrustUnverifiedEmail = true
	var verr *OIDCSettingsError
	if err := ValidateOIDCSettings(s, false); !errors.As(err, &verr) || verr.Field != "trustUnverifiedEmail" ||
		!strings.Contains(err.Error(), "İzinli alan adları boşken doğrulanmamış e-postaya güvenilemez") {
		t.Fatalf("boş liste + güven reddedilmedi: %v", err)
	}
	s.Enabled = false // kapatma anahtarı mutlak: kapalıyken kayıt her zaman geçer
	if err := ValidateOIDCSettings(s, false); err != nil {
		t.Fatalf("kapalı + boş liste + güven reddedildi: %v", err)
	}
	s.Enabled, s.AllowedDomains = true, []string{"example.test"}
	if err := ValidateOIDCSettings(s, false); err != nil {
		t.Fatalf("dolu liste + güven reddedildi: %v", err)
	}
}

// Elle yazılmış blob (izinli alan adı yok + güven açık): SSO DÜŞMEZ, bayrak
// kapalı sayılır, lastError söyler; giriş email_verified=false'u reddeder.
func TestLoadPersistedIgnoresTrustWithoutDomains(t *testing.T) {
	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	raw, _ := json.Marshal(map[string]any{
		"enabled": true, "issuerUrl": idp.URL, "clientId": "coremetry", "clientSecret": testSecret,
		"scopes": []string{"openid", "email"}, "defaultRole": "viewer", "trustUnverifiedEmail": true,
	})
	st.rows[OIDCSettingsKey] = raw
	o := newDevService(t, st)
	snap := o.Snapshot()
	if !snap.Active || snap.TrustUnverifiedEmail || !strings.Contains(snap.LastError, "trustUnverifiedEmail yok sayıldı") {
		t.Fatalf("tutarsız blob: %+v", snap)
	}
	idp.set(func(f *fakeIdP) { f.claims = map[string]any{"email_verified": false} })
	if _, err := o.Exchange(context.Background(), "code", "verifier", ""); OIDCLoginErrorClass(err) != "email_unverified" {
		t.Fatalf("email_unverified bekleniyordu: %v", err)
	}
}

func TestExchangeTrustUnverifiedEmail(t *testing.T) {
	idp := newFakeIdP(t)
	o := newDevService(t, newFakeOIDCStore())
	in := validInput(idp.URL)
	in.AllowedDomains = []string{"example.test"}
	in.TrustUnverifiedEmail = true
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	idp.set(func(f *fakeIdP) { f.claims = map[string]any{"email_verified": false} })
	cl, err := o.Exchange(context.Background(), "code", "verifier", "")
	if err != nil || cl.Email != "user@example.test" || cl.EmailVerified || !cl.ViaTrust {
		t.Fatalf("güvenle kabul (ViaTrust) bekleniyordu: %+v %v", cl, err)
	}
	idp.set(func(f *fakeIdP) { f.claims = map[string]any{"email_verified": true} })
	if cl, err := o.Exchange(context.Background(), "code", "verifier", ""); err != nil || cl.ViaTrust || !cl.EmailVerified {
		t.Fatalf("doğrulanmış giriş ViaTrust taşımamalı: %+v %v", cl, err)
	}
	idp.set(func(f *fakeIdP) { f.claims = map[string]any{"email_verified": false, "email": "user@evil.test"} })
	if _, err := o.Exchange(context.Background(), "code", "verifier", ""); OIDCLoginErrorClass(err) != "email_domain_denied" {
		t.Fatalf("alan adı dışı hâlâ reddedilmeli: %v", err)
	}
	// Anahtar kapanınca eski davranış.
	in.TrustUnverifiedEmail = false
	if _, err := o.SaveSettings(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	idp.set(func(f *fakeIdP) { f.claims = map[string]any{"email_verified": false} })
	if _, err := o.Exchange(context.Background(), "code", "verifier", ""); OIDCLoginErrorClass(err) != "email_unverified" {
		t.Fatalf("anahtar kapalıyken email_unverified bekleniyordu: %v", err)
	}
}

func TestTrustedUnverifiedLoginLogDomainOnlyAndDeduped(t *testing.T) {
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	dom := "dedupe-" + fmt.Sprint(now.UnixNano()) + ".example.test" // paket düzeyi haritada çakışmasın
	logTrustedUnverifiedLogin("alice.secret@"+dom, now, logf)
	logTrustedUnverifiedLogin("bob@"+dom, now.Add(time.Minute), logf)
	if len(lines) != 1 {
		t.Fatalf("aynı alan adı bir saat içinde tek satır olmalı: %q", lines)
	}
	if !strings.Contains(lines[0], "email_verified=false accepted (trustUnverifiedEmail) domain="+dom) ||
		strings.Contains(lines[0], "alice") || strings.Contains(lines[0], "@") {
		t.Fatalf("satır yanlış ya da tam e-posta taşıyor: %q", lines[0])
	}
	logTrustedUnverifiedLogin("carol@"+dom, now.Add(trustedLoginLogEvery+time.Second), logf)
	if len(lines) != 2 {
		t.Fatalf("saat dolunca yeniden loglanmalı: %q", lines)
	}
}

func TestWarnTrustUnverifiedEmailOnlyWhenEnabled(t *testing.T) {
	var n int
	logf := func(string, ...any) { n++ }
	warnTrustUnverifiedEmail(OIDCSettings{Enabled: true, TrustUnverifiedEmail: false}, logf)
	warnTrustUnverifiedEmail(OIDCSettings{Enabled: false, TrustUnverifiedEmail: true}, logf)
	if n != 0 {
		t.Fatalf("kapalıyken uyarı: %d", n)
	}
	warnTrustUnverifiedEmail(OIDCSettings{Enabled: true, TrustUnverifiedEmail: true, AllowedDomains: []string{"example.test"}}, logf)
	if n != 1 {
		t.Fatalf("açıkken tek uyarı bekleniyordu: %d", n)
	}
}

// v0.10.1403 (operatör: "Bu hesap doğrulanmamış e-posta ile SSO'dan açılamaz"
// → "Coremetry tarafını yap" + "LDAP'ı bir süre sonra hiç kullanmayacağım,
// sadece merkezi login Keycloak üzerinden olacak") — linkLdapByUsername açıkken
// ViaTrust giriş imzalı id_token'daki kullanıcı adını (küçük harf) taşır;
// kapalıyken, doğrulanmış girişte ya da geçersiz değerde taşımaz; ViaUsername
// HİÇ açılmaz (admin kapısı F2 değişmez).
func TestExchangeTrustLinkUsername(t *testing.T) {
	idp := newFakeIdP(t)
	o := newDevService(t, newFakeOIDCStore())
	in := validInput(idp.URL)
	in.AllowedDomains = []string{"corp.example.test"}
	in.TrustUnverifiedEmail = true
	save := func(link, admin bool) {
		t.Helper()
		in.LinkLdapByUsername, in.LinkLdapByUsernameAllowAdmin = link, admin
		if _, err := o.SaveSettings(context.Background(), in); err != nil {
			t.Fatal(err)
		}
	}
	exch := func(claims map[string]any) *OIDCClaims {
		t.Helper()
		idp.set(func(f *fakeIdP) { f.claims = claims; f.userinfo = nil })
		cl, err := o.Exchange(context.Background(), "code", "verifier", "")
		if err != nil {
			t.Fatalf("Exchange: %v", err)
		}
		return cl
	}
	const mail = "first.last@corp.example.test"
	save(false, false)
	if o.LinkLdapByUsername() || o.LinkLdapAllowAdmin() {
		t.Fatal("bayrak kapalıyken erişimci açık")
	}
	if cl := exch(map[string]any{"email": mail, "email_verified": false, "preferred_username": "N0000001"}); cl.Username != "" || !cl.ViaTrust {
		t.Fatalf("bayrak kapalıyken Username dolmamalı: %+v", cl)
	}
	save(true, false)
	if !o.LinkLdapByUsername() || o.LinkLdapAllowAdmin() {
		t.Fatal("bağlama açık, admin kapalı bekleniyordu")
	}
	cl := exch(map[string]any{"email": mail, "email_verified": false, "preferred_username": "N0000001"})
	if cl.Username != "n0000001" || !cl.ViaTrust || cl.ViaUsername || cl.EmailSource != OIDCEmailSourceIDToken {
		t.Fatalf("Username küçük harfle dolmalı, ViaUsername kapalı: %+v", cl)
	}
	if cl := exch(map[string]any{"email": mail, "email_verified": true, "preferred_username": "n0000001"}); cl.Username != "" || cl.ViaTrust {
		t.Fatalf("doğrulanmış girişte Username dolmamalı: %+v", cl)
	}
	for _, bad := range []any{"n0000001@corp.example.test", "n000000İ", "", 42} {
		if cl := exch(map[string]any{"email": mail, "email_verified": false, "preferred_username": bad}); cl.Username != "" {
			t.Fatalf("geçersiz kullanıcı adı %v taşındı: %+v", bad, cl)
		}
	}
	// Yalnız yapılandırılan claim okunur — başka ad tahmin edilmez.
	if cl := exch(map[string]any{"email": mail, "email_verified": false, "sAMAccountName": "n0000001"}); cl.Username != "" {
		t.Fatalf("yapılandırılmamış claim okundu: %+v", cl)
	}
	in.UsernameClaim = "sAMAccountName"
	save(true, true)
	if !o.LinkLdapAllowAdmin() {
		t.Fatal("admin açık-seçimi canlı istemciye uygulanmadı")
	}
	if cl := exch(map[string]any{"email": mail, "email_verified": false, "sAMAccountName": "n0000001"}); cl.Username != "n0000001" {
		t.Fatalf("ayarlı claim okunmadı: %+v", cl)
	}
	// UserInfo yolu (id_token'da e-posta yok): kullanıcı adı önce id_token'dan.
	in.UsernameClaim = UsernameClaimDefault
	save(true, false)
	idp.set(func(f *fakeIdP) {
		f.claims = map[string]any{"email": "", "preferred_username": "N0000001"}
		f.userinfo = map[string]any{"sub": "u-1", "email": mail, "email_verified": false, "preferred_username": "n0000009"}
	})
	cl, err := o.Exchange(context.Background(), "code", "verifier", "")
	if err != nil || cl.EmailSource != OIDCEmailSourceUserInfo || !cl.ViaTrust || cl.Username != "n0000001" || cl.ViaUsername {
		t.Fatalf("UserInfo ViaTrust yolu: %+v %v", cl, err)
	}
	// id_token'da yoksa sub'ı eşleşmiş UserInfo'dan.
	idp.set(func(f *fakeIdP) {
		f.claims = map[string]any{"email": ""}
		f.userinfo = map[string]any{"sub": "u-1", "email": mail, "email_verified": false, "preferred_username": "N0000002"}
	})
	if cl, err := o.Exchange(context.Background(), "code", "verifier", ""); err != nil || cl.Username != "n0000002" {
		t.Fatalf("UserInfo kullanıcı adı: %+v %v", cl, err)
	}
}

// v0.10.1403 — bağlama yalnız güven anahtarıyla, admin açık-seçimi yalnız
// bağlamayla anlamlı (normalize); blob/snapshot gidiş-dönüşü; güveni düşüren
// elle yazılmış blob bağlamayı da düşürür.
func TestLinkLdapByUsernameSettings(t *testing.T) {
	s := NormalizeOIDCSettings(OIDCSettings{LinkLdapByUsername: true, LinkLdapByUsernameAllowAdmin: true}, testPublicURL)
	if s.LinkLdapByUsername || s.LinkLdapByUsernameAllowAdmin {
		t.Fatalf("güven kapalıyken bağlama açık kaldı: %v", s)
	}
	s = NormalizeOIDCSettings(OIDCSettings{TrustUnverifiedEmail: true, LinkLdapByUsernameAllowAdmin: true}, testPublicURL)
	if s.LinkLdapByUsernameAllowAdmin {
		t.Fatal("bağlama kapalıyken admin açık-seçimi açık kaldı")
	}
	if !strings.Contains(OIDCSettings{TrustUnverifiedEmail: true, LinkLdapByUsername: true}.String(), "linkLdap:true") {
		t.Fatal("String() bağlama bayrağını taşımıyor")
	}

	idp := newFakeIdP(t)
	st := newFakeOIDCStore()
	o := newDevService(t, st)
	in := validInput(idp.URL)
	in.AllowedDomains = []string{"corp.example.test"}
	in.TrustUnverifiedEmail, in.LinkLdapByUsername, in.LinkLdapByUsernameAllowAdmin = true, true, true
	snap, err := o.SaveSettings(context.Background(), in)
	if err != nil || !snap.LinkLdapByUsername || !snap.LinkLdapByUsernameAllowAdmin {
		t.Fatalf("kayıt: %+v %v", snap, err)
	}
	raw := string(st.rows[OIDCSettingsKey])
	if !strings.Contains(raw, `"linkLdapByUsername":true`) || !strings.Contains(raw, `"linkLdapByUsernameAllowAdmin":true`) {
		t.Fatalf("blob alanları taşımıyor: %s", raw)
	}
	// Güven kapanınca bağlama da kapanır (kayıt yolu).
	in.TrustUnverifiedEmail = false
	if snap, err := o.SaveSettings(context.Background(), in); err != nil || snap.LinkLdapByUsername || snap.LinkLdapByUsernameAllowAdmin || o.LinkLdapByUsername() {
		t.Fatalf("güven kapalıyken bağlama kaldı: %+v %v", snap, err)
	}

	// Elle yazılmış blob: alan adı yok + güven + bağlama → ikisi de düşer, SSO düşmez.
	st2 := newFakeOIDCStore()
	b, _ := json.Marshal(map[string]any{
		"enabled": true, "issuerUrl": idp.URL, "clientId": "coremetry", "clientSecret": testSecret,
		"scopes": []string{"openid", "email"}, "defaultRole": "viewer", "trustUnverifiedEmail": true,
		"linkLdapByUsername": true, "linkLdapByUsernameAllowAdmin": true,
	})
	st2.rows[OIDCSettingsKey] = b
	o2 := newDevService(t, st2)
	if sn := o2.Snapshot(); !sn.Active || sn.TrustUnverifiedEmail || sn.LinkLdapByUsername || sn.LinkLdapByUsernameAllowAdmin || o2.LinkLdapByUsername() {
		t.Fatalf("tutarsız blob bağlamayı düşürmedi: %+v", sn)
	}
}
