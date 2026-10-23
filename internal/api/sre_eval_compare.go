package api

// sre_eval_compare.go — v0.10.1450 — AI SRE program 2a: iki değerlendirme
// koşusunun karşılaştırması.
//
//   GET /api/sre/eval/compare?base=<runId>&cand=<runId> → viewer görür
//       (koşu ölçüleri gibi; satırlar LLM / istem / e-posta taşımaz).
//       200 sre.EvalCompare · 400 geçersiz kimlik · 404 koşulardan biri yok ·
//       409 vaka kümesi farklı (karşılaştırma yalnız aynı dondurulmuş kümenin
//       — rerunOf — koşuları arasında anlamlı).
//
// Hesap SAF sre.CompareEvalRuns'ta; burada yalnız okuma + hata eşlemesi. Neden
// ayrı dosya: yeni uç = yeni dosya (TestApiGoDoesNotGrow; sre_eval.go'yu da
// büyütmez). Neden serveCached DEĞİL: 409'u writeErr eşleyemez — aynı
// çekirdek (cachedJSON, 15 s) doğrudan çağrılır. Anahtar iki kimliği taşır;
// koşu ilerledikçe düşürülmez, 15 s TTL ile tazelenir (sürüyor iken FE'nin
// koşu yoklaması zaten 15 s). Neden requireCopilot ARKASINDA DEĞİL: LLM yok.
// MCP aracı yok (74 sabit).

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre-eval-compare", (*Server).registerSREEvalCompareRoutes) }

func (s *Server) registerSREEvalCompareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sre/eval/compare", s.getSREEvalCompare)
}

func sreEvalCompareKey(base, cand string) string { return "sre:eval:compare:" + base + ":" + cand }

func (s *Server) getSREEvalCompare(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSpace(r.URL.Query().Get("base"))
	cand := strings.TrimSpace(r.URL.Query().Get("cand"))
	if !sre.ValidEvalRunID(base) || !sre.ValidEvalRunID(cand) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz koşu kimliği (base, cand)")
		return
	}
	tag := chstore.RouteQueryTag(r.Pattern, r.URL.Path)
	body, tier, err := s.cachedJSON(r.Context(), sreEvalCompareKey(base, cand), sreEvalTTL, r.URL.Query().Get("refresh") == "1",
		func(ctx context.Context) (any, error) {
			ctx = chstore.WithQueryTag(ctx, tag)
			st := sreEvalStoreOf(s)
			now := time.Now()
			b, ok, err := sre.LoadEvalRun(ctx, st, base, now)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, notFoundf("taban koşusu %s", base)
			}
			c, ok, err := sre.LoadEvalRun(ctx, st, cand, now)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, notFoundf("aday koşusu %s", cand)
			}
			return sre.CompareEvalRuns(b, c)
		})
	if errors.Is(err, sre.ErrEvalCompareMismatch) {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	writeCacheHit(w, tier, body)
}
