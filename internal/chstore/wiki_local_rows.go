package chstore

// wiki_local_rows.go — v0.10.1176 — yüklenen belgelerde (wiki indeksi, yerel
// kaynak) birebir değer araması: tablo satırı kademesinin okuması
// (api/chat_doc_rows.go). Durum tablosu (wiki_chunks), zaman sütunu yok;
// sınır wiki_id + LIMIT + max_execution_time.

import (
	"context"
	"strings"
)

// LocalDocChunk — eşleşen parça (belge yolu, başlık, metin).
type LocalDocChunk struct {
	Path  string
	Title string
	URL   string
	Text  string
}

// localDocRowsSQL — SAF: needle sayısı kadar OR'lu büyük/küçük harfsiz arama.
func localDocRowsSQL(n int) string {
	conds := make([]string, n)
	for i := range conds {
		conds[i] = "positionCaseInsensitiveUTF8(text, ?) > 0"
	}
	return `SELECT path, title, url, text FROM wiki_chunks FINAL
		WHERE wiki_id = ? AND deleted = 0 AND (` + strings.Join(conds, " OR ") + `)
		ORDER BY path, chunk_idx
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// LocalDocChunksMatching — wikiID kaynağında needle'lardan birini içeren parçalar.
func (s *Store) LocalDocChunksMatching(ctx context.Context, wikiID string, needles []string, limit int) ([]LocalDocChunk, error) {
	if len(needles) == 0 {
		return nil, nil
	}
	args := []any{wikiID}
	for _, n := range needles {
		args = append(args, n)
	}
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, localDocRowsSQL(len(needles)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalDocChunk
	for rows.Next() {
		var c LocalDocChunk
		if err := rows.Scan(&c.Path, &c.Title, &c.URL, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
