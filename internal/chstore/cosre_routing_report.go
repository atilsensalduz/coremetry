package chstore

// cosre_routing_report.go — v0.10.1464: haftalık yanlış-yönlendirme raporunun
// TEK okuması (api ai_routing_report.go). Yeni şema yok: cosre_interactions
// zaten tur başına kademe, etiket (cosre-turn:<kademe>/<rota>), sonuç, boş /
// kanıtsız cevap ve istemcinin yeniden deneme beyanını taşıyor. Tablo küçük
// (tur başına bir satır); yine de zaman sınırlı + LIMIT + max_execution_time,
// metinler kırpılmış, araç dizilerinden yalnız uzunluk okunur.
//
// ORDER BY created_at DESC: LIMIT taşarsa EN YENİ turlar kalır (rapor "bu
// hafta" der). Puanlama ve yeniden-sorma eşleştirmesi Go'da, saf.

import (
	"context"
	"time"
)

// RoutingTurn — rapor girdisi: bir CoSRE turunun yönlendirme özeti.
type RoutingTurn struct {
	ExchangeID     string
	ConversationID string
	Turn           uint16
	CreatedAt      time.Time
	UserEmail      string
	Tier           string
	Surface        string
	Service        string
	Page           string
	Question       string
	Answer         string // ilk 200 rune
	Status         string
	ErrorMsg       string
	Attempt        string
	EmptyAnswer    bool
	NoEvidence     bool
	TotalMs        uint32
	ModelCalls     uint16
	Tools          uint32 // tool_name dizisinin uzunluğu
}

// cosreRoutingTurnsSQL — bind: from, to (DateTime64(3) — toDateTime64(?, 3)
// tz'siz argümanla), limit. Metinler substringUTF8 ile (rune sınırı).
const cosreRoutingTurnsSQL = `SELECT exchange_id, conversation_id, turn, created_at, user_email,
		       tier, surface, service, page,
		       substringUTF8(question, 1, 300), substringUTF8(answer, 1, 200),
		       status, substringUTF8(error_msg, 1, 200), attempt, empty_answer, no_evidence,
		       total_ms, model_calls, toUInt32(length(tool_name))
		FROM cosre_interactions
		WHERE created_at >= toDateTime64(?, 3, 'UTC')
		  AND created_at <  toDateTime64(?, 3, 'UTC')
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 15`

// CosreRoutingTurns — penceredeki turlar (en yeni önce, en çok limit).
func (s *Store) CosreRoutingTurns(ctx context.Context, from, to time.Time, limit int) ([]RoutingTurn, error) {
	rows, err := s.conn.Query(ctx, cosreRoutingTurnsSQL, chDateTime64Arg(from), chDateTime64Arg(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutingTurn
	for rows.Next() {
		var r RoutingTurn
		var empty, noEv uint8
		if err := rows.Scan(&r.ExchangeID, &r.ConversationID, &r.Turn, &r.CreatedAt, &r.UserEmail,
			&r.Tier, &r.Surface, &r.Service, &r.Page, &r.Question, &r.Answer,
			&r.Status, &r.ErrorMsg, &r.Attempt, &empty, &noEv,
			&r.TotalMs, &r.ModelCalls, &r.Tools); err != nil {
			return nil, err
		}
		r.EmptyAnswer, r.NoEvidence = empty == 1, noEv == 1
		out = append(out, r)
	}
	return out, rows.Err()
}
