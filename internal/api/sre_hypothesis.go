package api

// sre_hypothesis.go — v0.10.1288 (AI SRE Faz 2: hipotez kartları + doğrula/ele).
//
//   POST /api/problems/{id}/investigation/hypotheses/{kind}
//        {state: dismissed|confirmed, note?} → editor+ ("Ele" / "Onayla"),
//        audit sre.hypothesis.mark. Kartın HumanState'i yazılır; skor, bant ve
//        kontroller DEĞİŞMEZ (Score saf ve yalnız telemetri gerçeklerini okur —
//        işaret skora hiçbir yoldan girmez). "Doğrula" ayrı uç değil:
//        POST /api/problems/{id}/investigation {kind:"verify", hypothesis}
//        (sre_status.go).
//
// Yazım YALNIZ ajanın kendi kaydına (saved_views page='sre-investigation') —
// root_cause_hypotheses / problems / öncelik dokunulmaz (sentezleyici tek
// yazıcı, /aiops §6; TestNeverWritesHypothesisTable bu dosyayı da tarar).
// Kuyrukta / koşan kayıt 409: drain'in checkpoint'i işareti ezerdi.
//
// Neden ayrı dosya: api.go büyümez (TestApiGoDoesNotGrow); defter kaydı.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre-hypothesis", (*Server).registerSREHypothesisRoutes) }

func (s *Server) registerSREHypothesisRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/problems/{id}/investigation/hypotheses/{kind}",
		auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.postSREHypothesisMark)))
}

func (s *Server) postSREHypothesisMark(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	kind := strings.TrimSpace(r.PathValue("kind"))
	var body struct {
		State string `json:"state"`
		Note  string `json:"note"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if id == "" || !sre.IsHypothesisKind(kind) {
		writeJSONError(w, http.StatusBadRequest, "problem kimliği ve bilinen hipotez türü zorunlu")
		return
	}
	if body.State != sre.MarkDismissed && body.State != sre.MarkConfirmed {
		writeJSONError(w, http.StatusBadRequest, "state yalnız dismissed | confirmed")
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = firstNonEmpty(c.Email, c.UserID)
	}
	h, err := sre.ApplyHypothesisMark(r.Context(), sreStoreOf(s), id, sre.MarkRequest{Kind: kind, State: body.State, Note: body.Note, By: by})
	switch {
	case errors.Is(err, sre.ErrMarkInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, sre.ErrMarkNotFound):
		writeJSONError(w, http.StatusNotFound, "araştırma ya da hipotez bulunamadı")
		return
	case errors.Is(err, sre.ErrMarkBusy):
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"kind": kind, "state": body.State, "score": h.Score, "band": h.Band, "noted": strings.TrimSpace(body.Note) != ""})
	s.audit(r, "sre.hypothesis.mark", "problem", id, string(details))
	s.PublishSRE(sre.EventKind, map[string]any{"problemId": id, "state": "marked"})
	writeJSON(w, h)
}
