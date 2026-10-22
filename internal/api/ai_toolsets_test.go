package api

// ai_toolsets_test.go — v0.10.1434 (CoSRE araştırma programı PR 1, §6.1):
// toolset yüzeyi — rol kapısı + audit + settings round-trip, PUT doğrulaması
// (bilinmeyen düşer, kapatılamaz 400), cache anahtarı tüm girdiyi taşır,
// dış MCP kapısının adlandırılmış reddi (rate bütçesini tüketmez), sohbet
// kataloğu rol başına altınla bayt-bayt aynı (hepsi açıkken) ve kapalı
// toolset sohbet kataloğundan düşer.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

type toolsetsFakeStore struct {
	mu    sync.Mutex
	saved chstore.AIToolsets
	saves int
}

func (f *toolsetsFakeStore) SaveAIToolsets(_ context.Context, a chstore.AIToolsets) (chstore.AIToolsets, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	f.saved = chstore.NormalizeAIToolsets(a)
	return f.saved, nil
}

func newToolsetsTestEnv(t *testing.T) (*Server, *http.ServeMux, *toolsetsFakeStore) {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &toolsetsFakeStore{}
	prevStore, prevCur := aiToolsetsStoreOf, chstore.CurrentAIToolsets()
	aiToolsetsStoreOf = func(*Server) aiToolsetsStore { return st }
	t.Cleanup(func() { aiToolsetsStoreOf = prevStore; chstore.SetAIToolsets(prevCur) })
	chstore.SetAIToolsets(chstore.AIToolsets{})
	mux := http.NewServeMux()
	s.registerAIToolsetsRoutes(mux)
	return s, mux, st
}

func TestAIToolsetsRolesAuditRoundTrip(t *testing.T) {
	s, mux, st := newToolsetsTestEnv(t)

	// Viewer GÖRÜR: 12 satır, hepsi açık.
	w := rulesDo(mux, "GET", "/api/ai/toolsets", "", auth.RoleViewer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("viewer GET %d: %s", w.Code, w.Body)
	}
	var got aiToolsetsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Toolsets) != 12 || len(got.Disabled) != 0 {
		t.Fatalf("GET: %d set, kapalı %v", len(got.Toolsets), got.Disabled)
	}
	for _, ts := range got.Toolsets {
		if !ts.Enabled || len(ts.Tools) == 0 || ts.Health.State == "" {
			t.Errorf("%s: %+v", ts.Name, ts)
		}
	}

	// Viewer / editor yazamaz; yazım ve audit yok.
	body := `{"disabled":["logs","devops-code"]}`
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := rulesDo(mux, "PUT", "/api/ai/toolsets", body, role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT %d, 403 bekleniyordu", role, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı / audit yazmamalı")
	}

	// Admin kapatır (bilinmeyen ad düşer) → kayıt + audit + süreç-içi küme.
	w = rulesDo(mux, "PUT", "/api/ai/toolsets", `{"disabled":["logs","devops-code","no-such-set"," LOGS "]}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin PUT %d: %s", w.Code, w.Body)
	}
	var put struct {
		aiToolsetsResponse
		Ignored []string `json:"ignored"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if strings.Join(put.Disabled, ",") != "devops-code,logs" || strings.Join(put.Ignored, ",") != "no-such-set" || put.UpdatedBy != "admin@example.test" || put.Rev == 0 {
		t.Fatalf("PUT cevabı: %+v", put)
	}
	if strings.Join(st.saved.Disabled, ",") != "devops-code,logs" {
		t.Fatalf("saklanan: %+v", st.saved)
	}
	if cur := chstore.CurrentAIToolsets().DisabledSet(); !cur["logs"] || !cur["devops-code"] || len(cur) != 2 {
		t.Fatalf("süreç-içi küme güncellenmedi: %v", cur)
	}
	a := nextAudit(t, s)
	if a.Action != "settings.ai_toolsets.update" || a.TargetID != chstore.AIToolsetsKey ||
		!strings.Contains(a.Details, `"after":["devops-code","logs"]`) || !strings.Contains(a.Details, `"before":[]`) || !strings.Contains(a.Details, `"ignored":["no-such-set"]`) {
		t.Fatalf("audit = %+v", a)
	}

	// GET artık kapalıları gösterir (anahtar değişti → bayat önbellek yok).
	w = rulesDo(mux, "GET", "/api/ai/toolsets", "", auth.RoleViewer, "")
	got = aiToolsetsResponse{}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	for _, ts := range got.Toolsets {
		if want := !(ts.Name == "logs" || ts.Name == "devops-code"); ts.Enabled != want {
			t.Errorf("GET sonrası %s enabled=%v", ts.Name, ts.Enabled)
		}
	}

	// Kapatılamaz set → 400, yazım / audit yok, durum değişmez.
	for _, bad := range []string{`{"disabled":["meta"]}`, `{"disabled":["logs","services"]}`, `{"disabled":["traces","problems"]}`} {
		w := rulesDo(mux, "PUT", "/api/ai/toolsets", bad, auth.RoleAdmin, "")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "kapatılamaz") {
			t.Fatalf("%s → %d %s", bad, w.Code, w.Body)
		}
	}
	if st.saves != 1 || len(s.auditQ) != 0 {
		t.Fatalf("400 yazmamalı: saves=%d audit=%d", st.saves, len(s.auditQ))
	}
	if w := rulesDo(mux, "PUT", "/api/ai/toolsets", `{not json`, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("bozuk JSON %d", w.Code)
	}

	// Hepsini aç → boş liste, audit önceki kümeyi taşır.
	if w := rulesDo(mux, "PUT", "/api/ai/toolsets", `{"disabled":[]}`, auth.RoleAdmin, ""); w.Code != http.StatusOK {
		t.Fatalf("açma PUT %d", w.Code)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"before":["devops-code","logs"]`) || !strings.Contains(a.Details, `"after":[]`) {
		t.Fatalf("açma audit = %+v", a)
	}
	if chstore.CurrentAIToolsets().DisabledSet() != nil {
		t.Fatal("hepsi açık → nil küme")
	}
}

func TestValidateToolsetsPut(t *testing.T) {
	cases := []struct {
		in       []string
		disabled string
		ignored  string
		err      bool
	}{
		{nil, "", "", false},
		{[]string{"k8s", "oracle", "k8s"}, "k8s,oracle", "", false},
		{[]string{"Knowledge", "ghost"}, "knowledge", "ghost", false},
		{[]string{"meta"}, "", "", true},
		{[]string{"logs", "problems"}, "", "", true},
	}
	for _, c := range cases {
		d, ig, err := validateToolsetsPut(c.in)
		if (err != nil) != c.err {
			t.Errorf("%q: err=%v", c.in, err)
			continue
		}
		if err == nil && (strings.Join(d, ",") != c.disabled || strings.Join(ig, ",") != c.ignored || d == nil) {
			t.Errorf("%q → %q / %q", c.in, d, ig)
		}
	}
}

// v0.5.187 sınıfı: anahtar kapalı kümenin İÇERİĞİNİ ve rev'i taşır; sıra ve
// kapatılamaz/bilinmeyen adlar (etkisiz) anahtarı değiştirmez.
func TestAIToolsetsCacheKeyCarriesAllInputs(t *testing.T) {
	at := time.Unix(1_790_000_000, 0)
	k := func(names []string, at time.Time) string {
		return aiToolsetsCacheKey(chstore.AIToolsets{Disabled: names, UpdatedAt: at})
	}
	if k([]string{"logs"}, at) == k([]string{"k8s"}, at) {
		t.Fatal("aynı uzunlukta farklı kümeler aynı anahtar (v0.5.187)")
	}
	if k([]string{"logs", "k8s"}, at) != k([]string{"k8s", "logs"}, at) {
		t.Fatal("sıra anahtarı değiştirmemeli")
	}
	if k([]string{"logs"}, at) == k([]string{"logs"}, at.Add(time.Second)) {
		t.Fatal("rev anahtarda olmalı (updatedBy/At cevabın parçası)")
	}
	if k(nil, at) != k([]string{"meta", "ghost"}, at) {
		t.Fatal("etkisiz adlar (kapatılamaz / bilinmeyen) anahtarı bölmemeli")
	}
}

// Dış MCP kapısı: kapalı toolset'in aracı adlandırılmış (sızdırmaz) retle
// döner, rate bütçesini TÜKETMEZ; açık araç ve kaynak/istem yolları geçer.
func TestMCPGateRejectsDisabledToolset(t *testing.T) {
	prev := chstore.CurrentAIToolsets()
	t.Cleanup(func() { chstore.SetAIToolsets(prev) })
	chstore.SetAIToolsets(chstore.AIToolsets{Disabled: []string{"logs"}, UpdatedBy: "secret-admin@example.test"})
	s := &Server{}
	now := time.Now()
	for i := 0; i < mcpRateLimit+5; i++ {
		err := s.mcpGateDecide(auth.RoleAdmin, "agent-1", mcp.GateCall{Kind: "tool", Name: "search_logs"}, now)
		if err == nil {
			t.Fatal("kapalı toolset'in aracı geçmemeli")
		}
		msg := err.Error()
		if !strings.Contains(msg, `"search_logs"`) || !strings.Contains(msg, `toolset "logs"`) || !strings.Contains(msg, "do not retry") {
			t.Fatalf("ret adlandırılmış olmalı: %q", msg)
		}
		if strings.Contains(msg, "secret-admin") || strings.Contains(msg, "rate limited") {
			t.Fatalf("ret sızdırmamalı / rate değil: %q", msg)
		}
	}
	// Rate bütçesi yanmadı: aynı kimliğin açık araç çağrısı geçer.
	if err := s.mcpGateDecide(auth.RoleAdmin, "agent-1", mcp.GateCall{Kind: "tool", Name: "list_services"}, now); err != nil {
		t.Fatalf("açık araç geçmeli (bütçe yanmamalı): %v", err)
	}
	// Kaynak / istem yolu toolset kapısına girmez; bilinmeyen (dış) araç da.
	for _, c := range []mcp.GateCall{{Kind: "resource", Name: "coremetry://services"}, {Kind: "prompt", Name: "explain_trace"}, {Kind: "tool", Name: "ext_tool"}} {
		if err := toolsetGateError(c.Kind, c.Name, chstore.CurrentAIToolsets().DisabledSet()); err != nil {
			t.Errorf("%+v toolset kapısına takılmamalı: %v", c, err)
		}
	}
	// Kimliksiz istek önce rol kapısında düşer (sıra korunur).
	if err := s.mcpGateDecide("", "anon", mcp.GateCall{Kind: "tool", Name: "search_logs"}, now); err == nil || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("rol kapısı önce: %v", err)
	}
	// Kapatılamaz set blobda olsa bile ret yok.
	chstore.SetAIToolsets(chstore.AIToolsets{Disabled: []string{"meta", "services"}})
	if err := s.mcpGateDecide(auth.RoleViewer, "agent-2", mcp.GateCall{Kind: "tool", Name: "get_capabilities"}, now); err != nil {
		t.Fatalf("meta kapatılamaz: %v", err)
	}
}

// Sohbet kataloğu (copilot_chat.go'nun okuduğu zincir: tek kurucu mcpDeps →
// ChatToolListFor → toolsForRole) her rolde, hepsi açıkken, toolset katmanı
// yazılmadan ÖNCE üretilmiş altınla bayt-bayt aynı; kapalı toolset düşer.
func TestChatCatalogPerRoleMatchesGolden(t *testing.T) {
	prev := chstore.CurrentAIToolsets()
	t.Cleanup(func() { chstore.SetAIToolsets(prev) })
	chstore.SetAIToolsets(chstore.AIToolsets{})

	raw, err := os.ReadFile("../mcptools/testdata/toolset_catalog_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden map[string]struct {
		Count  int    `json:"count"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	for _, variant := range []string{"full", "simple"} {
		want := golden["zero/chat_"+variant]
		if want.SHA256 == "" {
			t.Fatalf("altında zero/chat_%s yok", variant)
		}
		for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
			tools := toolsForRole(mcptools.ChatToolListFor(s.mcpDeps(), variant), role)
			if got := apiCatalogSHA(t, tools); got != want.SHA256 || len(tools) != want.Count {
				t.Errorf("%s/%s: katalog altından saptı (%d araç, sha %.12s; altın %d, %.12s)", variant, role, len(tools), got, want.Count, want.SHA256)
			}
		}
	}
	// Kapalı toolset: aynı zincirde araçları yok, gerisi aynı sırada.
	chstore.SetAIToolsets(chstore.AIToolsets{Disabled: []string{"logs", "metrics"}})
	off := toolsForRole(mcptools.ChatToolListFor(s.mcpDeps(), "full"), auth.RoleViewer)
	for _, tl := range off {
		if ts := mcptools.ToolsetOfTool(tl.Name); ts == "logs" || ts == "metrics" {
			t.Errorf("kapalı toolset'in aracı sohbette: %s", tl.Name)
		}
	}
	if len(off) != golden["zero/chat_full"].Count-14 {
		t.Errorf("logs(8)+metrics(6) kapalıyken %d araç, beklenen %d", len(off), golden["zero/chat_full"].Count-14)
	}
}

// apiCatalogSHA — mcptools/toolset_catalog_golden_test.go serializeCatalog'un
// birebir eşi (paket sınırı: test yardımcısı dışa açılmaz).
func apiCatalogSHA(t *testing.T, tools []mcp.Tool) string {
	t.Helper()
	type row struct {
		Name       string `json:"name"`
		Desc       string `json:"description"`
		Short      string `json:"short"`
		ChatFull   string `json:"chatFull"`
		ChatSimple string `json:"chatSimple"`
		MinRole    string `json:"minRole"`
		Schema     any    `json:"schema"`
	}
	rows := make([]row, 0, len(tools))
	for _, tl := range tools {
		rows = append(rows, row{tl.Name, tl.Description, tl.ShortDescription, tl.ChatDescriptionFor(false), tl.ChatDescriptionFor(true), tl.MinRole, tl.InputSchema})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
