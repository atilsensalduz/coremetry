package mcptools

// similar_incidents — CoSRE öğrenme Faz 3: vaka hafızasında benzer geçmiş
// olaylar (incident_cases). Puan ClickHouse'ta: 3·servis + 2·|kod kesişimi|
// + yakınlık (exp(−gün/30)); ONAYLI vakalar önce. Onaysız taslak olgu
// değildir — satır `confirmed:false` + uyarı taşır. Salt-okunur.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const (
	similarIncidentsMax      = 3
	similarIncidentsDefRange = 180 * 86400
	similarIncidentsMaxRange = 365 * 86400
	similarIncidentsMaxCodes = 10
)

type similarIncidentsArgs struct {
	Service    string   `json:"service,omitempty"`
	ErrorCodes []string `json:"error_codes,omitempty"`
	RangeS     int      `json:"range_s,omitempty"`
}

// normalizeIncidentCodes — SAF: boşluk kırpar, tekiller, tavanlar.
func normalizeIncidentCodes(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c != "" && !seen[c] && len(out) < similarIncidentsMaxCodes {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// incidentRangeS — SAF: 0 → 180 gün, tavan 365 gün.
func incidentRangeS(r int) int {
	if r <= 0 {
		return similarIncidentsDefRange
	}
	if r > similarIncidentsMaxRange {
		return similarIncidentsMaxRange
	}
	return r
}

// similarIncidentView — SAF: modele giden satır; onaysızda nihai alanlar YOK.
func similarIncidentView(c chstore.SimilarCase) map[string]any {
	v := map[string]any{
		"caseId": c.CaseID, "source": c.Source, "sourceId": c.SourceID,
		"service": c.Service, "title": c.Title, "closedAt": c.ClosedAt.UTC().Format(time.RFC3339),
		"errorCodes": c.ErrorCodes, "matchedCodes": c.MatchedCodes,
		"deployInWindow": c.DeployInWindow, "score": c.Score,
		"confirmed": c.Status == chstore.CaseConfirmed,
	}
	if c.DeployVersion != "" {
		v["deployVersion"] = c.DeployVersion
	}
	if c.Status == chstore.CaseConfirmed {
		v["finalCause"], v["category"], v["resolution"] = c.FinalCause, c.Category, c.Resolution
		v["confirmedBy"] = c.ConfirmedBy
	} else {
		v["warning"] = "UNCONFIRMED draft — not verified by a human; treat as a hint, never as fact"
		if c.SuggestedCause != "" {
			v["suggestedCauseAtTheTime"] = c.SuggestedCause
		}
	}
	return v
}

func similarIncidentsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "similar_incidents",
		ShortDescription: "Benzer geçmiş vakalar: onaylı neden+çözüm önce, servis/hata kodu eşleşmesi.",
		Description:      "Past incidents with a similar signature from CoSRE's case memory (closed Problems and resolved exception groups), up to 3, human-CONFIRMED cases first. Matching: same service and/or overlapping error codes (exception types such as MCA-00029, function codes, channel codes), newer cases rank higher. A confirmed row carries finalCause / category / resolution written by a human; an unconfirmed row is a machine draft (confirmed:false) — present it only as a hint and say it is unverified. Give service, error_codes or both. range_s default 180 days, max 365 days. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":     map[string]any{"type": "string", "description": "Service name of the current problem / exception."},
				"error_codes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": similarIncidentsMaxCodes, "description": "Error codes / exception types / function or channel codes seen now."},
				"range_s":     map[string]any{"type": "integer", "minimum": 0, "maximum": similarIncidentsMaxRange, "description": "Look-back in seconds. Default 15552000 (180 days), max 31536000 (365 days)."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a similarIncidentsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			service, codes := strings.TrimSpace(a.Service), normalizeIncidentCodes(a.ErrorCodes)
			if service == "" && len(codes) == 0 {
				return nil, fmt.Errorf("give service, error_codes or both")
			}
			_, to := rangeWindow(ctx, 0) // pencere sonu çıpadan (operatörün zoom'u)
			from := to.Add(-time.Duration(incidentRangeS(a.RangeS)) * time.Second)
			rows, err := d.Store.SimilarIncidentCases(ctx, service, codes, from, similarIncidentsMax)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(rows))
			confirmed := 0
			for _, c := range rows {
				if c.Status == chstore.CaseConfirmed {
					confirmed++
				}
				out = append(out, similarIncidentView(c))
			}
			return map[string]any{"rows": out, "count": len(out), "confirmed": confirmed}, nil
		},
	}
}
