package api

import (
	"context"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_step_phase.go — v0.10.1273 (CoSRE adım 4): ⚙ adım çipinin İNSAN
// OKUR evresi. `step` olayına İSTEĞE BAĞLI `phase` alanı olarak biner
// (eski alanlar aynen; eski frontend alanı yok sayar). Frontend turun
// evrelerini "Metrik → Log → Trace" kırıntısı olarak çizer — operatör
// soruşturmanın ŞEKLİNİ (hangi sinyalden hangisine geçildi) çip çip
// okumadan görür.
//
// Harita bilerek kaba: evre bir sinyal ailesi, araç değil. Bilinmeyen ad
// (dış MCP aracı, modelin uydurduğu ad) evre almaz — kırıntıda "Araç"
// gibi anlamsız bir halka yerine hiç halka olmaz.

var stepPhaseByTool = map[string]string{
	// Metrik
	"query_metrics": "Metrik", "query_metric": "Metrik", "cluster_metric": "Metrik",
	"list_metric_names": "Metrik", "list_metric_labels": "Metrik",
	"get_metrics_for_span": "Metrik", "compare_periods": "Metrik", "render_chart": "Metrik",
	// Log
	"query_logs": "Log", "search_logs": "Log", "log_patterns": "Log",
	"get_log_histogram": "Log", "get_logs_for_trace": "Log", "list_log_fields": "Log",
	// Trace
	"query_traces": "Trace", "search_traces": "Trace", "get_trace": "Trace",
	"find_trace_by_request_id": "Trace", "find_trace_by_span": "Trace",
	"get_exemplar_traces": "Trace", "get_linked_traces": "Trace", "trace_stats": "Trace",
	"compare_traces": "Trace", "bubble_up": "Trace", "explain_trace": "Trace",
	"pivot_from_trace": "Trace", // v0.10.1275 — CoSRE adım 6: trace → pod → JVM → log
	// Sağlık (servis / operasyon / veritabanı / kuyruk / SLO)
	"get_service_health": "Sağlık", "get_operation_health": "Sağlık", "get_db_health": "Sağlık",
	"get_messaging_health": "Sağlık", "list_slo_status": "Sağlık", "explain_service_health": "Sağlık",
	"service_health_sweep": "Sağlık", // v0.10.1276 — CoSRE adım 7: pod/operasyon/fonksiyon taraması
	"list_operations":      "Sağlık", "list_services": "Sağlık",
	// Problem / anomali / exception
	"list_problems": "Problem", "get_problem": "Problem", "get_problem_root_cause": "Problem",
	"list_problem_window_events": "Problem", "similar_problems": "Problem", "similar_incidents": "Problem",
	"explain_problem": "Problem", "get_correlation_evidence": "Problem", "get_blast_radius": "Problem",
	"list_anomalies":        "Anomali",
	"list_exception_groups": "Exception", "get_exception_samples": "Exception", "explain_exception": "Exception",
	// Topoloji / altyapı
	"get_topology": "Topoloji", "list_pods": "Altyapı", "get_pod_health": "Altyapı",
	"list_workloads": "Altyapı", "list_clusters": "Altyapı", "list_namespaces": "Altyapı",
	"list_environments": "Altyapı",
	// Değişiklik
	"list_deploys": "Değişiklik", "list_deployments": "Değişiklik", "get_deploy_diff": "Değişiklik",
	"deploy_impact": "Değişiklik", "get_correlated_changes": "Değişiklik",
	// Bilgi
	"search_knowledge": "Bilgi", "get_runbook": "Bilgi", "suggest_runbook": "Bilgi", "product_guide": "Bilgi",
	"resolve_business_code": "Bilgi",
}

// stepPhaseTR — aracın evresi; bilinmeyen ad "".
func stepPhaseTR(tool string) string { return stepPhaseByTool[tool] }

// chatProgressStep — v0.10.1275: bileşik aracın ilerleme satırı. Etiket-yalnız
// `step` olayı ({label} + isteğe bağlı phase) — araç adı / `i` YOK: şeffaflık
// paneli onu araç çağrısı saymaz, eski istemci düz etiket çizer. Saf.
func chatProgressStep(phase, label string) map[string]string {
	m := map[string]string{"label": label}
	if phase != "" {
		m["phase"] = phase
	}
	return m
}

// chatDeltaPayload — akan turun `delta` olayı. `reset` İSTEĞE BAĞLI ve yalnız
// true iken yazılır: o ana dek akan metin geri alınır (tool'lu turun önsözü
// ya da düşünce bloğu sonrası — provider/tools_stream_gate.go). Eski frontend
// alanı yok sayar ve boş metni ekler; `answer` yine otoriter.
func chatDeltaPayload(d copilot.TextDelta) map[string]any {
	m := map[string]any{"text": d.Text}
	if d.Reset {
		m["reset"] = true
	}
	return m
}

// chatLoopTurn — serbest döngünün TEK model turu, akan (v0.10.1273). Final
// turun metni `delta` olaylarıyla canlı akar; tool-call turu metin yayınlamaz.
// emit, handler'ın paylaşılan kilitli emit'idir (blocks.Emitter): heartbeat
// ile delta aynı kilitten geçer. ctx iptali (istemci koptu / alışveriş
// tavanı) üst akış HTTP isteğini keser.
func (s *Server) chatLoopTurn(ctx context.Context, system string, conv []copilot.ChatMessage, specs []copilot.ToolSpec, emit func(string, any)) (copilot.ChatTurn, error) {
	return s.copilot.ChatWithToolsStream(ctx, system, conv, specs, func(d copilot.TextDelta) {
		emit("delta", chatDeltaPayload(d))
	})
}
