package api

// v0.10.1494 (CoSRE 2d) — Context Pack ayar ucu: admin kapısı (viewer/editor
// 403), normalize, audit, sıcak yol yayını, ayrı anahtar (sre_agent blobuna
// dokunmaz).

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func TestSREContextPackSettingsRoute(t *testing.T) {
	s, _, st := newSRETestEnv(t)
	mux := http.NewServeMux()
	s.registerSREContextPackRoutes(mux)
	t.Cleanup(func() { sre.SetContextPackSettings(sre.DefaultContextPackSettings()) })

	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := sreDo(mux, "PUT", "/api/settings/sre/contextpack", `{"enabled":true}`, role); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT = %d, 403 beklenir", role, w.Code)
		}
		if w := sreDo(mux, "GET", "/api/settings/sre/contextpack", "", role); w.Code != http.StatusForbidden {
			t.Fatalf("%s GET = %d, 403 beklenir", role, w.Code)
		}
	}
	if sre.CurrentContextPackSettings().Enabled {
		t.Fatal("varsayılan kapalı")
	}
	w := sreDo(mux, "PUT", "/api/settings/sre/contextpack", `{"enabled":true,"maxTokens":99999,"baselines":["week_before","bogus","week_before"],"topAnomalies":0}`, auth.RoleAdmin)
	if w.Code != 200 {
		t.Fatalf("admin PUT = %d %s", w.Code, w.Body)
	}
	var got sre.ContextPackSettings
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Enabled || got.MaxTokens != 8000 || len(got.Baselines) != 1 || got.Baselines[0] != "week_before" || got.TopAnomalies != 5 || got.UpdatedBy != "admin@example.test" {
		t.Fatalf("normalize: %+v", got)
	}
	if !sre.CurrentContextPackSettings().Enabled {
		t.Fatal("PUT sıcak yola yayınlamalı")
	}
	if _, ok := st.settings[sre.ContextPackSettingsKey]; !ok || st.settings[sre.SettingsKey] != nil {
		t.Fatalf("yalnız kendi anahtarı yazılır: %v", st.settings)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "settings.sre_agent_contextpack.update" {
			t.Fatalf("audit: %+v", a)
		}
	default:
		t.Fatal("audit yazılmadı")
	}
	if w := sreDo(mux, "PUT", "/api/settings/sre/contextpack", `{`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk JSON = %d", w.Code)
	}
	if w := sreDo(mux, "GET", "/api/settings/sre/contextpack", "", auth.RoleAdmin); w.Code != 200 {
		t.Fatalf("admin GET = %d", w.Code)
	}
}
