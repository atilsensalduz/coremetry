package api

// cosre_cases_pending.go — v0.10.1475 — /ai "Onay bekleyen vakalar".
//
//   GET /api/cases/pending?source=problem|exception|all&days=&offset=&limit=
//
// Neden: AI SRE doğruluk ölçümü (sre/eval.go) yalnız İNSAN ONAYLI
// incident_cases okur; taslaklar Problem sayfalarında tek tek onay bekliyor
// ve kimse o sayfaları gezmiyor → eval 0 vaka buldu. Bu uç taslakları tek
// listede verir; YAZMA burada YOK — satırın formu mevcut
// POST /api/cases/{caseId}/confirm ucunu çağırır (aynı doğrulama, aynı
// cosre_correct hükmü, aynı audit satırı). Ayrı bir toplu-onay ucu bilerek
// yazılmadı: toplu onay = öneriyi okumadan kabul; manşet ölçü "bağımsız"
// vakalar (onaylayan öneriyi aynen kabul ETMEDİ) olduğu için her vaka tek
// tık + görünür form olmalı. Hiçbir şey otomatik onaylanmaz; 👍/👎 bu
// yüzeye hiç girmez (operatör kuralı, TestEvalIgnoresFeedback).
//
// Rol kapısı YOK — viewer listeyi salt-okunur GÖRÜR (CLAUDE.md #7; aynı
// öneri metni zaten kapısız GET /api/cases/lookup ile Problem sayfasında
// görünüyor); yazma kapısı onay ucunda (editorRoles). serveCached 15 s,
// anahtar tüm girdileri taşır; onay ucu "cases-pending:" önekini düşürür
// ki onaylanan satır listede kalmasın.

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func init() { registerRoutesExtra("cosre-cases-pending", (*Server).registerPendingCaseRoutes) }

func (s *Server) registerPendingCaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cases/pending", s.getPendingCases)
}

const (
	pendingCasesCachePrefix = "cases-pending:"
	pendingCasesTTL         = 15 * time.Second
	pendingCasesDefaultDays = 90
	pendingCasesDefaultLim  = 25
	// evalMinConfirmedCases — operatör (2026-10-23): doğruluk ölçümünün
	// anlamlı olması için en az 20 onaylı PROBLEM vakası. Yalnız ipucu;
	// hiçbir kapı bu sayıya bakmaz.
	evalMinConfirmedCases = 20
)

// pendingCasesDayRungs — pencere basamakları (cache anahtarı sınırlı).
var pendingCasesDayRungs = []int{7, 30, 90, 180, 365}

// incidentCaseStore — vaka uçlarının store dikişi (test bellek-içi sahte
// kullanır; üretimde *chstore.Store). Onay ucu da buradan okur/yazar:
// listeden onay ile Problem kartından onay TEK yol.
type incidentCaseStore interface {
	GetIncidentCase(ctx context.Context, caseID string) (*chstore.IncidentCase, error)
	UpsertIncidentCases(ctx context.Context, cases []chstore.IncidentCase) error
	ListIncidentCases(ctx context.Context, q chstore.IncidentCaseQuery) ([]chstore.IncidentCase, error)
	ListPendingIncidentCases(ctx context.Context, q chstore.PendingCaseQuery) ([]chstore.IncidentCase, error)
	CountIncidentCases(ctx context.Context, from time.Time) ([]chstore.IncidentCaseCount, error)
}

var caseStoreFor = func(s *Server) incidentCaseStore { return s.store }

type pendingCasesParams struct {
	Source string // "" = iki kaynak
	Days   int
	Offset int
	Limit  int
}

// parsePendingCasesParams — SAF: kaynak kapalı küme, gün basamağa oturur,
// limit/offset kırpılır — crafted parametre sınırsız cache girdisi basmasın.
func parsePendingCasesParams(src, days, offset, limit string) (pendingCasesParams, error) {
	p := pendingCasesParams{Days: pendingCasesDefaultDays, Limit: pendingCasesDefaultLim}
	switch strings.TrimSpace(src) {
	case "", "problem":
		p.Source = "problem" // varsayılan: eval yalnız problem vakalarını sayar
	case "exception":
		p.Source = "exception"
	case "all":
		p.Source = ""
	default:
		return p, fmt.Errorf("source=problem|exception|all olmalı")
	}
	if d := parseInt(days, pendingCasesDefaultDays); slices.Contains(pendingCasesDayRungs, d) {
		p.Days = d
	}
	if l := parseInt(limit, pendingCasesDefaultLim); l > 0 && l <= chstore.PendingCaseListMax {
		p.Limit = l
	}
	if o := parseInt(offset, 0); o > 0 {
		p.Offset = min(o, chstore.PendingCaseOffsetMax)
	}
	return p, nil
}

func pendingCasesKey(p pendingCasesParams) string {
	return fmt.Sprintf("%ssrc=%s:d=%d:o=%d:l=%d", pendingCasesCachePrefix, p.Source, p.Days, p.Offset, p.Limit)
}

// pendingCaseCounts — sayaç: pencere içi durum × kaynak + eval'in manşet
// kümesi (onaylı problem vakalarından öneriyi aynen kabul ETMEYENLER).
type pendingCaseCounts struct {
	DraftProblem       uint64 `json:"draftProblem"`
	DraftException     uint64 `json:"draftException"`
	ConfirmedProblem   uint64 `json:"confirmedProblem"`
	ConfirmedException uint64 `json:"confirmedException"`
	// IndependentProblem — onaylı problem vakalarından onaylayanın CoSRE
	// önerisini değiştirdiği (chstore.SameCause yanlış) olanlar; eval'in
	// AcceptedSuggestion bayrağının tersi, aynı fonksiyon.
	IndependentProblem int `json:"independentProblem"`
	// IndependentCapped — onaylı küme IncidentCaseListMax'ı aştı; bağımsız
	// sayı ilk 200 vakadan.
	IndependentCapped bool `json:"independentCapped,omitempty"`
}

// tallyCaseCounts — SAF.
func tallyCaseCounts(rows []chstore.IncidentCaseCount, confirmed []chstore.IncidentCase) pendingCaseCounts {
	var c pendingCaseCounts
	for _, r := range rows {
		switch {
		case r.Status == chstore.CaseDraft && r.Source == "problem":
			c.DraftProblem += r.N
		case r.Status == chstore.CaseDraft && r.Source == "exception":
			c.DraftException += r.N
		case r.Status == chstore.CaseConfirmed && r.Source == "problem":
			c.ConfirmedProblem += r.N
		case r.Status == chstore.CaseConfirmed && r.Source == "exception":
			c.ConfirmedException += r.N
		}
	}
	for _, k := range confirmed {
		if !chstore.SameCause(k.SuggestedCause, k.FinalCause) {
			c.IndependentProblem++
		}
	}
	c.IndependentCapped = len(confirmed) >= chstore.IncidentCaseListMax
	return c
}

type pendingCasesResponse struct {
	Items            []chstore.IncidentCase `json:"items"`
	Total            uint64                 `json:"total"` // süzgeçteki taslak sayısı (sayfalama)
	Counts           pendingCaseCounts      `json:"counts"`
	Categories       []string               `json:"categories"`
	Days             int                    `json:"days"`
	Offset           int                    `json:"offset"`
	Limit            int                    `json:"limit"`
	EvalMinConfirmed int                    `json:"evalMinConfirmed"`
}

func (s *Server) getPendingCases(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := parsePendingCasesParams(q.Get("source"), q.Get("days"), q.Get("offset"), q.Get("limit"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := caseStoreFor(s)
	s.serveCached(w, r, pendingCasesKey(p), pendingCasesTTL, func(ctx context.Context) (any, error) {
		now := time.Now()
		from := now.Add(-time.Duration(p.Days) * 24 * time.Hour)
		items, err := st.ListPendingIncidentCases(ctx, chstore.PendingCaseQuery{Source: p.Source, From: from, Limit: p.Limit, Offset: p.Offset})
		if err != nil {
			return nil, err
		}
		counts, err := st.CountIncidentCases(ctx, from)
		if err != nil {
			return nil, err
		}
		// Eval ile aynı küme: onaylı + problem + opened_at penceresi.
		confirmed, err := st.ListIncidentCases(ctx, chstore.IncidentCaseQuery{Status: chstore.CaseConfirmed, Source: "problem",
			From: from, To: now.Add(time.Hour), Limit: chstore.IncidentCaseListMax})
		if err != nil {
			return nil, err
		}
		c := tallyCaseCounts(counts, confirmed)
		total := c.DraftProblem + c.DraftException
		switch p.Source {
		case "problem":
			total = c.DraftProblem
		case "exception":
			total = c.DraftException
		}
		if items == nil {
			items = []chstore.IncidentCase{}
		}
		return pendingCasesResponse{Items: items, Total: total, Counts: c, Categories: cosrelearn.CaseCategories,
			Days: p.Days, Offset: p.Offset, Limit: p.Limit, EvalMinConfirmed: evalMinConfirmedCases}, nil
	})
}
