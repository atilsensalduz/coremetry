package chstore

// cosre_grounding.go — CoSRE öğrenme Faz 4: çevrimdışı dayanak denetiminin
// G/Ç'si (DDL: store.go "cosre_answer_grounding"). Hakem mantığı
// internal/cosrelearn/grounding.go.

import (
	"context"
	"time"
)

// CosreGroundingInput — hakeme giden tur: soru, cevap, okunan tool çıktıları.
type CosreGroundingInput struct {
	ExchangeID string
	CreatedAt  time.Time
	Question   string
	Answer     string
	Tools      []CosreTool // Name + OK + Output
}

// ListCosreForGrounding — günün örneklemi: dolu, hatasız, kanıtsız-ilan
// etmemiş cevaplar; exchange_id hash'iyle deterministik örnek (aynı gün
// yeniden koşu aynı örneği seçer).
func (s *Store) ListCosreForGrounding(ctx context.Context, from, to time.Time, limit int) ([]CosreGroundingInput, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, created_at, question, answer, tool_name, tool_ok, tool_output
		FROM cosre_interactions
		WHERE created_at >= ? AND created_at < ?
		  AND status = 'ok' AND empty_answer = 0 AND no_evidence = 0 AND answer_chars > 0
		ORDER BY cityHash64(exchange_id)
		LIMIT ?
		SETTINGS max_execution_time = 20`, chDateTime64Arg(from), chDateTime64Arg(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CosreGroundingInput
	for rows.Next() {
		var g CosreGroundingInput
		var names, outs []string
		var oks []uint8
		if err := rows.Scan(&g.ExchangeID, &g.CreatedAt, &g.Question, &g.Answer, &names, &oks, &outs); err != nil {
			return nil, err
		}
		for i := range names {
			t := CosreTool{Name: names[i]}
			if i < len(oks) {
				t.OK = oks[i] == 1
			}
			if i < len(outs) {
				t.Output = outs[i]
			}
			g.Tools = append(g.Tools, t)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// CosreGrounding — bir cevabın hakem sonucu.
type CosreGrounding struct {
	ExchangeID string    `json:"exchangeId"`
	CreatedAt  time.Time `json:"createdAt"`
	Verdict    string    `json:"verdict"` // grounded | ungrounded | no_evidence | no_claims | judge_error
	Claims     uint16    `json:"claims"`
	Grounded   uint16    `json:"grounded"`
	Ungrounded uint16    `json:"ungrounded"`
	NoEvidence uint16    `json:"noEvidence"`
	Details    string    `json:"details"` // iddia JSON'u
	Model      string    `json:"model,omitempty"`
}

func (s *Store) InsertCosreGrounding(ctx context.Context, rows []CosreGrounding) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO cosre_answer_grounding
		(exchange_id, created_at, verdict, claims, grounded, ungrounded, no_evidence, details, model)`)
	if err != nil {
		return err
	}
	for _, g := range rows {
		if err := batch.Append(g.ExchangeID, g.CreatedAt.UTC(), g.Verdict, g.Claims, g.Grounded, g.Ungrounded, g.NoEvidence, g.Details, g.Model); err != nil {
			return err
		}
	}
	return batch.Send()
}

// UngroundedExchanges — penceredeki dayanaksız iddia içeren cevaplar
// (Faz 2 puanına SigUngrounded olarak girer).
func (s *Store) UngroundedExchanges(ctx context.Context, from, to time.Time) (map[string]bool, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id FROM cosre_answer_grounding FINAL
		WHERE created_at >= ? AND created_at < ? AND ungrounded > 0
		LIMIT 100000
		SETTINGS max_execution_time = 10`, chDateTime64Arg(from), chDateTime64Arg(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// CosreGroundingByExchange — evalset dışa aktarımı için hakem ayrıntıları.
func (s *Store) CosreGroundingByExchange(ctx context.Context, ids []string, from time.Time) (map[string]CosreGrounding, error) {
	out := map[string]CosreGrounding{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, created_at, verdict, claims, grounded, ungrounded, no_evidence, details, model
		FROM cosre_answer_grounding FINAL
		WHERE created_at >= ? AND exchange_id IN (?)
		LIMIT ?
		SETTINGS max_execution_time = 10`, chDateTime64Arg(from), ids, len(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g CosreGrounding
		if err := rows.Scan(&g.ExchangeID, &g.CreatedAt, &g.Verdict, &g.Claims, &g.Grounded, &g.Ungrounded, &g.NoEvidence, &g.Details, &g.Model); err != nil {
			return nil, err
		}
		out[g.ExchangeID] = g
	}
	return out, rows.Err()
}

// CosreInteractionsForEval — evalset vakası için soru + çalışan tool'lar.
func (s *Store) CosreInteractionsForEval(ctx context.Context, ids []string, from time.Time) (map[string]CosreInteraction, error) {
	out := map[string]CosreInteraction{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT exchange_id, question, answer, tier, page, service, tool_name, tool_ok
		FROM cosre_interactions
		WHERE created_at >= ? AND exchange_id IN (?)
		LIMIT ?
		SETTINGS max_execution_time = 10`, chDateTime64Arg(from), ids, len(ids)*2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r CosreInteraction
		var names []string
		var oks []uint8
		if err := rows.Scan(&r.ExchangeID, &r.Question, &r.Answer, &r.Tier, &r.Page, &r.Service, &names, &oks); err != nil {
			return nil, err
		}
		for i := range names {
			r.Tools = append(r.Tools, CosreTool{Name: names[i], OK: i < len(oks) && oks[i] == 1})
		}
		out[r.ExchangeID] = r
	}
	return out, rows.Err()
}
