package api

// logs_team_test.go — v0.10.1477 (/logs `team=` süzgeci + /api/logs/my-team).
// Çivilenen: doğrulama (400), her /logs okumasının Filter.Team/TeamValues'u
// taşıması, takımsız isteğin bugünkü anahtarı BAYT-BAYT koruması, takımlı
// anahtarların takıma VE değer kümesine duyarlılığı, my-team'in kimlik /
// özellik kapısı (kapalıyken kullanıcı satırı okunmaz) ve anahtarı.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// esBackendLogStore — Backend() "elasticsearch" diyen yakalayıcı.
type esBackendLogStore struct{ patternFilterLogStore }

func (s *esBackendLogStore) Backend() string { return "elasticsearch" }

func TestLogsTeamFromQuery(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"", "", true},
		{"   ", "", true},
		{" orders-team ", "orders-team", true},
		{"Takım Ödeme", "Takım Ödeme", true},
		{"bad\nteam", "", false},
		{strings.Repeat("x", logsTeamMaxLen), strings.Repeat("x", logsTeamMaxLen), true},
		{strings.Repeat("x", logsTeamMaxLen+1), "", false},
	}
	for _, c := range cases {
		got, ok := logsTeamFromQuery(c.raw)
		if got != c.want || ok != c.ok {
			t.Errorf("%q → (%q,%v) want (%q,%v)", c.raw, got, ok, c.want, c.ok)
		}
	}
}

func TestLogsReads_TeamParam(t *testing.T) {
	const win = "&from=1759489200000000000&to=1759492800000000000"
	reads := []struct {
		name string
		call func(s *Server, q string) *httptest.ResponseRecorder
	}{
		{"GET /api/logs", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogs(w, httptest.NewRequest("GET", "/api/logs?"+q, nil))
			return w
		}},
		{"POST /api/logs/search", func(s *Server, q string) *httptest.ResponseRecorder {
			team, _ := url.QueryUnescape(strings.TrimPrefix(strings.SplitN(q, "&", 2)[0], "team="))
			b, _ := json.Marshal(map[string]string{"team": team, "search": "x"})
			body := string(b)
			w := httptest.NewRecorder()
			s.postLogsSearch(w, httptest.NewRequest("POST", "/api/logs/search", strings.NewReader(body)))
			return w
		}},
		{"GET /api/logs/timeseries", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogsTimeseries(w, httptest.NewRequest("GET", "/api/logs/timeseries?groupBy=severity&"+q, nil))
			return w
		}},
		{"GET /api/logs/fieldstats", func(s *Server, q string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			s.getLogsFieldStats(w, httptest.NewRequest("GET", "/api/logs/fieldstats?field=service&"+q, nil))
			return w
		}},
	}
	for _, r := range reads {
		t.Run(r.name+" — team → Filter.Team + değerler", func(t *testing.T) {
			st := &patternFilterLogStore{}
			s := patternFilterServer(st)
			s.logsMgr = teamLogsMgr("kubernetes.labels.team-a,kubernetes.labels.team-b")
			w := r.call(s, "team=orders-team&search=x"+win)
			if w.Code != 200 || st.calls != 1 {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, st.calls, w.Body.String())
			}
			if st.got.Team != "orders-team" || !reflect.DeepEqual(st.got.TeamValues, []string{"orders-team"}) {
				t.Fatalf("Filter team=%q values=%v", st.got.Team, st.got.TeamValues)
			}
		})
		t.Run(r.name+" — geçersiz takım → 400, arka uca gidilmez", func(t *testing.T) {
			st := &patternFilterLogStore{}
			w := r.call(patternFilterServer(st), "team=bad%0Ateam"+win)
			if w.Code != 400 || st.calls != 0 {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, st.calls, w.Body.String())
			}
		})
	}
	t.Run("takımsız istek — Filter.Team boş (bugünkü davranış)", func(t *testing.T) {
		st := &patternFilterLogStore{}
		w := httptest.NewRecorder()
		patternFilterServer(st).getLogs(w, httptest.NewRequest("GET", "/api/logs?search=x"+win, nil))
		if w.Code != 200 || st.got.Team != "" || st.got.TeamValues != nil {
			t.Fatalf("code=%d team=%q values=%v", w.Code, st.got.Team, st.got.TeamValues)
		}
	})
	t.Run("alan yapılandırılmamış — çözüm yapılmaz, yalnız ad (unapplied bayrağı için)", func(t *testing.T) {
		st := &patternFilterLogStore{}
		w := httptest.NewRecorder()
		patternFilterServer(st).getLogs(w, httptest.NewRequest("GET", "/api/logs?team=orders-team"+win, nil))
		if w.Code != 200 || !reflect.DeepEqual(st.got.TeamValues, []string{"orders-team"}) {
			t.Fatalf("code=%d values=%v", w.Code, st.got.TeamValues)
		}
	})
}

// Genişletme: kanonik takım → katalog + alias yazımları, filtrenin değerleri
// olarak (TeamFilterValues tavanı ve sırası korunur).
func TestLogsTeamExpansionIntoFilter(t *testing.T) {
	ta := chstore.TeamAliases{Aliases: map[string]string{"alpha-squad": "Team-Alpha"}}
	mds := map[string]chstore.ServiceMetadata{
		"orders-api":  {OwnerTeam: "Team-Alpha", OwnerTeamAuto: "team-alpha"},
		"billing-api": {SRETeam: "TEAM-ALPHA"},
		"search-api":  {OwnerTeam: "team-beta"},
	}
	f := logstore.Filter{Team: "Team-Alpha", TeamValues: teamLogLabelValues(ta, mds, "Team-Alpha")}
	got := logstore.TeamFilterValues(f)
	want := []string{"Team-Alpha", "TEAM-ALPHA", "alpha-squad", "team-alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, v := range got {
		if v == "team-beta" {
			t.Fatal("başka takımın yazımı sızdı")
		}
	}
}

// Önbellek anahtarları: takımsız anahtar bugünkü biçim (":team=" yok);
// takım ve değer kümesi anahtara girer; değer sırası anahtarı değiştirmez.
func TestLogsCacheKeys_IncludeTeam(t *testing.T) {
	base := logstore.Filter{Search: "x"}
	if k := logsSearchKey(base, "1", "2"); strings.Contains(k, "team=") {
		t.Fatalf("takımsız anahtar değişmemeli: %s", k)
	}
	if logsTeamKey(base) != "" {
		t.Fatal("takımsız → boş parça")
	}
	variants := []logstore.Filter{
		base,
		{Search: "x", Team: "orders-team", TeamValues: []string{"orders-team"}},
		{Search: "x", Team: "orders-team", TeamValues: []string{"orders-team", "ORDERS"}},
		{Search: "x", Team: "billing-team", TeamValues: []string{"billing-team"}},
	}
	keys := map[string][]string{}
	for _, f := range variants {
		keys["search"] = append(keys["search"], logsSearchKey(f, "1", "2"))
		keys["timeseries"] = append(keys["timeseries"], logsTimeseriesKey(f, "1", "2", 60, "severity"))
		keys["fieldstats"] = append(keys["fieldstats"], logsFieldStatsKey("service", f, "1", "2", 5))
		keys["tail"] = append(keys["tail"], tailFilterKey(f))
		keys["patterns"] = append(keys["patterns"], logsPatternsKey(f, "1", "2", 50, 500, false))
	}
	for name, ks := range keys {
		seen := map[string]bool{}
		for _, k := range ks {
			if seen[k] {
				t.Fatalf("%s anahtarı takımdan bağımsız: %v", name, ks)
			}
			seen[k] = true
		}
	}
	a := logstore.Filter{Team: "t", TeamValues: []string{"a", "b"}}
	b := logstore.Filter{Team: "t", TeamValues: []string{"b", "a"}}
	if logsTeamKey(a) != logsTeamKey(b) {
		t.Fatal("değer sırası anahtarı değiştirmemeli")
	}
}

func TestLogsMyTeamKey(t *testing.T) {
	k := logsMyTeamKey("u1", []string{"f1"}, "elasticsearch")
	for _, other := range []string{
		logsMyTeamKey("u2", []string{"f1"}, "elasticsearch"),
		logsMyTeamKey("u1", []string{"f2"}, "elasticsearch"),
		logsMyTeamKey("u1", []string{"f1", "f2"}, "elasticsearch"),
		logsMyTeamKey("u1", []string{"f1"}, "clickhouse"),
	} {
		if other == k {
			t.Fatalf("my-team anahtarı bir girdiden bağımsız: %s", k)
		}
	}
	if logsMyTeamKey("u1", []string{"f1"}, "elasticsearch") != k {
		t.Fatal("kararlı değil")
	}
}

func decodeMyTeam(t *testing.T, body string) logsMyTeamPayload {
	t.Helper()
	var p logsMyTeamPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return p
}

// my-team kapıları: alan yok / CH backend → available=false ve kullanıcı satırı
// OKUNMAZ (store nil: okunsaydı panik); kimliksiz → boş takım.
func TestLogsMyTeamGates(t *testing.T) {
	call := func(s *Server, claims *auth.Claims) logsMyTeamPayload {
		r := httptest.NewRequest("GET", "/api/logs/my-team", nil)
		if claims != nil {
			r = r.WithContext(auth.ContextWithClaims(context.Background(), claims))
		}
		w := httptest.NewRecorder()
		s.getLogsMyTeam(w, r)
		if w.Code != 200 {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
		return decodeMyTeam(t, w.Body.String())
	}
	me := &auth.Claims{UserID: "u1", Role: auth.RoleViewer}

	// Alan boş (ES backend olsa bile) → kapalı.
	es := &esBackendLogStore{}
	if p := call(&Server{logs: es, logsMgr: teamLogsMgr("")}, me); p.Available || p.Team != "" || p.Fields == nil {
		t.Errorf("alan boş: %+v", p)
	}
	// Alan dolu ama CH backend → kapalı (süzgeç uygulanamaz).
	if p := call(&Server{logs: stubLogStore{}, logsMgr: teamLogsMgr("labels.team")}, me); p.Available || p.Team != "" {
		t.Errorf("CH backend: %+v", p)
	}
	// Alan dolu + ES, kimliksiz → available ama takım boş.
	if p := call(&Server{logs: es, logsMgr: teamLogsMgr("labels.team")}, nil); !p.Available || p.Team != "" || len(p.Fields) != 1 {
		t.Errorf("kimliksiz: %+v", p)
	}
	// logsMgr yok → kapalı, panik yok.
	if p := call(&Server{logs: es}, me); p.Available {
		t.Errorf("logsMgr yok: %+v", p)
	}
}
