package api

// sre_eval.go — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü.
//
//   POST /api/sre/eval/runs          {from, to (unix ms), limit ≤50} → admin,
//        audit sre.eval.run. Onaylı problem vakaları için Kind=replay kuyruk
//        işleri (sre.StartEvalRun); 409 = ajan kapalı / koşu sürüyor.
//   GET  /api/sre/eval/runs          → son 50 koşu + ölçüler + canlı ilk
//        hipoteze varış p50/p90 (viewer görür; serveCached 15 s).
//   GET  /api/sre/eval/runs/{id}     → koşu + vaka tablosu + ölçüler (viewer).
//   GET  /api/sre/eval/review?days=  → sre-* yüzeyli AI çağrılarına verilmiş
//        👎'ler — "incelemeye değer" ADAY listesi, admin (prompt / cevap örneği
//        + e-posta taşır; /api/ai/feedback/negative ile aynı kapı).
//
// Doğruluk etiketi YALNIZ onaylı incident_cases (operatör: "oylara çok
// güvenmeyelim"). 👎 listesi bu dosyada AYRI bir okumadır: koşu ölçüsüne
// girmez, sre paketine geçmez (sre.ScoreEvalRun yalnız vakaları + canlı
// örneklemi alır; cosrelearn TestVotesNeverReachRuntimeDecisions bu dosyayı
// inceleme yüzeyi olarak listeler).
//
// Neden ayrı dosya: api.go büyümez (TestApiGoDoesNotGrow); defter kaydı. Neden
// requireCopilot ARKASINDA DEĞİL: tekrar LLM çağırmaz (anlatım atlanır),
// ölçüm AI kapalıyken de alınabilir. Önbellek anahtarları sabit / tek girdili;
// drain her tekrar yazımında PublishSRE ile düşürür (evalRun alanı).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre-eval", (*Server).registerSREEvalRoutes) }

func (s *Server) registerSREEvalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sre/eval/runs", s.listSREEvalRuns)
	mux.HandleFunc("GET /api/sre/eval/runs/{id}", s.getSREEvalRun)
	mux.Handle("POST /api/sre/eval/runs",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.postSREEvalRun)))
	mux.Handle("GET /api/sre/eval/review",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getSREEvalReview)))
}

const (
	sreEvalTTL       = 15 * time.Second
	sreEvalReviewTTL = 60 * time.Second
	sreEvalRunsKey   = "sre:eval:runs"
	sreEvalRunsLimit = 50
)

func sreEvalRunKey(id string) string { return "sre:eval:run:" + id }

// sreEvalReviewDays — SAF: ?days= bir basamağa oturur (önbellek anahtarı sınırlı).
func sreEvalReviewDays(raw string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(raw))
	for _, d := range []int{1, 7, 30, 90} {
		if n <= d {
			return d
		}
	}
	return 90
}

func sreEvalReviewKey(days int) string { return "sre:eval:review:d=" + strconv.Itoa(days) }

// sreEvalReviewStore — 👎 inceleme okuması (*chstore.Store karşılar).
type sreEvalReviewStore interface {
	ListSREReviewDownvotes(ctx context.Context, from, to time.Time, limit int) ([]chstore.SREReviewDownvote, error)
}

var (
	sreEvalStoreOf       = func(s *Server) sre.EvalStore { return s.store }
	sreEvalReviewStoreOf = func(s *Server) sreEvalReviewStore { return s.store }
)

// sreEvalRunItem — FE: SREEvalRunItem (liste satırında vakalar YOK).
type sreEvalRunItem struct {
	sre.EvalRun
	Score sre.EvalScore `json:"score"`
}

// sreEvalListResponse — FE: SREEvalRunsResponse.
type sreEvalListResponse struct {
	Runs []sreEvalRunItem `json:"runs"`
	Live sre.EvalLive     `json:"live"`
	// Categories — hipotez türü → incident_cases kategorisi; Unmapped —
	// eşlenmeyen kategoriler + nedeni (isabet paydasına girmez).
	Categories map[string]string `json:"categories"`
	Unmapped   map[string]string `json:"unmapped"`
	Enabled    bool              `json:"enabled"`
}

func evalUnmappedMap() map[string]string {
	out := map[string]string{}
	for _, c := range []string{"infra", "data", "noise", "other"} {
		if r := sre.EvalUnmappedReason(c); r != "" {
			out[c] = r
		}
	}
	return out
}

func (s *Server) listSREEvalRuns(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, sreEvalRunsKey, sreEvalTTL, func(ctx context.Context) (any, error) {
		st := sreEvalStoreOf(s)
		now := time.Now()
		runs, err := sre.ListEvalRuns(ctx, st, sreEvalRunsLimit, now)
		if err != nil {
			return nil, err
		}
		live, err := sre.LoadLiveSamples(ctx, st)
		if err != nil {
			return nil, err
		}
		out := sreEvalListResponse{Runs: make([]sreEvalRunItem, 0, len(runs)), Categories: sre.EvalCategoryMap(),
			Unmapped: evalUnmappedMap(), Enabled: sre.CurrentSettings().IsEnabled()}
		for _, run := range runs {
			sc := sre.ScoreEvalRun(run, live)
			run.Cases = nil
			out.Runs = append(out.Runs, sreEvalRunItem{EvalRun: run, Score: sc})
		}
		out.Live = sre.ScoreEvalRun(sre.EvalRun{}, live).Live
		return out, nil
	})
}

func (s *Server) getSREEvalRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > 40 || strings.ContainsAny(id, "-/ ") {
		writeJSONError(w, http.StatusBadRequest, "geçersiz koşu kimliği")
		return
	}
	s.serveCached(w, r, sreEvalRunKey(id), sreEvalTTL, func(ctx context.Context) (any, error) {
		st := sreEvalStoreOf(s)
		run, ok, err := sre.LoadEvalRun(ctx, st, id, time.Now())
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNotFound
		}
		live, err := sre.LoadLiveSamples(ctx, st)
		if err != nil {
			return nil, err
		}
		if run.Cases == nil {
			run.Cases = []sre.EvalCase{}
		}
		return sreEvalRunItem{EvalRun: run, Score: sre.ScoreEvalRun(run, live)}, nil
	})
}

func (s *Server) postSREEvalRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From  int64 `json:"from"` // unix ms
		To    int64 `json:"to"`   // unix ms
		Limit int   `json:"limit"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Limit < 0 || in.Limit > sre.EvalMaxCases {
		writeJSONError(w, http.StatusBadRequest, "limit 1–50 olmalı")
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
	}
	run, err := sre.StartEvalRun(r.Context(), sreEvalStoreOf(s), sre.EvalRunRequest{
		From: time.UnixMilli(in.From), To: time.UnixMilli(in.To), Limit: in.Limit, By: by,
	}, sre.CurrentSettings())
	switch {
	case errors.Is(err, sre.ErrDisabled):
		writeJSONError(w, http.StatusConflict, "AI SRE kapalı — yönetici Ayarlar → AI SRE (/settings/ai-sre) bölümünden açabilir")
		return
	case errors.Is(err, sre.ErrEvalBusy):
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, sre.ErrEvalInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	queued := 0
	for _, c := range run.Cases {
		if c.State == sre.StateQueued {
			queued++
		}
	}
	details, _ := json.Marshal(map[string]any{"runId": run.RunID, "from": in.From, "to": in.To, "limit": run.Limit,
		"found": run.Found, "queued": queued})
	s.audit(r, "sre.eval.run", "sre-eval", run.RunID, string(details))
	s.cacheInvalidate(r.Context(), sreEvalRunsKey)
	s.cacheInvalidate(r.Context(), sreStatusKey)
	if s.bus != nil && queued > 0 {
		s.bus.Publish(sre.EventKind, map[string]any{"evalRun": run.RunID, "state": run.State})
	}
	writeJSON(w, sreEvalRunItem{EvalRun: run, Score: sre.ScoreEvalRun(run, nil)})
}

// sreEvalReviewResponse — FE: SREEvalReviewResponse.
type sreEvalReviewResponse struct {
	Days  int                         `json:"days"`
	Items []chstore.SREReviewDownvote `json:"items"`
	// Note — ekranda değişmez beyan: aday, etiket değil.
	Note string `json:"note"`
}

const sreEvalReviewNote = "incelemeye değer — 👎 etiket DEĞİL, değerlendirme puanına girmez (doğruluk yalnız onaylı vakalardan)"

func (s *Server) getSREEvalReview(w http.ResponseWriter, r *http.Request) {
	days := sreEvalReviewDays(r.URL.Query().Get("days"))
	s.serveCached(w, r, sreEvalReviewKey(days), sreEvalReviewTTL, func(ctx context.Context) (any, error) {
		to := time.Now()
		items, err := sreEvalReviewStoreOf(s).ListSREReviewDownvotes(ctx, to.Add(-time.Duration(days)*24*time.Hour), to, chstore.SREReviewDownvotesMax)
		if err != nil {
			return nil, err
		}
		if items == nil {
			items = []chstore.SREReviewDownvote{}
		}
		return sreEvalReviewResponse{Days: days, Items: items, Note: sreEvalReviewNote}, nil
	})
}
