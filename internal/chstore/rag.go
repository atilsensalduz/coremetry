package chstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// rag.go — v0.8.438 RAG (doküman soru-cevap) depo katmanı.
//
// Tek tablo: rag_chunks. saved_views'a BİLEREK gitmiyor (invariant #5
// istisnası, savunma): embedding Array(Float32) + cosineDistance
// sorgusu kendi şeklini ister ve bu İÇERİK verisidir, kullanıcı view
// state'i değil. ReplacingMergeTree(version) ORDER BY (doc_id,
// chunk_idx): bir dokümanın yeniden yüklenmesi / wiki sayfasının
// yeniden senkronu aynı (doc_id, chunk_idx) satırlarını yeni
// version'la değiştirir — diff mantığı bedava. Hacim düşük (yüzlerce
// doküman × onlarca chunk), FINAL okumaları bütçe içinde.
type RagChunk struct {
	// SourceHash (v0.8.442) — url kaynağında sayfa içeriğinin sha256'sı;
	// senkron diff'i "hash değişmediyse yeniden embed etme" bununla yapar.
	SourceHash string    `json:"-"`
	DocID      string    `json:"docId"`
	DocName    string    `json:"docName"`
	Source     string    `json:"source"`              // upload | url
	SourceRef  string    `json:"sourceRef,omitempty"` // url kaynağında sayfa adresi
	UploadedBy string    `json:"uploadedBy,omitempty"`
	ChunkIdx   uint32    `json:"chunkIdx"`
	Content    string    `json:"content"`
	Embedding  []float32 `json:"-"`
}

// RagDocument — liste görünümü (GROUP BY projeksiyonu).
type RagDocument struct {
	DocID      string `json:"docId"`
	DocName    string `json:"docName"`
	Source     string `json:"source"`
	SourceRef  string `json:"sourceRef,omitempty"`
	UploadedBy string `json:"uploadedBy,omitempty"`
	Chunks     uint64 `json:"chunks"`
	Bytes      uint64 `json:"bytes"`
	UpdatedAt  int64  `json:"updatedAt"` // unix ns
	SourceHash string `json:"-"`
}

// RagHit — retrieval sonucu: chunk + benzerlik skoru.
type RagHit struct {
	RagChunk
	Score float64 `json:"score"` // 1 - cosineDistance; 1.0 = özdeş
}

const ragChunksDDL = `
CREATE TABLE IF NOT EXISTS rag_chunks (
    doc_id      String,
    doc_name    String,
    source      LowCardinality(String),
    source_ref  String DEFAULT '',
    uploaded_by String DEFAULT '',
    chunk_idx   UInt32,
    content     String CODEC(ZSTD(3)),
    embedding   Array(Float32),
    source_hash String DEFAULT '',
    updated_at  DateTime64(9) DEFAULT now64(9),
    version     UInt64
) ENGINE = ReplacingMergeTree(version)
ORDER BY (doc_id, chunk_idx)`

// ReplaceDocumentChunks — bir dokümanın TÜM chunk'larını tek version
// damgasıyla yazar. Eski yüklemenin daha uzun kuyruk chunk'ları (yeni
// içerik kısaldıysa) ayrıca temizlenir.
func (s *Store) ReplaceDocumentChunks(ctx context.Context, chunks []RagChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	oldN, err := s.ragChunkCount(ctx, chunks[0].DocID) // v0.10.1172 — kuyruk mezar taşları için
	if err != nil {
		return err
	}
	version := uint64(time.Now().UnixNano())
	ctx2 := asyncInsertCtx(ctx)
	batch, err := s.conn.PrepareBatch(ctx2, `INSERT INTO rag_chunks
		(doc_id, doc_name, source, source_ref, uploaded_by, chunk_idx, content, embedding, source_hash, updated_at, version)`)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, c := range chunks {
		if err := batch.Append(c.DocID, c.DocName, c.Source, c.SourceRef, c.UploadedBy,
			c.ChunkIdx, c.Content, c.Embedding, c.SourceHash, now.UTC(), version); err != nil {
			return err
		}
	}
	if err := batch.Send(); err != nil {
		return err
	}
	// Kuyruk temizliği: yeni chunk sayısının ötesindeki eski satırlar
	// MEZAR TAŞIYLA (v0.10.1172) — eskiden ALTER … DELETE mutasyonuydu;
	// kümede ON CLUSTER kuyruğu zaman aşımına düşüp yüklemeyi hatalı
	// gösterebiliyordu.
	return s.ragTombstones(ctx, chunks[0].DocID, uint32(len(chunks)), oldN)
}

// RagDocumentText — v0.10.1163: dokümanın metni, parçaları sırayla
// birleştirerek (wiki indeksine aktarım için; parçalar örtüşmez).
func (s *Store) RagDocumentText(ctx context.Context, docID string) (string, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT content FROM rag_chunks FINAL
		WHERE doc_id = ? AND doc_name != ''
		ORDER BY chunk_idx
		LIMIT 20000
		SETTINGS max_execution_time = 10`, docID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(c)
	}
	return b.String(), rows.Err()
}

// DeleteRagDocument — dokümanı tamamen kaldırır (admin, audit'li).
//
// v0.10.1172 — mutasyon (ALTER … DELETE, kümede ON CLUSTER) yerine MEZAR
// TAŞI: aynı (doc_id, chunk_idx) anahtarına daha yüksek version'lı boş
// satır (doc_name=”); ReplacingMergeTree FINAL onu seçer, okumalar
// doc_name != ” ile eler. Operatör-bildirimi: belge silerken hata
// (dağıtık kümede ON CLUSTER DDL kuyruğu zaman aşımı). Arka plan
// birleşmesi satırları zamanla tekleştirir.
func (s *Store) DeleteRagDocument(ctx context.Context, docID string) error {
	n, err := s.ragChunkCount(ctx, docID)
	if err != nil {
		return err
	}
	return s.ragTombstones(ctx, docID, 0, n)
}

// ragChunkCount — canlı parça sayısı (en yüksek idx + 1; mezar taşları hariç).
func (s *Store) ragChunkCount(ctx context.Context, docID string) (uint32, error) {
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT toUInt64(if(count() = 0, 0, max(chunk_idx) + 1)) FROM rag_chunks FINAL
		WHERE doc_id = ? AND doc_name != ''
		SETTINGS max_execution_time = 10`, docID).Scan(&n)
	return uint32(n), err
}

// ragTombstones — [from, to) parçalarına mezar taşı (boş satır, yeni version).
func (s *Store) ragTombstones(ctx context.Context, docID string, from, to uint32) error {
	if from >= to {
		return nil
	}
	batch, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO rag_chunks
		(doc_id, doc_name, source, source_ref, uploaded_by, chunk_idx, content, embedding, source_hash, updated_at, version)`)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	version := uint64(now.UnixNano())
	for i := from; i < to; i++ {
		if err := batch.Append(docID, "", "", "", "", i, "", []float32{}, "", now, version); err != nil {
			return err
		}
	}
	return batch.Send()
}

// ListRagDocuments — katalog: doküman başına chunk sayısı + boyut.
func (s *Store) ListRagDocuments(ctx context.Context) ([]RagDocument, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT doc_id, anyLast(doc_name), anyLast(source), anyLast(source_ref),
		       anyLast(uploaded_by), count() AS chunks, sum(length(content)) AS bytes,
		       toUnixTimestamp64Nano(max(updated_at)), anyLast(source_hash)
		FROM rag_chunks FINAL
		WHERE doc_name != '' -- v0.10.1172 mezar taşları hariç
		GROUP BY doc_id
		ORDER BY doc_id
		LIMIT 1000
		SETTINGS max_execution_time = 10`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RagDocument
	for rows.Next() {
		var d RagDocument
		if err := rows.Scan(&d.DocID, &d.DocName, &d.Source, &d.SourceRef,
			&d.UploadedBy, &d.Chunks, &d.Bytes, &d.UpdatedAt, &d.SourceHash); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ragTopKSQL — retrieval: soru embedding'ine kosinüs benzerliğiyle en
// yakın k chunk. Düşük hacimde brute-force tarama bütçe içinde
// (~100k chunk'a kadar); üzeri ANN index follow-up'ı.
const ragTopKSQL = `
		SELECT doc_id, doc_name, source, source_ref, chunk_idx, content,
		       1 - cosineDistance(embedding, ?) AS score
		FROM rag_chunks FINAL
		WHERE length(embedding) = length(?) AND doc_name != ''
		ORDER BY score DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`

// TopKRagChunks — en benzer k chunk (k [1,20] aralığına kelepçelenir).
func (s *Store) TopKRagChunks(ctx context.Context, queryEmbedding []float32, k int) ([]RagHit, error) {
	if len(queryEmbedding) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}
	if k < 1 {
		k = 5
	}
	if k > 20 {
		k = 20
	}
	rows, err := s.conn.Query(ctx, ragTopKSQL, queryEmbedding, queryEmbedding, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RagHit
	for rows.Next() {
		var h RagHit
		if err := rows.Scan(&h.DocID, &h.DocName, &h.Source, &h.SourceRef,
			&h.ChunkIdx, &h.Content, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ── BM25/keyword köprüsü (v0.9.161, v0.9.162 precision) — bge-m3 (embedding)
// yapılandırılmadan doküman grounding çalışsın. content üzerinde sorgu
// terimlerini WORD-BOUNDARY hasTokenCaseInsensitive ile arar (v0.9.162 review:
// substring 'rate'∈'generate' gibi parça-eşleşmeler alakasız dokümanı grounding
// yapıp chat tool-loop'unu bastırıyordu — false-positive yanlış cevap verir,
// miss ise doğru fallthrough; precision > recall, recall'u bge-m3 çözer). En
// çok terim eşleşen k chunk; score = eşleşen/toplam terim (0..1, LEXICAL —
// cosine DEĞİL). Çağıran ragChatAnswer bir de floor uygular. LIMIT +
// max_execution_time (invariant).

var ragStopwords = map[string]bool{
	// TR
	"ve": true, "ile": true, "için": true, "bir": true, "bu": true, "şu": true,
	"nasıl": true, "nedir": true, "neden": true, "hangi": true, "var": true,
	"mı": true, "mi": true, "mu": true, "mü": true, "ama": true, "veya": true,
	// EN
	"the": true, "and": true, "for": true, "with": true, "how": true, "what": true,
	"why": true, "which": true, "does": true, "this": true, "that": true, "are": true,
}

// ragQueryTerms — sorguyu keyword retrieval terimlerine indirger: küçük harf,
// harf/rakam-dışı ayraç, <3 harf + stopword ele, dedup, tavan 12.
func ragQueryTerms(query string) []string {
	q := strings.ToLower(query)
	seen := make(map[string]bool)
	var terms []string
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(tok)) < 3 || ragStopwords[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		terms = append(terms, tok)
		if len(terms) >= 12 {
			break
		}
	}
	return terms
}

func (s *Store) TopKRagChunksByContent(ctx context.Context, query string, k int) ([]RagHit, error) {
	terms := ragQueryTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	if k < 1 {
		k = 5
	}
	if k > 20 {
		k = 20
	}
	parts := make([]string, len(terms))
	args := make([]any, 0, len(terms)+1)
	for i, t := range terms {
		parts[i] = "hasTokenCaseInsensitive(content, ?)"
		args = append(args, t)
	}
	// score = eşleşen terim sayısı / toplam terim (CH `/` her zaman Float64).
	scoreExpr := "(" + strings.Join(parts, " + ") + ") / " + strconv.Itoa(len(terms))
	q := `SELECT doc_id, doc_name, source, source_ref, chunk_idx, content, score FROM (
		SELECT doc_id, doc_name, source, source_ref, chunk_idx, content, ` + scoreExpr + ` AS score
		FROM rag_chunks FINAL
		WHERE doc_name != ''
	) WHERE score > 0
	ORDER BY score DESC, chunk_idx ASC
	LIMIT ?
	SETTINGS max_execution_time = 5`
	args = append(args, k)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RagHit
	for rows.Next() {
		var h RagHit
		if err := rows.Scan(&h.DocID, &h.DocName, &h.Source, &h.SourceRef,
			&h.ChunkIdx, &h.Content, &h.Score); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// mutationTarget — cluster modunda mutation'lar Distributed sarmalayıcı
// yerine _local tabloya (ON CLUSTER) gitmeli; tek düğümde tablo adının
// kendisi. (dropCombinedMV'nin bilinen dersinin mutation hali.)
func (s *Store) mutationTarget(table string) string {
	if s.clusterMode() {
		return table + "_local ON CLUSTER " + s.ClusterName()
	}
	return table
}
