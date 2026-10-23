package api

// chat_offtopic_general.go — v0.10.1458 (operatör kararı 2026-10-23: "Genel
// soruları açık bırakalım, anahtar da olabilir."). v0.10.819'un kararını
// (copilot_offtopic.go: konu dışı soru MODEL cevabı almaz) anahtarla tersine
// çevirir: system_settings `ai.generalQuestions.allow` (varsayılan AÇIK) iken
// sınıflandırıcının off_topic dediği ve sunucu vetosunun geçirdiği soru KISA bir
// genel bilgi cevabı alır; altında v0.10.819'un yol tarifi çipleri/bağlantıları
// aynen durur (kullanıcı CoSRE'nin ne yaptığını yine öğrenir).
//
// Sınırlar (neden böyle):
//   - Tek araçsız çağrı (copilotStreamSurface → StreamText): tool tanımı yok,
//     telemetri döngüsü yok, RAG/wiki içeriği yok — prompt yalnız soru, o yüzden
//     localOnly kapısı (kod/wiki → yerel model) burada söz konusu değil.
//   - Derin kip YOK sayılır (copilot.WithoutDeepAnswer): genel cevap kısa kalır,
//     bütçe profilin tabanı. Sabit max_tokens tavanı KONMAZ: düşünen modeller
//     (Qwen3, gpt-5 …) küçük tavanda boş cevap döndürür (modelcaps dersi);
//     kısalık istemden (SystemPromptOffTopicGeneral), bütçe profilden.
//   - Zararlı istek (offTopicIsHarmful) anahtar açıkken de MODELE GİTMEZ:
//     v0.10.819'un kesin sınırı bayt bayt.
//   - Model hata verir / boş dönerse v0.10.819 sınırı (exchangeId'siz) — cevap
//     hiçbir zaman kesilmez; akış ortasında kopan çağrının kısmi metni korunur
//     (chat-general emsali, copilot_intent.go).
//   - Satır aisurface.ChatOffTopicGeneral (RoleAnswer): kök exchange'i aynen
//     taşır → oylanabilir (chat-general gibi); KB'ye terfi edemez (NoKB).
//     Anahtar açıkken chat-offtopic işaret satırı YAZILMAZ — model satırı onun
//     yerini alır (aynı tür /ai'da kendi yüzeyiyle görünür).

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// offTopicGeneralNoteTR — genel cevabın ÜSTÜNE sunucunun koyduğu not: cevabın
// telemetriden gelmediği modele bırakılmaz (intentGeneralNoteTR emsali).
const offTopicGeneralNoteTR = "_Bu soru Coremetry'nin izlediği telemetriyle ilgili değil — genel bilgiyle kısaca cevaplıyorum; aşağıdaki cevap senin sisteminin verisine dayanmaz._"

// offTopicUsesModel — SAF: konu dışı soru model cevabı alır mı? Anahtar açık
// VE istek zararlı değil.
func offTopicUsesModel(allow bool, question string) bool {
	return allow && !offTopicIsHarmful(normalizeGuidedMsg(question))
}

// offTopicGeneralEnabled — canlı karar: anahtar + zararlılık + model hazır.
func (s *Server) offTopicGeneralEnabled(question string) bool {
	if s == nil || s.copilot == nil || !s.copilot.Active() {
		return false
	}
	return offTopicUsesModel(chstore.CurrentAIGeneralQuestions().Allow, question)
}

// guidedOffTopicDispatch — runGuidedRoute'un off_topic dalı: anahtar açıksa
// kısa genel cevap, kapalıysa (ya da zararlı istek) v0.10.819 sınırı.
func (s *Server) guidedOffTopicDispatch(ctx context.Context, emit func(string, any), question, ctxService string) (bool, bool) {
	if s.offTopicGeneralEnabled(question) {
		return s.offTopicGeneralAnswer(ctx, emit, question, ctxService)
	}
	return s.guidedOffTopicAnswer(emit, question, ctxService)
}

// offTopicGeneralText — SAF: model çıktısı (+ hata) → ekrana giden metin.
// general=false ⇒ v0.10.819 sınırı (deterministik, oylanmaz).
// Akış ortasında kopan çağrının kısmi metni korunur (hata adım çipinde görünür).
func offTopicGeneralText(raw, question, ctxService string) (text string, general bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return offTopicAnswerText(question, ctxService), false
	}
	return offTopicGeneralNoteTR + "\n\n" + raw + "\n\n" + offTopicGuidanceTR(ctxService), true
}

// offTopicGeneralAnswer — tek araçsız çağrı + çipler/bağlantılar.
func (s *Server) offTopicGeneralAnswer(ctx context.Context, emit func(string, any), question, ctxService string) (bool, bool) {
	q := question
	if r := []rune(q); len(r) > 1500 {
		q = string(r[:1500])
	}
	const tool = "off_topic_general"
	args, _ := json.Marshal(map[string]string{"q": clipRunes(question, 120), "service": ctxService})
	n := emitGuidedStep(emit, tool, string(args))
	t0 := time.Now()
	emit("delta", map[string]string{"text": offTopicGeneralNoteTR + "\n\n"})
	raw, err := s.copilotStreamSurface(copilot.WithoutDeepAnswer(ctx), aisurface.ChatOffTopicGeneral,
		copilot.SystemPromptOffTopicGeneral(), q, func(d string) { emit("delta", map[string]string{"text": d}) })
	text, general := offTopicGeneralText(raw, question, ctxService)
	if err != nil {
		emitStepEvidence(emit, n, tool, "", err)
	} else if n > 0 {
		preview, trunc := clipStepPreview(strings.TrimSpace(raw))
		emit("step-result", map[string]any{
			"i": n, "tool": tool, "ok": true, "preview": preview, "truncated": trunc,
			"bytes": len(raw), "durationMs": time.Since(t0).Milliseconds(),
		})
	}
	ans := map[string]any{
		"text":        text,
		"suggestions": offTopicChips(ctxService),
		"links":       offTopicLinks(ctxService),
	}
	if general {
		// Model metni oylanabilir: exchangeId = sohbetin kendi alışverişi (satır
		// RoleAnswer, kök kimliği aynen taşır). Deterministik düşüşte YOK.
		ans["exchangeId"] = copilot.MetaFromContext(ctx).ExchangeID
	}
	emit("answer", ans)
	return true, true
}
