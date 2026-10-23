package api

// argocd_sync_webhook_test.go — v0.10.1445 — POST /api/argocd/sync-events:
// defter kaydı (api.go büyümez), rol kapısı (anonim 401, viewer 403),
// kayıtsız/kapalı instance 400, doğrulama 400, gövde tavanı 413, yazım +
// audit, depo hatası 500.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
