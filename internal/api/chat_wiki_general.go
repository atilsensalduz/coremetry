package api

// chat_wiki_general.go — v0.10.1549 (operatör, ekran görüntüsü: "Opentelemetry
// javada nasıl instrumente edebilirim" → "Wikide bulunamadı; kaynaklar yalnızca
// … linkler sunmaktadır". "Model buna kendi bilgisinden yanıt dönebilir.")
//
// Wiki kademesi "bulunamadı" dediğinde cevap orada BİTMEZ: genel sorular
// anahtarı (ai.generalQuestions, varsayılan açık) ve zararlı-istek süzgeci
// izin veriyorsa (offTopicGeneralEnabled — off_topic yolunun AYNI kapısı) tool'suz
// TEK genel-bilgi çağrısı (chat-general yüzeyi; v0.10.1550'den beri ayrıntılı
// teknik istem SystemPromptWikiGeneral — örnek kod/config, adımlar) akışla
// cevaplar. Etiket sunucudan, modelden beklenmez: önce wiki'de olmadığı ve
// cevabın kuruma özgü olmadığı yazılır. Wiki'nin bulduğu sayfa çipleri altta
// kalır (ilgili sayfalar). Çağrıya wiki içeriği GİTMEZ — yalnız soru; hassas
// (localOnly) wiki sayfası bu yolda okunmaz.
//
// Kapı kapalı, model hata verdi ya da boş döndü → bayt bayt eski ret cevabı.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// wikiGeneralNoteTR — genel bilgi cevabının sunucu etiketi.
const wikiGeneralNoteTR = "_Kurum wiki'sinde bu soruya dair bilgi bulunamadı — aşağıdaki cevap modelin genel bilgisidir, kurumunuza özgü (ortam, standart, servis) bilgi içermez._"

// wikiGeneralText — SAF: model çıktısı → ekrana giden metin; boşsa ok=false
// (çağıran ret cevabını aynen gönderir).
func wikiGeneralText(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	return wikiGeneralNoteTR + "\n\n" + raw, true
}

// wikiGeneralFallback — wiki ret cevabını (ans) genel bilgi cevabına çevirir.
// true → ans güncellendi (metin + oylanabilir exchangeId; kaynak çipleri aynen).
func (s *Server) wikiGeneralFallback(ctx context.Context, question string, ans map[string]any, emit func(string, any)) bool {
	if !s.offTopicGeneralEnabled(question) {
		return false
	}
	const gtool = "general_answer"
	gi := emitStepChipOrigin(emit, gtool, intentStepArgs(question), "wiki")
	t0 := time.Now()
	emit("delta", map[string]string{"text": wikiGeneralNoteTR + "\n\n"})
	raw, err := s.copilotStreamSurface(ctx, "chat-general", copilot.SystemPromptWikiGeneral(), question, // v0.10.1550 — ayrıntılı teknik istem
		func(d string) { emit("delta", map[string]string{"text": d}) })
	if err != nil {
		emitStepEvidence(emit, gi, gtool, "", err)
		// Akış ortasında kopan çağrı metni taşır; hiç metin yoksa ret cevabı.
		if strings.TrimSpace(raw) == "" {
			return false
		}
	} else if gi > 0 {
		preview, trunc := clipStepPreview(strings.TrimSpace(raw))
		emit("step-result", map[string]any{
			"i": gi, "tool": gtool, "ok": true, "preview": preview, "truncated": trunc,
			"bytes": len(raw), "durationMs": time.Since(t0).Milliseconds(),
		})
	}
	text, ok := wikiGeneralText(raw)
	if !ok {
		return false
	}
	ans["text"] = text
	ans["exchangeId"] = copilot.MetaFromContext(ctx).ExchangeID
	return true
}
