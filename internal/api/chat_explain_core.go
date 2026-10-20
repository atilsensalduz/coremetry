package api

// chat_explain_core.go — v0.10.1400 (Tek CoSRE; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor.").
//
// Sohbet akışının İÇİNDE bir ✨ açıklaması üretmenin TEK gövdesi. Üç çağıran:
//   • chatExplainTurn (chat_explain_turn.go) — yapısal explainAsk turu
//     (✨ düğmesi, nudge, ?ai= derin linki, grup çipi);
//   • guidedTraceExplain (trace_explain_unified.go) — sohbete yazılan
//     "<id> açıkla" ve trace'e dair odaklı soru;
//   • opExceptionExplainInChat (chat_operation_exception_explain.go,
//     v0.10.1399) — "<OP> operasyonu neden hata veriyor?".
//
// Sözleşme (üçünde aynı, ✨ düğmesinin deliverExplain'iyle birebir):
//   • önbellek anahtarı çağırandan (düğmenin formülü — explainCacheKey(system,
//     in.User, "")); boş anahtar = soruya özel, önbelleğe girmez;
//   • isabet → LLM yok, ai_calls satırı yok; metin TEK delta olarak akar,
//     exchangeId SAKLANAN kimlik (cevabı üreten gerçek çağrı), cached +
//     cachedAtMs etiketi;
//   • ıska → copilotStreamSurface(ctx, surface, …) delta delta; HAM çıktı
//     saklanır (deliverExplain de ham saklar — düğme ve sohbet aynı satırı
//     okur);
//   • model hatası → `error` olayı (çağıran turu sahiplenmiş sayar).

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// explainCoreResult — çekirdeğin çıktısı; çağıran answer çerçevesini kurar.
type explainCoreResult struct {
	Text       string // ham çıktı (isabette saklanan metin)
	Xid        string // cevabı üreten çağrının exchange kimliği
	Cached     bool
	CachedAtMs int64
}

// streamExplainCore — v0.10.1400: önbellek → akan üretim → önbellek yazımı.
// ok=false → model hatası `error` olayı olarak yayınlandı.
func (s *Server) streamExplainCore(ctx context.Context, emit func(string, any), surface, system, user, cacheKey string) (explainCoreResult, bool) {
	res := explainCoreResult{Xid: copilot.MetaFromContext(ctx).ExchangeID}
	if env, hit := s.explainCacheGetCtx(ctx, cacheKey); hit {
		res.Text, res.Xid, res.Cached, res.CachedAtMs = env.Text, env.Xid, true, env.AtMs
		emit("delta", map[string]string{"text": env.Text})
		return res, true
	}
	out, err := s.copilotStreamSurface(ctx, surface, system, user, func(d string) {
		if d != "" {
			emit("delta", map[string]string{"text": d})
		}
	})
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return res, false
	}
	res.Text = out
	s.explainCacheSet(ctx, cacheKey, out, res.Xid)
	return res, true
}

// withExplainCached — SAF: isabet etiketini answer çerçevesine yazar
// (deliverExplain'in hitExtra'sıyla aynı alanlar).
func withExplainCached(ans map[string]any, r explainCoreResult) map[string]any {
	if r.Cached {
		ans["cached"] = true
		if r.CachedAtMs > 0 {
			ans["cachedAtMs"] = r.CachedAtMs
		}
	}
	return ans
}

// trimExplainText — SAF: cevap metninin görünen hâli (baş/son boşluk düşer).
func trimExplainText(r explainCoreResult) string { return strings.TrimSpace(r.Text) }
