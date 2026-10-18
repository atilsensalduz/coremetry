package api

// chat_eval_mode.go — v0.10.1316 (değerlendirme seti gerçek ajan döngüsünde;
// DECISIONS "2026-10-18 — Değerlendirme seti gerçek ajan döngüsünde").
//
// Ajan kipi evalset vakası canlı sohbet handler'ını (copilotChat) SÜREÇ
// İÇİNDE koşar (D1): aynı kademeler, aynı katalog, aynı Executor, aynı tur /
// çağrı tavanları. Handler'ın üretimden ayrıldığı yerler yalnız bu ctx
// işaretine bakan küçük anahtarlardır (copilot_chat.go / chat_exchange.go,
// her biri "v0.10.1316" işaretli):
//
//   - ai_calls yüzeyi + exchange kimliği işaretten (evalset- öneki → /ai'da
//     "Kaynak: Değerlendirme", üretim sayılarına karışmaz);
//   - turun etkileşim satırı (cosre-turn) ve cosre_interactions YAZILMAZ —
//     sonuç Sink'e gider;
//   - araç seti işaretten (koşunun çözümü / açık seçimi);
//   - dış MCP araçları ve sohbet bağlamı araçları kataloğa girmez, sohbet
//     bağlamı (Redis) okunmaz/yazılmaz;
//   - araç başına mcp.tool.call audit'i yazılmaz (D4 — koşu düzeyi audit
//     ai.evalset.run kalır);
//   - her model turu OnTurn'e, her Executor çağrısı OnTool'a bildirilir.
//
// İşaret nil iken (üretim) her yol bayt bayt eskisidir
// (TestChatEvalModeNilIsByteIdentical). Bu dosya bilinçli olarak yüzey
// LİTERALİ taşımaz (TestChatSurfacesRegistered chat_*.go'yu tarar): etiket
// ai_evalset_agent.go'da kurulur, buraya değer olarak gelir.

import (
	"context"
	"encoding/json"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatEvalMode — ajan kipi evalset vakasının handler'a taşıdığı işaret.
type chatEvalMode struct {
	// Surface — ai_calls yüzeyi (evalset-<Yüzey>); ExchangeID — "<koşu>/<vaka>".
	Surface, ExchangeID string
	// Toolset — koşunun araç seti (full | simple); "" = canlı çözüm.
	Toolset string
	// OnTurn — serbest döngünün her model turu; round < 0 = tavan turu
	// (araç çağrısı yasak son tur). nil = bildirim yok.
	OnTurn func(round int, turn copilot.ChatTurn, err error)
	// OnTool — Executor'dan geçen her çağrı (yürütülmeyenler dâhil; tavanı
	// aşıp Executor'a hiç gitmeyenler hariç). args = gerçekten koşan argüman.
	OnTool func(name string, args json.RawMessage, oc agenttools.Outcome)
	// Sink — turun sonucu (recordChatExchange'in yerine). nil = sonuç atılır.
	Sink func(evalAgentOutcome)
}

// evalAgentOutcome — ajan kipi turun sonucu (chatExchange'den, kilit altında
// okunur). Oy / geri bildirim alanı YOKTUR (TestEvalNeverReadsVotes).
type evalAgentOutcome struct {
	Tier, Route, Status, ErrMsg, Answer, Model string
	// Tools — turun araç çipleri, cosre_interactions ile AYNI kırpmayla
	// (args 1 KiB, çıktı önizlemesi 4 KiB, hata 512 B); guided ön-yüklemesi
	// Origin "guided", niyet sınıflandırıcısı Origin "intent".
	Tools   []chstore.CosreTool
	Deep    bool
	Toolset string
	// ModelCalls / InputTokens / OutputTokens — turun BÜTÜN model çağrıları
	// (niyet, guided anlatımı, döngü) — copilot.CallSink toplamı.
	ModelCalls                uint16
	InputTokens, OutputTokens uint32
}

type chatEvalModeKey struct{}

// withChatEvalMode — işareti ctx'e koyar; nil ctx'i aynen döndürür.
func withChatEvalMode(ctx context.Context, em *chatEvalMode) context.Context {
	if em == nil {
		return ctx
	}
	return context.WithValue(ctx, chatEvalModeKey{}, em)
}

// chatEvalModeFrom — ctx'teki işaret; üretimde nil.
func chatEvalModeFrom(ctx context.Context) *chatEvalMode {
	if ctx == nil {
		return nil
	}
	em, _ := ctx.Value(chatEvalModeKey{}).(*chatEvalMode)
	return em
}

// evalOutcomeFromExchange — SAF (x kilidi altında okur): Sink yükü.
func evalOutcomeFromExchange(x *chatExchange, tier, status, msg string) evalAgentOutcome {
	x.mu.Lock()
	defer x.mu.Unlock()
	return evalAgentOutcome{
		Tier: tier, Route: x.route, Status: status, ErrMsg: msg, Answer: x.answer, Model: x.model,
		Tools: append([]chstore.CosreTool(nil), x.tools...), Deep: x.deep, Toolset: x.toolset,
		ModelCalls: x.modelCalls, InputTokens: x.inTok, OutputTokens: x.outTok,
	}
}
