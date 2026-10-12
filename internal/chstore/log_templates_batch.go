package chstore

import (
	"context"
	"time"
)

// v0.10.1157 — prod: coremetry-worker'da `clickhouse.batch INSERT INTO
// log_templates` span'leri 2µs'de "context deadline exceeded" ile düşüyordu.
// Templater her turda 30 sn'lik TEK bağlamla önce ES örneğini çekip sonra
// küme başına UpsertLogTemplate çağırıyordu: şablon başına bir `SELECT …
// FINAL` + bir INSERT = 2N gidiş-dönüş. Bütçe bitince kalan şablonlar daha
// başlamadan düşüyor, last_seen tazelenmiyor ve trace'ler hata gürültüsüyle
// doluyordu. UpsertLogTemplates aynı semantiği (first_seen yapışkan, yalnız
// geriye gider) İKİ sorguda kurar: parçalı tek SELECT + tek batch INSERT.

// logTemplateLookupChunk — `id IN (?)` parça boyu.
const logTemplateLookupChunk = 1000

// UpsertLogTemplates — UpsertLogTemplate'in toplu ikizi. Boş liste no-op.
func (s *Store) UpsertLogTemplates(ctx context.Context, ts []LogTemplate) error {
	if len(ts) == 0 {
		return nil
	}
	firsts := make(map[string]time.Time, len(ts))
	for i := 0; i < len(ts); i += logTemplateLookupChunk {
		end := i + logTemplateLookupChunk
		if end > len(ts) {
			end = len(ts)
		}
		ids := make([]string, 0, end-i)
		for _, t := range ts[i:end] {
			ids = append(ids, t.ID)
		}
		rows, err := s.conn.Query(ctx,
			`SELECT id, min(first_seen) FROM log_templates FINAL
			 WHERE id IN (?) GROUP BY id LIMIT ?
			 SETTINGS max_execution_time = 10`, ids, len(ids))
		if err != nil {
			// Yapışkanlık en iyi çaba: okuma düşerse yazım yine yapılır
			// (tek şablon yolu da hatayı yutuyordu).
			continue
		}
		for rows.Next() {
			var id string
			var f time.Time
			if rows.Scan(&id, &f) == nil {
				firsts[id] = f
			}
		}
		_ = rows.Close()
	}

	batch, err := s.conn.PrepareBatch(ctx,
		`INSERT INTO log_templates (id, template, first_seen, last_seen,
			total_count, services, exception_type, sample, version)`)
	if err != nil {
		return err
	}
	ver := uint64(time.Now().UnixNano())
	for _, t := range ts {
		first := time.Unix(0, t.FirstSeen).UTC()
		if ex, ok := firsts[t.ID]; ok && !ex.IsZero() && ex.Before(first) {
			first = ex
		}
		services := t.Services
		if services == nil {
			services = []string{}
		}
		if err := batch.Append(
			t.ID, t.Template, first, time.Unix(0, t.LastSeen).UTC(), t.TotalCount,
			services, t.ExceptionType, t.Sample, ver,
		); err != nil {
			_ = batch.Abort()
			return err
		}
	}
	return batch.Send()
}
