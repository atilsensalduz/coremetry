package chstore

// cosre_interactions.go — CoSRE öğrenme Faz 1: tur başına etkileşim satırı
// (DDL: store.go "cosre_interactions"). Yazıcı api katmanındaki asenkron
// kuyruk (internal/api/cosre_interactions.go); burada yalnız toplu INSERT.

import (
	"context"
	"sync/atomic"
	"time"
)

// CosreTool — turda çalışan bir tool / kanıt okuması. Output, modelin
// gördüğü metnin önizlemesi (çağıran kırpar); Bytes kırpılmamış boy.
type CosreTool struct {
	Name   string
	Origin string // "" = modelin çağrısı, "guided" = sunucu ön-yüklemesi
	Args   string
	Ms     uint32
	OK     bool
	Error  string
	Bytes  uint32
	Output string
	// Evidence (v0.10.1280) — çipin kanıt ÖZETİ: "kaynak[#hop]:satır,…"
	// (api stepEvidenceSummary). Hacimli kanıt gövdesi değil.
	Evidence string
}

// CosreInteraction — bir CoSRE turu.
type CosreInteraction struct {
	ExchangeID     string
	ConversationID string
	Turn           uint16
	CreatedAt      time.Time
	UserID         string
	UserEmail      string
	Tier           string
	Surface        string
	Page           string
	Service        string
	ProblemID      string
	TraceID        string
	ExceptionID    string
	Question       string
	Answer         string
	AnswerChars    uint32
	Tools          []CosreTool
	Model          string
	ModelCalls     uint16
	InputTokens    uint32
	OutputTokens   uint32
	TTFTMs         uint32
	TotalMs        uint32
	Status         string
	ErrorMsg       string
	Attempt        string // "" | retry | regenerate | edit (istemci beyanı)
	Deep           bool
	EmptyAnswer    bool
	NoEvidence     bool
}

const cosreInteractionsBaseCols = `exchange_id, conversation_id, turn, created_at, user_id, user_email,
	tier, surface, page, service, problem_id, trace_id, exception_id,
	question, answer, answer_chars,
	tool_name, tool_origin, tool_args, tool_ms, tool_ok, tool_error, tool_bytes, tool_output,
	model, model_calls, input_tokens, output_tokens, ttft_ms, total_ms,
	status, error_msg, attempt, deep, empty_answer, no_evidence`

// cosreInteractionsEvidenceCol — v0.10.1280 tool_evidence kolonu INSERT
// hedefinde var mı (boot probe; küme kipinde ALTER ertelenir → ilk boot
// false okur ve INSERT kolonu ANMAZ — iki-boot sözleşmesi, ai_calls emsali).
var cosreInteractionsEvidenceCol atomic.Bool

// CosreInteractionsEvidenceCol — kolon bayrağı (test/teşhis).
func CosreInteractionsEvidenceCol() bool { return cosreInteractionsEvidenceCol.Load() }

// probeCosreInteractionsEvidenceColumn — INSERT hedefi cosre_interactions'ta
// (küme kipinde Distributed) tool_evidence var mı.
func (s *Store) probeCosreInteractionsEvidenceColumn(ctx context.Context) bool {
	var n uint64
	err := s.conn.QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = currentDatabase() AND table = 'cosre_interactions' AND name = 'tool_evidence'`).Scan(&n)
	ok := err == nil && n > 0
	cosreInteractionsEvidenceCol.Store(ok)
	return ok
}

// cosreInteractionsInsertSQL — SAF: kolon listesi bayrağa göre.
func cosreInteractionsInsertSQL(evidence bool) string {
	cols := cosreInteractionsBaseCols
	if evidence {
		cols += ", tool_evidence"
	}
	return "INSERT INTO cosre_interactions (\n\t" + cols + ")"
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// cosreInteractionArgs — SAF: satırın INSERT argümanları; tool dizileri
// paralel ve her zaman aynı uzunlukta.
// v0.10.1280 — evidence=true iken son arg paralel tool_evidence dizisi.
func cosreInteractionArgs(r CosreInteraction, evidence bool) []any {
	n := len(r.Tools)
	names, origins, args := make([]string, n), make([]string, n), make([]string, n)
	ms, bytes := make([]uint32, n), make([]uint32, n)
	oks := make([]uint8, n)
	errs, outs, evs := make([]string, n), make([]string, n), make([]string, n)
	for i, t := range r.Tools {
		names[i], origins[i], args[i] = t.Name, t.Origin, t.Args
		ms[i], bytes[i], oks[i] = t.Ms, t.Bytes, b2u8(t.OK)
		errs[i], outs[i], evs[i] = t.Error, t.Output, t.Evidence
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	status := r.Status
	if status == "" {
		status = "ok"
	}
	out := []any{
		r.ExchangeID, r.ConversationID, r.Turn, created.UTC(), r.UserID, r.UserEmail,
		r.Tier, r.Surface, r.Page, r.Service, r.ProblemID, r.TraceID, r.ExceptionID,
		r.Question, r.Answer, r.AnswerChars,
		names, origins, args, ms, oks, errs, bytes, outs,
		r.Model, r.ModelCalls, r.InputTokens, r.OutputTokens, r.TTFTMs, r.TotalMs,
		status, r.ErrorMsg, r.Attempt, b2u8(r.Deep), b2u8(r.EmptyAnswer), b2u8(r.NoEvidence),
	}
	if evidence {
		out = append(out, evs)
	}
	return out
}

// InsertCosreInteractions — toplu INSERT (tek batch). Boş dilim no-op.
func (s *Store) InsertCosreInteractions(ctx context.Context, rows []CosreInteraction) error {
	if s == nil || len(rows) == 0 {
		return nil
	}
	ev := cosreInteractionsEvidenceCol.Load()
	batch, err := s.conn.PrepareBatch(ctx, cosreInteractionsInsertSQL(ev))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(cosreInteractionArgs(r, ev)...); err != nil {
			return err
		}
	}
	return batch.Send()
}
