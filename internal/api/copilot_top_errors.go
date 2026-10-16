package api

// copilot_top_errors.go — v0.10.1256 (operatör: "en yavaş trace'ler tüm
// trace'ler arasında anlamlı değil; takımla ilgili ya da hatalı olanlar, en
// hatalı servisler, sonra onların operasyonları"). İki adımlı akış:
//   "(takımımın) en hatalı servisleri?" → hata sayısına göre ilk 10 servis
//   "X en hatalı operasyonları?"         → o servisin hatalı operasyonları
// İkisi de MV'den (service_summary_5m / operation_summary_5m).

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const topErrorRows = 10

// hasTopErrorSignal — SAF: "en/çok/top" + "hatal" ("en hatalı servisler",
// "en çok hata alan operasyonlar"). Sıralama isteği; düz "hatalar" değil.
func hasTopErrorSignal(toks []string) bool {
	rank := false
	for _, t := range toks {
		if t == "en" || t == "top" {
			rank = true
		}
	}
	if !rank && !tokenHasPrefix(toks, "çok") {
		return false
	}
	return tokenHasPrefix(toks, "hatal", "hata")
}

func hasOperationWord(toks []string) bool {
	return tokenHasPrefix(toks, "operasyon", "işlem", "endpoint", "uç")
}

func hasServiceWord(toks []string) bool { return tokenHasPrefix(toks, "servis", "service") }

// renderTopErrorsTR — SAF.
func renderTopErrorsTR(title string, rows []chstore.ErrorRank, rangeS int64, noun string) string {
	var b strings.Builder
	if len(rows) == 0 {
		fmt.Fprintf(&b, "%s: son %s içinde hata yok.\n", title, fmtAgoTR(rangeS))
		return b.String()
	}
	fmt.Fprintf(&b, "%s (son %s, hata sayısına göre, ilk %d %s):\n", title, fmtAgoTR(rangeS), len(rows), noun)
	for _, r := range rows {
		rate := 0.0
		if r.Spans > 0 {
			rate = float64(r.Errors) / float64(r.Spans) * 100
		}
		fmt.Fprintf(&b, "- %s: %d hata / %d istek (%%%.2f)\n", r.Name, r.Errors, r.Spans, rate)
	}
	return b.String()
}

func (s *Server) guidedTopErrorServicesFleet(ctx context.Context, emit func(string, any), from, to time.Time) (string, string, []chstore.ErrorRank, error) {
	n := emitGuidedStep(emit, "top_error_services", `{"scope":"fleet"}`)
	rows, err := s.store.TopErrorServices(ctx, from, to, nil, topErrorRows)
	ev := ""
	if err == nil {
		ev = renderTopErrorsTR("Filoda en hatalı servisler", rows, int64(to.Sub(from).Seconds()), "servis")
	}
	emitGuidedStepResult(emit, n, "top_error_services", ev, err)
	return ev, "servis hata sıralaması (service_summary_5m)", rows, err
}

func (s *Server) guidedTopErrorOpsBundle(ctx context.Context, emit func(string, any), service string, from, to time.Time) (string, string, error) {
	n := emitGuidedStep(emit, "top_error_operations", `{"service":`+jsonStr(service)+`}`)
	rows, err := s.store.TopErrorOperations(ctx, service, from, to, topErrorRows)
	if err != nil {
		emitGuidedStepResult(emit, n, "top_error_operations", "", err)
		return "", "", err
	}
	ev := renderTopErrorsTR(service+" en hatalı operasyonları", rows, int64(to.Sub(from).Seconds()), "operasyon")
	emitGuidedStepResult(emit, n, "top_error_operations", ev, nil)
	return ev, "operasyon hata sıralaması (operation_summary_5m)", nil
}

// topErrorFollowups — SAF: ilk üç servisin operasyon çipi.
func topErrorFollowups(names []string) []string {
	var out []string
	for i, n := range names {
		if i >= 3 {
			break
		}
		out = append(out, n+" en hatalı operasyonları?")
	}
	return out
}
