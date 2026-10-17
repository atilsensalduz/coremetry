package mcptools

// evidence_link.go — v0.10.1272 (CoSRE adım 3/8): kanıt çipi + "Explore'da aç".
//
// query_metrics / query_traces / query_logs her sonuçta `evidence` taşır
// (cosre_query.go). Sohbet bu kanıtı çip olarak çizer; çipe tıklamak AYNI
// sorguyu AYNI pencerede (+ env/cluster) ürünün ilgili sayfasında açmalı.
//
// TEK SAF FONKSİYON: EvidenceHref(tool, evidence, request) → href. Mevcut
// üreticiler yeniden kullanılır (TracesDeepLink, buildLink); elle URL
// birleştirme yok, kodlama url.Values'tan. Girdi aracın KENDİ sonucudur
// (model metni değil): `request` sunucunun normalize ettiği argümanlar,
// `evidence.window` gerçekten okunan pencere (kapı daraltması dâhil).
//
// EŞLEME (kaynak → sayfa):
//
//	query_traces  · ClickHouse            → /traces (service, name=operation
//	                                        süzgeci, hasError, minMs, sort, env, range)
//	query_logs    · Elasticsearch|CH      → /logs (q, service, severity 13|17,
//	                                        traceId, env, cluster, range)
//	query_metrics · VictoriaMetrics RED   → /service Overview, src=metric (Overview
//	                                        METRİK kipi = aracın okuduğu okuyucunun ta kendisi;
//	                                        kaydedilen MetricsQL birden çok ifade —
//	                                        hiçbir sayfa ham MetricsQL'i URL'den almıyor)
//	query_metrics · ClickHouse span RED   → /service Overview, src=span
//	query_metrics · jvm_* (VM|CH)         → /service Infrastructure (runtime bölümü)
//	query_metrics · Thanos pod_*          → /service Infrastructure (servis varsa),
//	                                        yoksa /clusters?cluster=<ad>&section=pods.
//	                                        PromQL konsolunun (internal/thanos/console.go)
//	                                        frontend sayfası YOK — sorguyu URL'den
//	                                        alan bir PromQL sayfası bulunmuyor.
//
// Pencere daima mutlak `range=custom:<fromMs>-<toMs>` (build_link sözleşmesi).
// Eşleme kurulamazsa (servis yok, pencere bozuk) "" — çip yine çizilir,
// yalnız tıklanmaz (uydurma hedef yok).

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ChatEvidence — sohbet olaylarına (step-result.evidence, answer.evidence[])
// giden kanıt: aracın evidence alanı + hangi araç + "Explore'da aç" href'i.
type ChatEvidence struct {
	Tool       string         `json:"tool"`
	Source     string         `json:"source"`
	Query      string         `json:"query"`
	QueryKind  string         `json:"queryKind"`
	Window     EvidenceWindow `json:"window"`
	Env        string         `json:"env,omitempty"`
	Cluster    string         `json:"cluster,omitempty"`
	Rows       *int           `json:"rows,omitempty"`
	Series     *int           `json:"series,omitempty"`
	DurationMs int64          `json:"durationMs"`
	Href       string         `json:"href,omitempty"`
	// Ref — çipin sohbet içi bloğuna bağı (blok yükündeki evidenceRef aynı
	// değer; sunucu adım kimliğinden kurar: "ev<i>"). Boş = bloksuz kanıt.
	Ref string `json:"ref,omitempty"`
}

// evidenceTools — kanıt zarfı taşıyan araçlar (adım 2'nin üçü).
var evidenceTools = map[string]bool{queryMetricsName: true, queryTracesName: true, queryLogsName: true}

// IsEvidenceTool — aracın sonucu kanıt zarfı taşır mı.
func IsEvidenceTool(tool string) bool { return evidenceTools[tool] }

// ToolResultEvidence — aracın HAM JSON sonucundan kanıt + href. Kanıt zarfı
// taşımayan araç, çözülemeyen içerik ya da kanıtsız sonuç → ok=false. Saf.
func ToolResultEvidence(tool, content string) (ChatEvidence, bool) {
	if !evidenceTools[tool] {
		return ChatEvidence{}, false
	}
	var r struct {
		Tool     string         `json:"tool"`
		Request  map[string]any `json:"request"`
		Evidence *cosreEvidence `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(content), &r); err != nil || r.Evidence == nil || r.Evidence.Source == "" {
		return ChatEvidence{}, false
	}
	if r.Tool != "" && r.Tool != tool {
		return ChatEvidence{}, false
	}
	ev := *r.Evidence
	return ChatEvidence{
		Tool: tool, Source: ev.Source, Query: ev.Query, QueryKind: ev.QueryKind, Window: ev.Window,
		Env: ev.Env, Cluster: ev.Cluster, Rows: ev.Rows, Series: ev.Series, DurationMs: ev.DurationMs,
		Href: EvidenceHref(tool, ev, r.Request),
	}, true
}

// EvidenceHref — kanıt → ürün href'i (kök-göreli). SAF, tablo testli
// (evidence_link_test.go). Kurulamazsa "".
func EvidenceHref(tool string, ev cosreEvidence, req map[string]any) string {
	w := ev.Window
	if w.FromMs <= 0 || w.ToMs <= w.FromMs {
		return ""
	}
	from, to := time.UnixMilli(w.FromMs).UTC(), time.UnixMilli(w.ToMs).UTC()
	rangeParam := fmt.Sprintf("custom:%d-%d", w.FromMs, w.ToMs)
	service := reqString(req, "service")
	switch tool {
	case queryTracesName:
		if service == "" {
			return ""
		}
		f := chstore.TraceFilter{
			Service:  service,
			HasError: reqString(req, "status") == qtStatusError,
			MinMs:    reqNumber(req, "min_duration_ms"),
			Env:      ev.Env,
			Sort:     "time",
		}
		if reqString(req, "sort_by") == qtSortSlowest {
			f.Sort = "duration"
		}
		if op := reqString(req, "operation"); op != "" {
			// query_traces'in uyguladığı süzgecin AYNISI (name = operation).
			f.Filters = []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{op}}}
		}
		return TracesDeepLink(f, from, to)
	case queryLogsName:
		traceID := reqString(req, "trace_id")
		if service == "" && traceID == "" {
			return ""
		}
		sev := 0
		switch reqString(req, "severity") {
		case qlSeverityWarn:
			sev = qlOTelWarnFloor
		case qlSeverityError:
			sev = qlOTelErrorFloor
		}
		// /logs `cluster` ham logstore süzgecidir (api_logs.go Filter.Cluster) —
		// query_logs'un okuduğu değerle aynı; Remote Cluster kimliği DEĞİL.
		return evidenceBuildLink(buildLinkArgs{
			Page: "logs", Query: reqString(req, "text"), Service: service, Severity: sev,
			TraceID: traceID, Env: ev.Env,
		}, ev.Cluster, rangeParam)
	case queryMetricsName:
		family := reqString(req, "family")
		if ev.Source == evSourceThanos {
			if service == "" {
				if ev.Cluster == "" {
					return ""
				}
				// Clusters sayfası ?cluster= değerini ClusterByRef ile çözer (id ya da ad).
				return evidenceBuildLink(buildLinkArgs{Page: "clusters", Tab: "pods"}, ev.Cluster, rangeParam)
			}
			return evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "infra", Env: ev.Env}, "", rangeParam)
		}
		if service == "" {
			return ""
		}
		if qmKindOf(family) == qmRuntime {
			return evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "infra", Env: ev.Env}, "", rangeParam)
		}
		src := "span"
		if ev.Source == evSourceVM {
			src = "metric"
		}
		return withQueryParam(evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "overview", Env: ev.Env}, "", rangeParam), "src", src)
	}
	return ""
}

// evidenceBuildLink — buildLink; hata = "" (kanıt çipi tıklanmaz, uydurulmaz).
func evidenceBuildLink(a buildLinkArgs, clusterID, rangeParam string) string {
	href, err := buildLink(a, clusterID, nil, rangeParam)
	if err != nil {
		return ""
	}
	return href
}

// withQueryParam — üretilmiş href'e bir parametre ekler (url.Values ile yeniden
// kodlar; buildLink'in kapsamadığı sayfa paramı için — Overview ?src=).
func withQueryParam(href, k, v string) string {
	if href == "" || v == "" {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

// reqString — normalize edilmiş istekten dize (JSON'dan gelen sayı da metne).
func reqString(req map[string]any, k string) string {
	switch v := req[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%g", v)
	}
	return ""
}

// reqNumber — normalize edilmiş istekten sayı (yoksa 0).
func reqNumber(req map[string]any, k string) float64 {
	if v, ok := req[k].(float64); ok && v > 0 {
		return v
	}
	return 0
}
