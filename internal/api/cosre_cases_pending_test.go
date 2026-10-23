package api

// v0.10.1475 — /ai "Onay bekleyen vakalar": parametre sınırları, cache
// anahtarı, sayaç (bağımsız = öneri aynen kabul EDİLMEDİ), rol kapıları ve
// listeden onayın Problem kartıyla AYNI ucu (doğrulama + audit) kullanması.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestParsePendingCasesParams(t *testing.T) {
	p, err := parsePendingCasesParams("", "", "", "")
	if err != nil || p.Source != "problem" || p.Days != pendingCasesDefaultDays || p.Limit != pendingCasesDefaultLim || p.Offset != 0 {
		t.Fatalf("varsayılan: %+v %v", p, err)
	}
	if p, _ := parsePendingCasesParams("all", "30", "50", "10"); p.Source != "" || p.Days != 30 || p.Offset != 50 || p.Limit != 10 {
		t.Errorf("all: %+v", p)
	}
	// Basamak dışı gün / crafted limit / derin offset → kırpılır.
	if p, _ := parsePendingCasesParams("exception", "31", "999999", "100000"); p.Source != "exception" || p.Days != pendingCasesDefaultDays ||
		p.Offset != chstore.PendingCaseOffsetMax || p.Limit != pendingCasesDefaultLim {
		t.Errorf("kırpma: %+v", p)
	}
	if _, err := parsePendingCasesParams("drop table", "", "", ""); err == nil {
		t.Error("bilinmeyen kaynak kabul edildi")
	}
}

func TestPendingCasesKeyHashesAllInputs(t *testing.T) {
	base := pendingCasesParams{Source: "problem", Days: 90, Offset: 0, Limit: 25}
	seen := map[string]bool{pendingCasesKey(base): true}
	for _, v := range []pendingCasesParams{
		{Source: "", Days: 90, Limit: 25}, {Source: "exception", Days: 90, Limit: 25},
		{Source: "problem", Days: 30, Limit: 25}, {Source: "problem", Days: 90, Offset: 25, Limit: 25},
		{Source: "problem", Days: 90, Limit: 50},
	} {
		k := pendingCasesKey(v)
		if seen[k] {
			t.Errorf("anahtar çakıştı: %s", k)
		}
		seen[k] = true
	}
	same := base
	if pendingCasesKey(base) != pendingCasesKey(same) {
		t.Error("kararsız anahtar")
	}
}

func TestTallyCaseCounts(t *testing.T) {
	rows := []chstore.IncidentCaseCount{
		{Status: "draft", Source: "problem", N: 7}, {Status: "draft", Source: "exception", N: 3},
		{Status: "confirmed", Source: "problem", N: 2}, {Status: "confirmed", Source: "exception", N: 1},
	}
	confirmed := []chstore.IncidentCase{
		{SuggestedCause: "DB havuzu doldu", FinalCause: "db  havuzu doldu"},                // aynen kabul → bağımsız DEĞİL
		{SuggestedCause: "deploy 2.3", FinalCause: "orders-api bağlantı havuzu sızıntısı"}, // düzeltildi → bağımsız
	}
	c := tallyCaseCounts(rows, confirmed)
	if c.DraftProblem != 7 || c.DraftException != 3 || c.ConfirmedProblem != 2 || c.ConfirmedException != 1 || c.IndependentProblem != 1 || c.IndependentCapped {
		t.Fatalf("sayaç: %+v", c)
	}
	// Öneri yoksa (boş) her onay bağımsızdır — eval'in AcceptedSuggestion'ı da yanlış.
	if c := tallyCaseCounts(nil, []chstore.IncidentCase{{FinalCause: "x"}}); c.IndependentProblem != 1 {
		t.Errorf("öneri yok: %+v", c)
	}
}

// memCaseStore — incidentCaseStore'un bellek-içi sahtesi.
type memCaseStore struct {
	mu      sync.Mutex
	cases   map[string]chstore.IncidentCase
	upserts int
	lastQ   chstore.PendingCaseQuery
}

func (m *memCaseStore) GetIncidentCase(_ context.Context, id string) (*chstore.IncidentCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.cases[id]
	if !ok {
		return nil, nil
	}
	return &c, nil
}

func (m *memCaseStore) UpsertIncidentCases(_ context.Context, cs []chstore.IncidentCase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range cs {
		m.cases[c.CaseID] = c
		m.upserts++
	}
	return nil
}

func (m *memCaseStore) sorted(status, source string) []chstore.IncidentCase {
	out := []chstore.IncidentCase{}
	for _, c := range m.cases {
		if c.Status == status && (source == "" || c.Source == source) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenedAt.After(out[j].OpenedAt) })
	return out
}

func (m *memCaseStore) ListIncidentCases(_ context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sorted(q.Status, q.Source), nil
}

func (m *memCaseStore) ListPendingIncidentCases(_ context.Context, q chstore.PendingCaseQuery) ([]chstore.IncidentCase, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastQ = q
	all := m.sorted(chstore.CaseDraft, q.Source)
	if q.Offset >= len(all) {
		return []chstore.IncidentCase{}, nil
	}
	all = all[q.Offset:]
	if len(all) > q.Limit {
		all = all[:q.Limit]
	}
	return all, nil
}

func (m *memCaseStore) CountIncidentCases(context.Context, time.Time) ([]chstore.IncidentCaseCount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := map[[2]string]uint64{}
	for _, c := range m.cases {
		n[[2]string{c.Status, c.Source}]++
	}
	out := []chstore.IncidentCaseCount{}
	for k, v := range n {
		out = append(out, chstore.IncidentCaseCount{Status: k[0], Source: k[1], N: v})
	}
	return out, nil
}

type caseHarness struct {
	s     *Server
	mux   *http.ServeMux
	store *memCaseStore
}

func newCaseHarness(t *testing.T) *caseHarness {
	t.Helper()
	now := time.Now()
	st := &memCaseStore{cases: map[string]chstore.IncidentCase{}}
	for i, c := range []chstore.IncidentCase{
		{CaseID: "p:p1", Source: "problem", SourceID: "p1", Service: "orders-api", Title: "error rate", SuggestedCause: "deploy 2.3", Provenance: "cosre"},
		{CaseID: "p:p2", Source: "problem", SourceID: "p2", Service: "kxzone", Title: "p99", SuggestedCause: "DB havuzu doldu", Provenance: "cosre"},
		{CaseID: "ex:f1", Source: "exception", SourceID: "f1", Service: "orders-api", Title: "NPE", Provenance: "cosre"},
	} {
		c.Status = chstore.CaseDraft
		c.OpenedAt = now.Add(-time.Duration(i+1) * time.Hour)
		c.ClosedAt = c.OpenedAt.Add(30 * time.Minute)
		st.cases[c.CaseID] = c
	}
	prev := caseStoreFor
	caseStoreFor = func(*Server) incidentCaseStore { return st }
	t.Cleanup(func() { caseStoreFor = prev })
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	mux := http.NewServeMux()
	s.registerCosreCaseRoutes(mux)
	s.registerPendingCaseRoutes(mux)
	return &caseHarness{s: s, mux: mux, store: st}
}

func (h *caseHarness) do(t *testing.T, method, target, body, role string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	w := httptest.NewRecorder()
	h.mux.ServeHTTP(w, r)
	return w
}

func decodePending(t *testing.T, w *httptest.ResponseRecorder) pendingCasesResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("GET %d: %s", w.Code, w.Body.String())
	}
	var out pendingCasesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPendingCasesListAndRoleGates(t *testing.T) {
	h := newCaseHarness(t)
	// Viewer listeyi GÖRÜR (salt-okunur); varsayılan kaynak problem.
	out := decodePending(t, h.do(t, http.MethodGet, "/api/cases/pending", "", auth.RoleViewer))
	if len(out.Items) != 2 || out.Items[0].CaseID != "p:p1" || out.Total != 2 || out.EvalMinConfirmed != 20 || len(out.Categories) != 10 {
		t.Fatalf("liste: %+v", out)
	}
	if out.Counts.DraftProblem != 2 || out.Counts.DraftException != 1 || out.Counts.ConfirmedProblem != 0 {
		t.Errorf("sayaç: %+v", out.Counts)
	}
	all := decodePending(t, h.do(t, http.MethodGet, "/api/cases/pending?source=all&limit=2&offset=2", "", auth.RoleViewer))
	if len(all.Items) != 1 || all.Total != 3 || h.store.lastQ.Limit != 2 || h.store.lastQ.Offset != 2 || h.store.lastQ.Source != "" {
		t.Errorf("sayfa: %+v q=%+v", all, h.store.lastQ)
	}
	if w := h.do(t, http.MethodGet, "/api/cases/pending?source=x", "", auth.RoleViewer); w.Code != http.StatusBadRequest {
		t.Errorf("geçersiz kaynak: %d", w.Code)
	}
	// Viewer onaylayamaz; hiçbir yazım olmaz.
	if w := h.do(t, http.MethodPost, "/api/cases/p:p1/confirm", `{"finalCause":"x","category":"deploy"}`, auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer onay: %d", w.Code)
	}
	if h.store.upserts != 0 || len(h.s.auditQ) != 0 {
		t.Fatal("viewer onayı yazmamalı")
	}
	// Liste GET'i hiçbir şey yazmaz (otomatik onay yok).
	if h.store.cases["p:p1"].Status != chstore.CaseDraft {
		t.Fatal("liste okuması durumu değiştirdi")
	}
}

func TestPendingCaseConfirmUsesSameHandler(t *testing.T) {
	h := newCaseHarness(t)
	_ = decodePending(t, h.do(t, http.MethodGet, "/api/cases/pending", "", auth.RoleEditor)) // cache'i ısıt
	// Düzeltilen öneri: editor nihai nedeni değiştirir.
	w := h.do(t, http.MethodPost, "/api/cases/p:p1/confirm",
		`{"finalCause":"orders-api bağlantı havuzu sızıntısı","category":"code_bug","resolution":"pool fix"}`, auth.RoleEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("onay %d: %s", w.Code, w.Body.String())
	}
	got := h.store.cases["p:p1"]
	if got.Status != chstore.CaseConfirmed || got.ConfirmedBy != "editor@example.test" || got.Category != "code_bug" || got.CosreCorrect != -1 {
		t.Fatalf("onaylı satır: %+v", got)
	}
	select {
	case e := <-h.s.auditQ:
		if e.Action != "incident_case.confirm" || e.TargetID != "p:p1" || e.ActorEmail != "editor@example.test" {
			t.Errorf("audit: %+v", e)
		}
	default:
		t.Fatal("audit satırı yok")
	}
	// Aynen kabul: öneri değiştirilmeden → cosre_correct=1, bağımsız sayılmaz.
	if w := h.do(t, http.MethodPost, "/api/cases/p:p2/confirm", `{"finalCause":"DB havuzu doldu","category":"capacity"}`, auth.RoleAdmin); w.Code != http.StatusOK {
		t.Fatalf("onay 2: %d", w.Code)
	}
	if h.store.cases["p:p2"].CosreCorrect != 1 {
		t.Errorf("aynen kabul cosre_correct=1 olmalı: %+v", h.store.cases["p:p2"])
	}
	// Doğrulama aynı: kapalı kategori dışı reddedilir.
	if w := h.do(t, http.MethodPost, "/api/cases/ex:f1/confirm", `{"finalCause":"x","category":"bilinmeyen"}`, auth.RoleEditor); w.Code != http.StatusBadRequest {
		t.Errorf("kategori doğrulaması: %d", w.Code)
	}
	// Onay cache önekini düşürdü: liste taze, sayaç güncel.
	out := decodePending(t, h.do(t, http.MethodGet, "/api/cases/pending", "", auth.RoleViewer))
	if len(out.Items) != 0 || out.Counts.ConfirmedProblem != 2 || out.Counts.IndependentProblem != 1 || out.Counts.DraftProblem != 0 {
		t.Fatalf("onay sonrası liste: %+v", out)
	}
}
