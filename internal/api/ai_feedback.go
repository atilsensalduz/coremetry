package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// AI answer feedback (v0.8.399, AI audit feedback slice). The chat
// drawer renders thumbs up/down under each assistant answer; a click
// POSTs the exchangeId the SSE answer event carried + the verdict.
// One ai_feedback row per exchange (ReplacingMergeTree — re-rating
// replaces), aggregated into the /ai per-surface breakdown as a
// thumbs-up rate. Provider-agnostic: the verdict rates the answer,
// not the model that produced it.
//
// Auth: any authenticated user (global auth middleware) — whoever can
// chat can rate the answer they got; mirrors POST /api/copilot/chat.
// Deliberately NO audit entry: this is high-frequency user-scoped
// quality signal, not an admin/config mutation (the saved_view.create
// audit precedent covers named artifacts other operators see; a
// thumb press is neither).

type aiFeedbackRequest struct {
	ExchangeID string `json:"exchangeId"`
	Verdict    int8   `json:"verdict"` // 1 | -1
	// Comment (v0.9.1193, Faz 5.1) — 👎'nin serbest metni. POINTER ve bu
	// tel sözleşmesinin kendisi: alan HİÇ yoksa (eski FE, ChatBubble'ın
	// düz oy tıkları) saklanan yorum KORUNUR; boş dize gönderilirse
	// TEMİZLENİR. İkisini ayırt etmeden, herhangi bir yüzeyden atılan bir
	// oy flip'i operatörün yazdığı metni sessizce silerdi
	// (ReplacingMergeTree tam-satır replace).
	Comment *string `json:"comment"`
	// CallID (v0.10.1280, CoSRE adım 8) — cevabı üreten ai_calls satırı
	// (sohbet `answer` olayının opsiyonel `callId`'si). Bir exchange'in
	// birden çok çağrısı olabildiği için oy SOMUT çağrıya bağlanır. Pointer
	// ve preserve sözleşmesi comment'le aynı: alan yoksa (eski istemci, düz
	// oy tıkı) saklanan kimlik korunur; eski istemci exchange düzeyinde oy
	// vermeye devam eder.
	CallID *string `json:"callId"`
	// Reason (v0.10.1280) — 👎'nin hazır nedeni (chstore.AIFeedbackReasons:
	// wrong | incomplete | irrelevant). nil → korunur, "" → temizlenir.
	Reason *string `json:"reason"`
}

// aiFeedbackMaxIDLen bounds the exchangeId a client can post. The
// server mints 32-char hex ids (newRandID(16)); 64 leaves headroom
// without letting a hostile client stuff a blob into the dedup key.
const aiFeedbackMaxIDLen = 64

// aiFeedbackMaxCommentRunes — yorum tavanı, RUNE cinsinden (Türkçe metin
// bayt tavanıyla karakter ortasından kesilirdi). 2000: birkaç paragraf
// yeter; ai_feedback bir madencilik tablosu, günlük tutma yeri değil.
const aiFeedbackMaxCommentRunes = 2000

// normalizeFeedbackComment — SAF gövde-yorum kararı (tablo-testli).
// nil → (preserve=true): saklanan taşınacak. Dolu → kırpılmış değer;
// tavan aşımı hata (sessiz kesme, operatörün yazdığını "kaydettim" deyip
// yarısını atmak olurdu).
func normalizeFeedbackComment(c *string) (val string, preserve bool, err error) {
	if c == nil {
		return "", true, nil
	}
	v := strings.TrimSpace(*c)
	if utf8.RuneCountInString(v) > aiFeedbackMaxCommentRunes {
		return "", false, fmt.Errorf("comment en fazla %d karakter olabilir", aiFeedbackMaxCommentRunes)
	}
	return v, false, nil
}

// normalizeFeedbackCallID — SAF (v0.10.1280): nil → preserve; dolu → ai_calls
// kimlik biçimi (hex, ≤ aiFeedbackMaxIDLen). Biçim dışı kimlik 400: dedup
// anahtarı olmasa da satıra serbest blob yazdırmayalım. Var olduğu
// DOĞRULANMAZ — ai_calls yazımı asenkron (RecordUsage gorutini), henüz
// inmemiş satırı reddetmek oyu düşürürdü (surface çözümünün aynı gerekçesi).
func normalizeFeedbackCallID(c *string) (val string, preserve bool, err error) {
	if c == nil {
		return "", true, nil
	}
	v := strings.TrimSpace(*c)
	if v == "" {
		return "", true, nil // boş kimlik bilgi taşımaz: saklananı silmesin
	}
	if len(v) > aiFeedbackMaxIDLen {
		return "", false, errors.New("callId too long")
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", false, errors.New("callId must be lowercase hex")
		}
	}
	return v, false, nil
}

// normalizeFeedbackReason — SAF (v0.10.1280): nil → preserve; "" → temizle;
// kapalı küme dışı → hata.
func normalizeFeedbackReason(r *string) (val string, preserve bool, err error) {
	if r == nil {
		return "", true, nil
	}
	v := strings.TrimSpace(*r)
	if v == "" {
		return "", false, nil
	}
	for _, ok := range chstore.AIFeedbackReasons {
		if v == ok {
			return v, false, nil
		}
	}
	return "", false, fmt.Errorf("reason must be one of %s", strings.Join(chstore.AIFeedbackReasons, ", "))
}

// validateAIFeedback is the pure request gate — split out so the
// v0.8.399 regression test can drive it table-style without HTTP.
func validateAIFeedback(exchangeID string, verdict int8) error {
	if strings.TrimSpace(exchangeID) == "" {
		return errors.New("exchangeId required")
	}
	if len(exchangeID) > aiFeedbackMaxIDLen {
		return errors.New("exchangeId too long")
	}
	if verdict != 1 && verdict != -1 {
		return errors.New("verdict must be 1 or -1")
	}
	return nil
}

// postAIFeedback stores one verdict. The surface label is resolved
// server-side from the ai_calls row carrying the same exchange_id —
// the client can't mislabel a verdict, and a not-yet-flushed row
// (RecordUsage inserts on a goroutine) degrades to surface ” rather
// than rejecting the click.
func (s *Server) postAIFeedback(w http.ResponseWriter, r *http.Request) {
	var req aiFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req.ExchangeID = strings.TrimSpace(req.ExchangeID)
	if err := validateAIFeedback(req.ExchangeID, req.Verdict); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	comment, keepComment, cerr := normalizeFeedbackComment(req.Comment)
	if cerr != nil {
		http.Error(w, cerr.Error(), http.StatusBadRequest)
		return
	}
	callID, keepCall, kerr := normalizeFeedbackCallID(req.CallID)
	if kerr != nil {
		http.Error(w, kerr.Error(), http.StatusBadRequest)
		return
	}
	reason, keepReason, rerr := normalizeFeedbackReason(req.Reason)
	if rerr != nil {
		http.Error(w, rerr.Error(), http.StatusBadRequest)
		return
	}
	if keepComment || keepCall || keepReason {
		// Gövde bir alanı taşımıyor (düz oy tıkı, eski istemci): saklananı
		// taşı — tam-satır replace aksi hâlde yorumu/nedeni/çağrı bağını
		// boş sürümle ezerdi. Soft-fail: okuma düşerse alan kaybı, oy
		// POST'unu düşürmekten ucuz.
		if stored, serr := s.store.AIFeedbackStoredByExchange(r.Context(), req.ExchangeID); serr == nil {
			comment, reason, callID = mergeStoredFeedback(stored, comment, reason, callID, keepComment, keepReason, keepCall)
		}
	}
	surface, err := s.store.AICallSurfaceByExchange(r.Context(), req.ExchangeID)
	if err != nil {
		writeErr(w, err)
		return
	}
	email := ""
	if c := auth.FromContext(r.Context()); c != nil {
		email = c.Email
	}
	if err := s.store.UpsertAIFeedback(r.Context(), chstore.AIFeedback{
		ExchangeID: req.ExchangeID,
		Surface:    surface,
		Verdict:    req.Verdict,
		Comment:    comment,
		UserEmail:  email,
		CallID:     callID,
		Reason:     reason,
	}); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// mergeStoredFeedback — SAF (v0.10.1280): gövdenin göndermediği (keep*)
// alanlar saklanandan; gönderilenler gövdeden. Tablo-testli.
func mergeStoredFeedback(stored chstore.AIFeedbackStored, comment, reason, callID string, keepComment, keepReason, keepCall bool) (string, string, string) {
	if keepComment {
		comment = stored.Comment
	}
	if keepReason {
		reason = stored.Reason
	}
	if keepCall {
		callID = stored.CallID
	}
	return comment, reason, callID
}

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
