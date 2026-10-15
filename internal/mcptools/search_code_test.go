package mcptools

import (
	"context"
	"encoding/json"
	"testing"
)

// v0.10.1208 — search_code: depo modelden ALINMAZ, sınır ve yokluk.
func TestSearchCodeTool(t *testing.T) {
	var gotSvc, gotQ string
	var gotLim int
	tl := searchCodeTool(Deps{CodeSearch: func(_ context.Context, svc, q string, lim int) (CodeSearchResult, error) {
		gotSvc, gotQ, gotLim = svc, q, lim
		return CodeSearchResult{Outcome: "ok"}, nil
	}})
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"service":" bsa-atm ","query":"MCA-00029","limit":50,"repo":"evil"}`)); err != nil {
		t.Fatal(err)
	}
	if gotSvc != "bsa-atm" || gotQ != "MCA-00029" || gotLim != searchCodeMax {
		t.Errorf("%q %q %d", gotSvc, gotQ, gotLim)
	}
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"service":"a"}`)); err == nil {
		t.Error("query zorunlu")
	}
	out, _ := searchCodeTool(Deps{}).Handler(context.Background(), json.RawMessage(`{"service":"a","query":"b"}`))
	if r, ok := out.(CodeSearchResult); !ok || r.Outcome != "error" {
		t.Errorf("yapılandırılmamış: %+v", out)
	}
	if chatOffered(Deps{}, SearchCodeToolName) || !chatOnlyTools[SearchCodeToolName] {
		t.Error("koşullu + yalnız sohbet")
	}
}
