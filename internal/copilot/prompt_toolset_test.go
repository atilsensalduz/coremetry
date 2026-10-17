package copilot

import (
	"regexp"
	"strings"
	"testing"
)

// v0.10.1302 — CoSRE: modele göre araç seti. simple katalogda gizli dört
// serbest-sorgu aracı (query_metric / search_logs / search_traces /
// list_metric_labels) prompt metninde anılmamalı — model sunulmayan aracı
// çağırıp "unknown tool" turu harcar. full varyant bayt bayt eski.
var hiddenToolRe = regexp.MustCompile(`\b(search_traces|search_logs|query_metric|list_metric_labels)\b`)

func TestSimpleToolsetPromptVariants(t *testing.T) {
	if SystemPromptChatAgentLoopFor(false) != SystemPromptChatAgentLoop() || TraceFollowUpAddendumFor(false) != TraceFollowUpAddendum() {
		t.Fatal("full varyant bayt bayt eski olmalı")
	}
	for name, txt := range map[string]string{
		"agentLoop.simple": SystemPromptChatAgentLoopFor(true),
		"traceFU.simple":   TraceFollowUpAddendumFor(true),
	} {
		if m := hiddenToolRe.FindString(txt); m != "" {
			t.Errorf("%s gizli aracı anıyor: %s", name, m)
		}
		if strings.Contains(txt, "query_metricss") {
			t.Errorf("%s: replacer query_metrics'i bozdu", name)
		}
	}
	// Ön koşul: full metin gizli araçları GERÇEKTEN anıyor (yoksa test boş koşar).
	if !hiddenToolRe.MatchString(SystemPromptChatAgentLoop()) || !hiddenToolRe.MatchString(TraceFollowUpAddendum()) {
		t.Fatal("full prompt'lar gizli araçları anmıyor — test bayatladı")
	}
	loop := SystemPromptChatAgentLoopFor(true)
	if !strings.Contains(loop, "query_traces / trace_stats / query_logs") {
		t.Errorf("adım 4 yapısal araçları anmalı:\n%s", loop)
	}
	if fu := strings.Join(strings.Fields(TraceFollowUpAddendumFor(true)), " "); !strings.Contains(fu, "(query_metrics)") {
		t.Errorf("trace takibi metrik adımı query_metrics olmalı: %s", fu)
	}
}

func TestToolsetTextReplacer(t *testing.T) {
	cases := map[string]string{
		"search_traces / trace_stats / search_logs'u":  "query_traces / trace_stats / query_logs'u",
		"(list_metric_labels, ardından query_metric)":  "(query_metrics)",
		"query_metrics ile":                            "query_metrics ile",
		"query_metric ile":                             "query_metrics ile",
		"(search_logs, compare_periods, query_metric)": "(query_logs, compare_periods, query_metrics)",
		"list_metric_labels ile doğrula":               "query_metrics ile doğrula",
	}
	for in, want := range cases {
		if got := ToolsetText(in, true); got != want {
			t.Errorf("ToolsetText(%q) = %q, want %q", in, got, want)
		}
		if got := ToolsetText(in, false); got != in {
			t.Errorf("full metne dokunmamalı: %q", got)
		}
	}
}

// *.simple anahtarları iki sicilde de kayıtlı (sürüm hash'i + dil kapısı).
func TestSimpleVariantsRegistered(t *testing.T) {
	for _, k := range []string{"systemChatAgentLoop.simple", "traceFollowUpAddendum.simple"} {
		if v, ok := promptVersionRegistry[k]; !ok || v == "" || hiddenToolRe.MatchString(v) {
			t.Errorf("promptVersionRegistry[%q] eksik ya da gizli araç anıyor", k)
		}
	}
	if promptRegistry()["ChatAgentLoop.simple"] != classTurkishNative || promptTexts()["ChatAgentLoop.simple"] != SystemPromptChatAgentLoopFor(true) {
		t.Error("ChatAgentLoop.simple promptRegistry/promptTexts'te kayıtlı değil")
	}
}
