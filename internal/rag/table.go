package rag

// table.go — v0.10.1171 — tablo dosyaları (.xlsx / .csv) RAG'e SATIR BAZLI
// girer (operatör: sunucu envanteri, Oracle veritabanları vb. Excel'i).
//
// Düz metne çevirmek kolon başlığını yalnız ilk parçada bırakırdı; bu yüzden
// her satır başlıklarıyla birlikte kendi paragrafı olur:
//
//	Envanter · satır 12 — Sunucu: SRV-123 · Ortam: prod · DB: ORCL19 · Sürüm: 19c
//
// Böylece hangi parçaya düşerse düşsün satır kendini açıklar ve tek-satır
// soruları ("SRV-123'te hangi Oracle var") doğru bulunur. Sayma / süzme
// soruları için tasarlanmadı (yalnız en iyi parçalar okunur).
//
// .xlsx için dış kütüphane YOK: dosya bir zip; paylaşılan dizgiler
// (xl/sharedStrings.xml) + her sayfa (xl/worksheets/sheetN.xml) okunur, sayfa
// adları workbook.xml'den. Tarih hücreleri Excel seri sayısı olarak kalır.
// Zip bombasına karşı her parça açılmış hâlde tavanlı okunur.

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	tableMaxRows       = 50000
	tableMaxCols       = 200
	xlsxPartMaxBytes   = 64 << 20 // açılmış parça tavanı
	tableCellMaxRunes  = 500
	tableRowHeaderSep  = " — "
	tableCellSeparator = " · "
)

// IsTableFile — SAF: uzantı tablo dosyası mı.
func IsTableFile(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(low, ".xlsx") || strings.HasSuffix(low, ".csv")
}

// TableToText — .xlsx / .csv içeriğini satır bazlı metne çevirir.
func TableToText(name string, b []byte) (string, error) {
	low := strings.ToLower(name)
	title := strings.TrimSuffix(strings.TrimSuffix(path.Base(name), ".xlsx"), ".csv")
	switch {
	case strings.HasSuffix(low, ".csv"):
		rows, err := readCSV(b)
		if err != nil {
			return "", err
		}
		return rowsToText([]sheetRows{{name: title, rows: rows}}), nil
	case strings.HasSuffix(low, ".xlsx"):
		sheets, err := readXLSX(b)
		if err != nil {
			return "", err
		}
		return rowsToText(sheets), nil
	}
	return "", fmt.Errorf("tablo dosyası değil: %s", name)
}

type sheetRows struct {
	name string
	rows [][]string
}

// rowsToText — SAF: ilk dolu satır başlık; sonraki her satır başlıklı paragraf.
// Başlığı boş kolon "Kolon N" olur; boş hücreler yazılmaz; tamamen boş satır atlanır.
func rowsToText(sheets []sheetRows) string {
	var b strings.Builder
	for _, sh := range sheets {
		hdrAt := -1
		for i, r := range sh.rows {
			if !rowEmpty(r) {
				hdrAt = i
				break
			}
		}
		if hdrAt < 0 {
			continue
		}
		header := sh.rows[hdrAt]
		label := func(j int) string {
			if j < len(header) {
				if h := strings.TrimSpace(header[j]); h != "" {
					return h
				}
			}
			return "Kolon " + strconv.Itoa(j+1)
		}
		for i := hdrAt + 1; i < len(sh.rows); i++ {
			r := sh.rows[i]
			if rowEmpty(r) {
				continue
			}
			var cells []string
			for j, v := range r {
				v = clampRunes(strings.Join(strings.Fields(v), " "), tableCellMaxRunes)
				if v == "" {
					continue
				}
				cells = append(cells, label(j)+": "+v)
			}
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "%s · satır %d%s%s", sh.name, i+1, tableRowHeaderSep, strings.Join(cells, tableCellSeparator))
		}
	}
	return b.String()
}

func rowEmpty(r []string) bool {
	for _, v := range r {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func clampRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// readCSV — ayraç ilk satırdan sezilir (; , sekme — Türkçe Excel ; yazar). UTF-8 BOM atılır.
func readCSV(b []byte) ([][]string, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	first := string(b)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	sep := ','
	best := strings.Count(first, ",")
	for _, c := range []rune{';', '\t'} {
		if n := strings.Count(first, string(c)); n > best {
			sep, best = c, n
		}
	}
	r := csv.NewReader(bytes.NewReader(b))
	r.Comma, r.FieldsPerRecord, r.LazyQuotes = sep, -1, true
	var out [][]string
	for len(out) <= tableMaxRows {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV okunamadı: %w", err)
		}
		if len(rec) > tableMaxCols {
			rec = rec[:tableMaxCols]
		}
		out = append(out, rec)
	}
	return out, nil
}

// ── xlsx ──

type xlsxCell struct {
	Ref string `xml:"r,attr"`
	T   string `xml:"t,attr"`
	V   string `xml:"v"`
	IS  struct {
		T []string `xml:"t"`
		R []struct {
			T string `xml:"t"`
		} `xml:"r"`
	} `xml:"is"`
}

type xlsxSheet struct {
	Rows []struct {
		R     int        `xml:"r,attr"`
		Cells []xlsxCell `xml:"c"`
	} `xml:"sheetData>row"`
}

func readXLSX(b []byte) ([]sheetRows, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, fmt.Errorf("xlsx açılamadı (zip değil): %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	shared, err := xlsxSharedStrings(files["xl/sharedStrings.xml"])
	if err != nil {
		return nil, err
	}
	names := xlsxSheetNames(files)
	var out []sheetRows
	var paths []string
	for name := range files {
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			paths = append(paths, name)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return sheetNum(paths[i]) < sheetNum(paths[j]) })
	total := 0
	for i, p := range paths {
		raw, err := readZipPart(files[p])
		if err != nil {
			return nil, err
		}
		var sh xlsxSheet
		if err := xml.Unmarshal(raw, &sh); err != nil {
			return nil, fmt.Errorf("xlsx sayfası okunamadı (%s): %w", p, err)
		}
		name := "Sayfa " + strconv.Itoa(i+1)
		if i < len(names) && names[i] != "" {
			name = names[i]
		}
		rows := make([][]string, 0, len(sh.Rows))
		for _, row := range sh.Rows {
			if total >= tableMaxRows {
				break
			}
			var rec []string
			next := 0
			for _, c := range row.Cells {
				col := xlsxColumn(c.Ref, next)
				if col >= tableMaxCols {
					continue
				}
				for len(rec) < col {
					rec = append(rec, "")
				}
				rec = append(rec, xlsxCellValue(c, shared))
				next = col + 1
			}
			// Satır numarası boşluk bırakıyorsa (atlanan satırlar) sıra korunur.
			for row.R > 0 && len(rows) < row.R-1 {
				rows = append(rows, nil)
			}
			rows = append(rows, rec)
			total++
		}
		out = append(out, sheetRows{name: name, rows: rows})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("xlsx'te sayfa yok")
	}
	return out, nil
}

func readZipPart(f *zip.File) ([]byte, error) {
	if f == nil {
		return nil, nil
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, xlsxPartMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > xlsxPartMaxBytes {
		return nil, fmt.Errorf("xlsx parçası %dMB tavanını aşıyor (%s)", xlsxPartMaxBytes>>20, f.Name)
	}
	return b, nil
}

func xlsxSharedStrings(f *zip.File) ([]string, error) {
	raw, err := readZipPart(f)
	if err != nil || raw == nil {
		return nil, err
	}
	var sst struct {
		SI []struct {
			T string `xml:"t"`
			R []struct {
				T string `xml:"t"`
			} `xml:"r"`
		} `xml:"si"`
	}
	if err := xml.Unmarshal(raw, &sst); err != nil {
		return nil, fmt.Errorf("xlsx paylaşılan dizgiler okunamadı: %w", err)
	}
	out := make([]string, len(sst.SI))
	for i, si := range sst.SI {
		s := si.T
		for _, r := range si.R {
			s += r.T
		}
		out[i] = s
	}
	return out, nil
}

// xlsxSheetNames — workbook.xml sırası (sheet1.xml ↔ ilk ad; tipik dosyada geçerli).
func xlsxSheetNames(files map[string]*zip.File) []string {
	raw, err := readZipPart(files["xl/workbook.xml"])
	if err != nil || raw == nil {
		return nil
	}
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	if xml.Unmarshal(raw, &wb) != nil {
		return nil
	}
	out := make([]string, len(wb.Sheets))
	for i, s := range wb.Sheets {
		out[i] = s.Name
	}
	return out
}

func sheetNum(p string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(p, "xl/worksheets/sheet"), ".xml"))
	return n
}

// xlsxColumn — SAF: "C5" → 2 (0 tabanlı); ref yoksa sıradaki kolon.
func xlsxColumn(ref string, next int) int {
	col := 0
	n := 0
	for _, ch := range ref {
		if ch >= 'A' && ch <= 'Z' {
			col = col*26 + int(ch-'A'+1)
			n++
			continue
		}
		break
	}
	if n == 0 {
		return next
	}
	return col - 1
}

func xlsxCellValue(c xlsxCell, shared []string) string {
	switch c.T {
	case "s":
		if i, err := strconv.Atoi(strings.TrimSpace(c.V)); err == nil && i >= 0 && i < len(shared) {
			return shared[i]
		}
		return ""
	case "inlineStr":
		s := strings.Join(c.IS.T, "")
		for _, r := range c.IS.R {
			s += r.T
		}
		return s
	case "b":
		if c.V == "1" {
			return "TRUE"
		}
		return "FALSE"
	}
	return c.V
}
