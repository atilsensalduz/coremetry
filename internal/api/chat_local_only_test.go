package api

// chat_local_only_test.go — v0.10.1451 (karar 8, operasyon kartı planı R1):
// yerel profil yokken kod/wiki yolları "kapalı: yerel profil yok" der, araçlar
// sunulmaz ve prompt + katalog o özellikler kapalıymış gibi bayt bayt aynı;
// yerel profil varsa alışveriş o profile gider. Fikstürler gerçek copilot
// taşır (localOnlyTestSeam yalnız copilot'suz sunucuda devreye girer).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// TestCodeToolsAbsentWithoutLocalProfile — panel trace takibi (read_source_code
// normalde sunulur): DevOps bağlı ama hiçbir profil yerel değil → katalog ve
// sistem prompt'u DevOps'suz koşuyla BAYT BAYT aynı + dürüst step; tikle
// araç geri gelir.
func TestCodeToolsAbsentWithoutLocalProfile(t *testing.T) {
	run := func(withDevOps, local bool) (map[string]any, []chatFrame) {
		llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("tamam") })
		s, _ := newLoopTestServer(t, llm, "u-a")
		if !local {
			untickLocalOnly(s.copilot)
		}
		if withDevOps {
			_, s.devops = newSCGit(t)
		} else {
			s.devops = devops.New()
		}
		frames, _ := scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("Özetle?", anchoredToMs(), "conv-lo"))
		calls := llm.calls()
		if len(calls) != 1 {
			t.Fatalf("model %d kez çağrıldı", len(calls))
		}
		return calls[0], frames
	}
	off, _ := run(false, false)
	closed, frames := run(true, false)
	if hasToolName(llmToolNames(closed), mcptools.SourceCodeToolName) {
		t.Fatal("yerel profil yokken read_source_code sunuldu")
	}
	if llmSystem(closed) != llmSystem(off) {
		t.Fatal("yerel profil yokken sistem prompt'u DevOps'suz koşudan farklı (bayt kimliği)")
	}
	ct, _ := json.Marshal(closed["tools"])
	ot, _ := json.Marshal(off["tools"])
	if string(ct) != string(ot) {
		t.Fatal("yerel profil yokken katalog DevOps'suz koşudan farklı")
	}
	if !strings.Contains(strings.Join(stepLabels(frames), "\n"), "kapalı: yerel profil yok") {
		t.Fatalf("dürüst step yok: %v", stepLabels(frames))
	}
	open, _ := run(true, true)
	if !hasToolName(llmToolNames(open), mcptools.SourceCodeToolName) {
		t.Fatal("yerel profil varken read_source_code sunulmalı")
	}
}

// TestChatCodeTurnGoesToLocalProfile — varsayılan profil yerel değil, başka
// bir profil yerel: kod araçlı alışverişin TÜM model turları yerel uca gider,
// dış uç 0 istek alır.
func TestChatCodeTurnGoesToLocalProfile(t *testing.T) {
	ext := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("dış") })
	loc := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("yerel") })
	s, _ := newLoopTestServer(t, ext, "u-a")
	s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: copilot.DefaultProfileID, Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "qwen3-32b", BaseURL: ext.srv.URL},
		{ID: "loc", Provider: copilot.ProviderOpenAI, APIKey: "test-key", Model: "gemma4-31b", BaseURL: loc.srv.URL, LocalOnly: true},
	}, copilot.DefaultProfileID, nil)
	_, s.devops = newSCGit(t)
	scPost(t, s, "u-a", auth.RoleEditor, traceDrawerBody("Özetle?", anchoredToMs(), "conv-lo2"))
	if len(ext.calls()) != 0 {
		t.Fatalf("kod araçlı tur dış profile gitti (%d istek)", len(ext.calls()))
	}
	if len(loc.calls()) == 0 || !hasToolName(llmToolNames(loc.calls()[0]), mcptools.SourceCodeToolName) {
		t.Fatal("yerel profil kod araçlı turu almadı")
	}
}

// TestExplainCodeClosedWithoutLocalProfile — ✨ "Kodu da incele": yerel
// profil yokken kod HİÇ çekilmez (sahte git 0 istek) ve gerekçe dürüst; kod
// bloğu yine de kurulmuşsa (arka kapı) çağrı ErrLocalOnly ile düşer, sağlayıcı
// 0 istek.
func TestExplainCodeClosedWithoutLocalProfile(t *testing.T) {
	fp := newFakeProvider(t, false)
	s := codeServer(t, fp, newCapRecorder())
	untickLocalOnly(s.copilot)
	git, dv := newSCGit(t)
	s.devops = dv
	cc := s.buildCodeContext(context.Background(), "payments", "java.lang.IllegalStateException: x\n\tat com.example.pay.ChargeHandler.charge(ChargeHandler.java:120)", "")
	if cc.Reason != localOnlyClosedReason || !cc.Empty() || len(git.requests()) != 0 {
		t.Fatalf("yerel profil yokken kod çekildi: reason=%q windows=%d git=%d", cc.Reason, len(cc.Windows), len(git.requests()))
	}
	op := s.buildOperationCodeContext(context.Background(), traceExplainInput{RootService: "payments", MainOperation: "POST /charge"})
	if op.Reason != localOnlyClosedReason && op.Reason != devops.OperationSearchOffReason {
		t.Fatalf("operasyon kodu kapısı: %q", op.Reason)
	}

	block := devops.CodeContext{Repo: "payments", Windows: []devops.CodeWindow{{Path: "/src/A.java", Line: 3, FromLine: 1, ToLine: 5, Content: "3| " + secretCodeMarker}}}
	r := httptest.NewRequest(http.MethodPost, "/api/copilot/explain-exception/fp1", nil)
	_, err := s.copilotExplainCode(r, copilot.SystemPromptException(), copilot.SystemPromptExceptionWithCode(), "Exception GRUBU: x", block)
	if !errors.Is(err, copilot.ErrLocalOnly) {
		t.Fatalf("kod bloğu yerel olmayan profile gidecekti, err=%v", err)
	}
	for _, p := range fp.sent() {
		if strings.Contains(p, secretCodeMarker) {
			t.Fatal("kod yerel olmayan sağlayıcıya ulaştı")
		}
	}
	// Tikle aynı çağrı geçer ve kod modele gider.
	tickLocalOnly(s.copilot)
	if _, err := s.copilotExplainCode(r, copilot.SystemPromptException(), copilot.SystemPromptExceptionWithCode(), "Exception GRUBU: x", block); err != nil {
		t.Fatalf("yerel profilde: %v", err)
	}
	found := false
	for _, p := range fp.sent() {
		found = found || strings.Contains(p, secretCodeMarker)
	}
	if !found {
		t.Fatal("yerel profilde kod modele gitmedi")
	}
}

// TestWikiTierHonestWithoutLocalProfile — açık wiki sorusu, yerel profil yok:
// anlatım ÇAĞRILMAZ, cevap dürüst "kapalı"; işaretsiz soru kademeyi sessizce
// geçer (handled=false).
func TestWikiTierHonestWithoutLocalProfile(t *testing.T) {
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": runbookText}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	called := false
	prev := wikiNarrateFn
	wikiNarrateFn = func(*Server, context.Context, string, string) (string, error) { called = true; return "x", nil }
	t.Cleanup(func() { wikiNarrateFn = prev })
	cop := copilot.New(copilot.ProviderOpenAI, "k", "gemma4")
	cop.Configure(copilot.ProviderOpenAI, "k", "gemma4", "http://vllm.example.test/v1", false, true)
	s := &Server{copilot: cop}

	ev, emit := collectEmit()
	h, ok := s.wikiChatAnswer(sessionCtx(), emit, []copilot.ChatMessage{{Role: "user", Text: "svc-orders runbook'unda yeniden başlatma nasıl?"}}, wikiTierContext{})
	if !h || !ok || called {
		t.Fatalf("açık wiki sorusu: handled=%v ok=%v narrator=%v", h, ok, called)
	}
	if a := answerOf(t, *ev); a["text"] != localOnlyWikiClosedText {
		t.Fatalf("dürüst cevap yok: %v", a["text"])
	}
	_, emit2 := collectEmit()
	if h, _ := s.wikiChatAnswer(sessionCtx(), emit2, []copilot.ChatMessage{{Role: "user", Text: "kubectl rollout restart deploy komutunu çalıştırın"}}, wikiTierContext{}); h || called {
		t.Fatal("işaretsiz soru kademede kaldı / anlatım çağrıldı")
	}
}

func TestWikiHitsSensitivity(t *testing.T) {
	loc := wiki.Hit{ChunkRef: wiki.ChunkRef{WikiID: wiki.LocalWikiID}}
	dev := wiki.Hit{ChunkRef: wiki.ChunkRef{WikiID: "kx-wiki"}}
	if wikiHitsSensitive([]wiki.Hit{loc}) || !wikiHitsSensitive([]wiki.Hit{loc, dev}) {
		t.Fatal("wikiHitsSensitive")
	}
	if got := localDocHitsOnly([]wiki.Hit{dev, loc, dev}); len(got) != 1 || got[0].WikiID != wiki.LocalWikiID {
		t.Fatalf("localDocHitsOnly: %+v", got)
	}
	if localDocHitsOnly([]wiki.Hit{dev}) != nil {
		t.Fatal("kurum wiki'si yalnızken nil bekleniyordu")
	}
}

func TestSensitiveChatToolHelpers(t *testing.T) {
	names := []string{mcptools.SourceCodeToolName, mcptools.SearchCodeToolName, "search_wiki", "read_wiki_page"}
	for _, n := range names {
		if !isSensitiveChatTool(n) {
			t.Errorf("%s hassas sayılmalı", n)
		}
	}
	if isSensitiveChatTool("get_service_health") {
		t.Fatal("telemetri aracı hassas sayıldı")
	}
}

// TestCodeReadConfigShape — /api/copilot/config codeRead: DevOps yoksa alan
// yok; yerel profil yoksa kapalı + gerekçe; varsa yalnız ETİKET (adres yok).
func TestCodeReadConfigShape(t *testing.T) {
	if copilotCodeReadFor(false, "loc", "Yerel") != nil {
		t.Fatal("DevOps yokken codeRead dönmemeli")
	}
	if c := copilotCodeReadFor(true, "", ""); c.Available || c.Reason != localOnlyClosedReason {
		t.Fatalf("kapalı: %+v", c)
	}
	c := copilotCodeReadFor(true, "loc", "")
	raw, _ := json.Marshal(c)
	if !c.Available || c.ProfileLabel != "loc" || strings.Contains(string(raw), "http") {
		t.Fatalf("açık: %s", raw)
	}
}

// TestLocalOnlySitesWired — kaynak pini: kod/wiki taşıyan her yol işaret/kapı
// çağrısını kaybetmesin (mutasyon: satır silinirse kırmızı).
func TestLocalOnlySitesWired(t *testing.T) {
	pins := map[string][]string{
		"copilot_chat.go":           {"ctx, lo = s.chatLocalOnly(ctx, tools)", "tools = dropSensitiveChatTools(tools)"},
		"copilot_drawer.go":         {"s.routeSensitive(ctx, copilot.SensitiveCode)"},
		"copilot_code.go":           {"if !s.localOnlyAvailable(ctx) {", "copilot.SensitiveCode)", "devops.MaskCodeInPrompt(full, block, summary), true)"},
		"copilot_code_operation.go": {"if !s.localOnlyAvailable(ctx) {"},
		"chat_wiki_tier.go":         {"s.routeSensitiveStep(ctx, copilot.SensitiveWiki, selectEmit)", "if !s.localOnlyAvailable(ctx) {"},
		"chat_wiki_followup.go":     {"s.routeSensitiveStep(ctx, copilot.SensitiveWiki, emit)"},
		"rag.go":                    {"s.routeSensitiveStep(ctx, copilot.SensitiveWiki, emit)", "wikiHits = localDocHitsOnly(wikiHits)"},
		"chat_local_only.go":        {"rctx, ok := s.routeSensitive(ctx, kind)"}, // v0.10.1467 — Step/Switch aynı kapıdan
		"ai_observability.go":       {"s.routeSensitive(ctx, o.sensitive)"},
	}
	for f, want := range pins {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := stripGoCommentsAPI(string(b))
		for _, w := range want {
			if !strings.Contains(src, w) {
				t.Errorf("%s localOnly bağlantısını kaybetti: %q", f, w)
			}
		}
	}
	// Katalog kararı byName kurulumundan ÖNCE (yoksa düşen araç çağrılabilir kalır).
	b, _ := os.ReadFile("copilot_chat.go")
	src := stripGoCommentsAPI(string(b))
	if i, j := strings.Index(src, "s.chatLocalOnly(ctx, tools)"), strings.Index(src, "byName := make(map[string]mcp.ToolHandler"); i < 0 || j < 0 || i > j {
		t.Fatal("localOnly katalog kararı byName'den önce olmalı")
	}
}

// TestLocalOnlyHintNamesRealSettingsTab — v0.10.1457 (operator-reported:
// "Settings altında AI yok"): the honest closed text must point at the tab
// that exists (Settings › CoSRE, slug "ai"), not "Ayarlar › AI".
func TestLocalOnlyHintNamesRealSettingsTab(t *testing.T) {
	for _, s := range []string{localOnlyClosedReason, localOnlyWikiClosedText} {
		if !strings.Contains(s, "Settings › CoSRE › Model profilleri") || strings.Contains(s, "Ayarlar › AI") {
			t.Errorf("yanlış ayar yolu: %q", s)
		}
	}
	tab, err := os.ReadFile("../../frontend/src/pages/settings/tabIndex.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tab), "{ slug: 'ai', label: 'CoSRE' }") {
		t.Error("Settings sekme adı değişti — localOnly metnindeki yolu güncelle")
	}
}
