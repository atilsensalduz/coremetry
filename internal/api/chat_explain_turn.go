package api

// chat_explain_turn.go — v0.10.1400 (Tek CoSRE). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//   "Explain trace'te çıkan loading ikonu güzel ama chat'ten 'bu trace'i
//    açıkla' deyince aynı yerde devam etmiyor, sanki yeni drawer açılıyor."
//
// Kök sebep: trace / exception ✨ açıklaması CoSRE çekmecesinin İÇİNDE ama
// sohbetin DIŞINDA çiziliyordu — `?ai=` öznesi çekmeceyi "açıklama kipine"
// alıyor (AIDrawerBody: CopilotExplain + ikinci useChatThread + ikinci
// composer), sohbetteki "<id> açıkla" da v0.10.460'tan beri `open` ile o
// kipe sıçrıyordu. İki thread, iki composer, iki bekleme göstergesi.
//
// Şimdi bir açıklama, sohbetin SIRADAN bir turu: istemci kullanıcı turunu
// ("Bu trace'i açıkla · 5b8efff7…") yapısal niyetle gönderir —
// context.explainAsk {kind: trace|exception, id, src} — ve bu kademe cevabı
// ✨ düğmesinin BİREBİR çekirdeğiyle üretir:
//   • trace     → buildTraceExplainInput + SystemPromptTrace, anahtar
//                 explainCacheKey(SystemPromptTrace(), in.User, "")
//                 (explainTraceClassicPrepared ile aynı), yüzey
//                 explain-trace:chat;
//   • exception → GetExceptionGroup + exceptionExplainInput (dağıtıcının
//                 prompt'u; `ora:` → Oracle), anahtar explainCacheKey(system,
//                 in.User, "") (copilotExplainException ile aynı), yüzey
//                 explain-exception:chat.
// Düğme ve sohbet aynı önbellek satırını paylaşır; isabet LLM çağırmaz.
// answer çerçevesi sohbetin olağan alanları + `explain` künyesi (istemci
// ExplainBody ile çizer, kanıtı sayfaya duyurur, takip bağlamını kurar).
//
// Kademe sırası: niyet AÇIK olduğu için metin eşlemesinden önce — kapsam,
// hafıza, wiki, operasyon ön-getirmesi ve guided'dan ÖNCE
// (TestChatExplainTurnTierIsFirst). explainAsk yokken akış bayt bayt eski.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatExplainAsk — context.explainAsk: istemcinin yapısal açıklama niyeti.
type chatExplainAsk struct {
	Kind string `json:"kind"`          // trace | exception
	ID   string `json:"id"`            // trace id (32-hex) | grup parmak izi
	Src  string `json:"src,omitempty"` // button | nudge | deeplink | chip | starter (ölçüm; karar değil)
}

const (
	explainAskTrace     = "trace"
	explainAskException = "exception"
	// chatExplainIDMax — kimlik tavanı (rune); arşiv sanitize'ı da aynı.
	chatExplainIDMax = 200
	// chatExplainEvidenceMax — künyedeki kanıt kimliği tavanı (her liste).
	chatExplainEvidenceMax = 50
	chatExplainSrcMax      = 32
)

// chatExplainMeta — answer.explain (ve arşivde aiChatMessage.explain): turun
// açıklama künyesi. İstemci ExplainBody'yi, kanıt satırlarını ve takip
// bağlamını bundan kurar.
type chatExplainMeta struct {
	Kind             string   `json:"kind"`
	ID               string   `json:"id"`
	Surface          string   `json:"surface,omitempty"`
	Label            string   `json:"label,omitempty"` // exception: grup kodu / tipi
	Cached           bool     `json:"cached,omitempty"`
	CachedAtMs       int64    `json:"cachedAtMs,omitempty"`
	EvidenceSpanIDs  []string `json:"evidenceSpanIds,omitempty"`
	EvidenceTraceIDs []string `json:"evidenceTraceIds,omitempty"`
	OracleRows       int      `json:"oracleRows,omitempty"`
}

// normalizeExplainAsk — SAF: tür beyaz listesi + kimlik biçimi. trace 32-hex
// (küçük harfe indirilir); exception parmak izi 1..200 rune, yalnız
// [A-Za-z0-9:._-] (ora: önekli Oracle grupları dahil). ok=false → kademe
// düşer ve kullanıcı metni olağan yoldan cevaplanır.
func normalizeExplainAsk(a *chatExplainAsk) (chatExplainAsk, bool) {
	if a == nil {
		return chatExplainAsk{}, false
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	id := strings.TrimSpace(a.ID)
	src := clampChatRunes(a.Src, chatExplainSrcMax)
	switch kind {
	case explainAskTrace:
		id = strings.ToLower(id)
		if !fuTraceRe.MatchString(id) {
			return chatExplainAsk{}, false
		}
	case explainAskException:
		if !explainFingerprintOK(id) {
			return chatExplainAsk{}, false
		}
	default:
		return chatExplainAsk{}, false
	}
	return chatExplainAsk{Kind: kind, ID: id, Src: src}, true
}

// explainFingerprintOK — SAF: grup parmak izinin kabul edilen biçimi.
func explainFingerprintOK(id string) bool {
	if id == "" || utf8.RuneCountInString(id) > chatExplainIDMax {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ':', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// clampExplainIDs — SAF: kanıt kimliklerini künye tavanına indirir (boşlar düşer).
func clampExplainIDs(in []string) []string {
	out := make([]string, 0, min(len(in), chatExplainEvidenceMax))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || utf8.RuneCountInString(v) > chatExplainIDMax {
			continue
		}
		out = append(out, v)
		if len(out) >= chatExplainEvidenceMax {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// errExplainTurnNotFound — açıklanacak trace / grup bulunamadı (adım çipi).
var errExplainTurnNotFound = errors.New("bulunamadı")

// explainTurnNotFoundTR — SAF: bulunamayan öznenin dürüst, LLM'siz cevabı.
func explainTurnNotFoundTR(a chatExplainAsk) string {
	if a.Kind == explainAskTrace {
		return "Trace `" + a.ID + "` bulunamadı — ne Tempo'da ne ClickHouse'ta var (saklama süresi dolmuş ya da kimlik yanlış olabilir)."
	}
	return "Exception grubu `" + a.ID + "` bulunamadı — silinmiş ya da kimlik yanlış olabilir."
}

// chatExplainTurn — yapısal açıklama turu. handled=false → explainAsk geçersiz
// ya da copilot yok; çağıran olağan kademelere devam eder.
func (s *Server) chatExplainTurn(ctx context.Context, emit func(string, any), raw *chatExplainAsk, loc *time.Location) (handled, ok bool) {
	ask, valid := normalizeExplainAsk(raw)
	if !valid || s == nil || s.copilot == nil {
		return false, false
	}
	if ask.Kind == explainAskTrace {
		return s.chatExplainTraceTurn(ctx, emit, ask)
	}
	return s.chatExplainExceptionTurn(ctx, emit, ask, loc)
}

// chatExplainTraceTurn — ✨ Explain trace'in kodsuz klasik yolu, sohbet içinde.
func (s *Server) chatExplainTraceTurn(ctx context.Context, emit func(string, any), ask chatExplainAsk) (bool, bool) {
	n := emitGuidedStep(emit, "trace", `{"id":"`+ask.ID+`"}`)
	in, err := s.buildTraceExplainInput(ctx, ask.ID)
	if err != nil {
		if errors.Is(err, errExplainTraceNotFound) {
			emitGuidedStepResult(emit, n, "trace", "", errExplainTurnNotFound)
			emit("answer", map[string]any{"text": explainTurnNotFoundTR(ask), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
			return true, false
		}
		emitGuidedStepResult(emit, n, "trace", "", err)
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	emitGuidedStepResult(emit, n, "trace", in.User, nil)
	route := guidedRoute{Intent: guidedTraceByID, TraceID: ask.ID}
	return s.emitTraceExplainAnswer(ctx, emit, ask.ID, in, route, guidedAnswerLinks(route, noLinkWindow()))
}

// emitTraceExplainAnswer — v0.10.1400: trace açıklamasının sohbet cevabı.
// chatExplainTraceTurn ve guidedTraceExplain'in açıklama dalı paylaşır: aynı
// sistem/kullanıcı çifti, düğmenin anahtarı, aynı künye.
func (s *Server) emitTraceExplainAnswer(ctx context.Context, emit func(string, any), traceID string, in traceExplainInput, route guidedRoute, links []guidedAnswerLink) (bool, bool) {
	system := copilot.SystemPromptTrace()
	cacheKey := explainCacheKey(system, in.User, "") // explainTraceClassicPrepared ile AYNI formül
	res, ok := s.streamExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, in.User, cacheKey)
	if !ok {
		return true, false
	}
	text := trimExplainText(res)
	links = append(links, guidedAnswerLink{Label: "Trace'i aç", Href: "/trace?id=" + url.QueryEscape(traceID)})
	links = append(links, s.answerRequestIDLinks(ctx, text, in.RootService)...)
	meta := chatExplainMeta{Kind: explainAskTrace, ID: traceID, Surface: aisurface.ExplainTraceChat,
		Cached: res.Cached, CachedAtMs: res.CachedAtMs,
		EvidenceSpanIDs: clampExplainIDs(in.Evidence), OracleRows: in.OracleRows}
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"suggestions":     guidedSuggestions(route),
		"links":           dedupLinksByHref(links),
		"evidenceSpanIds": in.Evidence, // v0.10.453 sözleşmesi (eski istemci)
		"explain":         meta,
	}
	emit("answer", withExplainCached(ans, res))
	return true, true
}

// chatExplainExceptionTurn — ✨ Explain root cause'un (kodsuz) yolu, sohbet içinde.
func (s *Server) chatExplainExceptionTurn(ctx context.Context, emit func(string, any), ask chatExplainAsk, loc *time.Location) (bool, bool) {
	args, _ := json.Marshal(map[string]any{"fingerprint": ask.ID})
	n := emitGuidedStep(emit, opExceptionExplainTool, string(args))
	ictx, cancel := context.WithTimeout(ctx, opExceptionExplainInputTimeout)
	g, in, system, gok := s.chatExceptionExplainInput(ictx, ask.ID, loc)
	cancel()
	if !gok {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, "", errExplainTurnNotFound)
		emit("answer", map[string]any{"text": explainTurnNotFoundTR(ask), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
		return true, false
	}
	emitGuidedStepResult(emit, n, opExceptionExplainTool, in.User, nil)
	// copilotExplainException ile AYNI anahtar (kod dalı kapalı).
	res, ok := s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User, explainCacheKey(system, in.User, ""))
	if !ok {
		return true, false
	}
	text := trimExplainText(res)
	label := exceptionGroupLabel(g)
	links := []guidedAnswerLink{{Label: "Exceptions grubu " + clipRunes(label, exGroupOpRunes), Href: "/problems?exc=" + url.QueryEscape(g.Fingerprint)}}
	links = append(links, s.answerRequestIDLinks(ctx, text, g.Service)...)
	meta := chatExplainMeta{Kind: explainAskException, ID: g.Fingerprint, Surface: aisurface.ExplainExceptionChat,
		Label: clipRunes(label, exGroupOpRunes), Cached: res.Cached, CachedAtMs: res.CachedAtMs,
		EvidenceSpanIDs: clampExplainIDs(in.EvSpans), EvidenceTraceIDs: clampExplainIDs(in.EvTraces)}
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"links":           dedupLinksByHref(links),
		"evidenceSpanIds": in.EvSpans,
		"explain":         meta,
	}
	emit("answer", withExplainCached(ans, res))
	return true, true
}

// exceptionGroupLabel — SAF: grubun kısa adı (tip — Oracle grubunda hata
// kodu; yoksa parmak izi).
func exceptionGroupLabel(g *chstore.ExceptionGroup) string {
	if g == nil {
		return ""
	}
	if t := strings.TrimSpace(g.Type); t != "" {
		return t
	}
	return g.Fingerprint
}
