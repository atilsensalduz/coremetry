package api

// auth_session_test.go — v0.10.1375 regresyonu (operatör-bildirimi, OIDC/
// Keycloak SSO: "OIDC ile logout olunca sayfa bir daha yüklenir gibi
// oluyor. Ve network'te hata oluyor." — IdP çıkışından dönen /login
// yüklemesinde GET /api/auth/me → 401, Network sekmesinde kırmızı).
//
// Sözleşme: GET /api/auth/session HER ZAMAN 200 — kimliksiz/süresi
// geçmiş/geçersiz/silinmiş → {"authenticated":false}; geçerli oturum →
// {"authenticated":true,"user":{…me şekli…}}. Cache-Control: no-store.
// Gerçek middleware zinciri üzerinden de koşulur: yol SkipPath'te değilse
// (ya da metot-duyarsız açılırsa) burada yakalanır. /api/auth/me'nin 401
// sözleşmesi DEĞİŞMEZ.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const sessTestSecret = "0123456789abcdef0123456789abcdef0123456789"

// sessAuthzLookup — canlı yetki: listede olmayan kullanıcı silinmiş sayılır.
type sessAuthzLookup struct{ live map[string]string }

func (l sessAuthzLookup) LiveAuthz(_ context.Context, uid string) (string, bool, error) {
	role, ok := l.live[uid]
	return role, ok, nil
}

type sessErrLookup struct{}

func (sessErrLookup) LiveAuthz(context.Context, string) (string, bool, error) {
	return "", false, errors.New("ch down")
}

func newSessTestServer(t *testing.T) (*Server, *auth.Service) {
	t.Helper()
	svc := auth.NewService(sessTestSecret, time.Hour)
	svc.SetAuthzLookup(sessAuthzLookup{live: map[string]string{"u1": auth.RoleEditor}})
	s := &Server{auth: svc, meUsers: newMeCache(30 * time.Second)}
	// Satır cache'te: store'a (nil) gidilmez; /api/auth/me ile aynı yol.
	s.meUsers.put("u1", &chstore.User{ID: "u1", Email: "op@example.test", Role: auth.RoleEditor, FullName: "Ada Lovelace"}, time.Now())
	return s, svc
}

func mustToken(t *testing.T, svc *auth.Service, uid string) string {
	t.Helper()
	tok, _, err := svc.Issue(uid, uid+"@example.test", auth.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestAuthSessionProbe_v0101375(t *testing.T) {
	s, svc := newSessTestServer(t)
	valid := mustToken(t, svc, "u1")
	expired, _, err := auth.NewService(sessTestSecret, -time.Minute).Issue("u1", "op@example.test", auth.RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := auth.NewService("ffffffffffffffffffffffffffffffffffffffffff", time.Hour).Issue("u1", "op@example.test", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	deleted := mustToken(t, svc, "gone")

	cases := []struct {
		name     string
		cookie   string
		bearer   string
		wantAuth bool
	}{
		{name: "çerez yok", wantAuth: false},
		{name: "geçerli çerez", cookie: valid, wantAuth: true},
		{name: "geçerli Bearer", bearer: valid, wantAuth: true},
		{name: "süresi geçmiş", cookie: expired, wantAuth: false},
		{name: "başka anahtarla imzalı", cookie: foreign, wantAuth: false},
		{name: "bozuk çerez", cookie: "not-a-jwt", wantAuth: false},
		{name: "silinmiş kullanıcı (canlı yetki)", cookie: deleted, wantAuth: false},
	}
	// Gerçek zincir: auth.Middleware → buildMux (defter dahil).
	h := s.auth.Middleware(s.buildMux())
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tc.cookie})
			}
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, 200 bekleniyordu (sonda hiç 401 dönmez): %s", w.Code, w.Body.String())
			}
			if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
				t.Fatalf("Cache-Control %q, no-store bekleniyordu", cc)
			}
			var out struct {
				Authenticated bool           `json:"authenticated"`
				User          map[string]any `json:"user"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatalf("JSON değil: %v %q", err, w.Body.String())
			}
			if out.Authenticated != tc.wantAuth {
				t.Fatalf("authenticated=%v, beklenen %v: %s", out.Authenticated, tc.wantAuth, w.Body.String())
			}
			if !tc.wantAuth {
				if out.User != nil {
					t.Fatalf("kimliksiz cevapta user olmamalı: %s", w.Body.String())
				}
				return
			}
			// /api/auth/me ile aynı gövde (meUserPayload).
			if out.User["id"] != "u1" || out.User["email"] != "op@example.test" || out.User["firstName"] != "Ada" {
				t.Fatalf("user gövdesi /api/auth/me şeklinde değil: %v", out.User)
			}
		})
	}
}

// Sonda ve /api/auth/me AYNI gövdeyi döner; me'nin 401 sözleşmesi aynen.
func TestAuthSessionMatchesMe_v0101375(t *testing.T) {
	s, svc := newSessTestServer(t)
	h := s.auth.Middleware(s.buildMux())
	tok := mustToken(t, svc, "u1")

	get := func(path, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := get("/api/auth/me", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("/api/auth/me kimliksiz → %d, 401 kalmalı", w.Code)
	}
	me := get("/api/auth/me", tok)
	sess := get("/api/auth/session", tok)
	var meBody map[string]any
	var sessBody struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sess.Body.Bytes(), &sessBody); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(meBody)
	b, _ := json.Marshal(sessBody.User)
	if string(a) != string(b) {
		t.Fatalf("gövde ayrıştı:\n me      %s\n session %s", a, b)
	}
}

// Yalnız GET muaf: POST middleware'den geçmez (401), sonda yazma kapısı değil.
func TestAuthSessionSkipPathGETOnly_v0101375(t *testing.T) {
	if !auth.SkipPath(http.MethodGet, "/api/auth/session") {
		t.Fatal("GET /api/auth/session SkipPath'te olmalı (aksi hâlde middleware 401 yazar)")
	}
	if auth.SkipPath(http.MethodPost, "/api/auth/session") || auth.SkipPath(http.MethodGet, "/api/auth/session/x") {
		t.Fatal("yalnız GET /api/auth/session muaf olmalı")
	}
}

// Canlı yetki deposu hata verirse fail-open (Middleware'le aynı karar):
// sonda "oturum var" der — ikisi ayrışırsa SPA döngüye girer.
func TestAuthSessionFailOpenMatchesMiddleware_v0101375(t *testing.T) {
	s, svc := newSessTestServer(t)
	svc.SetAuthzLookup(sessErrLookup{})
	tok := mustToken(t, svc, "u1")
	h := s.auth.Middleware(s.buildMux())
	for _, path := range []string{"/api/auth/me", "/api/auth/session"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s → %d, fail-open 200 bekleniyordu", path, w.Code)
		}
	}
}

func TestAuthSessionRouteRegistered_v0101375(t *testing.T) {
	if _, ok := extraRouteRegistrars["auth-session"]; !ok {
		t.Fatal("auth-session defterde yok (registerRoutesExtra)")
	}
	mux := (&Server{}).buildMux()
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/api/auth/session", nil))
	if pattern != "GET /api/auth/session" {
		t.Fatalf("GET /api/auth/session kalıbı %q — kayıtsız yol SPA catch-all'ına (200 + index.html) düşer", pattern)
	}
}
