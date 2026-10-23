package api

// cosre_cases.go — CoSRE öğrenme Faz 3: vaka hafızasının insan yüzü.
//
//   GET  /api/cases/lookup?source=problem|exception&id=<id> — Problem /
//        exception sayfasının vaka kartı (taslak ya da onaylı; yoksa null).
//   POST /api/cases/{caseId}/confirm — editor+: tek "Onayla" düğmesi,
//        form CoSRE önerisiyle önceden dolu. Nihai neden + kategori +
//        çözüm, confirmed_by/at yazılır, audit satırı düşer. cosre_correct:
//        nihai metin öneriyle aynıysa 1 (insan onayladı); farklıysa
//        self-hosted model karşılaştırır; model yoksa -1 (bilinmiyor).
//
// Taslak (cosrelearn.BuildCases) hiçbir yüzeyde olgu diye sunulmaz:
// similar_incidents ve çekmece kanıtı onaysızı "ONAYSIZ" işaretler.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

const (
	caseFieldMax       = 2000
	pastCasesLookback  = 365 * 24 * time.Hour
	pastCasesInEvident = 3
)

func init() { registerRoutesExtra("cosre-cases", (*Server).registerCosreCaseRoutes) }

func (s *Server) registerCosreCaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cases/lookup", s.getIncidentCase)
	mux.HandleFunc("POST /api/cases/{caseId}/confirm", auth.RequireAnyRole(editorRoles, s.confirmIncidentCase))
}

func (s *Server) getIncidentCase(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if (source != "problem" && source != "exception") || id == "" {
		http.Error(w, "source=problem|exception and id required", http.StatusBadRequest)
		return
	}
	c, err := s.store.GetIncidentCase(r.Context(), chstore.CaseIDFor(source, id))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"case": c, "categories": cosrelearn.CaseCategories})
}

type caseConfirmRequest struct {
	FinalCause string `json:"finalCause"`
	Category   string `json:"category"`
	Resolution string `json:"resolution"`
}

// validateCaseConfirm — SAF: zorunlu neden, kapalı kategori, alan tavanı.
func validateCaseConfirm(req caseConfirmRequest) (caseConfirmRequest, error) {
	req.FinalCause = strings.TrimSpace(req.FinalCause)
	req.Resolution = strings.TrimSpace(req.Resolution)
	req.Category = strings.TrimSpace(req.Category)
	switch {
	case req.FinalCause == "":
		return req, errors.New("finalCause required")
	case !slices.Contains(cosrelearn.CaseCategories, req.Category):
		return req, fmt.Errorf("unknown category %q", req.Category)
	case len(req.FinalCause) > caseFieldMax || len(req.Resolution) > caseFieldMax:
		return req, errors.New("field too long")
	}
	return req, nil
}

func (s *Server) confirmIncidentCase(w http.ResponseWriter, r *http.Request) {
	caseID := r.PathValue("caseId")
	var req caseConfirmRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req, err := validateCaseConfirm(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := s.store.GetIncidentCase(r.Context(), caseID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if c == nil {
		http.Error(w, "case not found", http.StatusNotFound)
		return
	}
	who := ""
	if cl := auth.FromContext(r.Context()); cl != nil {
		who = cl.Email
		if who == "" {
			who = cl.UserID
		}
	}
	c.FinalCause, c.Category, c.Resolution = req.FinalCause, req.Category, req.Resolution
	c.Status, c.ConfirmedBy, c.ConfirmedAt = chstore.CaseConfirmed, who, time.Now()
	c.Provenance = "confirmed by " + who + " (" + c.Provenance + ")"
	c.CosreCorrect, c.CorrectNote = s.judgeSuggestion(r.Context(), c.SuggestedCause, c.FinalCause)
	if err := s.store.UpsertIncidentCases(r.Context(), []chstore.IncidentCase{*c}); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "incident_case.confirm", "incident_case", c.CaseID,
		fmt.Sprintf("category=%s cosre_correct=%d", c.Category, c.CosreCorrect))
	writeJSON(w, map[string]any{"case": c})
}

// judgeSuggestion — CoSRE'nin önerisi nihai nedenle örtüşüyor mu.
func (s *Server) judgeSuggestion(ctx context.Context, suggested, final string) (int8, string) {
	switch {
	case strings.TrimSpace(suggested) == "":
		return -1, "öneri yoktu"
	case chstore.SameCause(suggested, final):
		return 1, "onaylayan öneriyi değiştirmeden kabul etti"
	case s.copilot == nil:
		return -1, "model yok — karşılaştırılamadı"
	}
	// Etkin değilse çağrı hata döner → -1 (aşağıda); handler-içi kapı YOK
	// (TestNoInlineCopilotGates): onay modelsiz de yazılır.
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := s.copilotExplainSurface(cctx, "cosre-case-compare", copilot.SystemPromptCaseCompare(),
		"ÖNERİLEN NEDEN:\n"+suggested+"\n\nNİHAİ NEDEN (insan onaylı):\n"+final)
	if err != nil {
		return -1, "karşılaştırma başarısız: " + clipBytes(err.Error(), 200)
	}
	return parseCaseVerdict(out)
}

// parseCaseVerdict — SAF: modelin ilk kelimesi → 1 / 0 / -1 (kısmen ve
// anlaşılmayan cevap "bilinmiyor"; yanlış sayılmaz).
func parseCaseVerdict(out string) (int8, string) {
	out = strings.TrimSpace(out)
	first, rest, _ := strings.Cut(out, "\n")
	note := clipBytes(strings.TrimSpace(rest), 300)
	switch w := strings.ToUpper(strings.Trim(strings.TrimSpace(first), ".:*# ")); {
	case strings.HasPrefix(w, "EVET") || strings.HasPrefix(w, "YES"):
		return 1, note
	case strings.HasPrefix(w, "HAYIR") || strings.HasPrefix(w, "NO"):
		return 0, note
	}
	return -1, note
}

// pastCasesBlock — çekmece/serbest döngü kanıtına eklenen geçmiş vakalar
// (en çok 3; onaylı önce). Okunamazsa ya da yoksa "" — kanıt aynen kalır.
func (s *Server) pastCasesBlock(ctx context.Context, service string, codes []string, excludeCase string) string {
	if s.store == nil || (service == "" && len(codes) == 0) {
		return ""
	}
	rows, err := s.store.SimilarIncidentCases(ctx, service, codes, time.Now().Add(-pastCasesLookback), pastCasesInEvident+1)
	if err != nil {
		return ""
	}
	return formatPastCases(rows, excludeCase, pastCasesInEvident)
}

// formatPastCases — SAF: modele giden blok. Onaysız taslak AÇIKÇA olgu değil.
func formatPastCases(rows []chstore.SimilarCase, excludeCase string, max int) string {
	var b strings.Builder
	n := 0
	for _, c := range rows {
		if c.CaseID == excludeCase || n >= max {
			continue
		}
		if n == 0 {
			b.WriteString("\n\nGEÇMİŞ VAKALAR (benzer imza; ONAYLI olanlar insan doğrulamalı, ONAYSIZ olanlar yalnız ipucu — olgu gibi sunma):\n")
		}
		n++
		tag := "ONAYSIZ TASLAK"
		if c.Status == chstore.CaseConfirmed {
			tag = "ONAYLI (" + c.ConfirmedBy + ")"
		}
		fmt.Fprintf(&b, "- [%s] %s · %s · %s", tag, c.ClosedAt.UTC().Format("2006-01-02"), c.Service, c.Title)
		if len(c.MatchedCodes) > 0 {
			fmt.Fprintf(&b, " · eşleşen kod: %s", strings.Join(c.MatchedCodes, ", "))
		}
		b.WriteString("\n")
		if c.Status == chstore.CaseConfirmed {
			fmt.Fprintf(&b, "  neden: %s\n", clipBytes(c.FinalCause, 400))
			if c.Resolution != "" {
				fmt.Fprintf(&b, "  çözüm: %s\n", clipBytes(c.Resolution, 400))
			}
		} else if c.SuggestedCause != "" {
			fmt.Fprintf(&b, "  o zamanki öneri (doğrulanmadı): %s\n", clipBytes(c.SuggestedCause, 300))
		}
	}
	return b.String()
}
