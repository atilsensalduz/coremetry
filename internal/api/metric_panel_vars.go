package api

// metric_panel_vars.go — v0.10.1435 (Panelden "Bu grafiği açıkla"). Dashboard
// panelinin ${var} genişletmesinin SUNUCU İKİZİ: frontend
// components/dashboard/PanelRenderer.tsx applyVarsToMetric / applyVarsToSpan /
// applyVarsToPromql (+ lib/utils.ts substituteVars, expandPromqlVars).
//
// Neden ikiz: dashboard panelinin sorgusu sohbete URL'de ya da istemcinin
// yükünde GELMEZ — sunucu kayıtlı paneli kendisi yükler ve yalnız istemcinin
// değişken değerlerini (`vars`) kabul eder. İstemcinin çözdüğü yapılandırma
// sunucunun çözdüğüyle bayt bayt aynı olmalı (aksi: kayıtlı panelle uyuşmuyor
// → LLM'siz dürüst ret). İki taraf AYNI altın dosyadan sınanır:
// internal/api/testdata/panel_vars_golden.json (Go: metric_panel_vars_test.go,
// TS: components/dashboard/panelVarsGolden.test.ts).
//
// JS kuralları birebir:
//   - substituteVars: satır satır; bir satırdaki TÜM ${ad} referansları boş /
//     tanımsızsa satır DÜŞER, aksi her referans değeriyle (tanımsız → "").
//   - expandPromqlVars: değeri boş/tanımsız değişkene bağlı etiket eşleştiricisi
//     SİLİNİR (seçici "hepsi" olur), kalan ${ad} yerinde değiştirilir, sarkan
//     virgüller toplanır.
// Fark (bilinçli, belgeli): JS `\s` Unicode boşlukları da tanır, RE2 yalnız
// ASCII; dashboard sorgularında Unicode boşluk yok, altın dosya ASCII.

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	panelVarRefRe     = regexp.MustCompile(`\$\{([^}]+)\}`)
	panelVarMatcherRe = regexp.MustCompile(`,?\s*[\w.]+\s*(?:=~|!~|=|!=)\s*"\$\{([^}]+)\}"`)
	panelVarOpenComma = regexp.MustCompile(`\{\s*,`)
	panelVarCloseComa = regexp.MustCompile(`,\s*\}`)
	panelVarDblComma  = regexp.MustCompile(`,\s*,`)
)

// substitutePanelVarLines — SAF: lib/utils.ts substituteVars ikizi.
func substitutePanelVarLines(s string, vars map[string]string) string {
	if s == "" || vars == nil {
		return s
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		refs := panelVarRefRe.FindAllStringSubmatch(line, -1)
		if len(refs) > 0 {
			allEmpty := true
			for _, m := range refs {
				if vars[m[1]] != "" {
					allEmpty = false
					break
				}
			}
			if allEmpty {
				continue
			}
		}
		out = append(out, panelVarRefRe.ReplaceAllStringFunc(line, func(m string) string {
			return vars[m[2:len(m)-1]]
		}))
	}
	return strings.Join(out, "\n")
}

// expandPanelPromQLVars — SAF: PanelRenderer.tsx expandPromqlVars ikizi.
func expandPanelPromQLVars(q string, vars map[string]string) string {
	if q == "" || vars == nil {
		return q
	}
	q = panelVarMatcherRe.ReplaceAllStringFunc(q, func(m string) string {
		sub := panelVarMatcherRe.FindStringSubmatch(m)
		name := sub[1]
		if v := vars[name]; v != "" {
			return strings.Replace(m, "${"+name+"}", v, 1)
		}
		return ""
	})
	q = panelVarRefRe.ReplaceAllStringFunc(q, func(m string) string { return vars[m[2:len(m)-1]] })
	q = panelVarOpenComma.ReplaceAllString(q, "{")
	q = panelVarCloseComa.ReplaceAllString(q, "}")
	return panelVarDblComma.ReplaceAllString(q, ",")
}

// storedMetricPanelConfig — kayıtlı panelin (chstore.Dashboard.Panels, opak
// JSON) okunan alanları; lib/types.ts MetricPanelConfig / SpanMetricPanelConfig
// / PromqlPanelConfig. PromQL *string: alanın VARLIĞI kipi seçer (boş dize =
// PromQL kipi, sorgu yazılmamış).
type storedMetricPanelConfig struct {
	MetricName string  `json:"metricName"`
	Service    string  `json:"service"`
	Agg        string  `json:"agg"`
	GroupBy    string  `json:"groupBy"`
	Filters    string  `json:"filters"`
	PromQL     *string `json:"promql"`
	Field      string  `json:"field"`
	DSL        string  `json:"dsl"`
	Query      string  `json:"query"`
	Unit       string  `json:"unit"`
}

// substitutePanelVars — SAF: kayıtlı panel (tür + config) + değişkenler →
// çözülmüş sorgu kimliği. applyVarsToMetric / applyVarsToSpan /
// applyVarsToPromql'ın birleşik ikizi (yalnız sorguyu belirleyen alanlar).
// Desteklenmeyen tür → ok=false (✨ yalnız metric / spanmetric / promql).
func substitutePanelVars(typ string, cfg json.RawMessage, vars map[string]string) (metricPanelQuery, bool) {
	var c storedMetricPanelConfig
	if len(cfg) > 0 && json.Unmarshal(cfg, &c) != nil {
		return metricPanelQuery{}, false
	}
	switch typ {
	case "metric":
		if c.PromQL != nil {
			return metricPanelQuery{Src: metricPanelSrcPromQL, PromQL: expandPanelPromQLVars(*c.PromQL, vars), Unit: c.Unit}, true
		}
		return metricPanelQuery{Src: metricPanelSrcBuilder,
			MetricName: substitutePanelVarLines(c.MetricName, vars), Service: substitutePanelVarLines(c.Service, vars),
			Agg: c.Agg, GroupBy: substitutePanelVarLines(c.GroupBy, vars), Filters: substitutePanelVarLines(c.Filters, vars),
			Unit: c.Unit}, true
	case "spanmetric":
		return metricPanelQuery{Src: metricPanelSrcSpan, Agg: c.Agg, Field: c.Field,
			DSL: substitutePanelVarLines(c.DSL, vars), GroupBy: substitutePanelVarLines(c.GroupBy, vars),
			Filters: substitutePanelVarLines(c.Filters, vars), Unit: c.Unit}, true
	case "promql":
		return metricPanelQuery{Src: metricPanelSrcPromQL, PromQL: expandPanelPromQLVars(c.Query, vars), Unit: c.Unit}, true
	}
	return metricPanelQuery{}, false
}
