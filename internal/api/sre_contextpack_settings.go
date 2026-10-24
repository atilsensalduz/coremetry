package api

// sre_contextpack_settings.go — v0.10.1494 (CoSRE 2d): Context Pack bayrağı ve
// bütçesi (system_settings["sre_agent.contextpack"], sre.ContextPackSettings).
//
//   GET /api/settings/sre/contextpack → ayar (admin)
//   PUT /api/settings/sre/contextpack → (admin, audit settings.sre_agent_contextpack.update)
//
// Neden ayrı anahtar + ayrı uç: /api/settings/sre formu sre_agent blobunun
// TAMAMINI yazar — paket alanları orada olsaydı eski bir form kaydı bayrağı
// sessizce silerdi. Neden kendi dosyası: api.go büyümez (TestApiGoDoesNotGrow).
// Geri alma: PUT {"enabled":false} (≤30 s tüm pod'larda, cfgRefresh). UI PR 6.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() {
	registerRoutesExtra("sre-contextpack", (*Server).registerSREContextPackRoutes)
}

func (s *Server) registerSREContextPackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/sre/contextpack", auth.RequireRole(auth.RoleAdmin, s.getSREContextPackSettings))
	mux.HandleFunc("PUT /api/settings/sre/contextpack", auth.RequireRole(auth.RoleAdmin, s.putSREContextPackSettings))
}

func (s *Server) getSREContextPackSettings(w http.ResponseWriter, r *http.Request) {
	c, err := sre.GetContextPackSettings(r.Context(), sreStoreOf(s))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

func (s *Server) putSREContextPackSettings(w http.ResponseWriter, r *http.Request) {
	c := sre.DefaultContextPackSettings()
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if cl := auth.FromContext(r.Context()); cl != nil {
		c.UpdatedBy = cl.Email
	}
	c.UpdatedAt = time.Now().UnixNano()
	saved, err := sre.SaveContextPackSettings(r.Context(), sreStoreOf(s), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"enabled": saved.Enabled, "maxTokens": saved.MaxTokens,
		"baselines": saved.Baselines, "topAnomalies": saved.TopAnomalies})
	s.audit(r, "settings.sre_agent_contextpack.update", "settings", sre.ContextPackSettingsKey, string(details))
	s.cacheInvalidate(r.Context(), sreStatusKey)
	writeJSON(w, saved)
}
