package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// AI answer feedback — v0.8.399'dan v0.10.1392'ye kadar AI cevaplarının
// altında 👍/👎 rayı vardı (POST /api/ai/feedback → ai_feedback satırı).
//
// v0.10.1393 — operatör: "Faydalı faydasız kaldıralım." Ray HER yüzeyden
// kaldırıldı ve yazma ucu (POST /api/ai/feedback + istek kapısı) silindi:
// politika gereği oy zayıf sinyal, ne çalışma zamanı kararını ne
// değerlendirme etiketini sürer; arayüzü olmayan bir yazma ucu da ölü
// yüzeydir. Şema ve GEÇMİŞ veri durur (ai_feedback tablosu, 90g TTL): /ai
// okumaları (yüzey memnuniyeti, etkileşim Oy kolonu, 👎 paneli, evalset
// dışa aktarımı, KB terfi kuyruğu) eski satırları okumaya devam eder ve
// TTL dolunca kendiliğinden boşalır.

// aiFeedbackMaxIDLen bounds a client-posted exchangeId (CoSRE örtük
// sinyali, RAG küratörlüğü, evalset dışa aktarımı). The server mints
// 32-char hex ids (newRandID(16)); 64 leaves headroom without letting a
// hostile client stuff a blob into a key.
const aiFeedbackMaxIDLen = 64

// listNegativeAIFeedback (v0.9.423, CoSRE fikir #6) — 👎 madenciliği:
// pencere içindeki düşük puanlı cevaplar prompt örnekleriyle. Hangi
// soru şekilleri kötü cevap alıyor → yeni guided-intent adayları
// VERİDEN çıkar. Admin-gated (rota), 60s cache — panel okuması.
func (s *Server) listNegativeAIFeedback(w http.ResponseWriter, r *http.Request) {
	rangeS := int64(7 * 86400)
	if v := r.URL.Query().Get("rangeS"); v != "" {
		if n := parseInt(v, 0); n > 0 && n <= 90*86400 {
			rangeS = int64(n)
		}
	}
	key := fmt.Sprintf("ai:negfb:v3:range=%d:cc=%t", rangeS, chstore.AIFeedbackCallCols()) // v0.10.423 — satır exchangeId kazandı; v0.10.1280 reason/callId (şekil + bayrak)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		to := time.Now()
		from := to.Add(-time.Duration(rangeS) * time.Second)
		rows, err := s.store.ListNegativeFeedbackCalls(ctx, from, to, 100)
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []chstore.NegativeFeedbackCall{}
		}
		return map[string]any{"rows": rows, "rangeS": rangeS}, nil
	})
}
