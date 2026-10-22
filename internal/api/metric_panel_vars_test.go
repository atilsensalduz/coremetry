package api

// metric_panel_vars_test.go — v0.10.1435 (Panelden "Bu grafiği açıkla"):
// dashboard panelinin ${var} genişletmesinin Go ikizi (substitutePanelVars)
// istemciyle AYNI altın tablodan sınanır — testdata/panel_vars_golden.json;
// TS tarafı components/dashboard/panelVarsGolden.test.ts. Ayrışma = her
// dashboard ✨'ı "kayıtlı panelle uyuşmuyor" der (güven kuralı karşılaştırması).

import (
	"encoding/json"
	"os"
	"testing"
)

type panelVarsGolden struct {
	Cases []struct {
		Name   string            `json:"name"`
		Type   string            `json:"type"`
		Config json.RawMessage   `json:"config"`
		Vars   map[string]string `json:"vars"`
		Want   metricPanelAsk    `json:"want"`
	} `json:"cases"`
}

func TestSubstitutePanelVarsGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/panel_vars_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g panelVarsGolden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) < 8 {
		t.Fatalf("altın tablo %d vaka", len(g.Cases))
	}
	for _, c := range g.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, ok := substitutePanelVars(c.Type, c.Config, c.Vars)
			if !ok {
				t.Fatal("tür desteklenmedi")
			}
			if want := metricPanelQueryFromAsk(&c.Want); !got.sameQuery(want) {
				t.Fatalf("\n got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestSubstitutePanelVarsRejectsNonSeries(t *testing.T) {
	for _, typ := range []string{"stat", "gauge", "heatmap", "markdown", "topn", "row", ""} {
		if _, ok := substitutePanelVars(typ, json.RawMessage(`{}`), map[string]string{}); ok {
			t.Errorf("%q kabul edildi", typ)
		}
	}
	if _, ok := substitutePanelVars("metric", json.RawMessage(`{bozuk`), nil); ok {
		t.Error("bozuk config kabul edildi")
	}
	// PromQL kipi alanın VARLIĞIYLA seçilir: boş dize = PromQL kipi.
	q, _ := substitutePanelVars("metric", json.RawMessage(`{"metricName":"x","promql":""}`), map[string]string{})
	if q.Src != metricPanelSrcPromQL || q.PromQL != "" {
		t.Errorf("boş promql kipi %+v", q)
	}
}
