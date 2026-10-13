package wiki

// localdocs.go — v0.10.1163 — TEK İNDEKS: Ayarlar › Knowledge'a yüklenen
// belgeler wiki indeksine ayrı bir kaynak olarak yazılır (operatör: "wiki ana
// search olsun, onun yapısını baz al"). Böylece yüklenen belge ile kurum
// wiki'si AYNI motorla (Türkçe ek duyarlı lexical + semantik) aranır, aynı
// sohbet yolundan ve aynı search_wiki / read_wiki_page araçlarından geçer.
//
// Kaynak kimliği sabit ve DevOps'tan gelmez; bu yüzden üç yerde ayrıcalıklı:
//   - izin listeleri (Config.ProjectAllowed / WikiAllowed) onu hep kabul eder;
//   - senkronun "kaynakta artık olmayan wiki" süpürgesi ona dokunmaz (sync.go);
//   - canlı modda (yerel indeks okunmaz) yalnız bu kaynak indeksten okunur
//     (SearchWith) ve sayfası depodan gelir (ReadPage).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// LocalProject / LocalWikiName — kaynak çiplerinde görünen adlar.
	LocalProject  = "Yüklenen belgeler"
	LocalWikiName = "Belgeler"
	// LocalWikiID — indeks kimliği (DevOps wiki kimlikleri GUID'dir, çakışmaz).
	LocalWikiID = "coremetry-local-docs"
)

// IsLocalProject — SAF: yüklenen belgeler kaynağı mı.
func IsLocalProject(project string) bool {
	return strings.EqualFold(strings.TrimSpace(project), LocalProject)
}

// LocalDocPath — SAF: belge adından sayfa yolu. Yol tek seviyelidir ("/" adda
// "_" olur); NormalizePagePath ile kararlı (okuma yolları girdiyi öyle çevirir).
func LocalDocPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return NormalizePagePath("/" + strings.Trim(strings.ReplaceAll(name, "/", "_"), "_"))
}

// localDocTitle — SAF: başlık = uzantısız ad.
func localDocTitle(name string) string {
	t := strings.TrimSpace(name)
	low := strings.ToLower(t)
	for _, ext := range []string{".md", ".txt", ".pdf"} {
		if strings.HasSuffix(low, ext) && len(t) > len(ext) {
			return t[:len(t)-len(ext)]
		}
	}
	return t
}

// IndexLocalDoc — belgeyi indekse yazar (aynı içerik zaten indeksteyse
// dokunmaz). url isteğe bağlı (url kaynaklı belgenin adresi). Embedding
// opsiyonel: yoksa metin-yalnız indeks, reindex sonradan doldurur.
func (s *Service) IndexLocalDoc(ctx context.Context, name, content, url string) error {
	path := LocalDocPath(name)
	if s == nil || path == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(sum[:])
	var oldN uint32
	if old, err := s.store.GetWikiPage(ctx, LocalProject, LocalWikiID, path); err == nil && old != nil {
		if old.Hash == hash && old.Chunks > 0 {
			return nil
		}
		oldN = old.Chunks
	}
	rec := PageRecord{
		Project: LocalProject, WikiID: LocalWikiID, WikiName: LocalWikiName,
		Path: path, Title: localDocTitle(name), URL: strings.TrimSpace(url),
		Version: "local:" + hash[:12], Hash: hash, Content: content, UpdatedAt: s.now(),
	}
	chunks := BuildChunks(rec.Title, content)
	if len(chunks) == 0 {
		return nil
	}
	embedChunks(ctx, s.embedderOrNil(), rec.Title, chunks)
	rec.Chunks = uint32(len(chunks))
	return s.store.UpsertWikiPage(ctx, rec, chunks, oldN)
}

// DeleteLocalDoc — belgeyi indeksten kaldırır (yoksa sessiz).
func (s *Service) DeleteLocalDoc(ctx context.Context, name string) error {
	path := LocalDocPath(name)
	if s == nil || path == "" {
		return nil
	}
	old, err := s.store.GetWikiPage(ctx, LocalProject, LocalWikiID, path)
	if err != nil || old == nil {
		return err
	}
	return s.store.DeleteWikiPage(ctx, PageMeta{
		WikiID: LocalWikiID, Path: path, Project: LocalProject, WikiName: LocalWikiName, Chunks: old.Chunks,
	})
}

// LocalDocPaths — indeksteki yüklenen belge yolları (aktarım "eksik olanı
// yaz" kararını buna göre verir).
func (s *Service) LocalDocPaths(ctx context.Context) (map[string]bool, error) {
	idx, err := s.store.WikiPageIndex(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, m := range idx {
		if m.WikiID == LocalWikiID && m.Chunks > 0 {
			out[m.Path] = true
		}
	}
	return out, nil
}
