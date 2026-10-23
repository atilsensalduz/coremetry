package api

// copilot_service_exceptions.go — v0.10.1252 (operatör: "problem yerine
// exceptions / teknik hatalar desin"). Servisli exception sorusu
// ("X teknik hataları?", "X exception'ları") servisin exception gruplarını
// okur — takım kipinin (guidedMyTeamBundle "exceptions") tek-servis ikizi,
// aynı okuma (GetExceptions, type-service gruplama, pencere sınırlı).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// hasServiceExceptionSignal — SAF: exception / istisna / "teknik hata".
func hasServiceExceptionSignal(toks []string) bool {
	if tokenHasPrefix(toks, "exception", "istisna") {
		return true
	}
	return tokenHasPrefix(toks, "teknik") && tokenHasPrefix(toks, "hata")
}

// v0.10.1486 — route alır: örnek trace + stack tablosu route.AnswerTail'e,
// örnek trace çipleri route.ExcSampleLinks'e yazılır
// (copilot_service_exc_samples.go).
func (s *Server) guidedServiceExceptionsBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time) (string, string, error) {
	service := route.Service
	rd := serviceExcReadFn(s)
	if rd == nil {
		return "", "", errors.New("exception okuyucusu yok")
	}
	n := emitGuidedStep(emit, "list_exceptions", `{"service":`+jsonStr(service)+`}`)
	exs, err := rd.GetExceptions(ctx, chstore.ExceptionFilter{
		Services: []string{service}, GroupBy: "type-service", From: from, To: to, Limit: 30,
	})
	if err != nil {
		emitGuidedStepResult(emit, n, "list_exceptions", "", err)
		return "", "", err
	}
	ev := renderServiceExceptionsTR(service, exs, int64(to.Sub(from).Seconds()))
	emitGuidedStepResult(emit, n, "list_exceptions", ev, nil)
	if len(exs) > 0 {
		refs := excSampleRefs(exs)
		var stacks map[string]string
		var stErr error
		sn, read := 0, len(refs) > 0
		if read {
			sn = emitGuidedStep(emit, "exception_sample_stacks", fmt.Sprintf(`{"service":%s,"samples":%d}`, jsonStr(service), len(refs)))
			stacks, stErr = s.readServiceExcStacks(ctx, rd, service, refs)
		}
		rows := buildExcSampleRows(exs, stacks, stErr, s.serviceExcAppPrefixes())
		if read {
			emitGuidedStepResult(emit, sn, "exception_sample_stacks", excSampleEvidenceTR(rows), stErr)
		}
		route.AnswerTail = renderServiceExcSamplesTR(service, rows, len(exs))
		route.ExcSampleLinks = excSampleLinks(rows)
		ev += "\n" + excSampleEvidenceTR(rows)
	}
	return ev, fmt.Sprintf("%s exception grupları (teknik hatalar, son %s)", service, fmtAgoTR(int64(to.Sub(from).Seconds()))), nil
}

// renderServiceExceptionsTR — SAF.
func renderServiceExceptionsTR(service string, exs []chstore.ExceptionRow, rangeS int64) string {
	var b strings.Builder
	if len(exs) == 0 {
		fmt.Fprintf(&b, "%s servisinde son %s içinde teknik hata (exception) yok.\n", service, fmtAgoTR(rangeS))
		return b.String()
	}
	total := uint64(0)
	for _, e := range exs {
		total += uint64(e.Count)
	}
	fmt.Fprintf(&b, "%s teknik hataları (exception grupları, son %s): %d grup, toplam %d kez; en çok görülen önce:\n", service, fmtAgoTR(rangeS), len(exs), total)
	for i, e := range exs {
		if i >= 15 {
			fmt.Fprintf(&b, "… ve %d grup daha.\n", len(exs)-15)
			break
		}
		fmt.Fprintf(&b, "- %s · %d kez", e.Type, e.Count)
		if e.Message != "" {
			fmt.Fprintf(&b, " · %s", truncate(e.Message, 160))
		}
		b.WriteString("\n")
	}
	return b.String()
}
