package api

// chat_doc_rows_test.go — v0.10.1176: tablo satırı kademesi (saf parçalar).

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestDocRowsFromUploadedTable(t *testing.T) {
	text, err := rag.TableToText("envanter.csv", []byte("Hostname;AppName;Ortam;Not\nsrvappl01.corp.local;kredi-onay-api;prod;a|b\nsrvappl02.corp.local;kredi-onay-api;prod;\nsrvdb01;oracle-db;prod;\n"))
	if err != nil {
		t.Fatal(err)
	}
	chunks := []docRowChunk{{Title: "envanter", Text: text}}

	// hostname → karşılığı (uygulama)
	rows := matchDocRows(chunks, identifierTerms("srvappl01 hangi uygulama"))
	if len(rows) != 1 || rows[0].Row != 2 {
		t.Fatalf("hostname eşleşmesi: %+v", rows)
	}
	md := docRowsMarkdown([]string{"srvappl01"}, rows)
	for _, want := range []string{"| Satır | Hostname | AppName | Ortam | Not |", "| 2 | srvappl01.corp.local | kredi-onay-api | prod | a\\|b |"} {
		if !strings.Contains(md, want) {
			t.Fatalf("tablo %q içermeli:\n%s", want, md)
		}
	}
	// uygulama kodu → tüm hostlar; kolon ADI eşleşmesi satır seçmez
	rows = matchDocRows(chunks, identifierTerms("kredi-onay-api hangi sunucularda"))
	if len(rows) != 2 || rows[0].Row != 2 || rows[1].Row != 3 {
		t.Fatalf("uygulama eşleşmesi: %+v", rows)
	}
	if got := matchDocRows(chunks, []string{"hostname"}); len(got) != 0 {
		t.Fatal("yalnız HÜCRE değeri eşleşmeli, kolon adı değil")
	}
}

func TestDocRowsWiring(t *testing.T) {
	b, _ := os.ReadFile("copilot_chat.go")
	src := string(b)
	w, d, g := strings.Index(src, "s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC)"), strings.Index(src, "s.docRowsAnswer(ctx, emit, req.Messages)"), strings.Index(src, "s.ragChatAnswer(ctx, emit, req.Messages")
	if w < 0 || d < 0 || g < 0 || !(w < d && d < g) {
		t.Fatal("sıra: wiki kademesi → satır kademesi → … → doküman adımı")
	}
	if !hasLocalDocHit([]wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: wiki.LocalWikiID}}}) || hasLocalDocHit(nil) {
		t.Fatal("hasLocalDocHit")
	}
}
