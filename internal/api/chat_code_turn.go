package api

// chat_code_turn.go — v0.10.1461 (operasyon kartı planı R2, operatör onayı
// 2026-10-23: "bağımsız sohbette kod okumayı açalım"): bağımsız sohbette
// AÇIK kod turu.
//
// v0.10.1050 bağımsız sohbette kod okumayı kapatmıştı (chat_source_code.go
// başlığı): döngüde onaysız dış MCP araçları vardı ve ekilmiş bir log satırı
// modeli kodu bir dış aracın argümanına taşımaya yönlendirebilirdi.
// v0.10.1209 örtük bir yol açtı (search_code sunuluyor ve dış MCP
// YAPILANDIRILMAMIŞSA read_source_code da). Bu dosya o örtük yolun YERİNE tek,
// açık yolu koyar; iki kusur tur bazında kapanır:
//
//  1. Kullanıcı AÇIKÇA ister: composer'ın "Kod" hapı (context.code; kullanıcı
//     başına kalıcı, varsayılan KAPALI). Hap = turun onayı (araç çağrısı başına
//     onay akan döngüyü keser, küçük modelde yarım cevap üretir).
//  2. Kod turunda dış MCP araçları HİÇ KURULMAZ (katalog, köprü, extNames boş) —
//     kurulum bazlı Configured() kapısı yerine tur bazlı. Wiki araçları kalır
//     (ikisi de yerel modele gider).
//  3. Tur YALNIZ yerel (localOnly) profile gider: RouteSensitive (v0.10.1451,
//     copilot/sensitive.go) profili sabitler ve ctx'i işaretler; yerel profil
//     yoksa tur "kapalı: yerel profil yok" der ve kod aracı sunulmaz (copilot
//     kapısı arka kapıdır: işaretli ctx yerel olmayan profile ErrLocalOnly).
//  4. cmk_ API token'ı ve kimliksiz çağıran dışarıda (sourceCodeCallerAllowed).
//  5. Tur başına bir audit satırı: copilot.code_turn (araç çağrıları zaten
//     mcp.tool.call).
//
// Kapsam: read_source_code yalnız EKRANDAKİ servisin (tohum) ve bu turda bir
// araç sonucunun döndürdüğü servislerin kodunu okur (chat_resolved_code_scope.go);
// model metnindeki servis adı kapsam DEĞİLDİR. Depo yine yalnız servisten
// (katalog pini fail-closed → ad konvansiyonu).
//
// Kod turu bir KADEME değildir (kademe sırası pinli): serbest döngünün tur
// kipidir. Açıkken guided / çekmece / RAG / niyet kademeleri atlanır ve soru
// doğrudan döngüye gider (trace takibi kapısının ikizi) — prod'da niyet kademesi
// on_no_loop'ta döngüye hiç ulaşmayan soruyu genel cevaba çevirirdi. Açık
// kapsam / komut, hafıza, wiki ve operasyon ön-getirmesi aynen önce koşar.
//
// Hap kapalıyken (ya da tur kapalı düşünce) bağımsız sohbetin kataloğu ve
// prompt'u v0.10.1050 duruşundadır: read_source_code ve search_code yok, bayt
// bayt (TestStandaloneWithoutCodePillHasNoCodeTools).

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Kapalı düşüşün dürüst gerekçeleri (step etiketi "Kod okuma " + gerekçe).
const (
	codeTurnReasonToken    = "kapalı: API token'ı ile kod okunmaz — kod turu yalnız oturum kullanıcısına açık"
	codeTurnReasonNoDevOps = "kapalı: kod entegrasyonu (Azure DevOps) yapılandırılmamış"
	codeTurnReasonToolset  = "kapalı: kod araç seti (devops-code) operatörce kapatılmış"
)

// codeTurn — isteğin kod turu kararı. Sıfır değer = hap kapalı (eski davranış).
type codeTurn struct {
	Pill bool // istemci hapı açık gönderdi (context.code)
	On   bool // bu tur kod okuyabilir
	// Reason — Pill && !On iken dürüst step gerekçesi ("" = sessiz: panel
	// takibinin / değerlendirmenin kendi yolu var).
	Reason string
	// Profile / Label — applyCodeTurn sonrası turun yerel profili (kimlik + etiket).
	Profile, Label string
}

// codeTurnInputs — kararın SAF girdileri.
type codeTurnInputs struct {
	Pill           bool
	PanelFollowUp  bool // çekmece öznesi / açıklama bağlamı (trace, exception …)
	Eval           bool // ajan kipi evalset vakası
	CallerAllowed  bool // oturum kullanıcısı (token / kimliksiz değil)
	DevOps         bool // DevOps bağlı
	ToolsetOff     bool // devops-code toolset'i operatörce kapalı
	LocalAvailable bool // çözülen profil yerel ya da role açık yerel profil var
}

// decideCodeTurn — SAF: koşullar sırayla; ilk başarısız → kapalı + gerekçe.
func decideCodeTurn(in codeTurnInputs) codeTurn {
	ct := codeTurn{Pill: in.Pill}
	switch {
	case !in.Pill:
	case in.PanelFollowUp, in.Eval: // panel takibinin kendi kod yolu var; evalset kodsuz
	case !in.CallerAllowed:
		ct.Reason = codeTurnReasonToken
	case !in.DevOps:
		ct.Reason = codeTurnReasonNoDevOps
	case in.ToolsetOff:
		ct.Reason = codeTurnReasonToolset
	case !in.LocalAvailable:
		ct.Reason = localOnlyClosedReason
	default:
		ct.On = true
	}
	return ct
}

// chatCodeTurn — isteğin kod turu kararı (ctx: claims + açık profil seçimi
// uygulanmış alışveriş ctx'i). Hap kapalıyken hiçbir şey okunmaz.
func (s *Server) chatCodeTurn(ctx context.Context, req chatRequest, c *auth.Claims, eval bool) codeTurn {
	if !req.Context.Code {
		return codeTurn{}
	}
	in := codeTurnInputs{
		Pill:          true,
		PanelFollowUp: codeTurnPanelContext(req),
		Eval:          eval,
		CallerAllowed: sourceCodeCallerAllowed(c),
		DevOps:        s.devops != nil && s.devops.Configured(),
	}
	if _, off := mcptools.ToolDisabledBy(mcptools.SourceCodeToolName, currentToolsetsDisabled()); off {
		in.ToolsetOff = true
	}
	if in.CallerAllowed && in.DevOps && !in.ToolsetOff && !in.PanelFollowUp && !eval {
		in.LocalAvailable = s.localOnlyAvailable(ctx)
	}
	return decideCodeTurn(in)
}

// codeTurnPanelContext — SAF: istek bir çekmece/panel takibi mi (açıklama
// bağlamı ya da özne; trace takibi drawerTraceFollowUp özne ister)? Öyleyse
// kod turu sessizce kapalı: panelin kendi kod yolu (trace serbest döngüsü,
// exception tek araçlı döngüsü) aynen çalışır.
func codeTurnPanelContext(req chatRequest) bool {
	return strings.TrimSpace(req.Context.Explain) != "" || strings.TrimSpace(req.Context.Subject) != ""
}

// applyCodeTurn — açık turun ctx'i: yerel profile sabitlenir ve SensitiveCode
// ile işaretlenir (RouteSensitive). Yerel profil karar ile uygulama arasında
// kaybolduysa tur kapalı düşer (dürüst gerekçe) ve ctx aynen döner.
func (s *Server) applyCodeTurn(ctx context.Context, ct *codeTurn) context.Context {
	if !ct.On {
		return ctx
	}
	rctx, ok := s.routeSensitive(ctx, copilot.SensitiveCode)
	if !ok {
		ct.On, ct.Reason = false, localOnlyClosedReason
		return ctx
	}
	ct.Profile = s.copilot.ProfileIDFor(rctx)
	ct.Label = s.chatProfileLabel(rctx) // v0.10.1465 — yapışkan turla ortak
	return rctx
}

// stepLabel — turun step etiketi ("" = yayınlanmaz).
func (ct codeTurn) stepLabel(extConfigured bool) string {
	switch {
	case ct.On && extConfigured:
		return "Kod okuma: " + ct.Label + " · dış araçlar bu turda kapalı"
	case ct.On:
		return "Kod okuma: " + ct.Label
	case ct.Pill && ct.Reason != "":
		return "Kod okuma " + ct.Reason
	}
	return ""
}

// standaloneSearchCodeFor — SAF: search_code panel takibinde bugünkü gibi;
// bağımsız sohbette YALNIZ açık kod turunda (v0.10.1209/1210'un örtük yolu
// kalktı). read_source_code kararı sourceCodeToolsFor'da (resolvedCode = kod
// turu). Düşen araç ADIYLA düşer, sıra korunur.
func standaloneSearchCodeFor(tools []mcp.Tool, panelFollowUp, on bool) []mcp.Tool {
	if panelFollowUp || on {
		return tools
	}
	out := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Name != mcptools.SearchCodeToolName {
			out = append(out, t)
		}
	}
	return out
}

// codeTurnSeeds — SAF: kapsamın tohumu (ekrandaki servis; tekil, boşsuz, sıra korunur).
func codeTurnSeeds(svcs ...string) []string {
	var out []string
	for _, sv := range svcs {
		if sv = strings.TrimSpace(sv); sv != "" && !containsString(out, sv) {
			out = append(out, sv)
		}
	}
	return out
}

// chatCodeTurnPromptTR — kod turu açıkken döngü prompt'unun kısa eki; değilse "".
func chatCodeTurnPromptTR(on bool) string {
	if !on {
		return ""
	}
	return copilot.CodeTurnAddendum() + "\n\n"
}

// externalToolsCached — kod turunda KURULMAYAN dış araçların sayısı (audit).
// Ağ yok: kayıt defterinin önbellekli katalog boyu (Status) — kod turunda dış
// sunucuya tools/list bile gitmez.
func (s *Server) externalToolsCached() (servers, tools int) {
	if s.mcpClient == nil || !s.mcpClient.Configured() {
		return 0, 0
	}
	for _, st := range s.mcpClient.Registry().Status() {
		servers++
		tools += st.Tools
	}
	return servers, tools
}

// codeTurnAuditDetails — SAF: copilot.code_turn satırının ayrıntısı (kod yok,
// yalnız karar künyesi).
func codeTurnAuditDetails(profile string, extServers, extDropped int, seeds []string, wikiOffered bool) string {
	if seeds == nil {
		seeds = []string{}
	}
	b, _ := json.Marshal(map[string]any{
		"profile": profile, "extServers": extServers, "extDropped": extDropped,
		"scopeSeed": seeds, "wikiOffered": wikiOffered,
	})
	return string(b)
}
