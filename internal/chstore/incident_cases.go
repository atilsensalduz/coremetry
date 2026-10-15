package chstore

// incident_cases.go — CoSRE öğrenme Faz 3: vaka hafızası (DDL: store.go
// "incident_cases"). Taslağı internal/cosrelearn kurar; onay api katmanında
// (cosre_cases.go). Tam-satır replace: her yazıcı TÜM alanları taşır
// (invariant #4) — Upsert çağıran, okuduğu satırı değiştirip geri yazar.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Vaka durumları.
const (
	CaseDraft     = "draft"
	CaseConfirmed = "confirmed"
)

// IncidentCase — bir kapanmış olayın öğrenilmiş kaydı.
type IncidentCase struct {
	CaseID          string    `json:"caseId"`
	Source          string    `json:"source"` // problem | exception
	SourceID        string    `json:"sourceId"`
	Title           string    `json:"title"`
	Service         string    `json:"service"`
	Severity        string    `json:"severity,omitempty"`
	OpenedAt        time.Time `json:"openedAt"`
	ClosedAt        time.Time `json:"closedAt"`
	ErrorCodes      []string  `json:"errorCodes"`
	FunctionCodes   []string  `json:"functionCodes"`
	ChannelCodes    []string  `json:"channelCodes"`
	ErrRateBefore   float64   `json:"errRateBefore"`
	ErrRateDuring   float64   `json:"errRateDuring"`
	P99BeforeMs     float64   `json:"p99BeforeMs"`
	P99DuringMs     float64   `json:"p99DuringMs"`
	RateBefore      float64   `json:"rateBefore"`
	RateDuring      float64   `json:"rateDuring"`
	DeployInWindow  bool      `json:"deployInWindow"`
	DeployVersion   string    `json:"deployVersion,omitempty"`
	SuggestedCause  string    `json:"suggestedCause"`
	SuggestedSource string    `json:"suggestedSource,omitempty"`
	FinalCause      string    `json:"finalCause"`
	Category        string    `json:"category"`
	Resolution      string    `json:"resolution"`
	CosreCorrect    int8      `json:"cosreCorrect"` // -1 bilinmiyor, 0 yanlış, 1 doğru
	CorrectNote     string    `json:"correctNote,omitempty"`
	Status          string    `json:"status"`
	Provenance      string    `json:"provenance"`
	CreatedAt       time.Time `json:"createdAt"`
	ConfirmedBy     string    `json:"confirmedBy,omitempty"`
	ConfirmedAt     time.Time `json:"confirmedAt"`
}

// CaseIDFor — SAF: kaynak + kimlik → vaka kimliği (tekil, yeniden kurulabilir).
func CaseIDFor(source, sourceID string) string {
	switch source {
	case "exception":
		return "ex:" + sourceID
	default:
		return "p:" + sourceID
	}
}

const incidentCaseCols = `case_id, source, source_id, title, service, severity, opened_at, closed_at,
	error_codes, function_codes, channel_codes,
	err_rate_before, err_rate_during, p99_before_ms, p99_during_ms, rate_before, rate_during,
	deploy_in_window, deploy_version, suggested_cause, suggested_source,
	final_cause, category, resolution, cosre_correct, correct_note,
	status, provenance, created_at, confirmed_by, confirmed_at`

func incidentCaseArgs(c IncidentCase) []any {
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	status := c.Status
	if status == "" {
		status = CaseDraft
	}
	confirmedAt := c.ConfirmedAt
	if confirmedAt.IsZero() {
		confirmedAt = time.Unix(0, 0)
	}
	return []any{
		c.CaseID, c.Source, c.SourceID, c.Title, c.Service, c.Severity, c.OpenedAt.UTC(), c.ClosedAt.UTC(),
		nonNilStrings(c.ErrorCodes), nonNilStrings(c.FunctionCodes), nonNilStrings(c.ChannelCodes),
		c.ErrRateBefore, c.ErrRateDuring, c.P99BeforeMs, c.P99DuringMs, c.RateBefore, c.RateDuring,
		b2u8(c.DeployInWindow), c.DeployVersion, c.SuggestedCause, c.SuggestedSource,
		c.FinalCause, c.Category, c.Resolution, c.CosreCorrect, c.CorrectNote,
		status, c.Provenance, created.UTC(), c.ConfirmedBy, confirmedAt.UTC(),
	}
}

func (s *Store) UpsertIncidentCases(ctx context.Context, cases []IncidentCase) error {
	if len(cases) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, "INSERT INTO incident_cases ("+incidentCaseCols+")")
	if err != nil {
		return err
	}
	for _, c := range cases {
		if err := batch.Append(incidentCaseArgs(c)...); err != nil {
			return err
		}
	}
	return batch.Send()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanIncidentCase(r rowScanner) (IncidentCase, error) {
	var c IncidentCase
	var dep uint8
	err := r.Scan(&c.CaseID, &c.Source, &c.SourceID, &c.Title, &c.Service, &c.Severity, &c.OpenedAt, &c.ClosedAt,
		&c.ErrorCodes, &c.FunctionCodes, &c.ChannelCodes,
		&c.ErrRateBefore, &c.ErrRateDuring, &c.P99BeforeMs, &c.P99DuringMs, &c.RateBefore, &c.RateDuring,
		&dep, &c.DeployVersion, &c.SuggestedCause, &c.SuggestedSource,
		&c.FinalCause, &c.Category, &c.Resolution, &c.CosreCorrect, &c.CorrectNote,
		&c.Status, &c.Provenance, &c.CreatedAt, &c.ConfirmedBy, &c.ConfirmedAt)
	c.DeployInWindow = dep == 1
	if c.ConfirmedAt.Unix() <= 0 {
		c.ConfirmedAt = time.Time{}
	}
	return c, err
}

// GetIncidentCase — FINAL; yoksa nil.
func (s *Store) GetIncidentCase(ctx context.Context, caseID string) (*IncidentCase, error) {
	rows, err := s.conn.Query(ctx, "SELECT "+incidentCaseCols+` FROM incident_cases FINAL
		WHERE case_id = ? LIMIT 1 SETTINGS max_execution_time = 5`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	c, err := scanIncidentCase(rows)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ExistingCaseIDs — verilen kimliklerden zaten kurulmuş olanlar.
func (s *Store) ExistingCaseIDs(ctx context.Context, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `SELECT case_id FROM incident_cases
		WHERE case_id IN (?) LIMIT 100000 SETTINGS max_execution_time = 10`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ClosedProblemForCase — vaka kurucusunun problem girdisi.
type ClosedProblemForCase struct {
	ID, RuleID, RuleName, Severity, Service, Description, AISummary string
	StartedAt, ResolvedAt                                           time.Time
}

// ListClosedProblemsForCases — from'dan beri çözülmüş problemler (yeniden
// yazımda resolved_at taşınır; FINAL son sürüm).
func (s *Store) ListClosedProblemsForCases(ctx context.Context, from time.Time, limit int) ([]ClosedProblemForCase, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT id, rule_id, rule_name, severity, service, description, ai_summary,
		       started_at, assumeNotNull(resolved_at)
		FROM problems FINAL
		WHERE status = 'resolved' AND resolved_at IS NOT NULL AND resolved_at >= ?
		ORDER BY resolved_at
		LIMIT ?
		SETTINGS max_execution_time = 15`, chDateTime64Arg(from), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClosedProblemForCase
	for rows.Next() {
		var p ClosedProblemForCase
		if err := rows.Scan(&p.ID, &p.RuleID, &p.RuleName, &p.Severity, &p.Service, &p.Description, &p.AISummary,
			&p.StartedAt, &p.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ResolvedExceptionForCase — vaka kurucusunun exception girdisi.
type ResolvedExceptionForCase struct {
	Fingerprint, Type, Message, Service, Notes, AISummary string
	FirstSeen, LastSeen, ResolvedAt                       time.Time
	Occurrences                                           uint64
}

func (s *Store) ListResolvedExceptionsForCases(ctx context.Context, from time.Time, limit int) ([]ResolvedExceptionForCase, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT fingerprint, ex_type, ex_message, service, notes, ai_summary,
		       first_seen, last_seen, assumeNotNull(resolved_at), occurrences
		FROM exception_groups FINAL
		WHERE state = 'resolved' AND resolved_at IS NOT NULL AND resolved_at >= ?
		ORDER BY resolved_at
		LIMIT ?
		SETTINGS max_execution_time = 15`, chDateTime64Arg(from), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResolvedExceptionForCase
	for rows.Next() {
		var e ResolvedExceptionForCase
		if err := rows.Scan(&e.Fingerprint, &e.Type, &e.Message, &e.Service, &e.Notes, &e.AISummary,
			&e.FirstSeen, &e.LastSeen, &e.ResolvedAt, &e.Occurrences); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TopExceptionCodesForService — penceride görülen en kalabalık exception
// türleri (Oracle gruplarında ex_type = hata kodu, ör. MCA-00029).
func (s *Store) TopExceptionCodesForService(ctx context.Context, service string, from, to time.Time, limit int) ([]string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT ex_type FROM exception_groups FINAL
		WHERE service = ? AND last_seen >= ? AND first_seen <= ? AND ex_type != ''
		GROUP BY ex_type
		ORDER BY sum(occurrences) DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, service, chDateTime64Arg(from), chDateTime64Arg(to), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SimilarCase — similar_incidents satırı: vaka + puan + eşleşen kodlar.
type SimilarCase struct {
	IncidentCase
	Score        float64  `json:"score"`
	MatchedCodes []string `json:"matchedCodes"`
}

// Benzerlik ağırlıkları (CH'de hesaplanır; similarCasesSQL pinler).
const (
	SimilarServiceWeight = 3.0
	SimilarCodeWeight    = 2.0
	SimilarRecencyDays   = 30.0
)

// similarCasesSQL — SAF: aday süzgeci (servis eşleşmesi YA DA kod
// kesişimi) + puan = 3·servis + 2·|kod kesişimi| + exp(−yaş_gün/30);
// sıra: önce onaylı, sonra puan. Reddedilen/gürültü kategori dışarıda.
func similarCasesSQL(service string, codes []string, from time.Time, limit int) (string, []any) {
	if codes == nil {
		codes = []string{}
	}
	q := fmt.Sprintf(`
		SELECT %s,
		       arrayDistinct(arrayIntersect(arrayConcat(error_codes, function_codes, channel_codes), ?)) AS matched,
		       (service = ?) * %g + length(matched) * %g
		         + exp(-dateDiff('second', closed_at, now64(3)) / 86400.0 / %g) AS score
		FROM incident_cases FINAL
		WHERE closed_at >= ? AND category != 'noise'
		  AND (service = ? OR hasAny(arrayConcat(error_codes, function_codes, channel_codes), ?))
		ORDER BY (status = 'confirmed') DESC, score DESC, closed_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`,
		strings.ReplaceAll(incidentCaseCols, "\n", " "), SimilarServiceWeight, SimilarCodeWeight, SimilarRecencyDays)
	return q, []any{codes, service, chDateTime64Arg(from), service, codes, limit}
}

func (s *Store) SimilarIncidentCases(ctx context.Context, service string, codes []string, from time.Time, limit int) ([]SimilarCase, error) {
	q, args := similarCasesSQL(service, codes, from, limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SimilarCase
	for rows.Next() {
		var c IncidentCase
		var dep uint8
		var matched []string
		var score float64
		if err := rows.Scan(&c.CaseID, &c.Source, &c.SourceID, &c.Title, &c.Service, &c.Severity, &c.OpenedAt, &c.ClosedAt,
			&c.ErrorCodes, &c.FunctionCodes, &c.ChannelCodes,
			&c.ErrRateBefore, &c.ErrRateDuring, &c.P99BeforeMs, &c.P99DuringMs, &c.RateBefore, &c.RateDuring,
			&dep, &c.DeployVersion, &c.SuggestedCause, &c.SuggestedSource,
			&c.FinalCause, &c.Category, &c.Resolution, &c.CosreCorrect, &c.CorrectNote,
			&c.Status, &c.Provenance, &c.CreatedAt, &c.ConfirmedBy, &c.ConfirmedAt,
			&matched, &score); err != nil {
			return nil, err
		}
		c.DeployInWindow = dep == 1
		if matched == nil {
			matched = []string{}
		}
		out = append(out, SimilarCase{IncidentCase: c, Score: score, MatchedCodes: matched})
	}
	return out, rows.Err()
}

// TopRollupErrorCodes — servisin penceredeki en çok HATA veren function_code
// / channel_code değerleri (rollup ailesi; MV-first). Rollup tabloları
// kurulmamışsa (migrations elle) boş döner — best-effort.
func (s *Store) TopRollupErrorCodes(ctx context.Context, service, dim string, from, to time.Time, limit int) ([]string, error) {
	plan, err := PickRollup(RollupQuery{From: from, To: to, MaxDataPoints: 60, Dims: []string{dim}}, time.Now())
	if err != nil {
		return nil, nil
	}
	if ready, rerr := s.RollupTableReady(ctx, plan.Table); rerr != nil || !ready {
		return nil, nil
	}
	series, _, err := s.QueryRollupRED(ctx, plan, RollupSeriesFilter{Service: service, GroupBy: dim, MaxGroups: 20}, from, to)
	if err != nil {
		return nil, err
	}
	return topErrorGroups(series, limit), nil
}

// topErrorGroups — SAF: hata toplamına göre grup adları (hatasız grup yok).
func topErrorGroups(series []RollupSeries, limit int) []string {
	type g struct {
		name string
		errs uint64
	}
	var gs []g
	for _, s := range series {
		var e uint64
		for _, p := range s.Points {
			e += p.Errors
		}
		if e > 0 && s.Group != "" {
			gs = append(gs, g{s.Group, e})
		}
	}
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && (gs[j].errs > gs[j-1].errs || (gs[j].errs == gs[j-1].errs && gs[j].name < gs[j-1].name)); j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
	out := []string{}
	for i := 0; i < len(gs) && i < limit; i++ {
		out = append(out, gs[i].name)
	}
	return out
}
