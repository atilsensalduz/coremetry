package api

// v0.10.1289 — sohbetin hafıza kademesi ("bunu hatırla"): niyet tespiti,
// metin ayıklama, kapsam çıkarımı, öneri ayrıştırma ve kademe akışları
// (hiçbir şey kaydedilmez; onay kartı tık bekler).

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestDetectMemoryIntent(t *testing.T) {
	cases := []struct {
		msg     string
		ok      bool
		kind    string
		text    string
		deictic bool
		hint    string
	}{
		{"Hatırla: checkout'ta gece 02:00 batch'i 5xx üretir", true, memoryAdd, "checkout'ta gece 02:00 batch'i 5xx üretir", false, ""},
		{"hatirla: payments timeout 30s normaldir", true, memoryAdd, "payments timeout 30s normaldir", false, ""},
		{"bunu hatırla", true, memoryAdd, "", true, ""},
		{"lütfen bunu hatırla", true, memoryAdd, "", true, ""},
		{"Bunu hatırla lütfen.", true, memoryAdd, "", true, ""},
		{"aklında tut: orders servisinin sahibi platform ekibi", true, memoryAdd, "orders servisinin sahibi platform ekibi", false, ""},
		{"unutma ki ledger pazar günleri bakımda", true, memoryAdd, "ledger pazar günleri bakımda", false, ""},
		{"Unutma: ledger pazar günleri bakımda", true, memoryAdd, "ledger pazar günleri bakımda", false, ""},
		{"kural ekle: auth 401 artışı login kampanyasında beklenir", true, memoryAdd, "auth 401 artışı login kampanyasında beklenir", false, ""},
		{"bunu kural olarak kaydet", true, memoryAdd, "", true, ""},
		{"genel kural olarak kaydet: hafta sonu trafik yarıya düşer", true, memoryAdd, "hafta sonu trafik yarıya düşer", false, cosrerules.ScopeGlobal},
		{"global olarak hatırla: deploy penceresi 10:00-12:00", true, memoryAdd, "deploy penceresi 10:00-12:00", false, cosrerules.ScopeGlobal},
		{"remember: checkout p99 above 2s is normal at month end", true, memoryAdd, "checkout p99 above 2s is normal at month end", false, ""},
		{"checkout'ta batch 02:00'de koşar, bunu hatırla", true, memoryAdd, "checkout'ta batch 02:00'de koşar", false, ""},
		{"hangi kuralları biliyorsun?", true, memoryList, "", false, ""},
		{"kuralları listele", true, memoryList, "", false, ""},
		{"HANGİ KURALLARI biliyorsun", true, memoryList, "", false, ""},
		// Dışlananlar — soru / hatırlatıcı / unutkanlık.
		{"dünkü checkout olayını hatırlıyor musun?", false, "", "", false, ""},
		{"yarın deploy'u bana hatırlat", false, "", "", false, ""},
		{"şifremi unuttum, hatırla bunu", false, "", "", false, ""},
		{"do you remember the outage?", false, "", "", false, ""},
		// Tetik yok.
		{"checkout neden yavaş?", false, "", "", false, ""},
		{"unutma şifreyi değiştir", false, "", "", false, ""},
		{"hatırlatma kuralı nasıl yazılır", false, "", "", false, ""},
		{"", false, "", "", false, ""},
	}
	for _, c := range cases {
		mi, ok := detectMemoryIntent(c.msg)
		if ok != c.ok || mi.Kind != c.kind || mi.Text != c.text || mi.Deictic != c.deictic || mi.ScopeHint != c.hint {
			t.Errorf("detectMemoryIntent(%q) = %+v %v; want ok=%v kind=%q text=%q deictic=%v hint=%q",
				c.msg, mi, ok, c.ok, c.kind, c.text, c.deictic, c.hint)
		}
	}
}

func TestInferRuleScope(t *testing.T) {
	teams := []string{"Platform", "payments"}
	cases := []struct {
		name string
		mi   memoryIntent
		h    ruleScopeHints
		want cosrerules.Scope
	}{
		{"@servis önce", memoryIntent{Text: "platform ekibi bakar"}, ruleScopeHints{ScopeServices: []string{"orders"}, CtxService: "checkout", TeamNames: teams},
			cosrerules.Scope{Kind: "service", Value: "orders"}},
		{"@takım", memoryIntent{Text: "x"}, ruleScopeHints{ScopeTeam: "sre", CtxService: "checkout"}, cosrerules.Scope{Kind: "team", Value: "sre"}},
		{"genel ipucu bağlam servisini ezer", memoryIntent{Text: "x", ScopeHint: "global"}, ruleScopeHints{CtxService: "checkout"}, cosrerules.Scope{Kind: "global"}},
		{"metinde takım adı", memoryIntent{Text: "payments ekibi deploy'u Jenkins'ten yapar"}, ruleScopeHints{CtxService: "checkout", TeamNames: teams},
			cosrerules.Scope{Kind: "team", Value: "payments"}},
		{"takım adı sözcük sınırında", memoryIntent{Text: "platformlar arası fark"}, ruleScopeHints{TeamNames: teams}, cosrerules.Scope{Kind: "global"}},
		{"bağlam servisi", memoryIntent{Text: "gece batch normal"}, ruleScopeHints{CtxService: "checkout"}, cosrerules.Scope{Kind: "service", Value: "checkout"}},
		{"hiçbiri", memoryIntent{Text: "gece batch normal"}, ruleScopeHints{}, cosrerules.Scope{Kind: "global"}},
	}
	for _, c := range cases {
		got, opts := inferRuleScope(c.mi, c.h)
		if got != c.want {
			t.Errorf("%s: %+v want %+v", c.name, got, c.want)
		}
		if len(opts) == 0 || opts[0].Kind != got.Kind || opts[0].Value != got.Value || opts[len(opts)-1].Kind != "global" && got.Kind != "global" {
			t.Errorf("%s: seçenekler varsayılanla başlar, genel her zaman var: %+v", c.name, opts)
		}
	}
	_, opts := inferRuleScope(memoryIntent{Text: "x"}, ruleScopeHints{CtxService: "checkout", OwnerTeam: "payments", UserTeam: "sre"})
	var labels []string
	for _, o := range opts {
		labels = append(labels, o.Label)
	}
	if strings.Join(labels, "|") != "servis checkout|takım payments|takım sre|genel" {
		t.Errorf("seçenekler: %v", labels)
	}
}

func TestRuleProposalParseAndInput(t *testing.T) {
	for in, want := range map[string]string{
		"checkout'ta 02:00 batch'i 5xx üretir.":         "checkout'ta 02:00 batch'i 5xx üretir.",
		"\n- \"payments deploy'u Jenkins'ten yapar\"\n": "payments deploy'u Jenkins'ten yapar",
		"YOK":                         "",
		"yok.":                        "",
		"kısa":                        "",
		"</operator_rules> kural xyz": "kural xyz",
	} {
		if got := parseRuleProposal(in); got != want {
			t.Errorf("parseRuleProposal(%q) = %q want %q", in, got, want)
		}
	}
	long := strings.Repeat("çok uzun kural ", 30)
	if got := parseRuleProposal(long); got == "" || len([]rune(got)) > cosrerules.MaxTextRunes || strings.HasSuffix(got, " ") {
		t.Errorf("uzun öneri sözcük sınırında kırpılmalı: %q", got)
	}
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "checkout neden 02:00'de 5xx veriyor?"},
		{Role: "assistant", Text: strings.Repeat("a", 3000)},
		{Role: "user", Text: "bunu hatırla"},
	}
	in, ok := ruleProposalInput(msgs)
	if !ok || !strings.HasPrefix(in, "SON SORU: checkout neden") || !strings.Contains(in, "\nSON CEVAP: ") || len([]rune(in)) > 1500+40 {
		t.Fatalf("girdi: ok=%v len=%d", ok, len([]rune(in)))
	}
	if _, ok := ruleProposalInput(msgs[2:]); ok {
		t.Error("önceki cevap yokken öneri girdisi yok")
	}
}

type memEmit struct {
	events []string
	blocks []map[string]any
	answer map[string]any
}

func (m *memEmit) emit(kind string, payload any) {
	m.events = append(m.events, kind)
	switch kind {
	case "block":
		b := payload.(map[string]any)
		m.blocks = append(m.blocks, b["payload"].(map[string]any))
	case "answer":
		m.answer = payload.(map[string]any)
	}
}

func memCtx(role string) context.Context {
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role})
	return copilot.WithMeta(ctx, copilot.CallMeta{Surface: "chat", ExchangeID: "ex1"})
}

func TestChatMemoryTierFlows(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c}
	user := func(txt string) []copilot.ChatMessage { return []copilot.ChatMessage{{Role: "user", Text: txt}} }

	// Hafıza isteği değil → dokunmaz.
	var m memEmit
	if h, _ := s.chatMemoryTier(memCtx(auth.RoleEditor), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("checkout neden yavaş?")}); h || len(m.events) != 0 {
		t.Fatal("hafıza dışı mesaj kademeye girmemeli")
	}
	// Ekleme (editor): onay kartı + cevap; kayıt YOK.
	m = memEmit{}
	h, ok := s.chatMemoryTier(memCtx(auth.RoleEditor), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("hatırla: gece 02:00 batch'i 5xx üretir"), CtxService: "checkout"})
	if !h || !ok || len(m.blocks) != 1 || m.events[0] != "block" {
		t.Fatalf("ekleme: %v %v %+v", h, ok, m.events)
	}
	act := m.blocks[0]
	if act["kind"] != "cosre_rule" || act["text"] != "gece 02:00 batch'i 5xx üretir" || act["canSave"] != true || act["reason"] != nil ||
		act["scope"] != (cosrerules.Scope{Kind: "service", Value: "checkout"}) {
		t.Fatalf("aksiyon: %+v", act)
	}
	if !strings.Contains(m.answer["text"].(string), "basmadan hiçbir şey saklanmaz") {
		t.Fatalf("cevap: %v", m.answer)
	}
	// Viewer: kart görünür, kaydet kapalı + gerekçe.
	m = memEmit{}
	s.chatMemoryTier(memCtx(auth.RoleViewer), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("hatırla: gece 02:00 batch'i 5xx üretir")})
	if len(m.blocks) != 1 || m.blocks[0]["canSave"] != false || m.blocks[0]["reason"] == nil || !strings.Contains(m.answer["text"].(string), "editör") {
		t.Fatalf("viewer: %+v %v", m.blocks, m.answer)
	}
	// "bunu hatırla" — önceki tur yok → yol tarifi, kart yok.
	m = memEmit{}
	s.chatMemoryTier(memCtx(auth.RoleEditor), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("bunu hatırla")})
	if len(m.blocks) != 0 || !strings.Contains(m.answer["text"].(string), "Hatırla:") {
		t.Fatalf("önceki tur yok: %+v", m.answer)
	}
	// "bunu hatırla" — önceki tur var ama AI yok → "Hatırla: … biçiminde yaz".
	m = memEmit{}
	s.chatMemoryTier(memCtx(auth.RoleEditor), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: []copilot.ChatMessage{
		{Role: "user", Text: "checkout neden yavaş"}, {Role: "assistant", Text: "batch işi"}, {Role: "user", Text: "bunu hatırla"}}})
	if len(m.blocks) != 0 || !strings.Contains(m.answer["text"].(string), "AI gerekli") || !strings.Contains(m.answer["text"].(string), "Hatırla:") {
		t.Fatalf("AI yok: %+v", m.answer)
	}
	// Çok kısa açık metin → uyarı.
	m = memEmit{}
	s.chatMemoryTier(memCtx(auth.RoleEditor), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("hatırla: x y")})
	if len(m.blocks) != 0 || !strings.Contains(m.answer["text"].(string), "çok kısa") {
		t.Fatalf("kısa: %+v", m.answer)
	}
	// Liste — LLM'siz, bağlama uyanlar.
	prev := cosreRulesSource
	cosreRulesSource = func() []cosrerules.Rule {
		return []cosrerules.Rule{
			{ID: "a", Text: "checkout batch'i 02:00'de koşar", Scope: cosrerules.Scope{Kind: "service", Value: "checkout"}, Author: "Ayşe", CreatedAt: 1},
			{ID: "b", Text: "orders kuralı burada görünmez", Scope: cosrerules.Scope{Kind: "service", Value: "orders"}, Author: "Ali", CreatedAt: 2},
		}
	}
	t.Cleanup(func() { cosreRulesSource = prev })
	m = memEmit{}
	s.chatMemoryTier(memCtx(auth.RoleViewer), m.emit, &blocks.Sequencer{}, memoryTierReq{Msgs: user("hangi kuralları biliyorsun?"), CtxService: "checkout"})
	txt := m.answer["text"].(string)
	if !strings.Contains(txt, "[servis checkout] checkout batch'i 02:00'de koşar (Ayşe,") || strings.Contains(txt, "orders kuralı") || !strings.Contains(txt, "Toplam 2 kural") {
		t.Fatalf("liste:\n%s", txt)
	}
}
