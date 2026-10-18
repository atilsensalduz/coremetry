package api

// tour_routes_test.go — v0.10.1334 rehberli tur durum ucu sözleşmesi:
// deterministik satır kimliği, status enum'u (yalnız completed|skipped
// yazılır), version sınırı, 7 günlük yeni-kullanıcı penceresi (sınır dahil,
// sıfır damga yeni sayılmaz), saklanan satırın çözümü (sahip izolasyonu,
// tombstone, bozuk içerik → 'none'), claims yoksa 401, gövde ≤ 1 KiB.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTourRowID(t *testing.T) {
	if got := tourRowID("u1"); got != "tour:u1" {
		t.Fatalf("tourRowID: %q", got)
	}
	if tourRowID("a") == tourRowID("b") {
		t.Fatal("farklı kullanıcı aynı satıra düşmemeli")
	}
}

func TestValidateTourStatus(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"completed", true},
		{"skipped", true},
		{"none", false}, // yokluk; yazılmaz
		{"", false},
		{"Completed", false},
		{"done", false},
		{" skipped", false},
	}
	for _, c := range cases {
		if err := validateTourStatus(c.in); (err == nil) != c.ok {
			t.Errorf("%q: err=%v, ok istenen %v", c.in, err, c.ok)
		}
	}
}

func TestValidateTourVersion(t *testing.T) {
	for _, c := range []struct {
		v  int
		ok bool
	}{{1, true}, {2, true}, {tourVersionMax, true}, {0, false}, {-1, false}, {tourVersionMax + 1, false}} {
		if err := validateTourVersion(c.v); (err == nil) != c.ok {
			t.Errorf("version %d: err=%v, ok istenen %v", c.v, err, c.ok)
		}
	}
}

func TestIsNewUser(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		created int64
		want    bool
	}{
		{"bugün açıldı", now.Add(-time.Hour).UnixNano(), true},
		{"6 gün önce", now.Add(-6 * 24 * time.Hour).UnixNano(), true},
		{"tam 7 gün (sınır dahil)", now.Add(-7 * 24 * time.Hour).UnixNano(), true},
		{"7 gün + 1 sn", now.Add(-7*24*time.Hour - time.Second).UnixNano(), false},
		{"30 gün önce", now.Add(-30 * 24 * time.Hour).UnixNano(), false},
		{"sıfır damga (bilinmiyor)", 0, false},
		{"negatif damga", -1, false},
	}
	for _, c := range cases {
		if got := isNewUser(c.created, now); got != c.want {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}
}

func TestParseStoredTour(t *testing.T) {
	row := func(owner, name, qs string) *chstore.SavedView {
		return &chstore.SavedView{ID: tourRowID("u1"), OwnerID: owner, Name: name, Page: tourPage, QueryString: qs, CreatedAt: 42}
	}
	cases := []struct {
		name        string
		v           *chstore.SavedView
		wantStatus  string
		wantVersion int
		wantAt      int64
	}{
		{"satır yok", nil, "none", 0, 0},
		{"tamamlandı", row("u1", "tour", `{"status":"completed","version":1}`), "completed", 1, 42},
		{"atlandı", row("u1", "tour", `{"status":"skipped","version":3}`), "skipped", 3, 42},
		{"başka sahip", row("u2", "tour", `{"status":"completed","version":1}`), "none", 0, 0},
		{"tombstone", row("u1", "", `{"status":"completed","version":1}`), "none", 0, 0},
		{"bozuk JSON", row("u1", "tour", `{`), "none", 0, 0},
		{"tanınmayan status", row("u1", "tour", `{"status":"weird","version":1}`), "none", 0, 0},
	}
	for _, c := range cases {
		st, ver, at := parseStoredTour(c.v, "u1")
		if st != c.wantStatus || ver != c.wantVersion || at != c.wantAt {
			t.Errorf("%s: (%q,%d,%d), istenen (%q,%d,%d)", c.name, st, ver, at, c.wantStatus, c.wantVersion, c.wantAt)
		}
	}
}

func TestCanonicalTourJSON(t *testing.T) {
	if got := canonicalTourJSON(tourStateBody{Status: "completed", Version: 1}); got != `{"status":"completed","version":1}` {
		t.Fatalf("kanonik JSON: %s", got)
	}
}

func TestTourRequiresClaims(t *testing.T) {
	s := &Server{}
	mux := s.buildMux()
	for _, m := range []string{"GET", "PUT"} {
		req := httptest.NewRequest(m, "/api/tour/state", strings.NewReader(`{"status":"completed","version":1}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s claims yokken %d, istenen 401", m, rec.Code)
		}
	}
}

// PUT doğrulaması store'a DOKUNMADAN önce: kötü status/version 400, 1 KiB
// üstü gövde 413 (s.store nil — buraya ulaşılsaydı panic ederdi).
func TestPutTourStateRejectsBeforeStore(t *testing.T) {
	s := &Server{}
	cases := []struct {
		name string
		body string
		want int
	}{
		{"status none", `{"status":"none","version":1}`, http.StatusBadRequest},
		{"status boş", `{"version":1}`, http.StatusBadRequest},
		{"version 0", `{"status":"completed","version":0}`, http.StatusBadRequest},
		{"bozuk JSON", `{`, http.StatusBadRequest},
		{"1 KiB üstü", `{"status":"completed","version":1,"x":"` + strings.Repeat("a", tourBodyMax) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		req := httptest.NewRequest("PUT", "/api/tour/state", strings.NewReader(c.body))
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleViewer}))
		rec := httptest.NewRecorder()
		s.putTourState(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: %d, istenen %d", c.name, rec.Code, c.want)
		}
	}
}
