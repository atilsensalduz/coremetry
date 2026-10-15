package api

// chat_doc_rows.go — v0.10.1176 — TABLO SATIRI KADEMESİ: yüklenen Excel / CSV
// envanterinde soruda geçen kimliğin (hostname, uygulama kodu, IP) BİREBİR
// eşleştiği satırlar, tüm kolonlarıyla tablo olarak cevaplanır (operatör:
// "Excel'deki kolonlara göre ilişki kursun, tabloya çevirsin, search edince
// karşılıklarını bulsun").
//
// Neden embedding / LLM okuması değil: envanter binlerce satır; parça
// benzerliği hostname'i taşıyan satırı kaçırıyordu (v0.10.1175 kök nedeni) ve
// tüm belgeyi modele okutmak pencereye sığmaz. Bu kademe deterministik:
// wiki indeksindeki yerel kaynakta değeri içeren parçalar okunur, satırlar
// rowsToText biçiminden geri ayrıştırılır (rag.ParseTableRows), YALNIZ
// hücresinde kimlik geçen satırlar tabloya girer. LLM çağrısı yok.
//
// Sıra: wiki kademesinden sonra, guided telemetri yönlendiricisinden ÖNCE
// (servis adı gibi görünen uygulama kodu telemetriye kaçmasın) — ama soru
// telemetri sinyali taşıyorsa (wikiWeakCueVetoed: "hata oranı", "gecikme"…)
// kademe sessizce çekilir. Eşleşme yoksa hiçbir olay basılmaz.

import (
	"context"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	docRowsChunkLimit = 200
	docRowsMax        = 50
	docRowsMaxCols    = 12
)

// docRowMatch — eşleşen satır + kaynağı.
type docRowMatch struct {
	Doc string // belge başlığı
	URL string
	rag.TableRow
}

// matchDocRows — SAF: parçalardan, hücresinde kimliklerden biri geçen
// satırlar (belge + sayfa + satır tekil), en çok docRowsMax.
func matchDocRows(chunks []docRowChunk, ids []string) []docRowMatch {
	var out []docRowMatch
	seen := map[string]bool{}
	for _, c := range chunks {
		for _, r := range rag.ParseTableRows(c.Text) {
			if !rowHasID(r, ids) {
				continue
			}
			k := fmt.Sprintf("%s\x00%s\x00%d", c.Title, r.Sheet, r.Row)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, docRowMatch{Doc: c.Title, URL: c.URL, TableRow: r})
			if len(out) >= docRowsMax {
				return out
			}
		}
	}
	return out
}

type docRowChunk struct{ Title, URL, Text string }

func rowHasID(r rag.TableRow, ids []string) bool {
	for _, c := range r.Cells {
		low := strings.ToLower(c.Val)
		for _, id := range ids {
			if strings.Contains(low, id) {
				return true
			}
		}
	}
	return false
}

// docRowsMarkdown — SAF: belge + sayfa başına bir Markdown tablosu; kolonlar
// ilk görülme sırasıyla (en çok docRowsMaxCols), hücrelerde | kaçışlı.
func docRowsMarkdown(ids []string, rows []docRowMatch) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Yüklenen belgelerde **%s** için %d satır bulundu", strings.Join(ids, ", "), len(rows))
	if len(rows) >= docRowsMax {
		fmt.Fprintf(&b, " (ilk %d)", docRowsMax)
	}
	b.WriteString(":\n")
	type group struct {
		title string
		cols  []string
		rows  []docRowMatch
	}
	var groups []*group
	at := map[string]*group{}
	for _, r := range rows {
		k := r.Doc + " › " + r.Sheet
		g := at[k]
		if g == nil {
			g = &group{title: k}
			at[k] = g
			groups = append(groups, g)
		}
		for _, c := range r.Cells {
			if !containsStr(g.cols, c.Key) && len(g.cols) < docRowsMaxCols {
				g.cols = append(g.cols, c.Key)
			}
		}
		g.rows = append(g.rows, r)
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
	for _, g := range groups {
		fmt.Fprintf(&b, "\n**%s**\n\n| Satır | %s |\n|---|%s\n", esc(g.title),
			strings.Join(mapStr(g.cols, esc), " | "), strings.Repeat("---|", len(g.cols)))
		for _, r := range g.rows {
			vals := make([]string, len(g.cols))
			for _, c := range r.Cells {
				for i, col := range g.cols {
					if col == c.Key {
						vals[i] = esc(c.Val)
					}
				}
			}
			fmt.Fprintf(&b, "| %d | %s |\n", r.Row, strings.Join(vals, " | "))
		}
	}
	return b.String()
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func mapStr(xs []string, f func(string) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// docRowsAnswer — kademe. handled=false → olay yok, akış aynen sürer.
func (s *Server) docRowsAnswer(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage) (handled, ok bool) {
	if !wikiDocsActive(ctx) || s.store == nil {
		return false, false
	}
	question := lastUserText(msgs)
	ids := identifierTerms(question)
	if len(ids) == 0 || s.wikiWeakCueVetoed(ctx, question) {
		return false, false
	}
	found, err := s.store.LocalDocChunksMatching(ctx, wiki.LocalWikiID, ids, docRowsChunkLimit)
	if err != nil || len(found) == 0 {
		return false, false
	}
	chunks := make([]docRowChunk, len(found))
	for i, c := range found {
		chunks[i] = docRowChunk{Title: c.Title, URL: c.URL, Text: c.Text}
	}
	rows := matchDocRows(chunks, ids)
	if len(rows) == 0 {
		return false, false
	}
	emitGuidedContextStep(emit, "yüklenen belgeler · satır araması")
	var sources []chatSource
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.Doc] {
			continue
		}
		seen[r.Doc] = true
		kind := chatSourceUpload
		if r.URL != "" {
			kind = chatSourceURL
		}
		sources = append(sources, chatSource{Doc: "Belge · " + r.Doc, Ref: r.URL, Chunk: 1, Score: 1, Kind: kind})
	}
	emit("answer", map[string]any{
		"text": docRowsMarkdown(ids, rows), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		"sources": dedupeChatSources(sources), "links": []guidedAnswerLink{}, "allowedLinks": []string{},
	})
	return true, true
}
