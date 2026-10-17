package api

// sre_recommendation.go — v0.10.1293 — AI SRE Faz 4: yalnız öneri (runbook
// eşleme + geri alma metni).
//
//   POST /api/problems/{id}/investigation/recommendations/{rid}
//        {state: ack|dismissed} → editor+ ("Uygulandı" / "Ele"), audit
//        sre.recommendation.ack. Önerinin State / AckBy / AckAt'i yazılır —
//        YALNIZ ETİKET: "Uygulandı" insanın beyanıdır, bu uç hiçbir şeyi
//        uygulamaz, runbook çalıştırmaz, geri alma tetiklemez. Runbook'u insan
//        mevcut runbook sayfasından başlatır (POST /api/runbooks/{id}/execute,
//        mevcut runbook.execute audit'i, problemId bağlı).
//
// Yazım YALNIZ ajanın kendi kaydına (saved_views page='sre-investigation') —
// problems / öncelik / root_cause_hypotheses dokunulmaz
// (TestNeverWritesHypothesisTable + TestSRENeverUpsertsProblemsOrPriority bu
// dosyayı da tarar). Kuyrukta / koşan kayıt 409 (drain checkpoint'i ezerdi).
//
// Neden ayrı dosya: api.go büyümez (TestApiGoDoesNotGrow); defter kaydı.
// Neden hipotez ucuna eklenmedi: kaynak farklı (öneri ≠ hipotez kartı) ve
// durum sözlüğü farklı (ack|dismissed vs dismissed|confirmed).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() {
	registerRoutesExtra("sre-recommendation", (*Server).registerSRERecommendationRoutes)
}

func (s *Server) registerSRERecommendationRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/problems/{id}/investigation/recommendations/{rid}",
		auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.postSRERecommendationAck)))
}

func (s *Server) postSRERecommendationAck(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	rid := strings.TrimSpace(r.PathValue("rid"))
	var body struct {
		State string `json:"state"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if id == "" || rid == "" || len(rid) > 120 {
		writeJSONError(w, http.StatusBadRequest, "problem ve öneri kimliği zorunlu")
		return
	}
	if body.State != sre.RecAck && body.State != sre.RecDismissed {
		writeJSONError(w, http.StatusBadRequest, "state yalnız ack | dismissed")
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = firstNonEmpty(c.Email, c.UserID)
	}
	rec, err := sre.ApplyRecommendationAck(r.Context(), sreStoreOf(s), id, sre.RecAckRequest{ID: rid, State: body.State, By: by})
	switch {
	case errors.Is(err, sre.ErrMarkInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, sre.ErrMarkNotFound):
		writeJSONError(w, http.StatusNotFound, "araştırma ya da öneri bulunamadı")
		return
	case errors.Is(err, sre.ErrMarkBusy):
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	d := map[string]any{"recommendation": rid, "state": body.State, "hypothesisKind": rec.HypothesisKind}
	if rec.Runbook != nil {
		d["runbookId"] = rec.Runbook.ID
	}
	if rec.Rollout != nil {
		d["rollout"] = rec.Rollout.Workload
	}
	details, _ := json.Marshal(d)
	s.audit(r, "sre.recommendation.ack", "problem", id, string(details))
	s.PublishSRE(sre.EventKind, map[string]any{"problemId": id, "state": "recommendation"})
	writeJSON(w, rec)
}
