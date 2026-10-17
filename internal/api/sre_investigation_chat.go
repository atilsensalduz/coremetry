package api

// sre_investigation_chat.go — v0.10.1287 (AI SRE Faz 1b): sohbetin kayıtlı
// araştırmayı yeniden kullanması.
//
//   - get_problem `investigation{at, state, findings≤3, summary}` —
//     Deps.Investigation (mcp_deps.go) → sre.LoadSummary.
//   - guided kök-neden demeti — servisin açık problemlerinden (≤3) ilk bitmiş
//     kaydın "KAYITLI ARAŞTIRMA (HH:MM)" bloğu (sre.RenderSavedTR).
//
// Yalnız okuma (saved_views page='sre-investigation'); kayıt öneridir, yaşı
// blokta yazılı.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// guidedSavedInvestigationMax — demette bakılan en çok problem.
const guidedSavedInvestigationMax = 3

// sreReadStore — kayıt okuyucusu; mağaza yoksa nil.
func (s *Server) sreReadStore() sre.QueueStore {
	st := sreStoreOf(s)
	if cs, ok := st.(*chstore.Store); ok && cs == nil {
		return nil
	}
	return st
}

// mcpInvestigationSummary — Deps.Investigation.
func (s *Server) mcpInvestigationSummary(ctx context.Context, problemID string) (*mcptools.InvestigationSummary, error) {
	st := s.sreReadStore()
	if st == nil {
		return nil, nil
	}
	return sre.LoadSummary(ctx, st, problemID)
}

// guidedSavedInvestigationTR — kök-neden demetinin kayıtlı araştırma bloğu
// ("" = yok). Okuma hatası sessizce boş: demet kayıtsız da tamdır.
func (s *Server) guidedSavedInvestigationTR(ctx context.Context, probs []chstore.Problem) string {
	st := s.sreReadStore()
	if st == nil {
		return ""
	}
	for i, p := range probs {
		if i >= guidedSavedInvestigationMax {
			break
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		sum, err := sre.LoadSummary(c, st, p.ID)
		cancel()
		if err != nil || sum == nil {
			continue
		}
		ref := p.DisplayID
		if ref == "" {
			ref = p.ID
		}
		if txt := sre.RenderSavedTR(ref, *sum, tzdefault.Location()); txt != "" {
			return txt
		}
	}
	return ""
}
