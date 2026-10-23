package api

// argocd_sync_webhook_test.go — v0.10.1445 — POST /api/argocd/sync-events:
// defter kaydı (api.go büyümez), rol kapısı (anonim 401, viewer 403),
// kayıtsız/kapalı instance 400, doğrulama 400, gövde tavanı 413, yazım +
// audit, depo hatası 500. v0.10.1466: başarılı yazım "rollouts:" önbelleğini
// birleştirilmiş süpürmeyle temizler.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

type fakeSyncWriter struct {
	rows []argocd.SyncEventRow
	err  error
}

func (f *fakeSyncWriter) ArgoCDWriteSyncEvents(_ context.Context, rows []argocd.SyncEventRow) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, rows...)
	return nil
}

const syncWebhookBody = `{"instanceId":"argo-main","app":{"namespace":"argocd","name":"shop-checkout-prod"},
"operation":{"phase":"Succeeded","startedAt":"2026-10-21T09:12:00Z","finishedAt":"2026-10-21T09:13:10Z","automated":true,"initiator":""},
"sync":{"revision":"4b7d0aa3","repoURL":"https://git.example.invalid/shop/deploy.git","targetRevision":"main"},
"history":[{"id":5,"revision":"9f3c2e1b","deployStartedAt":"2026-10-18T09:12:00Z","deployedAt":"2026-10-18T09:13:00Z"},
           {"id":6,"revision":"4b7d0aa3","deployStartedAt":"2026-10-21T09:12:00Z","deployedAt":"2026-10-21T09:13:10Z"}],
"resources":["Deployment/shop/checkout","Service/shop/checkout"],"managed":["Deployment/shop/checkout"],
"destination":{"server":"https://kubernetes.default.svc","namespace":"shop"}}`

func syncWebhookEnv(t *testing.T, w *fakeSyncWriter, known map[string]bool) (*Server, *http.ServeMux) {
	t.Helper()
	prevW, prevI := argocdSyncWriterOf, argocdSyncInstance
	argocdSyncWriterOf = func(*Server) argocdSyncWriter { return w }
	argocdSyncInstance = func(id string) (argocd.Instance, bool) {
		return argocd.Instance{ID: id, HubClusterID: "hub-a", Enabled: true}, known[id]
	}
	t.Cleanup(func() { argocdSyncWriterOf, argocdSyncInstance = prevW, prevI })
	s := &Server{auditQ: make(chan chstore.AuditEntry, 16)}
	mux := http.NewServeMux()
	s.registerArgoCDSyncWebhookRoutes(mux)
	return s, mux
}

func syncWebhookDo(mux *http.ServeMux, body, role string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/argocd/sync-events", strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestArgoSyncWebhookRegisteredViaExtra(t *testing.T) {
	src, err := os.ReadFile("argocd_sync_webhook.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stripGoComments(string(src)), `registerRoutesExtra("argocd-sync-webhook", (*Server).registerArgoCDSyncWebhookRoutes)`) {
		t.Fatal("defter kaydı yok")
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(string(b), "sync-events") {
		t.Fatal("api.go'ya rota eklenmiş")
	}
}

func TestArgoSyncWebhookAuthAndUnknownInstance(t *testing.T) {
	w := &fakeSyncWriter{}
	_, mux := syncWebhookEnv(t, w, map[string]bool{"argo-main": true})
	if c := syncWebhookDo(mux, syncWebhookBody, "").Code; c != http.StatusUnauthorized {
		t.Fatalf("anonim: %d", c)
	}
	if c := syncWebhookDo(mux, syncWebhookBody, auth.RoleViewer).Code; c != http.StatusForbidden {
		t.Fatalf("viewer: %d", c)
	}
	unk := strings.Replace(syncWebhookBody, `"argo-main"`, `"argo-other"`, 1)
	if r := syncWebhookDo(mux, unk, auth.RoleEditor); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "unknown instanceId") {
		t.Fatalf("kayıtsız instance: %d %s", r.Code, r.Body.String())
	}
	bad := strings.Replace(syncWebhookBody, `"revision":"4b7d0aa3","repoURL"`, `"revision":"","repoURL"`, 1)
	if r := syncWebhookDo(mux, bad, auth.RoleEditor); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "without sync.revision") {
		t.Fatalf("revizyonsuz Succeeded: %d %s", r.Code, r.Body.String())
	}
	if c := syncWebhookDo(mux, "{not json", auth.RoleEditor).Code; c != http.StatusBadRequest {
		t.Fatalf("bozuk JSON: %d", c)
	}
	huge := `{"instanceId":"argo-main","message":"` + strings.Repeat("x", argocd.SyncWebhookMaxBody) + `"}`
	if c := syncWebhookDo(mux, huge, auth.RoleEditor).Code; c != http.StatusRequestEntityTooLarge {
		t.Fatalf("256 KiB üstü: %d", c)
	}
	if len(w.rows) != 0 {
		t.Fatalf("reddedilen istekler yazmamalı: %d", len(w.rows))
	}
}

func TestArgoSyncWebhookWritesAndAudits(t *testing.T) {
	w := &fakeSyncWriter{}
	s, mux := syncWebhookEnv(t, w, map[string]bool{"argo-main": true})
	r := syncWebhookDo(mux, syncWebhookBody, auth.RoleEditor)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"written":2`) {
		t.Fatalf("yazım: %d %s", r.Code, r.Body.String())
	}
	// canlı + 9f3c2e1b tohumu (canlı senkronun kendi history girdisi tekrarlanmaz)
	if len(w.rows) != 2 || w.rows[0].SyncRevision != "4b7d0aa3" || w.rows[1].SyncRevision != "9f3c2e1b" || w.rows[0].ClusterID != "hub-a" {
		t.Fatalf("satırlar: %+v", w.rows)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "argocd.sync_event.create" || a.TargetKind != "argocd_app" || a.TargetID != "argo-main/argocd/shop-checkout-prod" ||
			!strings.Contains(a.Details, `"revision":"4b7d0aa3"`) {
			t.Fatalf("audit: %+v", a)
		}
	default:
		t.Fatal("audit girdisi yok")
	}
	w.err = errors.New("ch down")
	if c := syncWebhookDo(mux, syncWebhookBody, auth.RoleEditor).Code; c != http.StatusInternalServerError {
		t.Fatalf("depo hatası: %d", c)
	}
}

// v0.10.1466 — başarılı yazım "rollouts:" önbelleğini süpürür (rozet hemen);
// reddedilen / yazılamayan istek süpürmez; süpürme birleştirilir.
func TestArgoSyncWebhookSweepsRolloutsCache(t *testing.T) {
	w := &fakeSyncWriter{}
	_, mux := syncWebhookEnv(t, w, map[string]bool{"argo-main": true})
	prevInv, prevAfter, prevGate := argocdSyncInvalidate, argocdSyncAfter, argocdSyncGate
	var sweeps atomic.Int32
	var trailing []func()
	argocdSyncInvalidate = func(*Server) { sweeps.Add(1) }
	argocdSyncAfter = func(_ time.Duration, f func()) { trailing = append(trailing, f) }
	argocdSyncGate = &rolloutSweepGate{}
	t.Cleanup(func() { argocdSyncInvalidate, argocdSyncAfter, argocdSyncGate = prevInv, prevAfter, prevGate })

	bad := strings.Replace(syncWebhookBody, `"revision":"4b7d0aa3","repoURL"`, `"revision":"","repoURL"`, 1)
	if c := syncWebhookDo(mux, bad, auth.RoleEditor).Code; c != http.StatusBadRequest || sweeps.Load() != 0 {
		t.Fatalf("400 süpürmemeli: %d sweeps=%d", c, sweeps.Load())
	}
	w.err = errors.New("ch down")
	if c := syncWebhookDo(mux, syncWebhookBody, auth.RoleEditor).Code; c != http.StatusInternalServerError || sweeps.Load() != 0 {
		t.Fatalf("yazım hatası süpürmemeli: %d sweeps=%d", c, sweeps.Load())
	}
	w.err = nil
	if c := syncWebhookDo(mux, syncWebhookBody, auth.RoleEditor).Code; c != http.StatusOK || sweeps.Load() != 1 {
		t.Fatalf("ilk yazım hemen süpürmeli: %d sweeps=%d", c, sweeps.Load())
	}
	// Aralık içinde üç yazım daha → tek artçı, hemen süpürme yok.
	for i := 0; i < 3; i++ {
		syncWebhookDo(mux, syncWebhookBody, auth.RoleEditor)
	}
	if sweeps.Load() != 1 || len(trailing) != 1 {
		t.Fatalf("birleştirme: sweeps=%d artçı=%d", sweeps.Load(), len(trailing))
	}
	trailing[0]()
	if sweeps.Load() != 2 {
		t.Fatalf("artçı süpürmeli: %d", sweeps.Load())
	}
}

func TestRolloutSweepGateTable(t *testing.T) {
	g := &rolloutSweepGate{}
	t0 := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	gap := 2 * time.Second
	steps := []struct {
		at      time.Duration
		now     bool
		trailIn time.Duration
		fire    bool
	}{
		{0, true, 0, false}, // ilk: hemen
		{500 * time.Millisecond, false, 1500 * time.Millisecond, false}, // aralıkta: artçı
		{900 * time.Millisecond, false, 0, false},                       // artçı bekliyor: hiçbir şey
		{2 * time.Second, false, 0, true},                               // artçı ateşlendi (son = 2 s)
		{3 * time.Second, false, time.Second, false},                    // artçıdan 1 s sonra: yeni artçı
		{3500 * time.Millisecond, false, 0, true},                       // artçı (son = 3.5 s)
		{6 * time.Second, true, 0, false},                               // aralık geçti: hemen
	}
	for i, st := range steps {
		if st.fire {
			g.fire(t0.Add(st.at))
			continue
		}
		now, d := g.request(t0.Add(st.at), gap)
		if now != st.now || d != st.trailIn {
			t.Fatalf("adım %d: (%v,%v) beklenen (%v,%v)", i, now, d, st.now, st.trailIn)
		}
	}
}

// Eşzamanlı istekler: -race temiz, aralık içinde en çok 1 hemen + 1 artçı.
func TestRolloutSweepGateConcurrent(t *testing.T) {
	g := &rolloutSweepGate{}
	now := time.Now()
	var immediate, trailing atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, d := g.request(now, time.Minute)
			if n {
				immediate.Add(1)
			}
			if d > 0 {
				trailing.Add(1)
			}
		}()
	}
	wg.Wait()
	if immediate.Load() != 1 || trailing.Load() != 1 {
		t.Fatalf("hemen=%d artçı=%d (1/1 beklenir)", immediate.Load(), trailing.Load())
	}
}

// v0.10.1466 — çok kaynaklı uygulama (syncResult.revision boş) docs'taki
// çok kaynaklı şablon gövdesiyle (çıplak "{{...revisions}}" → "[a b]") 400
// değil yazım; kimlik sıralı birleşim (audit'te de).
func TestArgoSyncWebhookMultiSourceAccepted(t *testing.T) {
	w := &fakeSyncWriter{}
	s, mux := syncWebhookEnv(t, w, map[string]bool{"argo-main": true})
	body := strings.Replace(syncWebhookBody, `"revision":"4b7d0aa3","repoURL"`, `"revision":"","revisions":"[bbb222 aaa111]","repoURL"`, 1)
	r := syncWebhookDo(mux, body, auth.RoleEditor)
	if r.Code != http.StatusOK || len(w.rows) == 0 || w.rows[0].Identity() != "aaa111,bbb222" {
		t.Fatalf("çok kaynaklı: %d %s %+v", r.Code, r.Body.String(), w.rows)
	}
	a := <-s.auditQ
	if !strings.Contains(a.Details, `"revision":"aaa111,bbb222"`) {
		t.Fatalf("audit: %s", a.Details)
	}
}
