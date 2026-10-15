package api

// rag_wiki_bridge.go — v0.10.1163 — tek indeks köprüsü: Knowledge'a giren her
// belge (yükleme, url kaynağı, KB terfisi, postmortem) wiki indeksine de
// yazılır; arama wiki motorundan yapılır (wiki/localdocs.go). rag_chunks
// belgenin KAYNAĞI olarak kalır: liste / silme / wiki kapalı kurulumda arama.

import (
	"context"
	"log"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/wiki"
)

// wikiDocsActive — bu çağıran için wiki araması açık mı (ragWikiHits kapısı).
func wikiDocsActive(ctx context.Context) bool {
	w := wikiKB()
	return w != nil && w.Enabled() && sourceCodeCallerAllowed(auth.FromContext(ctx))
}

// wikiIndexDoc — en-iyi-çaba: indeks hatası yüklemeyi düşürmez (aktarım
// sonraki turda yeniden dener).
func (s *Server) wikiIndexDoc(ctx context.Context, name, url, text string) {
	w := wikiKB()
	if w == nil {
		return
	}
	if err := w.IndexLocalDoc(ctx, name, text, url); err != nil {
		log.Printf("[wiki] yüklenen belge indekslenemedi (%s): %v", name, err)
	}
}

func (s *Server) wikiDeleteDoc(ctx context.Context, name string) {
	w := wikiKB()
	if w == nil || name == "" {
		return
	}
	if err := w.DeleteLocalDoc(ctx, name); err != nil {
		log.Printf("[wiki] yüklenen belge indeksten silinemedi (%s): %v", name, err)
	}
}

// ragDocName — doc_id → ad ("" = bulunamadı).
func (s *Server) ragDocName(ctx context.Context, docID string) string {
	docs, err := s.store.ListRagDocuments(ctx)
	if err != nil {
		return ""
	}
	for _, d := range docs {
		if d.DocID == docID {
			return d.DocName
		}
	}
	return ""
}

// ragWikiBackfill — wiki indeksinde OLMAYAN belgeleri rag_chunks'tan aktarır
// (ilk kurulum, wiki temizliği sonrası). İndekste olan belgeye dokunmaz:
// içerik değişimi zaten yükleme anında yazılır.
func (s *Server) ragWikiBackfill(ctx context.Context) {
	w := wikiKB()
	if w == nil || s.store == nil {
		return
	}
	// v0.10.1166 — arka plan en-iyi-çaba işi süreci düşüremez: 1163'te wiki
	// temizliğinden sonra başlatılan goroutine, deposu bağlı olmayan sunucuda
	// (test kurulumu) nil bağlantıda panikleyip tüm paketi düşürdü (CI).
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[wiki] belge aktarımı panikledi (atlandı): %v", r)
		}
	}()
	docs, err := s.store.ListRagDocuments(ctx)
	if err != nil || len(docs) == 0 {
		return
	}
	have, err := w.LocalDocPaths(ctx)
	if err != nil {
		return
	}
	n := 0
	for _, d := range docs {
		if ctx.Err() != nil {
			break
		}
		if have[wiki.LocalDocPath(d.DocName)] {
			continue
		}
		text, err := s.store.RagDocumentText(ctx, d.DocID)
		if err != nil || text == "" {
			continue
		}
		if err := w.IndexLocalDoc(ctx, d.DocName, text, d.SourceRef); err == nil {
			n++
		}
	}
	if n > 0 {
		log.Printf("[wiki] %d yüklenen belge wiki indeksine aktarıldı", n)
	}
}

// hasLocalDocHit — SAF: isabetlerde yüklenen belge var mı (v0.10.1176).
func hasLocalDocHit(h []wiki.Hit) bool {
	for _, x := range h {
		if x.WikiID == wiki.LocalWikiID {
			return true
		}
	}
	return false
}
