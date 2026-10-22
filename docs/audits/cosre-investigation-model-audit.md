# CoSRE → "Toolset + Otomatik Araştırma" Modeli — AUDIT + ONAYLI PLAN

**Tarih:** 2026-10-22 · **Durum:** Faz 1 audit operatörce onaylandı; 13 karar §7'de; plan (§3–§6) kararlara göre güncellendi. Faz 2 PR 1 ile başlar; **her PR sonunda dur, özet ver, onay bekle.**
**Referans tabanı:** bütün dosya/satır atıfları **HEAD v0.10.1430 (commit 12098744)**
içindir. `internal/api/copilot_guided.go`, `copilot_followup.go` ve
`internal/copilot/prompts.go` denetim sırasında başka bir iş tarafından
düzenlendiği için o üç dosyanın satırları `git show HEAD:<path>` çıktısına göre
verilmiştir. Referans mimariler: HolmesGPT (toolset + araştırma döngüsü +
runbook + otomatik tetik) ve Datadog Bits AI SRE (hipotez ağacı, sonuçsuz çıkış,
trace/araştırma ikili görünümü, hafıza). Önceki denetimler tekrar edilmez:
[cosre-agent-v2](../audit/cosre-agent-v2.md), [cosre-telemetry-agent](../audit/cosre-telemetry-agent.md),
[oracle-error-log](../audit/oracle-error-log-2026-09-09.md). **Influx ayrı workstream** — bu planın parçası değildir.

Bağlayıcı ev kuralları (tüm öneriler buna uyar): **"sunucu deterministik toplar,
model anlatır"** (DECISIONS.md:320, :8042); **oylar hiçbir çalışma zamanı kararını
ve değerlendirme etiketini sürmez** (v0.10.1393, DECISIONS.md:7822); **yalnız
öneri, uygulama yok** (DECISIONS.md:6072); `api.go` büyümez; tek depo ClickHouse.
**Invariant 5 güncellemesi (karar 1):** "Kullanıcı-kaydı `saved_views`'ta kalır;
**araştırma verisi `saved_views` dışında**, kendi tablolarında tutulur ve Problem
ile `investigation_id` bağıyla ilişkilenir; `saved_views`'a yalnız özet +
`investigation_id` işaretçisi yazılır." (PR 3 CLAUDE.md / DECISIONS.md'ye işler.)
Belgede kurum adı / host adı yok; örnekler depodaki sentetik adlardır
(`checkout-service`).

## Özet

| § | Başlık | Hüküm (tek satır) |
|---|---|---|
| 1 | Envanter | 74 araç tek düz katalogda (`internal/mcptools/tools.go:62`, pin `discovery_test.go:575`); iki döngü var: sohbet serbest döngüsü (model araç seçer, 5 tur/6 çağrı) ve AI SRE drain (sabit 8 adımlı plan, LLM yalnız 1 anlatım çağrısı). |
| 2 | Fark analizi | HolmesGPT: toolset **kısmen**, sağlık **kısmen**, loop **var (iki kip)**, bütçe **var (token hariç)**, runbook **kısmen**, otomatik tetik **var (dar)**. Bits: hipotez ağacı **kısmen**, sonuçsuz çıkış **yok**, ikili görünüm **kısmen**, hafıza **kısmen**, tetik/API **kısmen**. |
| 3 | Tetikleme | Karar 6: servis + db + exception grubu, P1/P2, ≤15 dk; Oracle shadow/external ve geri tarihli `anomaly-auto:` **açıkça dışlanır** (yalnız manuel). Exception grupları Problem satırı değildir (notify-only) → üreticiye ikinci aday kaynağı gerekir. |
| 4 | Veri modeli | Karar 1: üç tablo — `ai_investigations`, `ai_investigation_hypotheses` (parent_id ağacı), `ai_investigation_steps` (adım + token); `saved_views`'a yalnız özet + işaretçi; outcome enum `confirmed|likely|ambiguous|inconclusive|no_data|llm_skipped|error`; `human_*` yalnız etiket. |
| 5 | Risk/bütçe | Karar 3–4: hipotez başına 3 araç çağrısı, 2 paralel dal, ≤10 LLM çağrısı/araştırma, hedef ≤5 dk, 30/saat korunur (dolunca kuyrukta bekler), araştırma başına token bütçesi (aşım → `inconclusive`); karar 8: localOnly kapısı kodda zorunlu. |
| 6 | Plan | 7 sürüm: **PR 1 toolset** (detaylı) → döngü+ağaç+inconclusive (flag) → veri modeli (+karar 9) → runbook enjeksiyonu → tetik politikası + API + MCP → ikili görünüm UI → hafıza. PR 1 modele giden araç listesini **bayt-bayt** değiştirmez. |
| 7 | Kararlar | 13 soru → karar → sonuç tablosu. |

---

## 1. Mevcut durum envanteri

### 1.1 Katman haritası

| Katman | Nerede | Not |
|---|---|---|
| Araç tanımı | `mcp.Tool{Name, Description, ShortDescription, InputSchema, Handler, MinRole}` — `internal/mcp/mcp.go:251-289`; fabrika `xxxTool(d Deps)`; liste `ToolList` `internal/mcptools/tools.go:359-536`; `Deps` `tools.go:203-302`; tek kurucu `internal/api/mcp_deps.go:42-102` | `Deps` alanı nil ⇒ araç yumuşak `disabled`/boş döner ya da sohbete sunulmaz (`tools.go:549-561 chatOffered`). |
| Kayıt | Dış MCP: `Register` `tools.go:633-653` (7 `chatOnlyTools` `:543` hariç → 67). Sohbet: `ChatToolList` `tools.go:573-582` (`externalOnlyTools` `:568` hariç + `chatOffered`) → `ChatToolListFor` `:611-625` (simple seti 4 aracı gizler `:600`) → `toolsForRole` `internal/api/mcp_gate.go:95-103` → `copilot_chat.go:557-566` | Rol zorlaması `CallGate` (`mcp_gate.go`); tüm araçlar viewer (`no_write_tool_test.go:30`). |
| Sistem promptu | `internal/copilot/prompts.go` (HEAD): sohbet çekirdeği `:2038`, tur tavanı eki `:2104`, degen eki `:2116`, kural eki `:2228`, wiki `:2209`, kod `:2194`, SRE anlatımı `:1649-1709`, runbook `:673-727`, `DataNotInstruction` `:1879`. Döngü promptu montajı `copilot_chat.go:806-820` | `promptVersionRegistry` → `ai_calls.prompt_version` (`chat.go:171`). |
| Kademeler | explain turu (`copilot_chat.go:370`) → kapsam → hafıza → wiki → guided → drawer → RAG → niyet → serbest döngü; sıra pini `chat_tier_order_test.go:53-58`; niyet varsayılanı `on_no_loop` (DECISIONS.md:352) | 40 guided niyet (`git show HEAD:internal/api/copilot_guided.go:62-191`). |
| Serbest döngü | `copilot_chat.go:919-1300`; tavanlar `:62-67` (5 tur / 6 çağrı), derin 8/12 (`chat_deep.go:33-38`); yürütme `internal/ai/agent/tools/executor.go:87,139,157` (tekrar muhafızı, 20 s `mcp.ToolCallBudget` `internal/mcp/toolerr.go:214`); sonuç kırpma 6000 rune (`chat_tool_budget.go:41-66`); tavan turu araçsız (`:1250-1266`); uçtan uca deadline 3×çağrı tavanı ∈ [180 s, 900 s] (`chat_deadline.go:52-78`) | Tur içi çağrılar **sıralı** (`:1006`). |
| Model çağrısı | `copilot.ChatWithTools` `internal/copilot/chat.go:105-119` → `internal/ai/provider/tools.go`; akış `chat_stream.go:38`; ayar anlık görüntüsü `provider_calls.go:45-81` | Tek ai_calls satırı alışveriş başına (`chat.go:128-176`). |
| AI SRE | `internal/sre`: akış `orchestrator.go:1-25`, plan `plan.go:25-50`, drain `drain.go:1-40`, üretici `enqueuer.go:1-15`, anlatım `narrate.go:1-20`, hipotez `hypotheses.go:1-30`, öneri `recommend.go:1-35`, eval `eval.go:1-30` | Okumalar sohbetle **aynı çekirdekler ve önbellek anahtarları** (`orchestrator.go:81-104`). |
| Kanıt sözleşmesi | `mcptools.ChatEvidence` `evidence_link.go:57-79`; zarflı araçlar `query_*` `:82`, bileşik `:89`; sohbet içi blok `evidence_blocks.go:140,309`; sohbet `chat_evidence.go`; SRE adımı aynı dönüşüm (`sre/steps.go:47-62`) | Kanıt = yürütülen sorgu + pencere + kaynak; UI "Explore'da aç". |
| Ekran bağlamı / ilerleme | `chat_tool_scope.go`, `mcptools/progress.go:23-30`, evre kırıntısı `chat_step_phase.go:19-66` | Evre haritası yalnız UI; toolset değil. |
| Kurallar / hafıza | `internal/ai/cosrerules` (≤200 kural, prompt'a ≤5/≤600 B); giriş `cosre_rules_prompt.go:1-19`, `sre/narrate.go:112-118`; "hatırla" kademesi `chat_memory.go:1-27` | İnsan yazımı; skora girmez (`TestRulesNeverReachScoring`). |
| Runbook | `get_runbook` `knowledge_tools.go:220`, `search_knowledge` `:328`, `search_wiki`/`read_wiki_page` `wiki_tools.go:80,169`; SRE önerisi `recommend.go matchRunbooks`; explain turu `explain_runbook.go` | Araştırma **öncesi** enjeksiyon yok (§2). |
| Sağlık probları (bugün) | `capabilitiesFrom` + `get_capabilities` `problem_tools.go:~400-470`; `sourcestate` (`internal/sourcestate/sourcestate.go:30-55`: ok/empty/unreachable/unauthorized/timeout/partial/delayed/truncated/not_configured/error); Oracle `ReportSourceHealth` (`external.go:169-233`); SRE kalp atışı `drain.go:182-200` | PR 1'in sağlık sözleşmesi bunların üstüne kurulur. |

### 1.2 Model çağrısı: bütçe, retry, timeout, max_tokens, thinking

| Eksen | Sohbet serbest döngüsü | AI SRE drain |
|---|---|---|
| Tur / çağrı | 5 / 6, derin 8 / 12 | Sabit plan: 3 dalga, 8 adım (`plan.go:196-264`); LLM ≤1 çağrı |
| Araç süresi | 20 s/çağrı (`toolerr.go:214`) | Adım tavanları 3–17 s, anlatım 45 s (`plan.go:38-50`) |
| Uçtan uca | 3 × çağrı tavanı, [180 s, 900 s] | `itemBudgetSec` 90 (30–600) (`settings.go:48,91`) |
| `max_tokens` | Profil; varsayılan 4096 (`copilot.go:411-416, 915-920`), klamp 256–32768 (`:434-435`); degen yeniden denemesinde `WithMaxTokensCap` (`chat.go:81-99`) | Aynı `Explain` yolu |
| Timeout | 180 s, 10–600 (`copilot.go:422-444`) | Aynı + adım ctx |
| Retry | Sağlayıcıda yok; akış→tamponlu 1 kez (`stream.go:28-37`), bağlam taşması 1 kez (`copilot_chat.go:971-980`), degen araçsız 1 kez (`:877-917`); 429 → 1 saat kesici (`copilot.go:745-785`) | Anlatım kapısı (`narrate.go:55-67`); retry yok |
| Thinking | `modelcaps` gövde anahtarı (`provider_calls.go:81,277-290`); `<think>`/kanal bloğu soyma (`provider/live_think_gate.go`, `tools_stream_gate.go`); Anthropic imzalı `RawContent` (`chat.go:60-62`) | Aynı |
| Token bütçesi | **Yok** (`ai.budget` gösterim, `ai_budget.go:9-12`) | **Yok**; yalnız çağrı sayısı (`orchestrator.go:548-613`) |
| Paralellik | Tur içi sıralı | Dalga içi paralel (`orchestrator.go:334`), `maxConcurrent` 2/pod |

### 1.3 Kayıt ve geri bildirim

- **ai_calls** (`store.go:2847-2869` + ALTER'lar): sohbet alışverişi başına 1 satır (`chat.go:128-176`) + LLM'siz `cosre-turn:<kademe>` satırı (`chat.go:186-218`); SRE anlatımı `surface='sre-investigation'`, callId/exchangeId çağrıdan önce (`narrate.go:103-109`). Yüzey kaydı `internal/ai/aisurface`, pin `ai_surface_registry_test.go:132`.
- **Geri bildirim:** `ai_feedback` tablosu duruyor (90 g TTL, `store.go:2884-2893`), yazma ucu v0.10.1393'te silindi (`ai_feedback.go:12-21`). İnsan işaretleri etiket: `HumanState{dismissed|confirmed}` (`hypotheses.go:82-99`), öneri `ack|dismissed` (`recommend.go:40-50`). Değerlendirme etiketi yalnız onaylı `incident_cases` (`eval.go:1-12`).
- **Politika sapması → karar 9:** `internal/chstore/rca_verdict_store.go:459-466` `ConfirmedRCASignatures` `ai_feedback ... verdict = 1` okur; PR 3'te kaldırılır.

### 1.4 Araç envanteri (74)

Ortak kurallar: pencere `range_s` → `rangeWindow` (`tools.go:793`; çıpa `anchor.go:61`; varsayılan 1800 s, tavan 7 g); `clampLimit` (`tools.go:813`); handler Go hatası → `isError=true` + `ToolErrorJSON` (`internal/mcp/mcp.go:1139-1153`); 20 s bütçe; `has_more` (`has_more_test.go:14`). "Yumuşak" = başarılı sonuç içinde `found:false` / `disabled:true` / `reasons` / `source.state` / `cosreFail`. Test yolları `internal/mcptools/` altındadır. **Sayı pini:** `discovery_test.go:575`, `guided_parity_test.go:141`, `internal/api/mcp_authz_test.go:297` (`TestMCPToolCount` adlı test yok). **TS** sütunu = PR 1'deki kaynak toolset'i (§6.1).

| Araç (tanım satırı) | TS | Kaynak | Girdi | Çıktı | Hata | Test | Sohbet |
|---|---|---|---|---|---|---|---|
| list_services (tools.go:834) | services | CH `service_summary_5m`, kısa pencerede sınırlı `spans` (`services_read.go:65-112`) | name_contains, env, range_s, limit(50,500) | services, has_more, candidates | Go; bulanık adaylar | tools_test.go, cost_claim_test.go | ✓ |
| list_teams (team_ownership.go:533) | services | `service_metadata` + takma adlar | name_contains, limit(30,200) | takım satırları | yumuşak | team_ownership_test.go | ✓ |
| get_team_services (:599) | services | `service_metadata` + `service_summary_5m` | team*, range_s, limit(20,100) | satırlar, has_more | Go (boş takım) | team_ownership_test.go | ✓ |
| service_health_sweep (service_sweep.go:1930) | services | servis/op MV'leri, problems, pod/JVM, logstore, `spanmetrics_1m` | service*, env, window, deep, top_n | cosre zarfı, evidences≤6 | 15 s; bölüm başına durum; memo | service_sweep_test.go, _checks_, _latency_, _kinds_, _node_test.go | ✓ |
| get_service_health (tools.go:1103) | services | list_services okuyucusu + `CountProblems` | service*, env, range_s | found, summary | Go; found:false | tools_test.go | ✓ |
| get_operation_health (operation_health.go:111) | services | `spanmetrics_1m` | service*, range_s, sort, limit(20,50) | RED satırları | Go; yumuşak | operation_health_test.go | ✓ |
| get_pod_health (guided_parity.go:900) | metrics | `metric_points` + RuntimePods (VM/CH) | service, range_s, limit | pods, heap | yumuşak | guided_parity_test.go, runtime_pods_reader_test.go | ✓ |
| get_topology (analysis.go:357) | services | `topology_edges_5m` | service, range_s, limit(40,200) | edges | Go (altyapı düğümü) | analysis_test.go | ✓ |
| get_blast_radius (analysis.go:550) | services | `service_callers_5m` + problems | service*, range_s | callers | Go | analysis_test.go | ✓ |
| get_db_health (guided_parity.go:315) | services | `db_summary_5m` | range_s, limit | rows | yumuşak | guided_parity_test.go | ✓ |
| get_messaging_health (:560) | services | messaging MV'leri | range_s, limit | rows | yumuşak | guided_parity_test.go | ✓ |
| list_environments (discovery.go:281) | services | `spans` deploy_env | pattern, limit | envs | Go | discovery_test.go | ✓ |
| list_problems (tools.go:1201) | problems | `problems` + enrich + `root_cause_hypotheses` | status, service, env, severity, priority, limit | problems, has_more | Go | tools_test.go | ✓ |
| list_problem_window_events (guided_parity.go:1184) | problems | problems + alert_rules | service, env, range_s, limit | events | yumuşak | guided_parity_test.go | ✓ |
| get_problem_root_cause (tools.go:966) | problems | `root_cause_hypotheses`; `investigate` → anlık derin kanıt (15 s) | problem_id*, investigate | computed, candidates | Go; computed:false | tools_test.go, rootcause_map_test.go, problem_ref_test.go | ✓ |
| list_anomalies (tools.go:1306) | problems | `anomaly_events` | service, env, range_s, limit | anomalies | Go | tools_test.go | ✓ |
| query_logs (query_logs.go:92) | logs | logstore (ES/CH) | service*/trace_id, severity, text, window, env, cluster, limit(20,50) | cosreResult + evidence | yumuşak; 10 s | cosre_query_test.go | ✓ |
| search_logs (logs_tools.go:419) | logs | logstore | query, kapsam, severity_min, range_s, limit(50,200) | logs + source.state | ~8 s; yumuşak | logs_tools_test.go | ✓ (simple'da gizli) |
| list_log_fields (:701) | logs | ES mapping / CH kolonları | pattern, limit | fields | 8 s | logs_tools_test.go | ✓ |
| list_clusters (discovery.go:334) | services | `spans` | range_s | clusters | Go | discovery_test.go | ✓ |
| get_log_histogram (analysis.go:807) | logs | logstore Histogram | service, env, query, range_s, buckets | bantlar | Go (`:868`) | analysis_test.go | ✓ |
| explain_log_spike (log_spike.go:1233) | logs | logstore histogram+fieldstats+signature | service, env, query, level, window, spike_*_iso | spike, contributors, evidences | 15 s; yumuşak; memo | log_spike_test.go, log_spike_score_test.go | ✓ |
| list_namespaces (entity_catalog.go:140) | k8s | `entities` | cluster, query, limit | rows | disabled (`:39`) | entity_catalog_test.go | ✓ |
| list_workloads (:298) | k8s | `entities` + spans | namespace*, … | workloads | Go (ns yok) | entity_catalog_test.go | ✓ |
| list_pods (:516) | k8s | `entities` | namespace*, … | pods | aynı | entity_catalog_test.go | ✓ |
| resolve_entity (resolve_entity.go:307) | k8s | servis adları + entities | text*≥3 | adaylar | Go (<3) | resolve_entity_test.go | ✓ |
| resolve_business_code (business_code.go:268) | services | `business_code_summary_5m` + katalog | text*, kind, range_s | candidates | Go; yumuşak | business_code_test.go | ✓ |
| describe_attributes (attr_discovery.go:231) | traces | `spans` örneği | kapsam*, range_s, top | keys | Go | attr_discovery_test.go | ✓ |
| find_attribute_by_value (:286) | traces | `AttrValueProbe` | value* + kapsam | aday anahtarlar | Go | attr_discovery_test.go | ✓ |
| get_trace (trace_tools.go:639) | traces | `spans` → `trace_summary_5m` | trace_id* | spans≤200, source | yumuşak; Go (bozuk id) | trace_tools_test.go, trace_payload_test.go | ✓ |
| resolve_oracle_operation (oracle_operation.go:145) | oracle | `oracle_error_log` | name* | matches≤5 | Go; enabled:false | oracle_operation_test.go | ✓ (Oracle açıksa) |
| search_traces (search_traces.go:46) | traces | `GetTraces` + OracleOps | service, search, filters[], … | traces | Go (filtre) | search_filters_test.go, evidence_link_test.go | ✓ (simple'da gizli) |
| query_traces (query_traces.go:105) | traces | `service_summary_5m` + `GetTraces` | service*, operation, status, … | cosreResult | yumuşak; 10 s | cosre_query_test.go, operation_link_test.go | ✓ |
| trace_stats (trace_stats.go:100) | traces | `GetTraceAggregate` | group_by, filtreler | groups | Go | trace_stats_test.go | ✓ |
| find_trace_by_span (discovery.go:586) | traces | `FindTraceIDBySpan` | span_id* | found, trace_id | yumuşak | discovery_test.go | ✓ |
| find_trace_by_request_id (find_trace_by_request_id.go:112) | logs | logstore ±10 dk | request_id* | found, trace_id | Go; yumuşak | find_trace_by_request_id_test.go | ✓ |
| list_slo_status (list_slo_status.go:29) | services | `slos` + spans | service, limit | slos | Go | **yok** (yalnız has_more_test.go pini) | ✓ |
| query_metrics (query_metrics.go:226) | metrics | MetricRouter VM/CH; JVM; Thanos | service, family, window, env, cluster, group_by | cosreResult | yumuşak; 10 s | cosre_query_test.go | ✓ |
| query_metric (metrics_tools.go:568) | metrics | VM / CH `metric_points` | name*, kapsam, labels, … | ≤20 seri | Go; 5/10 s | metrics_tools_test.go | ✓ (simple'da gizli) |
| list_metric_names (list_metric_names.go:27) | metrics | metrik kataloğu | service, pattern, limit | metrics | Go | discovery_test.go, has_more_test.go | ✓ |
| list_metric_labels (metrics_tools.go:1190) | metrics | VM `/labels` / CH | name*, label | labels | Go; 5 s | metrics_tools_test.go | ✓ (simple'da gizli) |
| list_operations (discovery.go:200) | services | `operation_summary_5m` | service*, pattern | names | Go | discovery_test.go | ✓ |
| list_exception_groups (list_exception_groups.go:29) | problems | `exception_groups` | service, search, … | groups | Go | exception_samples_test.go | ✓ |
| get_exception_samples (exception_samples.go:173) | problems | `exception_groups` + spans | fingerprint* | samples | Go; yumuşak | exception_samples_test.go | ✓ |
| search_code (search_code.go:44) | devops-code | Azure DevOps Code Search | service*, query* | hits | Go; yumuşak | search_code_test.go | **yalnız sohbet**, koşullu |
| read_source_code (source_code.go:140) | devops-code | Azure DevOps repo | service*, file*, line | pencere ≤5800 rune | Go; yumuşak | source_code_test.go | **yalnız sohbet**, koşullu |
| get_correlated_changes (correlate_deploy.go:29) | changes | `service_summary_5m` | at_iso, window_s | sıralı servisler | Go | cost_claim_test.go | ✓ |
| get_deploy_diff (:109) | changes | `service_version_5m` + spans | service*, version | önce/sonra RED | Go; found:false | cost_claim_test.go, discovery_test.go | ✓ |
| compare_periods (compare_periods.go:618) | services | `spanmetrics_1m` / spans | service*, env, pencere | iki dönem RED | Go | compare_periods_test.go | ✓ |
| bubble_up (bubble_up.go:142) | traces | `spans` | service*, compare | attributes | 15 s → Go | bubble_up_test.go | **yalnız dış MCP** |
| list_deploys (discovery.go:435) | changes | `service_version_5m` | service, env, range_s | rows | Go | discovery_test.go | ✓ |
| list_deployments (list_deployments.go:151) | changes | deploys + `workload_rollouts` | cluster, ns, service, range_s | rows | Go (Store nil) | list_deployments_test.go, _render_test.go | ✓ |
| get_problem (problem_tools.go:113) | problems | `problems` + `Deps.Investigation` | problem_id* | problem + investigation | Go; found:false | problem_tools_test.go | ✓ |
| get_correlation_evidence (:240) | problems | `root_cause_hypotheses.deep_evidence` | problem_id*, sections[] | sections | Go | problem_tools_test.go | ✓ |
| similar_problems (:316) | problems | `problems` | problem_id / service+rule_id | rows | Go | problem_tools_test.go | ✓ |
| similar_incidents (similar_incidents.go:83) | problems | `incident_cases` | service/error_codes | rows≤3 | Go | similar_incidents_test.go | ✓ |
| get_capabilities (problem_tools.go:452) | meta | bellek içi problar | — | enabled/reason | hiç | problem_tools_test.go | ✓ |
| log_patterns (signal_tools.go:110) | logs | Drain örnekleme | window_s, service, kind | rows | disabled; Go (kind) | signal_tools_test.go | ✓ |
| cluster_metric (:216) | metrics | Thanos | kind*, cluster* | series | disabled | signal_tools_test.go | ✓ |
| get_runbook (knowledge_tools.go:222) | knowledge | alert_rules, service_metadata, runbooks | runbook_id/problem_id/rule_id/service | içerik ≤4000 rune | Go; found:false | knowledge_tools_test.go | ✓ |
| search_knowledge (:330) | knowledge | runbooks + rag_chunks | query* | rows | Go | knowledge_tools_test.go | ✓ |
| search_wiki (wiki_tools.go:80) | devops-wiki | Azure DevOps wiki dizini | query* | rows | Go; yumuşak | wiki_tools_test.go | **yalnız sohbet**, koşullu |
| read_wiki_page (:169) | devops-wiki | wiki sayfası | project*, wiki*, path* | ~4800 rune | Go / yumuşak | wiki_tools_test.go | **yalnız sohbet**, koşullu |
| get_logs_for_trace (pivots.go:95) | logs | logstore `LogsForTrace` | trace_id* | logs, degraded | yumuşak | pivots_test.go, logs_tools_test.go | ✓ |
| pivot_from_trace (pivot_trace.go:1799) | traces | spans, Thanos, JVM, logstore | trace_id / service | hops, evidences | 15 s; hop durumu; memo | pivot_trace_test.go | ✓ |
| get_exemplar_traces (pivots.go:287) | traces | `metric_points` exemplar | metric*, service* | items | Go | pivots_test.go | ✓ |
| get_linked_traces (:358) | traces | `span_links` | trace_id* | links | Go | pivots_test.go | ✓ |
| get_metrics_for_span (:417) | traces | `ServiceREDSeries` | service*, at_unix_ns* | ≤3 seri | Go | pivots_test.go | ✓ |
| render_chart (tools.go:1537) | services | ad doğrulama | service*, metric | spec | yumuşak | tools_test.go | ✓ |
| build_link (build_link.go:243) | meta | saf + OracleOps | page* | href | Go | build_link_test.go, operation_link_test.go | ✓ |
| product_guide (product_guide.go:384) | knowledge | statik | topic, question | steps | yumuşak | product_guide_test.go | ✓ |
| set_context / get_context / clear_context (context_tools.go:41,72,93) | meta | Redis sohbet bağlamı | kapsam alanları | context | Go (konuşma yok) | context_tools_test.go | **yalnız sohbet** |

Ek: 5 kaynak (`tools.go:662-752`) ve 7 MCP prompt'u (`mcptools/prompts.go:43-209`).

---

## 2. Fark analizi — HolmesGPT ve Bits AI SRE

| Başlık | Durum | Kanıt |
|---|---|---|
| **Toolset soyutlaması** | **Kısmen** | Tek düz katalog (`tools.go:359-536`). Gruplamalar: UI evre haritası (`chat_step_phase.go:19-66`), simple/full (`modelcaps/toolset.go:26-86`), chatOnly/externalOnly (`tools.go:543,568`), `Deps` nil → yumuşak disabled. Kaynak bazında toolset nesnesi, operatör enable/disable'ı yok. → PR 1 |
| **Sağlık kontrolü** | **Kısmen** | `sourcestate` (DECISIONS.md:768); `get_capabilities` (`problem_tools.go:452`); Oracle `ReportSourceHealth`; SRE kalp atışı. Araç **sunulmadan önce** yoklama yok. → PR 1 |
| **Investigation loop** | **Var (iki kip)** | Sohbet: model araç seçer; SRE: sabit plan, LLM anlatır (`orchestrator.go:1-25`); arka planda model-güdümlü döngü yok (DECISIONS.md:320). → PR 2 (karar 2b) |
| **Bütçe / limit** | **Var (token hariç)** | §1.2. → PR 2 (karar 3) |
| **Runbook enjeksiyonu** | **Kısmen** | Talep üzerine araçlar; SRE önerisinde eşleme sonrası; kurallar ≤5 blok ön-enjeksiyon. → PR 4 (karar 10) |
| **Otomatik tetikleme** | **Var (dar)** | `pickCandidates` (`enqueuer.go:84-116`). → PR 5 (karar 6) |
| **Kanıt zinciri → Problem** | **Kısmen** | `saved_views` kaydı; `ProblemInvestigationPanel.tsx`; `get_problem.investigation`. → PR 3/6 |
| **Hipotez ağacı** (Bits) | **Kısmen** | Düz 8 hipotez + kontroller (`hypotheses.go:43-51, 91-100`), saf `Score` (`:225`); doğrulama koşusu (`plan.go:117-141`); sentezleyici `candidates` (`store.go:2600-2615`). Parent/child, dal başına kanıt yok. → PR 2/3 |
| **Sonuçsuz çıkış** (Bits) | **Yok** | Durumlar `investigation.go:21-29`; `NoData` adım bayrağı (`:83-86`); düşük bant da kart (`hypotheses.go:71`). → PR 2 (karar 4) |
| **İkili görünüm** (Bits) | **Kısmen** | Sohbet canlı SSE ama arşivde yalnız cevap kanıtı (`ai_conversations.go:90-101`); SRE `Steps`+`Hypotheses` aynı JSON (`investigation.go:130-182`), canlı değil. → PR 3/6 |
| **Hafıza** (Bits) | **Kısmen** | `similar_problems`/`similar_incidents`, `incident_cases` (`store.go:3046`), `get_problem.investigation`, kurallar. Otomatik araştırmaya bağlam girmiyor (`orchestrator.go:44-79`). → PR 7 (karar 7) |
| **Tetik / API** (Bits) | **Kısmen** | Elle `POST /api/problems/{id}/investigation` (`sre_status.go:41`), sohbetten "doğrula" (`chat_actions.go:145`); liste ucu ve MCP Trigger/Get/List yok. → PR 5 (karar 5, 13) |

---

## 3. Tetikleme noktaları

### 3.1 Problem yazıcıları

Tüm yazımlar `Store.UpsertProblem` (`internal/chstore/problem.go:2021`) / `UpsertProblemAISummary` (`:2060`); dedektörler yalnız `mode.worker` altında (`main.go:700,1184,1471,1518,1546`; roller `parseRunModeValue` `main.go:134-151`).

| Üretici | rule_id / kind | Lider kilidi |
|---|---|---|
| Alert evaluator `evaluateOne` (`evaluator.go:841`) + stale/escalate/promote (`anomaly-auto:<fp>`, **StartedAt geri tarihli** `:1462-1470`), hedef kuralları, ES watcher, SLO burn, db-capacity/db-slow-stmt/db-health (**kind=db**), svc-slowdown, runtime jvm, `exception:shared-dependency`/`exception:fatal-infrastructure`, `self-*` | muhtelif önekler | `coremetry:lock:evaluator` (`evaluator.go:27,170`) |
| Anomali dedektörü `applyOutcome` (`anomaly.go:831`), clustering, `exception-storm` (`exception_storm.go:73`, Service="") | `anomaly:<svc>:<metric>` | `coremetry:lock:anomaly` |
| `ExternalScanner` (`anomaly/external.go:651-729`) — Oracle poller kancası (`internal/oracle`, `main.go:1202,1269-1272`; kip `off|shadow(varsayılan)|live` `oracle/settings.go:180-214`) | `anomaly:ext:` **kind=external**; servis çözülürse kind=service (`:684-693`) | `oracle-poller` |
| Monitor runner (`monitor/runner.go:381-461`) | `monitor:<id>` | `coremetry:lock:monitor-runner` |
| ProblemExplainer (`problem_explainer.go:130,208`) | yalnız `ai_summary` | `problem-explainer:lock` |
| Korelatör / sentezleyici | Problem yazmaz; `root_cause_hypotheses` (`rootcause_worker.go:232-311`) | `rootcause-synthesizer:lock` |

**Exception grupları Problem değildir:** `ExceptionGroupRulePrefix = "exception-group:"` yalnız bildirim kimliğidir, "Problem tablosuna yazılmaz (notify-only)" (`internal/chstore/notify_kind.go:49-51`; `notify/exception_notifier.go:177-200`). Kaynak `exception_groups` tablosu ve exception merdiveni önceliği (CLAUDE.md Triage).

### 3.2 Bugünkü kuyruk

Üretici `sre.StartEnqueuer` (`enqueuer.go:60-78`; lider `coremetry:lock:sre-enqueue`, 30 s) → `Enqueue` (`queue.go:89-160`) → `saved_views`; tüketici `StartDrain` (`drain.go`; iş kilidi `coremetry:lock:sre-item:<id>`, 3 dk kira / 30 s yenileme; `mode.sre` = sre|all, `main.go:1675-1683`). Dedupe: `seen` (1 s), mevcut kayıt, servis soğuması 15 dk, `maxPerTick 2`, `maxPerHour 20`, `maxQueue 200`. Girmeyenler: kind≠service (`enqueuer.go:94`), 15 dk'dan yaşlı (`:31`), `exception-storm`/`self-*`/`incident:*` (`:97`).

### 3.3 Tetik politikası (karar 6, PR 5)

| Aday kaynağı | Dahil | Koşul | Dışlanan (açık liste) |
|---|---|---|---|
| `problems` (`OpenProblemsSnapshot`) | kind=`service`, kind=`db` | status open/acknowledged; P1/P2 (`EnrichProblemsWithPriority`); yaş ≤15 dk — **yaş `StartedAt`'e göre** | kind=`external` (Oracle shadow dahil), `anomaly-auto:` (geri tarihli → yalnız manuel), `anomaly:ext:*`, `exception-storm`, `self-*`, `incident:*`, `monitor:*` |
| `exception_groups` (**yeni ikinci kaynak**) | açık/regressed grup | merdiven önceliği P1/P2; ilk görülme ≤15 dk; servis dolu | `ora:` grupları (Oracle, ikinci faz), çözümlü/yok sayılmış gruplar |

- Politika saf bir tablo fonksiyonu (`sre.TriggerPolicy{Kinds, Priorities, MaxAge, ExcludePrefixes}`) + ayar `sre_agent.trigger` olur; `pickCandidates` onu çağırır. Exception grubu için araştırma öznesi `problem_id = "exception-group:<fp>"` (mevcut bildirim kimliğiyle aynı biçim), `service` = grubun servisi; mevcut `Enqueue` doğrulaması (`queue.go:98-101` "problem kimliği ve servis zorunlu") değişmez.
- `db` türü için plan adımları: sweep/pivot servis odaklı; db öznesinde `deps`/`events`/`log_spike` koşar, `sweep` `db_summary_5m` okuyucusuna yönlenir (PR 2 kapsamında `planSteps` dalı).
- **Push uyandırma (isteğe bağlı):** `UpsertProblem` sonrası bus'a `problem.opened` (`s.PublishSRE` emsali `sre_status.go:70-86`); üretici yine snapshot'tan okur (idempotent).
- Dağıtık: üretici lideri (worker) + drain iş kilidi (N sre pod) korunur; LLM döngüsüyle iş süresi uzayacağından kira yenileme (30 s) sürer, çökme tespiti 2×kira (`drain.go:252-299`) aynen.
- Backpressure: `maxQueue`/`maxPerHour` + saatlik 30 LLM tavanı; **tavan dolunca iş kuyrukta bekler** (karar 3): drain `llmBudget.used ≥ 30` iken **claim etmez** (bugün claim edip anlatımı atlıyor, `narrate.go:55-67`); öncelik sırası `OrderQueued` (`drain.go:324-354`) korunur.

---

## 4. Veri modeli (karar 1, 3, 4, 7, 11)

### 4.1 Bugün

`saved_views(page='sre-investigation', id='sre-'+problemId, query_string=JSON ≤64 KB)` (`sre_investigation.go:6-43`); küçültme merdiveni kanıtı erken düşürür (`investigation.go:226-330`); SQL ile hipotez/adım analitiği yok; `ai_calls` bağı yalnız `summary.callId`.

### 4.2 Üç tablo — artı/eksi ve karar

| Seçenek | + | − |
|---|---|---|
| Tek tablo + JSON | Tüm-satır replace basit | Ağaç/adım analitiği yok, trace akışı için tam yeniden yazım |
| **Üç tablo (karar 1)** | Hipotez düzeyinde SQL (top-1 isabet, bant dağılımı), adım düzeyinde token/latency, append-only trace akışı; UI ikili görünümü aynı yazımdan | Üç yazıcı → checkpoint'te tutarlılık (adımlar/hipotezler önce, kayıt son; kayıt `version` ile okuyucuya "tamamlandı" sinyali); `FINAL` join'ler |

### 4.3 Şema taslağı (`/clickhouse-schema` incelemesi PR 3'te)

```
ai_investigations (
  id               String,                     -- "inv-<problem_id>-<run>"
  problem_id       String,  service LowCardinality(String), env LowCardinality(String),
  subject_kind     LowCardinality(String),     -- service|db|exception_group
  run              UInt16,  kind LowCardinality(String),   -- auto|manual|verify|replay|chat
  trigger          LowCardinality(String),     -- enqueuer|api|mcp|chat
  requested_by     String,  seed_hypothesis String,         -- karar 13: sohbetten serbest metin kök hipotez
  state            LowCardinality(String),     -- queued|running|done|partial|error|shared
  outcome          LowCardinality(String),     -- confirmed|likely|ambiguous|inconclusive|no_data|llm_skipped|error
  outcome_reason   LowCardinality(String),     -- low_score|low_coverage|budget_exhausted|token_exhausted|...
  confidence       Float64, coverage Float64, top_hypothesis_id String, second_hypothesis_id String,
  enqueued_at      DateTime64(9), started_at DateTime64(9), finished_at DateTime64(9),
  llm_calls        UInt16, tool_calls UInt16, tokens_in UInt64, tokens_out UInt64, wall_ms UInt32,
  budget           String,                     -- JSON {maxLLMCalls,maxToolCallsPerHyp,maxBranches,maxTokens,itemBudgetSec}
  timeline         String CODEC(ZSTD(3)), recommendations String CODEC(ZSTD(3)), narration String CODEC(ZSTD(3)),
  narration_call_id String, prior_context String,          -- hafıza izi (PR 7)
  version          UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version) ORDER BY (problem_id, run)
  PARTITION BY toYYYYMM(enqueued_at) TTL toDate(enqueued_at) + INTERVAL 90 DAY

ai_investigation_hypotheses (
  investigation_id String, hypothesis_id String, parent_id String DEFAULT '', depth UInt8,
  kind LowCardinality(String), label String, origin LowCardinality(String),   -- deterministic|llm|seed|synth
  state LowCardinality(String),               -- open|testing|confirmed|rejected|inconclusive
  score Float64, band LowCardinality(String), prior Float64,
  checks String CODEC(ZSTD(3)),               -- JSON [{name,result,rationale,evidenceRef}]
  evidence_steps Array(UInt32),               -- bu dalın kanıt adımları (steps.seq)
  tool_calls UInt8, tokens_in UInt32, tokens_out UInt32,
  human_state LowCardinality(String) DEFAULT '', human_note String DEFAULT '', human_by String DEFAULT '', human_at DateTime64(9),
  version UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version) ORDER BY (investigation_id, hypothesis_id)
  PARTITION BY toYYYYMM(human_at) -- NOT: PR 3'te /clickhouse-schema ile bölümleme kararı; aday enqueued_at kopyası
  TTL 90 DAY (ai_investigations ile aynı)

ai_investigation_steps (
  investigation_id String, seq UInt32, at DateTime64(9),
  phase LowCardinality(String),               -- plan|tool|assess|narrate
  hypothesis_id String DEFAULT '', tool LowCardinality(String) DEFAULT '', toolset LowCardinality(String) DEFAULT '',
  args_preview String, state LowCardinality(String), duration_ms UInt32,
  evidence String CODEC(ZSTD(3)),             -- mcptools.ChatEvidence JSON
  ai_call_id String DEFAULT '', model LowCardinality(String) DEFAULT '',
  tokens_in UInt32, tokens_out UInt32, cached_tokens UInt32,     -- karar 11: adım bazında token
  summary String
) ENGINE = MergeTree ORDER BY (investigation_id, seq)
  PARTITION BY toYYYYMM(at) TTL toDate(at) + INTERVAL 90 DAY
```

- `LowCardinality` yalnız enum/servis kolonları; kimlikler `String` (CLAUDE.md anti-pattern). `human_*` **yalnız etiket**: hiçbir okuma yolu (Score, outcome, plan, eval etiketi, hafıza bloğu puanı) girdi almaz; ratchet'ler genişletilir (`TestHypothesisMarkNeverChangesScore`, `TestEvalIgnoresFeedback`). "Feedback as context" (PR 7) yalnız `human_note` metnini veri bloğu olarak gösterir.
- **Outcome kuralı (karar 4, saf `outcomeOf`):** en iyi dal skoru < 0.40 **veya** kapsam (veri döndüren telemetri adımı payı) < %50 → `inconclusive`; ilk iki dal farkı < 0.10 → `ambiguous` (`top_hypothesis_id` + `second_hypothesis_id`, UI iki kartı birlikte çizer, tek kart yok); token/çağrı bütçesi aşımı → `inconclusive` + `outcome_reason=token_exhausted|budget_exhausted`; LLM kapısı kapalı → `llm_skipped` (deterministik sonuç korunur).
- **problems ile ilişki:** yalnız `problem_id` (+`run`); `problems`'a kolon eklenmez (tek yazıcı, /aiops §6); `saved_views(page='sre-investigation')` satırı **yalnız** `{investigationId, state, outcome, summary≤2 KB}` taşır (geri uyum: `get_problem.investigation`, guided "KAYITLI ARAŞTIRMA"). `root_cause_hypotheses` sentezleyici çıktısı olarak ayrı kalır; ağaca `origin=synth` düğümü olarak kopyalanır.
- **Migrasyon:** `store.go migrate()` `CREATE TABLE IF NOT EXISTS` (uygulama sahipli); küme: iki-boot sözleşmesi / `ON CLUSTER`; `async_insert` dokunulmaz. Geçiş: bayrak arkasında çift yazım → yeni tablolardan okuma; eski tam JSON 30 gün süpürmesiyle (`sre_investigation.go:140-157`) boşalır; `sre-eval` koşu satırları PR 3'te okuma tarafında yeni tablolara bağlanır.

---

## 5. Risk ve bütçe (karar 3, 8)

| Bütçe (karar 3) | Değer | Zorlama noktası |
|---|---|---|
| Hipotez başına araç çağrısı | 3 | `loop.go` dal yürütücüsü; 4. çağrı `skipped(call_cap)` (sohbet `splitByCallBudget` emsali `copilot_chat.go:81`) |
| Paralel dal | 2 | `runWave` deseni (`orchestrator.go:334`), dal başına ctx |
| LLM çağrısı / araştırma | ≤10 (plan 1 + dal turları + sentez 1 + anlatım 1) | sayaç kayıtta (`llm_calls`); aşım → kalan dallar `inconclusive` |
| Hedef latency | ≤5 dk | `itemBudgetSec` döngü açıkken varsayılan 300 (klamp 30–600 mevcut); deterministik dalgalar ≤~45 s + döngü ≤~4 dk; çağrı başına tavan `min(180 s, kalan/kalan_çağrı)` |
| Saatlik LLM | 30 (korunur) | dolunca **iş kuyrukta bekler**, düşürülmez (§3.3) |
| Token / araştırma | ayar `sre_agent.loop.maxTokens` (**öneri** varsayılan 120k giriş + 12k çıkış; `ai_calls` sağlayıcı sayımından) | aşım → `inconclusive(token_exhausted)`; adım başına kayıt (karar 11) |

- **Çağrı maliyeti:** katalog maliyeti ölçülü (33 araç 41.940 B, `mcp.go:276-278`); arka plan döngüsünde model **yalnız allowlist'li toolset'leri** görür (karar 8) → prompt küçülür. Gerçek latency `ai_calls.duration_ms` ve `sre.investigation.duration` (DECISIONS.md:8778) ile izlenir; 5 dk hedefi tutmuyorsa önce dal sayısı, sonra tur düşer (ayar, kod değil).
- **Rate limit:** 429 → 1 saat kesici (`copilot.go:762-785`); araştırma `llm_skipped` ile deterministik sonuç üretir, `error` değil.
- **Veri sızıntısı / karar 8 (localOnly, kodda zorunlu):** (a) arka plan döngüsünde allowlist dışı araçlar (`devops-code`, `devops-wiki`, `meta.context`, serbest sorgu `query_metric`/`search_*`) modele **hiç sunulmaz** (katalog süzülür, "unknown tool" bile değil); (b) profil alanı `LocalOnly bool` (`copilot.ModelProfile`, `profiles.go:50-77`) — kod/wiki içeriği taşıyan çağrı (`read_source_code`, `search_code`, `read_wiki_page` sonucu ya da `buildCodeContext`) `LocalOnly=false` bir profile giderse **hard fail** + log (`[copilot] localOnly ihlali: surface=… profile=…`), ai_calls `status=error error_class=local_only`; (c) drain `srv.MCPDeps()`'i aynen alır (`main.go:1677`) ama toolset süzgeci Deps'ten sonra uygulanır. Maskeleme Collector'da; uygulama içi redaksiyon yok (operatör kuralı).
- **Prompt enjeksiyonu:** `DataNotInstruction` (`prompts.go:1879`), `prompt_injection_test.go`, shield (`narrate.go:106-108`); döngüde araç argümanları model çıktısı → yalnız enum/klamp şemalı yapısal araçlar allowlist'te.
- **Gürültü:** `inconclusive`/`ambiguous` ayrı tip + ayrı UI; bildirim yalnız `confirmed|likely` için mevcut kanala (karar 12, `notify.Notifier`, yeni kanal yok); soğuma; otomatik asla yeniden koşmaz (`queue.go:110-116`).
- **CI:** `go test -race` (dal paralelliği `runWave` deseni), vitest pinleri (`pollingBudget.pin`, ratchet'ler), govulncheck/Trivy/CodeQL — yeni bağımlılık yok.

---

## 6. Uygulama planı (7 sürüm)

Depo PR dalı kullanmaz: her adım `main` üzerinde tek etiketli sürüm commit'idir (`/release`); CI kapıları (`go test -race`, vitest, govulncheck, Trivy, CodeQL) `main`'de koşar. **Geri alma tarifi (her sürüm için aynı):** `git revert <sürüm-sha>` → yeni `v0.10.X+1` etiketi; ayar blobları (`system_settings`) okuyansız kalır, zararsız; CH tabloları (PR 3) boş kalır, `DROP` ayrı ve elle. `internal/api/api.go` hiçbir sürümde değişmez (`TestApiGoDoesNotGrow`). **Her sürüm sonunda özet + onay.**

### 6.1 PR 1 — Toolset soyutlaması (74 araç → kaynak bazlı toolset'ler, aç/kapa, sağlık)

**Değişmezlik sözleşmesi:** varsayılan (hepsi etkin) durumda `ToolList`, `ChatToolList`, `ChatToolListFor`, `Register` çıktıları **ad, sıra, şema ve açıklama bazında bayt-bayt aynıdır**; 74 pini (`discovery_test.go:575`, `guided_parity_test.go:141`, `mcp_authz_test.go:297`) **sayı olarak değişmez**; `copilot_chat.go:566`'daki `toolsForRole(mcptools.ChatToolListFor(s.mcpDeps(), toolset), role)` literal'i korunur (kaynak pinleri `bubble_up_test.go:208`, `chat_tier_order_test.go:57`). Devre dışı bırakma yalnız **operatör açıkça kapattığında** katalogdan düşürür.

**Paket / dosya planı**

| Dosya | İçerik |
|---|---|
| `internal/mcptools/toolset.go` (yeni) | `type Toolset struct { Name string; Source string; Tools []string; Required func(d Deps) bool; Health func(ctx context.Context, d Deps) sourcestate.Status }`; `var Toolsets = []Toolset{…}` (sıra sabit, aşağıdaki tablo); `func ToolsetOf(name string) (Toolset, bool)`; `func ToolsetsFor(d Deps, disabled map[string]bool) []Toolset`; `func filterByToolsets(tools []mcp.Tool, disabled map[string]bool) []mcp.Tool` (sıra korunur). |
| `internal/mcptools/toolset_health.go` (yeni) | Toolset başına sağlık: **sorgu yapmaz**, mevcut problardan türetir (`capabilitiesFrom` `problem_tools.go`, `Deps` nil alanları, `d.LogStore.Backend()`, `d.MetricRouter`, `d.OracleOps.Enabled()`, `d.Wiki/SourceCode/CodeSearch`); isteğe bağlı 2 s'lik bağlantı yoklaması yalnız `Deps.Memo` üzerinden 60 s önbellekli (`Memo` `tools.go:278`). Dönüş `sourcestate.Status` — `ok` / `not_configured` / `unreachable` / `unauthorized` / `timeout` / `partial`; `Usable()` (`sourcestate.go:262`) = sunulabilir. |
| `internal/mcptools/tools.go` | `Deps` alanı ekle: `ToolsetsDisabled func() map[string]bool` (nil = hepsi etkin — mevcut çağıranlar derlenir, davranış aynı). `ChatToolList` ve `ChatToolListFor` sonunda `filterByToolsets(out, d.disabledToolsets())`; `Register` **katalog süzmez** (dış MCP `tools/list` boot'ta sabit, `main.go:1698-1707`), devre dışı çağrı `CallGate`'te reddedilir (aşağıda). Header sayım yorumu (`:62`) "74 araç · 12 toolset" olarak güncellenir. |
| `internal/mcptools/problem_tools.go` | `get_capabilities` sonucuna `toolsets: [{name, source, enabled, health, tools}]` alanı (ek alan; mevcut `capabilities/enabled/disabled` aynen — `problem_tools_test.go` pini korunur). |
| `internal/chstore/ai_toolsets.go` (yeni) | `system_settings["ai.toolsets"]` blobu: `{disabled:[string], updatedBy, updatedAt}`; `GetAIToolsets/PutAIToolsets`; süreç-içi `atomic.Pointer` + `CurrentAIToolsets()` (`sre/settings.go:139-200` deseni); `LoadAIToolsets` boot + `cfgRefresh.Add("ai-toolsets", …)` (`main.go:563`, 30 s). Normalize: bilinmeyen ad düşer, `meta` toolset'i kapatılamaz (sohbet durumu + `get_capabilities` + `build_link`), `required` toolset kapatılırsa 400. |
| `internal/api/ai_toolsets.go` (yeni) | `init(){ registerRoutesExtra("ai-toolsets", (*Server).registerAIToolsetsRoutes) }`; `GET /api/ai/toolsets` (her rol; `{toolsets:[{name,source,enabled,health:{state,note},tools:[…]}], rev}`; `serveCached` 10 s, anahtar `ai:toolsets:v1:disabled=<fnvDigest(sorted)>`), `PUT /api/ai/toolsets` (`auth.RequireRole(admin)`, gövde `{disabled:[…]}`, audit `settings.ai_toolsets.update`, yanıt yeni durum). `requireCopilot` **sarılmaz** (kurallar ucu emsali `cosre_rules.go:19-22`: AI yapılandırılmamışken de yönetilebilir). |
| `internal/api/mcp_deps.go` | `ToolsetsDisabled: func() map[string]bool { return chstore.CurrentAIToolsets().DisabledSet() }` tek kurucuya eklenir (`:42-100`); drain aynı kurucuyu kullandığından (`main.go:1677`) arka plan da süzgeci görür. |
| `internal/api/mcp_gate.go` | `mcpCallGate` (`:104+`): araç devre dışı toolset'teyse hata metni `"toolset <ad> operatör tarafından kapalı"` (rol reddi gibi adlandırılmış; model boşuna yeniden denemez). |
| `frontend/src/pages/settings/AiToolsetsPanel.tsx` (yeni) + `lib/types.ts` (`AIToolset`), `lib/api.ts` (`getAIToolsets/putAIToolsets`) | Ayarlar › AI › "Araç setleri": ≤12 sabit satırlı statik `<table>` (dört-tablo kuralı: ≤10 değil 12 → `useDataTable` **değil**, gerekçe yorumu "sabit katalog"), `Badge` sağlık, `Button` aç/kapa (admin), viewer salt-okunur görür. `/frontend-conventions` + `/frontend-design-system` gereği mevcut atomlar. |
| `docs/DECISIONS.md`, `docs/runbooks/mcp-claude-code.md` | Karar kaydı + runbook'ta toolset tablosu (74 pin yorumları bu dosyayı adres gösteriyor). |

**74 aracın toolset eşlemesi** (`TS` sütunu §1.4; her araç tam bir sette — `TestToolsetsPartitionCatalog`):

| Toolset | Kaynak | Araç (sayı) | `Required` | Sağlık türevi |
|---|---|---|---|---|
| `services` | CH servis/operasyon/topoloji/SLO MV'leri | list_services, list_teams, get_team_services, service_health_sweep, get_service_health, get_operation_health, get_topology, get_blast_radius, get_db_health, get_messaging_health, list_environments, list_clusters, list_slo_status, list_operations, compare_periods, render_chart, resolve_business_code (17) | evet | `Deps.Store` nil → not_configured; aksi ok |
| `traces` | CH `spans` / trace MV'leri | describe_attributes, find_attribute_by_value, get_trace, search_traces, query_traces, trace_stats, find_trace_by_span, bubble_up, pivot_from_trace, get_exemplar_traces, get_linked_traces, get_metrics_for_span (12) | evet | `Deps.Store`; Tempo yedeği `TraceWindow` |
| `problems` | problems / anomaly_events / root_cause_hypotheses / exception_groups / incident_cases | list_problems, list_problem_window_events, get_problem_root_cause, list_anomalies, list_exception_groups, get_exception_samples, get_problem, get_correlation_evidence, similar_problems, similar_incidents (10) | evet | `Deps.Store` |
| `changes` | deploy olayları / `service_version_5m` / `workload_rollouts` | get_correlated_changes, get_deploy_diff, list_deploys, list_deployments (4) | hayır | `Deps.Store`; `RolloutsEnabled` → detail |
| `metrics` | VictoriaMetrics / CH `metric_points` / Thanos | get_pod_health, query_metrics, query_metric, list_metric_names, list_metric_labels, cluster_metric (6) | hayır | `MetricRouter`/`Metrics` nil → not_configured; `ClusterMetrics` nil → partial (Thanos yok) |
| `logs` | logstore (ES/CH) | query_logs, search_logs, list_log_fields, get_log_histogram, explain_log_spike, find_trace_by_request_id, log_patterns, get_logs_for_trace (8) | hayır | `LogStore` nil → not_configured; `Backend()` detail |
| `k8s` | `entities` / Thanos | list_namespaces, list_workloads, list_pods, resolve_entity (4) | hayır | `EntityEnabled()` false → not_configured |
| `oracle` | `oracle_error_log` | resolve_oracle_operation (1) | hayır | `OracleOps.Enabled()` |
| `devops-code` | Azure DevOps Code Search / repo | search_code, read_source_code (2) | hayır | `CodeSearch`/`SourceCode` nil; **arka plan allowlist'i dışında (PR 2)** |
| `devops-wiki` | Azure DevOps wiki | search_wiki, read_wiki_page (2) | hayır | `Wiki` nil; **arka plan dışı** |
| `knowledge` | runbooks / rag_chunks / statik rehber | get_runbook, search_knowledge, product_guide (3) | hayır | `Store`; `RAGReady` detail |
| `meta` | sohbet durumu / saf yardımcılar | get_capabilities, build_link, set_context, get_context, clear_context (5) | evet (kapatılamaz) | ok |

Mevcut `chatOffered` (`tools.go:549-561`) ve `simpleHiddenTools` (`:600`) **aynen kalır** ve toolset süzgecinden **önce** uygulanır (bugünkü davranış); toolset yalnız ek bir süzgeç katmanıdır.

**Test stratejisi (hepsi saf / tablo-testli, CH-ES-VM yok)**
- `internal/mcptools/toolset_test.go` (mevcut dosyaya ek): `TestToolsetsPartitionCatalog` (74 aracın her biri tam bir toolset'te; birleşim = `ToolList` ad kümesi), `TestToolsetFilterKeepsOrder`, `TestChatToolListUnchangedWhenAllEnabled` (nil ve boş `ToolsetsDisabled` ile `ChatToolList`/`ChatToolListFor(full|simple)` ad+sıra+şema JSON'u eşit — "bayt-bayt" pini), `TestMetaToolsetCannotBeDisabled`, `TestDisabledToolsetHidesOnlyItsTools`.
- `toolset_health_test.go`: her toolset için `Deps{}` → beklenen durum tablosu; `Memo` sahteyle yoklama tek çağrı.
- `internal/chstore/ai_toolsets_test.go`: normalize (bilinmeyen ad düşer, required reddi), blob round-trip.
- `internal/api/ai_toolsets_test.go`: rota kaydı (`TestMuxRoutePatterns` çakışma), admin kapısı + audit, cache anahtarı tüm girdiyi taşır (`cache_key_test.go` emsali), `TestApiGoDoesNotGrow` yeşil.
- `mcp_gate` testi: devre dışı toolset → adlandırılmış ret.
- Pinler: 74 sayıları **değişmez**; `problem_tools_test.go` `get_capabilities` alanları ek alanla geçer; `short_desc_test.go`, `has_more_test.go`, `no_write_tool_test.go` değişmez (yeni araç yok); `bubble_up_test.go:208` literal korunur.
- Frontend: tsc/eslint/vitest; `tableUnityRatchet` için statik tablo gerekçe yorumu; `pollingBudget.pin` dokunulmaz (sayfa polling eklemez).

**Geri alma:** sürüm commit'i `git revert`; `ai.toolsets` blobu okuyansız kalır; katalog otomatik olarak HEAD davranışına döner (süzgeç kodla birlikte gider).

### 6.2 PR 2 — Investigation loop + bütçe + hipotez ağacı + inconclusive (flag, varsayılan kapalı)

- **Kapsam (karar 2b, 3, 4, 8):** deterministik dalgalar aynen; ardından `sre_agent.loop.enabled` iken sınırlı LLM döngüsü: `Score` çıktısı + `seed_hypothesis` + `SynthRef` → kök düğümler; dal başına ≤3 araç çağrısı, 2 paralel dal, ≤10 LLM çağrısı, token bütçesi; allowlist = `services`, `traces`, `problems`, `changes`, `metrics` (yalnız `query_metrics`), `logs` (yalnız `query_logs`, `explain_log_spike`, `get_logs_for_trace`), `knowledge` (yalnız `get_runbook`); model alt-hipotez önerebilir (`origin=llm`, `parent_id` dolu, derinlik ≤2); dal durumu `confirmed|rejected|inconclusive`; saf `outcomeOf`; `LocalOnly` profil kapısı. (c) tam döngüye geçiş yolu açık: allowlist ve "deterministik önce" bir **ayar**, mimari değil.
- **Dosyalar:** `internal/sre/{loop.go, tree.go, outcome.go}` (yeni), `hypotheses.go`, `plan.go` (db öznesi dalı), `investigation.go` (ağaç/outcome alanları — PR 3'e dek JSON'da), `narrate.go`, `settings.go`, `orchestrator.go`; `internal/copilot/prompts.go` (`SystemPromptSRELoop`, sicil değeri), `profiles.go` (`LocalOnly`); `internal/api/chat_source_code.go`/`copilot_code.go` (localOnly hard fail).
- **Test:** ağaç kurma / outcome eşikleri / ambiguous / bütçe tükenmesi tablo testleri; sahte anlatıcı + sahte araç koşucu (`chatLoopHandlerSeam` emsali) ile uçtan uca; korkuluklar: `TestSRENoMutatingCalls`, `TestLoopAllowlistHasNoCodeWikiFreeQuery`, `TestRulesNeverReachScoring`, `TestHumanMarksNeverReachOutcome`, `TestLocalOnlyHardFail`; `-race`.
- **Geri alma:** bayrak kapalı → kayıt bayt-bayt eski; sürüm revert.

### 6.3 PR 3 — Veri modeli (üç tablo) + karar 9

- **Kapsam:** §4.3 tabloları; bayrak arkasında çift yazım → yeni tablolardan okuma; `saved_views` satırı özet + işaretçi; eval okuması yeni tablolar; `ConfirmedRCASignatures` (`rca_verdict_store.go:459-466`) kaldırılır — aynı bilgi `ai_investigation_hypotheses.human_state='confirmed'` + onaylı `incident_cases`'ten türetilir; `ai_feedback` okuma yolu silinir; CLAUDE.md invariant 5 metni güncellenir.
- **Dosyalar:** `internal/chstore/ai_investigations.go` (yeni), `store.go migrate()`, `rca_verdict_store.go`, `internal/sre/{queue,investigation,eval}.go`, `internal/api/{sre_status,sre_eval}.go`, `CLAUDE.md`, `docs/DECISIONS.md`.
- **Test:** round-trip, migrasyon idempotans, `FINAL` okuma, checkpoint tutarlılığı (adım/hipotez önce, kayıt son), `TestRCASignaturesNeverReadVotes`; `/clickhouse-schema` incelemesi.
- **Geri alma:** bayrak → eski yol; tablolar boş kalır; sürüm revert.

### 6.4 PR 4 — Runbook enjeksiyonu (karar 10)

- **Kapsam:** araştırma/anlatım **öncesi** öncelik: Problem'e bağlı runbook (`resolveRunbookURL` `knowledge_tools.go:194`) → servisin wiki sayfası (`Wiki`, servis metadata/katalog bağlantısı) → genel runbook (`matchRunbooks`); **ilk 2 KB** `RUNBOOK (veri)` çitli blok olarak system prompt'a, `DataNotInstruction` sonda; sohbette yalnız Problem sayfası bağlamında.
- **Dosyalar:** `internal/sre/runbook_prompt.go` (yeni), `narrate.go`, `loop.go`, `internal/api/cosre_runbook_prompt.go` (yeni), `prompts.go` eki.
- **Test:** öncelik tablo testi, 2 KB kesme (rune sınırı), giriş pini (`TestRulesPromptSitesPinned` emsali), runbook yoksa prompt bayt-bayt eski.
- **Geri alma:** ayar kapalı → blok ""; sürüm revert.

### 6.5 PR 5 — Tetik politikası + API + MCP (karar 5, 6, 12, 13)

- **Kapsam:** §3.3 politikası + `exception_groups` ikinci aday kaynağı; `GET /api/investigations` (liste/filtre/sayfalama; **ayrı dosya** `internal/api/investigations.go`, `registerInvestigationRoutes()` via `registerRoutesExtra`); MCP: `get_investigation`, `list_investigation` (okuma, viewer), `trigger_investigation` (**ilk yazma aracı**: `MinRole editor` + audit `investigation.trigger`, `tools.go:33-46` politikası; **idempotent** — açık/koşan araştırma varsa onu döndürür; rate limit `CallGate` üzerinden: kullanıcı başına dakikada N, ayar); sohbet aynı aracı kullanır, serbest metin `seed_hypothesis`; sonuç bildirimi `confirmed|likely` için mevcut `notify.Notifier` kanalı; 74 → 77 pini üç yerde + `no_write_tool_test.go` allowlist'ine `trigger_investigation` (editor + audit gerekçesiyle).
- **Dosyalar:** `internal/sre/{enqueuer,policy}.go`, `internal/api/{investigations,chat_investigate}.go` (yeni), `internal/mcptools/investigation_tools.go` (yeni), `tools.go`, `mcp_gate.go`, `internal/notify` (yeni kanal yok, mevcut gönderici).
- **Test:** politika tablo testi (dışlama listesi birebir), idempotans, rate limit, rol/audit, sayı pinleri (mekanik), sohbet kademe sırası pini.
- **Geri alma:** ayar/bayrak; araçlar `chatOffered` ile gizlenir; sürüm revert.

### 6.6 PR 6 — UI ikili görünüm + kanıt zinciri

- **Kapsam:** `ProblemInvestigationPanel` sekmeleri **Akış** (`ai_investigation_steps`'ten canlı; yeni SSE türü `sre.investigation.step` → `eventInvalidations.ts`) ve **Sonuç** (hipotez ağacı, dal durumu, `inconclusive` için ayrı boş-durum/rozet — sonuç gibi çizilmez; `ambiguous` iki kart yan yana); araştırma toplam token/çağrı (karar 11); kanıt çipleri `EvidenceCard`/`ChatEvidence`; Problem listesinde outcome sütunu; `/api/investigations` sayfası.
- **Dosyalar:** `frontend/src/features/anomalies/ProblemInvestigation*.tsx`, `components/ai/EvidenceCard.tsx`, `lib/{types,api}.ts`, `lib/queries/eventInvalidations.ts`, `pages/Investigations.tsx` (yeni).
- **Test:** tsc/eslint/vitest; `pollingBudget.pin` (yeni polling yok, SSE), ratchet'ler; saf görünüm yardımcıları tablo testi.
- **Geri alma:** bayrak → eski panel; sürüm revert.

### 6.7 PR 7 — Hafıza (karar 7)

- **Kapsam:** salt-okunur ön bağlam, granülarite **servis + problem tipi** (rule_id öneki / exception grubu); 90 gün; son N araştırma özeti (`ai_investigations`), onaylı `incident_cases`, `human_note` metinleri → `ÖNCEKİ ARAŞTIRMALAR (veri)` bloğu (≤1500 rune); `prior_context` izi; endpoint seviyesi yok; oy/puan asla.
- **Dosyalar:** `internal/sre/memory.go` (yeni), `narrate.go`, `loop.go`, `internal/chstore/ai_investigations.go`.
- **Test:** granülarite seçimi tablo testi, boyut tavanı, `TestMemoryNeverReadsVotesOrScores`, prompt giriş pini.
- **Geri alma:** ayar kapalı → blok ""; sürüm revert.

---

## 7. Kararlar (operatör, 2026-10-22)

| # | Soru | Karar | Sonuç / nerede uygulanır |
|---|---|---|---|
| 1 | Invariant 5 vs yeni tablolar | **Evet, üç tablo**; `saved_views`'a yalnız özet + `investigation_id` | §4.3; invariant 5 metni güncellenir (PR 3, CLAUDE.md + DECISIONS.md) |
| 2 | Arka planda LLM döngüsü | **(b)** deterministik dalgalar → allowlist'li sınırlı döngü; flag; (c) yolu kapatılmaz | PR 2; allowlist + "deterministik önce" ayar olarak |
| 3 | Bütçe / paralellik / latency | hipotez başına 3 araç, 2 paralel dal, ≤5 dk, ≤10 LLM çağrısı/araştırma, 30/saat korunur (dolunca kuyrukta bekler), araştırma başına token bütçesi (aşım → inconclusive) | §5 tablosu; `itemBudgetSec` 300; drain claim kapısı (§3.3) |
| 4 | Inconclusive eşiği | **İkisi de**: skor < 0.40 veya kapsam < %50 → `inconclusive`; ilk iki dal farkı < 0.10 → `ambiguous` (iki hipotez birlikte) | `outcomeOf` (PR 2); outcome enum (PR 3); UI (PR 6) |
| 5 | MCP yazma aracı | `trigger_investigation` (idempotent, rate limit), `get_investigation`, `list_investigation`; HTTP `GET /api/investigations` ayrı dosya `registerInvestigationRoutes()` | PR 5; pin 74 → 77; `no_write_tool_test.go` allowlist (editor + audit) |
| 6 | Otomatik tetik türleri | servis + db + exception grubu; P1/P2; ≤15 dk; Oracle shadow/external ikinci faz (açıkça dışlanır); geri tarihli anomaliler yalnız manuel | §3.3 politika tablosu; **exception grupları Problem satırı değil → ikinci aday kaynağı** (PR 5) |
| 7 | Hafıza granülaritesi | servis + problem tipi; 90 gün; endpoint yok | PR 7; tablo TTL 90 g |
| 8 | Sağlayıcı kapısı | **localOnly kodda zorunlu**; allowlist dışı araçlar arka planda hiç sunulmaz; kod/wiki içeriği harici sağlayıcıya giderse hard fail + log | PR 2 (`ModelProfile.LocalOnly`, hard fail), PR 1 toolset süzgeci altyapısı |
| 9 | `ConfirmedRCASignatures` | Kaldırılır; bilgi onaylı insan işaretlerinden türetilir; 👍 okuma yolu silinir | PR 3 |
| 10 | Runbook önceliği | Problem runbook'u → servis wiki sayfası → genel runbook; ilk 2 KB araştırma öncesi system prompt'a | PR 4 |
| 11 | Token muhasebesi | Adım bazında (`ai_investigation_steps`); araştırma toplamı UI'da | PR 3 şema, PR 6 UI |
| 12 | Sonuç bildirimi | Problem sayfası + mevcut bildirim kanalı; yeni kanal yok | PR 5 (`confirmed|likely`) |
| 13 | Sohbetten tetik | Aynı `trigger_investigation` aracı; serbest metin → kök `seed_hypothesis` | PR 5; şema `seed_hypothesis`, ağaçta `origin=seed` |

**Plan güncellendi, PR 1 için hazır.**
