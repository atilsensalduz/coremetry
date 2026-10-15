package mcptools

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// CoSRE öğrenme Faz 3 — similar_incidents: onaysız satır nihai alan taşımaz.
func TestSimilarIncidentView(t *testing.T) {
	conf := similarIncidentView(chstore.SimilarCase{IncidentCase: chstore.IncidentCase{Status: chstore.CaseConfirmed, FinalCause: "x", Category: "deploy"}})
	if conf["confirmed"] != true || conf["finalCause"] != "x" || conf["warning"] != nil {
		t.Errorf("onaylı: %v", conf)
	}
	dr := similarIncidentView(chstore.SimilarCase{IncidentCase: chstore.IncidentCase{Status: chstore.CaseDraft, FinalCause: "x", SuggestedCause: "y"}})
	if dr["confirmed"] != false || dr["finalCause"] != nil || dr["warning"] == nil || dr["suggestedCauseAtTheTime"] != "y" {
		t.Errorf("taslak: %v", dr)
	}
}

func TestIncidentArgsNormalize(t *testing.T) {
	if got := normalizeIncidentCodes([]string{" A ", "A", "", "B"}); len(got) != 2 || got[0] != "A" {
		t.Errorf("%v", got)
	}
	if incidentRangeS(0) != similarIncidentsDefRange || incidentRangeS(1<<40) != similarIncidentsMaxRange || incidentRangeS(60) != 60 {
		t.Error("range")
	}
}
