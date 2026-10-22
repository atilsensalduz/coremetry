package mcptools

// toolset_health.go — v0.10.1434 (CoSRE araştırma programı PR 1, §6.1):
// toolset başına sağlık sözleşmesi.
//
// Kural: SORGU YOK. Durum mevcut problardan türetilir — Deps'teki nil
// alanlar, get_capabilities'in kapanışları (EntityEnabled, RolloutsEnabled,
// MetricsName, RAGReady), LogStore.Backend(), OracleOps.Enabled(). Dönüş
// sourcestate.Status (yeni durum sözlüğü yok): ok / not_configured / partial;
// isteğe bağlı yoklama unreachable / unauthorized / timeout da üretebilir.
// Usable() = kaynak kanıt verebilir mi.
//
// Sağlık kataloğu SÜZMEZ (PR 1): sunulma kararı yalnız operatörün aç/kapa
// seçimi + bugünkü chatOffered koşulları. Sağlığa göre otomatik gizleme,
// "her şey açıkken katalog bayt-bayt aynı" sözleşmesini bozardı (Deps{} ile
// koşan her test kurulumu kataloğu boşaltırdı).
//
// İsteğe bağlı yoklama (probeToolset): yalnız logs için, mevcut
// logstore.Store.Ping (/api/status'un kullandığı aynı canlılık yoklaması),
// 2 s zaman aşımı, Deps.Memo üzerinden 60 s önbellek (L1 → L2 + singleflight;
// tüm pod'larda dakikada ≤1 yoklama). Memo nil → yoklama YOK (türetilmiş
// durum aynen). Yalnız GET /api/ai/toolsets çağırır; get_capabilities
// "sıfır maliyet" kalır.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cilcenk/coremetry/internal/sourcestate"
)

const (
	toolsetProbeTimeout = 2 * time.Second
	toolsetProbeTTL     = 60 * time.Second
)

func okStatus(source, backend, detail string) sourcestate.Status {
	return sourcestate.Status{Source: source, Backend: backend, State: sourcestate.OK, Detail: detail}
}

func notConfigured(source, detail string) sourcestate.Status {
	return sourcestate.Status{Source: source, State: sourcestate.NotConfigured, Detail: detail}
}

// storeBacked — ClickHouse deposuna dayanan setler: Store nil → not_configured.
func storeBacked(source string, d Deps) (sourcestate.Status, bool) {
	if d.Store == nil {
		return notConfigured(source, "ClickHouse store not wired"), false
	}
	return okStatus(source, "clickhouse", ""), true
}

func healthServices(_ context.Context, d Deps) sourcestate.Status {
	st, _ := storeBacked(ToolsetServices, d)
	return st
}

func healthTraces(_ context.Context, d Deps) sourcestate.Status {
	st, ok := storeBacked(ToolsetTraces, d)
	if ok && d.TraceWindow != nil {
		st = st.WithNote("trace window resolver wired (log pivots use the trace's own time)", false)
	}
	return st
}

func healthProblems(_ context.Context, d Deps) sourcestate.Status {
	st, _ := storeBacked(ToolsetProblems, d)
	return st
}

func healthChanges(_ context.Context, d Deps) sourcestate.Status {
	st, ok := storeBacked(ToolsetChanges, d)
	if !ok {
		return st
	}
	switch {
	case d.RolloutsEnabled == nil:
	case d.RolloutsEnabled():
		st.Detail = "K8s rollouts included in list_deployments"
	default:
		st = st.WithNote("rollouts flag off — list_deployments returns inferred deploys only", false)
	}
	return st
}

// healthMetrics — metrik okuma yönlendiricisi (Metrics / MetricRouter) ya da
// Store yedeği yoksa not_configured; Thanos (ClusterMetrics) yoksa partial:
// cluster_metric ve pod aileleri cevap veremez.
func healthMetrics(_ context.Context, d Deps) sourcestate.Status {
	if d.Metrics == nil && d.MetricRouter == nil && d.Store == nil {
		return notConfigured(ToolsetMetrics, "no metric source wired")
	}
	backend := "clickhouse"
	if d.MetricsName != nil && d.MetricsName() == "vm" {
		backend = "victoriametrics"
	}
	st := okStatus(ToolsetMetrics, backend, "")
	if d.ClusterMetrics == nil {
		st = st.WithNote("Thanos not configured — cluster_metric and pod families unavailable", true)
	}
	return st
}

func healthLogs(_ context.Context, d Deps) sourcestate.Status {
	if d.LogStore == nil {
		return notConfigured(ToolsetLogs, "no log store")
	}
	return okStatus(ToolsetLogs, d.LogStore.Backend(), "")
}

func healthK8s(_ context.Context, d Deps) sourcestate.Status {
	switch {
	case d.EntityEnabled == nil:
		return notConfigured(ToolsetK8s, "entity layer probe not wired")
	case !d.EntityEnabled():
		return notConfigured(ToolsetK8s, "entity layer flag off (Settings → Entities)")
	}
	return okStatus(ToolsetK8s, "clickhouse", "")
}

func healthOracle(_ context.Context, d Deps) sourcestate.Status {
	if d.OracleOps == nil || !d.OracleOps.Enabled() {
		return notConfigured(ToolsetOracle, "no enabled Oracle source")
	}
	return okStatus(ToolsetOracle, "oracle", "")
}

func healthDevOpsCode(_ context.Context, d Deps) sourcestate.Status {
	switch {
	case d.CodeSearch == nil && d.SourceCode == nil:
		return notConfigured(ToolsetDevOpsCode, "Azure DevOps not connected")
	case d.CodeSearch == nil:
		return okStatus(ToolsetDevOpsCode, "azure-devops", "").WithNote("code search off — only read_source_code", true)
	case d.SourceCode == nil:
		return okStatus(ToolsetDevOpsCode, "azure-devops", "").WithNote("source reader off — only search_code", true)
	}
	return okStatus(ToolsetDevOpsCode, "azure-devops", "")
}

func healthDevOpsWiki(_ context.Context, d Deps) sourcestate.Status {
	if d.Wiki == nil {
		return notConfigured(ToolsetDevOpsWiki, "wiki knowledge off or Azure DevOps not connected")
	}
	return okStatus(ToolsetDevOpsWiki, "azure-devops", "")
}

// healthKnowledge — runbook'lar Store'da; product_guide statik. RAG hazır
// değilse durum ok kalır (çoğu kurulum RAG kullanmaz), not düşer.
func healthKnowledge(_ context.Context, d Deps) sourcestate.Status {
	st, ok := storeBacked(ToolsetKnowledge, d)
	if !ok {
		st.Detail = "runbook store not wired (product_guide still answers)"
		return st
	}
	switch {
	case d.RAGReady == nil:
	case d.RAGReady():
		st.Detail = "document RAG ready"
	default:
		st = st.WithNote("document RAG not ready — search_knowledge searches runbooks only", false)
	}
	return st
}

func healthMeta(_ context.Context, _ Deps) sourcestate.Status {
	return okStatus(ToolsetMeta, "", "")
}

// probeToolset — isteğe bağlı bağlantı yoklaması (yalnız logs). Türetilmiş
// durum kullanılamaz ise (not_configured) ya da Memo yoksa dokunulmaz.
func probeToolset(ctx context.Context, d Deps, name string, derived sourcestate.Status) sourcestate.Status {
	if name != ToolsetLogs || d.Memo == nil || d.LogStore == nil || derived.State != sourcestate.OK {
		return derived
	}
	backend := d.LogStore.Backend()
	body, err := d.Memo(ctx, "mcp:toolset-health:v1:logs:"+backend, toolsetProbeTTL, func(c context.Context) (any, bool, error) {
		pctx, cancel := context.WithTimeout(c, toolsetProbeTimeout)
		defer cancel()
		perr := d.LogStore.Ping(pctx)
		if perr == nil {
			return derived, true, nil
		}
		if c.Err() != nil { // çağıran vazgeçti: kaynak durumu değil, saklama
			return derived, false, nil
		}
		st := sourcestate.FromError(ToolsetLogs, backend, perr)
		if pctx.Err() == context.DeadlineExceeded {
			st.State = sourcestate.Timeout
		}
		st.Detail = "liveness probe failed" // ham hata metni yok: host/port sızmasın
		return st, true, nil
	})
	if err != nil {
		return derived
	}
	var st sourcestate.Status
	if json.Unmarshal(body, &st) != nil || st.State == "" {
		return derived
	}
	return st
}
