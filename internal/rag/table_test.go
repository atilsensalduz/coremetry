package rag

// table_test.go — v0.10.1171: tablo dosyaları satır bazlı metin olur.

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestTableCSVRowsCarryHeaders(t *testing.T) {
	csvb := []byte("\xef\xbb\xbfSunucu;Ortam;DB;Sürüm\nSRV-1;prod;ORCL19;19c\n;;;\nSRV-2;test;;12c\n")
	got, err := TableToText("envanter.csv", csvb)
	if err != nil {
		t.Fatal(err)
	}
	want := "envanter · satır 2 — Sunucu: SRV-1 · Ortam: prod · DB: ORCL19 · Sürüm: 19c\n\n" +
		"envanter · satır 4 — Sunucu: SRV-2 · Ortam: test · Sürüm: 12c"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !IsTableFile("A.XLSX") || !IsTableFile("b.csv") || IsTableFile("c.txt") {
		t.Fatal("IsTableFile")
	}
}

func TestTableXLSXSharedAndInlineStrings(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, body string) {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	put("xl/workbook.xml", `<workbook><sheets><sheet name="Oracle"/></sheets></workbook>`)
	put("xl/sharedStrings.xml", `<sst><si><t>Sunucu</t></si><si><t>Sürüm</t></si><si><r><t>SRV-</t></r><r><t>9</t></r></si></sst>`)
	put("xl/worksheets/sheet1.xml", `<worksheet><sheetData>
	  <row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>
	  <row r="2"><c r="A2" t="s"><v>2</v></c><c r="C2" t="inlineStr"><is><t>19c</t></is></c></row>
	  <row r="3"><c r="B3"><v>42</v></c></row>
	</sheetData></worksheet>`)
	_ = zw.Close()
	got, err := TableToText("db.xlsx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := "Oracle · satır 2 — Sunucu: SRV-9 · Sürüm: 19c\n\nOracle · satır 3 — Kolon 2: 42"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, err := TableToText("bozuk.xlsx", []byte("not a zip")); err == nil || !strings.Contains(err.Error(), "zip") {
		t.Fatalf("bozuk xlsx hatası: %v", err)
	}
	if xlsxColumn("AA10", 0) != 26 || xlsxColumn("", 3) != 3 {
		t.Fatal("xlsxColumn")
	}
}

func TestParseTableRowsRoundTrip(t *testing.T) {
	text, _ := TableToText("x.csv", []byte("A,B\n1,iki: üç\n,\n4,5\n"))
	rows := ParseTableRows(text + "\nserbest metin satırı")
	if len(rows) != 2 || rows[0].Row != 2 || rows[0].Cells[1].Key != "B" || rows[0].Cells[1].Val != "iki: üç" || rows[1].Row != 4 {
		t.Fatalf("%+v", rows)
	}
}
