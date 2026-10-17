package api

// v0.10.1282 — AI SRE Faz 0a: sre rolü, kuyruk, drain, durum. Uçların rol
// kapıları (okuma çıplak, POST editor+, ayar admin), audit ve önbellek
// düşürme sözleşmesi; durum cevabının saf kurucusu.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

type sreFakeStore struct {
	mu       sync.Mutex
	rows     map[string]chstore.SavedView
	settings map[string][]byte
	problems map[string]chstore.Problem
}

func (f *sreFakeStore) GetSREInvestigation(_ context.Context, pid string) (*chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.rows[chstore.SREInvestigationID(pid)]
	if !ok {
		return nil, nil
	}
	return &v, nil
}
func (f *sreFakeStore) PutSREInvestigation(_ context.Context, pid, state, body string, createdAt int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[chstore.SREInvestigationID(pid)] = chstore.SavedView{ID: chstore.SREInvestigationID(pid), Page: chstore.SREInvestigationPage, Name: state, QueryString: body, CreatedAt: createdAt}
	return nil
}
func (f *sreFakeStore) ListSREInvestigations(_ context.Context, states []string, _ int) ([]chstore.SavedView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []chstore.SavedView
	for _, v := range f.rows {
		for _, s := range states {
			if v.Name == s {
				out = append(out, v)
			}
		}
	}
	return out, nil
}
func (f *sreFakeStore) CountSREQueued(ctx context.Context) (int, error) {
	rows, _ := f.ListSREInvestigations(ctx, []string{sre.StateQueued}, 0)
	return len(rows), nil
}
func (f *sreFakeStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings[key], nil
}
func (f *sreFakeStore) PutSetting(_ context.Context, key string, v []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[key] = v
	return nil
}
func (f *sreFakeStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	p, ok := f.problems[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *sreFakeStore) EnrichProblemsForRead(_ context.Context, probs []chstore.Problem, _ time.Duration) []chstore.Problem {
	return chstore.EnrichProblemsWithPriority(probs)
}

func newSRETestEnv(t *testing.T) (*Server, *http.ServeMux, *sreFakeStore) {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &sreFakeStore{rows: map[string]chstore.SavedView{}, settings: map[string][]byte{}, problems: map[string]chstore.Problem{
		"p1": {ID: "p1", Service: "pay", Status: "open", Severity: "critical", RuleID: "r", StartedAt: time.Now().Add(-time.Minute).UnixNano()},
		"db": {ID: "db", Service: "pg:main", Kind: "db", Status: "open", RuleID: "db-health:x"},
	}}
	prev := sreStoreOf
	sreStoreOf = func(*Server) sreAPIStore { return st }
	sre.SetSettings(sre.DefaultSettings())
	t.Cleanup(func() { sreStoreOf = prev; sre.SetSettings(sre.DefaultSettings()) })
	mux := http.NewServeMux()
	s.registerSRERoutes(mux)
	return s, mux, st
}

func sreDo(mux *http.ServeMux, method, path, body, role string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestSREStatusRoles(t *testing.T) {
	s, mux, st := newSRETestEnv(t)

	// Okumalar viewer'a açık.
	for _, path := range []string{"/api/sre/status", "/api/problems/p1/investigation"} {
		if w := sreDo(mux, "GET", path, "", auth.RoleViewer); w.Code != 200 {
			t.Fatalf("viewer GET %s = %d %s", path, w.Code, w.Body)
		}
	}
	var inv problemInvestigationResponse
	_ = json.Unmarshal(sreDo(mux, "GET", "/api/problems/p1/investigation", "", auth.RoleViewer).Body.Bytes(), &inv)
	if inv.State != "none" || !inv.Enabled {
		t.Fatalf("kayıt yokken state=none: %+v", inv)
	}

	// POST: viewer 403, editor 200 + audit + önbellek düşer.
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation", "", auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer POST = %d, 403 beklenir", w.Code)
	}
	if len(st.rows) != 0 {
		t.Fatal("reddedilen POST kayıt yazmamalı")
	}
	w := sreDo(mux, "POST", "/api/problems/p1/investigation", "", auth.RoleEditor)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"queued"`) {
		t.Fatalf("editor POST = %d %s", w.Code, w.Body)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "problem.investigation.enqueue" || a.TargetID != "p1" {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("POST audit yazmalı")
	}
	_ = json.Unmarshal(sreDo(mux, "GET", "/api/problems/p1/investigation", "", auth.RoleViewer).Body.Bytes(), &inv)
	if inv.State != sre.StateQueued || inv.Position != 1 || inv.Investigation == nil || inv.Investigation.Kind != sre.KindManual {
		t.Fatalf("POST sonrası GET taze kayıt + sıra görmeli: %+v", inv)
	}
	if got := inv.Investigation.RequestBy; got != "editor@example.test" {
		t.Fatalf("isteyen = %q", got)
	}
	// Kuyruktaki işe ikinci POST çift kayıt açmaz, audit yazmaz.
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation", "", auth.RoleEditor); w.Code != 200 || strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatalf("ikinci POST = %d %s", w.Code, w.Body)
	}
	if len(s.auditQ) != 0 {
		t.Fatal("no-op POST audit yazmamalı")
	}
	// Servis olmayan problem 400, olmayan 404.
	if w := sreDo(mux, "POST", "/api/problems/db/investigation", "", auth.RoleEditor); w.Code != 400 {
		t.Fatalf("db problemi = %d", w.Code)
	}
	if w := sreDo(mux, "POST", "/api/problems/nope/investigation", "", auth.RoleEditor); w.Code != 404 {
		t.Fatalf("yok = %d", w.Code)
	}

	// Durum: kuyruk 1, rol yok (kalp atışı yok, env yok) → launcher none.
	var status sreStatusResponse
	_ = json.Unmarshal(sreDo(mux, "GET", "/api/sre/status", "", auth.RoleViewer).Body.Bytes(), &status)
	if status.QueueDepth != 1 || status.RoleSeen || status.Launcher != sre.LauncherNone || !status.Enabled {
		t.Fatalf("durum: %+v", status)
	}

	// Ayar: viewer/editor 403, admin 200 + audit; kapalıyken POST 409.
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := sreDo(mux, "PUT", "/api/settings/sre", `{"enabled":false}`, role); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT = %d", role, w.Code)
		}
		if w := sreDo(mux, "GET", "/api/settings/sre", "", role); w.Code != http.StatusForbidden {
			t.Fatalf("%s GET ayar = %d", role, w.Code)
		}
	}
	if w := sreDo(mux, "PUT", "/api/settings/sre", `{"enabled":false,"autoPriorities":["P1"]}`, auth.RoleAdmin); w.Code != 200 {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "settings.sre_agent.update" {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("ayar PUT audit yazmalı")
	}
	if sre.CurrentSettings().IsEnabled() {
		t.Fatal("PUT ayarı hemen yayınlamalı")
	}
	st.problems["p2"] = chstore.Problem{ID: "p2", Service: "cart", Status: "open", RuleID: "r"}
	if w := sreDo(mux, "POST", "/api/problems/p2/investigation", "", auth.RoleEditor); w.Code != http.StatusConflict {
		t.Fatalf("kapalıyken POST = %d", w.Code)
	} else if b := w.Body.String(); !strings.Contains(b, "/settings/ai-sre") || strings.Contains(b, "/api/settings/sre") {
		// v0.10.1286 — operatörü API'ye değil ayar ekranına yollar.
		t.Fatalf("kapalı mesajı ayar ekranını göstermeli: %s", b)
	}
	_ = json.Unmarshal(sreDo(mux, "GET", "/api/sre/status", "", auth.RoleViewer).Body.Bytes(), &status)
	if status.Enabled {
		t.Fatal("durum kapalı ayarı göstermeli (PUT önbelleği düşürür)")
	}
}

func TestBuildSREStatus(t *testing.T) {
	cfg := sre.NormalizeSettings(sre.DefaultSettings())
	local := &sre.Heartbeat{Pod: "all-0", Mode: "all"}
	got := buildSREStatus(cfg, "", local, nil, 3, 1)
	if got.Launcher != sre.LauncherInProcess || !got.RoleSeen || len(got.Pods) != 1 || got.QueueDepth != 3 || got.Running != 1 {
		t.Fatalf("all modu: %+v", got)
	}
	got = buildSREStatus(cfg, "deployment", nil, nil, 2, 0)
	if got.Launcher != sre.LauncherDeployment || got.RoleSeen || got.Pods == nil {
		t.Fatalf("chart deployment, pod yok (banner koşulu): %+v", got)
	}
	got = buildSREStatus(cfg, "", nil, []sre.Heartbeat{{Pod: "sre-x", Mode: "sre"}}, 0, 0)
	if got.Launcher != sre.LauncherDeployment || !got.RoleSeen {
		t.Fatalf("kalp atışından deployment: %+v", got)
	}
}

func TestQueuePosition(t *testing.T) {
	mk := func(id, prio, kind string, at int64) chstore.SavedView {
		body, _ := sre.Encode(sre.Investigation{ProblemID: id, Service: "s", Priority: prio, Kind: kind, EnqueuedAt: at})
		return chstore.SavedView{ID: chstore.SREInvestigationID(id), Page: chstore.SREInvestigationPage, Name: sre.StateQueued, QueryString: body, CreatedAt: at}
	}
	rows := []chstore.SavedView{mk("a", "P2", "auto", 1), mk("b", "P1", "auto", 2), mk("c", "P3", "manual", 3)}
	for id, want := range map[string]int{"c": 1, "b": 2, "a": 3, "zz": 0} {
		if got := queuePosition(rows, id); got != want {
			t.Errorf("queuePosition(%s) = %d, beklenen %d", id, got, want)
		}
	}
}

func TestSRERoutesRegistered(t *testing.T) {
	src, err := os.ReadFile("sre_status.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("sre", (*Server).registerSRERoutes)`) {
		t.Fatal("sre uçları deftere kaydolmalı (kayıtsız rota 200 + boş sayfa döner)")
	}
}
