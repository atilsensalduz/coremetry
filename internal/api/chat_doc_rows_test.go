package api

// chat_doc_rows_test.go — v0.10.1176 / v0.10.1183: tablo satırı kademesi.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/rag"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func docRowsFixture(t *testing.T) []docRowChunk {
	t.Helper()
	var b strings.Builder
	b.WriteString("HOSTNAME;DOMAIN;ENVIRONMENT;PRIMARY IP;USAGE PURPOSE;Not\n")
	b.WriteString("CCFWKAPP;AKNET;PROD;10.215.4.120;IIS SERVER;a|b\n")
	b.WriteString("srvappl01.corp.local;AKNET;PROD;10.1.1.1;kredi-onay-api;\n")
	b.WriteString("ABMVDICON1;AKNET;PROD;10.215.14.194;APPLICATION SERVER;\n")
	for i := 0; i < 60; i++ { // PROD / AKNET yaygın değerler
		b.WriteString("FILLER;AKNET;PROD;10.0.0.1;APPLICATION SERVER;\n")
	}
	text, err := rag.TableToText("envanter.csv", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	fn, _ := rag.TableToText("fonksiyon.csv", []byte("BT Ürün Adı;Sorumlu;Service Domain\nÇağrı Merkezi IVR;Mehmet Y.;Çağrı Merkezi\nATM;Levent T.;Şube\n"))
	return []docRowChunk{{Title: "envanter", Text: text}, {Title: "fonksiyon", Text: fn}}
}

func TestDocRowsCellValueMatch(t *testing.T) {
	chunks := docRowsFixture(t)
	cases := []struct {
		q      string
		strict bool
		want   []string // beklenen satırların ilk hücreleri
	}{
		{"CCFWKAPP sunucusunun ip adresi nedir", true, []string{"CCFWKAPP"}},           // rakamsız hostname (1183 kök neden)
		{"ccfwkapp hangi ortamda", false, []string{"CCFWKAPP"}},                        // küçük harf: gevşek kipte
		{"srvappl01 hangi uygulama", true, []string{"srvappl01.corp.local"}},           // kimlik alt-dize
		{"ATM ürününün sorumlusu kim", true, []string{"ATM"}},                           // tek kelimelik ürün
		{"çağrı merkezi ivr sorumlusu", false, []string{"Çağrı Merkezi IVR"}},           // katlanmış çok kelime
		{"prod ortamındaki sunucular", false, nil},                                      // yaygın değer satır seçmez
	}
	for _, c := range cases {
		rows, _ := matchDocRows(chunks, c.q, c.strict)
		var got []string
		for _, r := range rows {
			got = append(got, r.Cells[0].Val)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q strict=%v: got %v want %v", c.q, c.strict, got, c.want)
		}
	}
	// erken (strict) kip küçük harfli düz kelimeyi almaz → genel soru kaçmaz
	if rows, _ := matchDocRows(chunks, "ccfwkapp hangi ortamda", true); len(rows) != 0 {
		t.Fatalf("strict kip küçük harfli düz kelimeyle satır seçmemeli: %+v", rows)
	}
	rows, vals := matchDocRows(chunks, "CCFWKAPP ip", true)
	md := docRowsMarkdown(vals, rows)
	if !strings.Contains(md, "| 2 | CCFWKAPP | AKNET | PROD | 10.215.4.120 | IIS SERVER | a\\|b |") {
		t.Fatalf("tablo:\n%s", md)
	}
}

func TestDocRowsWiring(t *testing.T) {
	b, _ := os.ReadFile("copilot_chat.go")
	src := string(b)
	w, d, g := strings.Index(src, "s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC)"), strings.Index(src, "s.docRowsAnswer(ctx, emit, req.Messages, true)"), strings.Index(src, "s.ragChatAnswer(ctx, emit, req.Messages")
	if w < 0 || d < 0 || g < 0 || !(w < d && d < g) {
		t.Fatal("sıra: wiki kademesi → sıkı satır kademesi → … → doküman adımı")
	}
	r, _ := os.ReadFile("rag.go")
	if !strings.Contains(string(r), "s.docRowsAnswer(ctx, emit, msgs, false)") {
		t.Fatal("doküman adımı gevşek satır aramasını denemeli")
	}
	if !hasLocalDocHit([]wiki.Hit{{ChunkRef: wiki.ChunkRef{WikiID: wiki.LocalWikiID}}}) || hasLocalDocHit(nil) {
		t.Fatal("hasLocalDocHit")
	}
}
