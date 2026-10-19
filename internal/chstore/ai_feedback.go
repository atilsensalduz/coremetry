package chstore

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

// AI answer feedback (v0.8.399, AI audit feedback slice). One row per
// rated exchange; ReplacingMergeTree(version) ORDER BY exchange_id — the
// latest verdict wins on FINAL reads. The /ai page aggregates this into
// a per-surface thumbs-up rate next to the call stats.
//
// v0.10.1393 — 👍/👎 rayı AI yüzeylerinden ve yazma ucu (POST
// /api/ai/feedback) API'den kaldırıldı (operatör; oy zayıf sinyal). Bu
// dosyada artık YAZICI yok: UpsertAIFeedback + yorum/neden koruma okuması +
// yüzey çözümü silindi. Şema ve geçmiş satırlar durur (90g TTL); aşağıdaki
// okumalar /ai'nın geçmiş oy görünümlerini besler.

// aiFeedbackCallCols — v0.10.1280 call_id + reason kolonları INSERT
// hedefinde var mı (boot probe, iki-boot sözleşmesi: küme kipinde ALTER
// ertelenir, ilk boot false okur → INSERT/SELECT iki kolonu ANMAZ).
var aiFeedbackCallCols atomic.Bool

// AIFeedbackCallCols — okuyucu bayrağı (v0.10.1393'ten beri yazıcı yok).
func AIFeedbackCallCols() bool { return aiFeedbackCallCols.Load() }

// probeAIFeedbackCallColumns — iki kolon da INSERT hedefinde görünüyorsa true.
func (s *Store) probeAIFeedbackCallColumns(ctx context.Context) bool {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'ai_feedback' AND name IN ('call_id', 'reason')`).Scan(&n)
	ok := err == nil && n >= 2
	aiFeedbackCallCols.Store(ok)
	return ok
}

// aiFeedbackAgg is one surface's verdict tally over a window.
type aiFeedbackAgg struct {
	Total uint64
	Up    uint64
}

// aiFeedbackBySurface tallies latest-verdict-wins feedback per surface
// for ComputeAIStats. FINAL (ReplacingMergeTree house rule) so a
// re-rated exchange counts once, with its newest verdict. Bounded:
// tiny state table, window-filtered, LIMIT 50 mirrors the surface
// breakdown's own cap.
func (s *Store) aiFeedbackBySurface(ctx context.Context, from, to time.Time) (map[string]aiFeedbackAgg, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT surface,
		       toUInt64(count()),
		       toUInt64(countIf(verdict = 1))
		FROM ai_feedback FINAL
		WHERE created_at >= toDateTime64(?, 9, 'UTC')
		  AND created_at <  toDateTime64(?, 9, 'UTC')
		GROUP BY surface
		LIMIT 50
		SETTINGS max_execution_time = 5`,
		chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]aiFeedbackAgg)
	for rows.Next() {
		var surface string
		var agg aiFeedbackAgg
		if err := rows.Scan(&surface, &agg.Total, &agg.Up); err != nil {
			return nil, err
		}
		out[surface] = agg
	}
	return out, rows.Err()
}

// NegativeFeedbackCall — 👎 alan bir cevabın madencilik satırı (v0.9.423).
type NegativeFeedbackCall struct {
	// ExchangeID — v0.10.423 (E5): satırın kimliği; "vakaya çevir" ve
	// evalset export bu kimlikle KAYNAKTAN okur (liste bayat olabilir).
	ExchangeID string `json:"exchangeId"`
	Surface    string `json:"surface"`
	CreatedAt  int64  `json:"createdAt"` // unix ns
	UserEmail  string `json:"userEmail,omitempty"`
	Prompt     string `json:"prompt"`
	Response   string `json:"response,omitempty"`
	// Comment (v0.9.1193) — operatörün 👎'ye eklediği neden. Madenciliğin
	// asıl sinyali: prompt neyin SORULDUĞUNU, yorum neyin EKSİK olduğunu
	// söyler.
	Comment string `json:"comment,omitempty"`
	// v0.10.1280 — hazır neden + oylanan çağrının kimliği (varsa).
	Reason string `json:"reason,omitempty"`
	CallID string `json:"callId,omitempty"`
}

// negFeedbackCols — SAF: 👎 okumalarının kolon listesi (bayrakla call_id/reason).
func negFeedbackCols(callCols bool) string {
	cols := `f.exchange_id, f.surface, toUnixTimestamp64Nano(f.created_at), f.user_email,
		       c.prompt_sample, c.response_sample, f.comment`
	if callCols {
		cols += `, f.reason, f.call_id`
	}
	return cols
}

func negFeedbackScan(r *NegativeFeedbackCall, callCols bool) []any {
	dest := []any{&r.ExchangeID, &r.Surface, &r.CreatedAt, &r.UserEmail, &r.Prompt, &r.Response, &r.Comment}
	if callCols {
		dest = append(dest, &r.Reason, &r.CallID)
	}
	return dest
}

// ListNegativeFeedbackCalls (v0.9.423, CoSRE fikir #6) — pencere içindeki
// verdict=-1 feedback'leri ai_calls örnekleriyle birleştirir: hangi soru
// şekilleri kötü cevap alıyor → yeni guided-intent adayları VERİDEN çıkar.
// İki tablo da küçük state tablosu (ai_feedback ReplacingMergeTree 90g TTL,
// ai_calls örnekleri 4KB cap'li) — JOIN hot-path değil, admin paneli okuması.
func (s *Store) ListNegativeFeedbackCalls(ctx context.Context, from, to time.Time, limit int) ([]NegativeFeedbackCall, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	cc := aiFeedbackCallCols.Load()
	rows, err := s.conn.Query(ctx, `
		SELECT `+negFeedbackCols(cc)+`
		FROM ai_feedback AS f FINAL
		LEFT JOIN ai_calls AS c ON c.exchange_id = f.exchange_id
		WHERE f.verdict = -1 AND f.created_at >= ? AND f.created_at <= ?
		ORDER BY f.created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NegativeFeedbackCall
	for rows.Next() {
		var r NegativeFeedbackCall
		if err := rows.Scan(negFeedbackScan(&r, cc)...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SREReviewDownvote — v0.10.1301 (AI SRE Faz 5): sre-* yüzeyli bir AI
// çağrısına verilmiş 👎. YALNIZ İNCELEME ADAYI ("incelemeye değer") —
// operatör kuralı "oylara çok güvenmeyelim": değerlendirmenin doğruluk
// etiketi DEĞİL, puanlamanın girdisi DEĞİL (doğruluk = onaylı incident_cases).
// Okuyucusu yalnız /ai inceleme yüzeyi (api/sre_eval.go); internal/sre bu
// okuyucuyu çağırmaz (cosrelearn TestVotesNeverReachRuntimeDecisions).
type SREReviewDownvote struct {
	ExchangeID string `json:"exchangeId"`
	CallID     string `json:"callId,omitempty"`
	Surface    string `json:"surface"`
	CreatedAt  int64  `json:"createdAt"` // unix ns
	UserEmail  string `json:"userEmail,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Comment    string `json:"comment,omitempty"`
	Prompt     string `json:"prompt,omitempty"`   // ≤600 karakter
	Response   string `json:"response,omitempty"` // ≤600 karakter
}

// SREReviewDownvotesMax — inceleme listesi tavanı.
const SREReviewDownvotesMax = 100

// sreReviewDownvotesSQL — SAF: 👎 (verdict = −1) ⋈ ai_calls (call_id = id,
// adım 8 / v0.10.1280) yüzey sre-* ile. Sağ taraf pencereli alt sorgu (ai_calls
// MergeTree, aylık partition): oy çağrıdan sonra gelir, çağrı penceresi oy
// penceresinin 7 gün öncesinden başlar. callCols=false (iki-boot: call_id
// kolonu henüz yok) → join yok, oyun kendi yüzeyi süzülür.
func sreReviewDownvotesSQL(callCols bool) string {
	if !callCols {
		return `
		SELECT f.exchange_id, '' AS call_id, f.surface, toUnixTimestamp64Nano(f.created_at), f.user_email,
		       '' AS reason, f.comment, '' AS prompt, '' AS response
		FROM ai_feedback AS f FINAL
		WHERE f.verdict = -1 AND startsWith(f.surface, 'sre-')
		  AND f.created_at >= toDateTime64(?, 9, 'UTC') AND f.created_at <= toDateTime64(?, 9, 'UTC')
		ORDER BY f.created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
	}
	return `
		SELECT f.exchange_id, f.call_id, c.surface, toUnixTimestamp64Nano(f.created_at), f.user_email,
		       f.reason, f.comment, substring(c.prompt_sample, 1, 600), substring(c.response_sample, 1, 600)
		FROM ai_feedback AS f FINAL
		INNER JOIN (
			SELECT id, surface, prompt_sample, response_sample
			FROM ai_calls
			WHERE startsWith(surface, 'sre-')
			  AND created_at >= toDateTime64(?, 9, 'UTC') AND created_at <= toDateTime64(?, 9, 'UTC')
			LIMIT 100000
		) AS c ON c.id = f.call_id
		WHERE f.verdict = -1 AND f.call_id != ''
		  AND f.created_at >= toDateTime64(?, 9, 'UTC') AND f.created_at <= toDateTime64(?, 9, 'UTC')
		ORDER BY f.created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// sreReviewDownvotesArgs — SAF (v0.10.1312, canlı motor testi aynı bind'i
// kullansın diye ayrıldı): sreReviewDownvotesSQL'in yer tutucu sırası.
func sreReviewDownvotesArgs(callCols bool, from, to time.Time, limit int) []any {
	if callCols {
		return []any{chDateTime64Arg(from.Add(-7 * 24 * time.Hour)), chDateTime64Arg(to), chDateTime64Arg(from), chDateTime64Arg(to), limit}
	}
	return []any{chDateTime64Arg(from), chDateTime64Arg(to), limit}
}

// ListSREReviewDownvotes — v0.10.1301: [from, to] içindeki sre-* 👎'leri
// (≤100). İnceleme listesi; hiçbir karar / puan bunu okumaz.
func (s *Store) ListSREReviewDownvotes(ctx context.Context, from, to time.Time, limit int) ([]SREReviewDownvote, error) {
	if limit <= 0 || limit > SREReviewDownvotesMax {
		limit = SREReviewDownvotesMax
	}
	cc := aiFeedbackCallCols.Load()
	rows, err := s.conn.Query(ctx, sreReviewDownvotesSQL(cc), sreReviewDownvotesArgs(cc, from, to, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SREReviewDownvote{}
	for rows.Next() {
		var r SREReviewDownvote
		if err := rows.Scan(&r.ExchangeID, &r.CallID, &r.Surface, &r.CreatedAt, &r.UserEmail,
			&r.Reason, &r.Comment, &r.Prompt, &r.Response); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NegativeFeedbackCallByExchange — v0.10.431: tek 👎 satırının NOKTA
// okuması (evalset export ?exchangeId=). Eskiden export son 200 satırlık
// pencereyi çekip Go'da süzüyordu: 7 günden eski ya da 201. satırdaki bir
// exchange "cases: []" ile sessizce boş dönüyordu. Zaman sınırı yok —
// kimlik zaten tekil; verdict=-1 şartı korunur (👍 vaka olmaz). Yoksa nil.
func (s *Store) NegativeFeedbackCallByExchange(ctx context.Context, exchangeID string) (*NegativeFeedbackCall, error) {
	if exchangeID == "" {
		return nil, nil
	}
	cc := aiFeedbackCallCols.Load()
	rows, err := s.conn.Query(ctx, `
		SELECT `+negFeedbackCols(cc)+`
		FROM ai_feedback AS f FINAL
		LEFT JOIN ai_calls AS c ON c.exchange_id = f.exchange_id
		WHERE f.exchange_id = ? AND f.verdict = -1
		LIMIT 1
		SETTINGS max_execution_time = 5`, exchangeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var r NegativeFeedbackCall
	if err := rows.Scan(negFeedbackScan(&r, cc)...); err != nil {
		return nil, err
	}
	return &r, nil
}

// ── KB terfi kuyruğu (v0.9.1195, AI Faz 5.2) ───────────────────────────

// KBCandidate — 👍 almış, henüz KB'ye alınmamış bir cevap adayı.
type KBCandidate struct {
	ExchangeID string `json:"exchangeId"`
	Surface    string `json:"surface"`
	CreatedAt  int64  `json:"createdAt"` // unix ns
	UserEmail  string `json:"userEmail,omitempty"`
	Prompt     string `json:"prompt"`
	Response   string `json:"response"`
}

// ListKBCandidates — pencere içindeki verdict=1 feedback'ler, ai_calls
// örnekleriyle; ZATEN terfi etmiş olanlar DIŞARIDA (v0.9.1195).
//
// Terfi işareti ayrı bir tablo DEĞİL (invariant #5'in ruhu): terfi eden
// cevap rag_chunks'a source='curated', source_ref=exchange_id olarak
// yazılıyor — varlığı işaretin kendisi. NOT IN alt sorgusu GLOBAL
// (make audit CHECK 5: dağıtıkta GLOBAL'siz IN alt sorguyu her shard'da
// ayrı koşar ve sessizce yanlış eler).
//
// response_sample boş satırlar elenir: gövdesi olmayan bir cevabı KB'ye
// almak boş chunk üretirdi. Negatif ikizle (ListNegativeFeedbackCalls)
// aynı maliyet sınıfı: iki küçük state tablosu, admin paneli okuması.
//
// v0.10.194 — `chat-general` (telemetriyle eşleşmeyen sorunun GENEL BİLGİ
// cevabı, copilot_intent.go) aday OLAMAZ: kanıtsız bir cevabı 👍 ile
// rag_chunks'a (source='curated') yazmak, RAG kademesinin onu "kaynak" diye
// atıflamasıdır; üstelik response_sample sunucunun "telemetriyle eşleşmedi"
// notunu taşımaz. Oylanabilir kalır (kalite sinyali), KB'ye girmez.
func (s *Store) ListKBCandidates(ctx context.Context, from, to time.Time, limit int) ([]KBCandidate, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.conn.Query(ctx, `
		SELECT f.exchange_id, f.surface, toUnixTimestamp64Nano(f.created_at), f.user_email,
		       c.prompt_sample, c.response_sample
		FROM ai_feedback AS f FINAL
		LEFT JOIN ai_calls AS c ON c.exchange_id = f.exchange_id
		WHERE f.verdict = 1 AND f.created_at >= ? AND f.created_at <= ?
		  AND c.response_sample != ''
		  AND c.surface NOT IN (?)
		  AND f.exchange_id GLOBAL NOT IN (
		      SELECT source_ref FROM rag_chunks FINAL
		      WHERE source = 'curated' AND source_ref != '')
		ORDER BY f.created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, from, to, aisurface.NoKBLabels(), limit) // v0.10.1153 — yüzey kaydından (chat-general)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KBCandidate
	for rows.Next() {
		var r KBCandidate
		if err := rows.Scan(&r.ExchangeID, &r.Surface, &r.CreatedAt, &r.UserEmail, &r.Prompt, &r.Response); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AICallSampleByExchange — terfi anında adayın TAM örneği (surface +
// prompt + response). Liste bayat olabilir; terfi eden içerik her zaman
// KAYNAKTAN okunur, listedeki kopyadan değil.
func (s *Store) AICallSampleByExchange(ctx context.Context, exchangeID string) (surface, prompt, response string, err error) {
	if exchangeID == "" {
		return "", "", "", nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT surface, prompt_sample, response_sample FROM ai_calls
		WHERE exchange_id = ?
		ORDER BY created_at DESC
		LIMIT 1
		SETTINGS max_execution_time = 5`, exchangeID)
	if err != nil {
		return "", "", "", err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(&surface, &prompt, &response); err != nil {
			return "", "", "", err
		}
	}
	return surface, prompt, response, rows.Err()
}
