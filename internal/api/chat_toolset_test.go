package api

// v0.10.1302 — CoSRE: modele göre araç seti (küçük modelde serbest-sorgu
// araçları gizli). Ayar Settings → AI profilleri üzerinden yazılır (gövde →
// profil → görünüm → audit); serbest döngünün koştuğu varyant etkileşim
// satırına (cosre_interactions.toolset) iner; trace takibi talimatı simple
// varyantta gizli araçları anmaz.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func TestPutAIProfileToolsetRoundTrip(t *testing.T) {
	var in aiProfileInput
	if err := json.Unmarshal([]byte(`{"provider":"openai","baseUrl":"http://llm/v1","model":"gemma4-31b","toolset":" simple "}`), &in); err != nil {
		t.Fatal(err)
	}
	p := aiProfileFromInput("big", in)
	if p.Toolset != "simple" {
		t.Fatalf("gövdeden toolset: %q", p.Toolset)
	}
	if err := copilot.ValidateProfile(p); err != nil {
		t.Fatalf("geçerli profil reddedildi: %v", err)
	}
	if audit := aiProfileUpsertAudit(p, false); !strings.Contains(audit, " toolset=simple") {
		t.Errorf("audit toolset eki yok: %q", audit)
	}
	views := aiProfileViews([]copilot.ModelProfile{p, {ID: "small", Provider: "openai", Model: "qwen3.5-2b"}, {ID: "c", Provider: "anthropic", Model: "claude-sonnet-5"}}, "big")
	raw, _ := json.Marshal(views)
	var back []map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back[0]["toolset"] != "simple" || back[0]["effectiveToolset"] != "simple" {
		t.Errorf("açık ayar görünümde: %v", back[0])
	}
	if _, ok := back[1]["toolset"]; ok || back[1]["effectiveToolset"] != "simple" {
		t.Errorf("otomatik (2b) görünüm: %v", back[1])
	}
	if back[2]["effectiveToolset"] != "full" {
		t.Errorf("otomatik (claude) görünüm: %v", back[2])
	}
	// Otomatikte audit satırı bayt bayt eski; geçersiz değer doğrulamada düşer.
	p.Toolset = ""
	if strings.Contains(aiProfileUpsertAudit(p, false), "toolset") {
		t.Error("otomatik ayar audit'e eklenmemeli")
	}
	in.Toolset = "x"
	if err := copilot.ValidateProfile(aiProfileFromInput("big", in)); err == nil {
		t.Error("toolset \"x\" reddedilmeli")
	}
}

func TestCosreInteractionCarriesToolset(t *testing.T) {
	x := newChatExchange("ex1", "soru", false, time.Now())
	if r := buildCosreInteraction(x, "guided", copilot.CallMeta{}, "ok", "", time.Now()); r.Toolset != "" {
		t.Errorf("döngüye girmeyen kademe toolset taşımamalı: %q", r.Toolset)
	}
	x.setToolset("simple")
	if r := buildCosreInteraction(x, "loop", copilot.CallMeta{}, "ok", "", time.Now()); r.Toolset != "simple" {
		t.Errorf("döngü toolset'i satıra inmedi: %q", r.Toolset)
	}
	var nilX *chatExchange
	nilX.setToolset("full") // nil güvenli
}

var hiddenToolInPromptRe = regexp.MustCompile(`\b(search_traces|search_logs|query_metric|list_metric_labels)\b`)

func TestTraceFollowUpPromptFollowsToolset(t *testing.T) {
	tf := traceFollowUp{TraceID: tfTrace} // pencere yok → pencere satırı da gelir
	full := traceFollowUpPromptTR(&tf, "")
	if !hiddenToolInPromptRe.MatchString(full) {
		t.Fatal("full takip talimatı gizli araçları anmıyor — test bayatladı")
	}
	tf.Simple = true
	simple := traceFollowUpPromptTR(&tf, "")
	if m := hiddenToolInPromptRe.FindString(simple); m != "" {
		t.Errorf("simple takip talimatı gizli aracı anıyor: %s", m)
	}
	if !strings.Contains(simple, "query_logs") || !strings.Contains(simple, "query_metrics") {
		t.Errorf("simple takip yapısal karşılıkları anmalı:\n%s", simple)
	}
}

// Uçtan uca: sohbet model seçicisi (context.profile) küçük modeli seçince
// serbest döngünün kataloğu ve sistem mesajı simple varyant; seçimsiz istek
// (varsayılan büyük model) full varyant. Gizli ada model çağrısı "unknown
// tool" alır (byName aynı listeden).
func TestChatLoopToolsetFollowsComposerPicker(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("**Bulgu** — tamam.") })
	s, _ := newLoopTestServer(t, llm, "u-a")
	s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "gemma4-31b"},
		{ID: "small", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "qwen3.5-2b"},
	}, "big", nil)
	ft := &fakeTools{}
	ft.install(t)

	run := func(profile string) (map[string]bool, string, string) {
		before := len(llm.calls())
		body := traceDrawerBody("Bu hata neden oluyor?", anchoredToMs(), "conv-"+profile)
		if profile != "" {
			body["context"].(map[string]any)["profile"] = profile
		}
		postLoopChat(t, s, context.Background(), "u-a", body)
		calls := llm.calls()
		if len(calls) <= before {
			t.Fatalf("profil %q: model çağrılmadı", profile)
		}
		names := map[string]bool{}
		for _, n := range llmToolNames(calls[before]) {
			names[n] = true
		}
		specs, _ := json.Marshal(calls[before]["tools"])
		return names, llmSystem(calls[before]), string(specs)
	}
	full, fullSys, fullSpecs := run("")
	simple, simpleSys, simpleSpecs := run("small")
	// Spec'ler (açıklama + ŞEMA) işaret sızdırmaz; simple spec gizli aracı anmaz.
	if strings.ContainsAny(fullSpecs, "⟦⟧") || strings.ContainsAny(simpleSpecs, "⟦⟧") {
		t.Error("araç spec'lerinde ⟦full|simple⟧ işareti kaldı")
	}
	if m := hiddenToolInPromptRe.FindString(simpleSpecs); m != "" {
		t.Errorf("simple spec'ler (açıklama/şema) gizli aracı anıyor: %s", m)
	}
	for _, n := range []string{"query_metric", "search_logs", "search_traces", "list_metric_labels"} {
		if !full[n] {
			t.Errorf("varsayılan (31b) katalogda %s olmalı", n)
		}
		if simple[n] {
			t.Errorf("küçük model (2b) kataloğunda %s GİZLİ olmalı", n)
		}
	}
	if !simple["query_logs"] || !simple["query_traces"] || !simple["query_metrics"] || !simple["get_logs_for_trace"] {
		t.Errorf("simple katalog yapısal araçları taşımalı: %v", simple)
	}
	if !hiddenToolInPromptRe.MatchString(fullSys) {
		t.Error("full sistem mesajı gizli araçları anmıyor — test bayatladı")
	}
	if m := hiddenToolInPromptRe.FindString(simpleSys); m != "" {
		t.Errorf("simple sistem mesajı gizli aracı anıyor: %s", m)
	}
}

// Araç YÜRÜTMESİ döngünün araç setini ctx'te görür (sonuç ipuçları buna göre
// basılır): küçük model seçiliyken handler ctx'i simple, varsayılanda full.
func TestChatLoopToolExecutionCarriesToolset(t *testing.T) {
	llm := newLoopLLM(t, func(n int, _ map[string]any) map[string]any {
		if n%2 == 0 {
			return toolCallMsg([2]string{"get_logs_for_trace", `{"trace_id":"` + tfTrace + `"}`})
		}
		return answerMsg("**Bulgu** — tamam.")
	})
	s, _ := newLoopTestServer(t, llm, "u-a")
	s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "gemma4-31b"},
		{ID: "small", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "qwen3.5-2b"},
	}, "big", nil)
	var seen []bool
	chatLoopHandlerSeam = func(_ string, _ mcp.ToolHandler) mcp.ToolHandler {
		return func(ctx context.Context, _ json.RawMessage) (any, error) {
			seen = append(seen, mcp.SimpleToolsetFrom(ctx))
			return map[string]any{"source": map[string]any{"state": "ok"}}, nil
		}
	}
	t.Cleanup(func() { chatLoopHandlerSeam = nil })
	for _, profile := range []string{"", "small"} {
		body := traceDrawerBody("Bu hata neden oluyor?", anchoredToMs(), "conv-x"+profile)
		if profile != "" {
			body["context"].(map[string]any)["profile"] = profile
		}
		postLoopChat(t, s, context.Background(), "u-a", body)
	}
	if len(seen) != 2 || seen[0] || !seen[1] {
		t.Fatalf("handler ctx araç seti (varsayılan, küçük) = %v, want [false true]", seen)
	}
}
