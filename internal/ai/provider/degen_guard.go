package provider

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// degen_guard.go — v0.10.1324 (operatör-bildirimi): akan turun YİNELEME
// (dejenerasyon) muhafızı. Saf (ağ yok), tablo testli.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// Prod'da gemma4 31b (yerel openai-compat) "… operasyonu neden hata
// veriyor?" sorusunda sistem prompt'undaki bir kapsam cümlesini yankıladı ve
// "Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları … tool argümanlarında kullan."
// satırını max_tokens (4096) dolana dek onlarca kez yazdı. Akışta hiçbir
// yineleme kapısı yoktu: her satır operatöre aktı, fatura tavana dek sürdü.
//
// ── KURAL ───────────────────────────────────────────────────────────────
//
// Muhafız, tool-turn kapısının YAYINLADIĞI metinle (toolTurnGate.emit)
// beslenir ve onu birimlere böler: satır sonu her zaman, düzyazı satırında
// ayrıca cümle sonu (". " / "! " / "? ") — model yinelemeyi tek satırda da
// yapabilir. Birim NORMALLEŞTİRİLİR (DegenNormalize) ve iki koşuldan biri
// tetikler:
//
//  1. Aynı normal birim (≥ degenMinRunes rune) son degenWindow birim içinde
//     degenRepeatN kez görüldü.
//  2. Son degenTailN dolu birimde (≥ degenTailMinRunes rune) en çok
//     degenTailDistinct farklı değer var (A B A B A B).
//
//  3. v0.10.1452 (operatör-bildirimli: "lükste lükste lükste …" tek satırda,
//     noktasız aktı; birim hiç kapanmadığı için 1 ve 2 tetiklenmedi) — AÇIK
//     birimin kuyruğunda 1–degenTokPeriodMax kelimelik bir öbek arka arkaya
//     degenTokRepeatN kez yinelendi. Öbekte ≥2 harf/rakamlı en az bir kelime
//     şart ("0 0 0 …" ya da "- - -" tetiklemez). Kesim: öbeğin İKİNCİ
//     görünüşünün başı (bir kopya kalır).
//
// ``` çiti içi SAYILMAZ (kod satırları "}" / "end" yineler) ve tablo satırı
// ("|" ile başlayan) cümleye bölünmez. Numaralı/madde listeleri önekleri
// soyulduktan sonra DEĞERLERİYLE karşılaştırılır — benzer önekli farklı
// maddeler tetiklemez.
//
// Tetiklenince CleanText = yayınlanan metin, İLK YİNELEMENİN (birimin ikinci
// görünüşünün) başından kesilmiş hâli; çağıran yayını durdurur ve üst akış
// okumasını iptal eder (tools_stream.go).

const (
	degenMinRunes      = 20
	degenRepeatN       = 3
	degenWindow        = 12
	degenTailN         = 6
	degenTailDistinct  = 2
	degenTailMinRunes  = 8
	degenFenceMarker   = "```"
	degenTableLeadChar = '|'
	degenTokRepeatN    = 8 // v0.10.1452 — kural 3
	degenTokPeriodMax  = 4
)

type degenUnit struct {
	norm string
	off  int // birimin metindeki başlangıç baytı
}

// degenGuard — sıfır değeri kullanılabilir.
type degenGuard struct {
	text      strings.Builder
	scan      int  // taranan bayt sonu
	unitStart int  // açık birimin başı
	lineStart int  // açık satırın başı
	fence     bool // ``` çiti içinde
	window    []degenUnit
	tail      []degenUnit
	tripped   bool
	cut       int
}

// Feed — yayınlanan parçayı ekler; muhafız tetiklendiyse (şimdi ya da
// önceden) true.
func (d *degenGuard) Feed(s string) bool {
	if d.tripped {
		return true
	}
	d.text.WriteString(s)
	t := d.text.String()
	for i := d.scan; i < len(t); i++ {
		c := t[i]
		if c == '\n' {
			d.endLine(t, i)
			if d.tripped {
				return true
			}
			continue
		}
		// Cümle sonu: [.!?] + boşluk; çit ve tablo satırında bölünmez.
		if (c == ' ' || c == '\t') && i > d.unitStart && !d.fence && isSentenceEnd(t[i-1]) && !d.lineIsTableOrFence(t, i) && !onlyListMarker(t[d.unitStart:i]) {
			d.addUnit(t, d.unitStart, i)
			d.unitStart = i + 1
			if d.tripped {
				return true
			}
		}
		// v0.10.1452 — kural 3: kelime sınırında açık birimin kuyruğu.
		if (c == ' ' || c == '\t') && !d.fence && !d.lineIsTableOrFence(t, i) {
			d.tokenRun(t, i)
			if d.tripped {
				return true
			}
		}
	}
	d.scan = len(t)
	return false
}

func isSentenceEnd(c byte) bool { return c == '.' || c == '!' || c == '?' }

// onlyListMarker — "12." / "  3." gibi yalnız numara öneki (cümle sonu değil).
func onlyListMarker(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) >= 2 && len(s) <= 4 && strings.Trim(s[:len(s)-1], "0123456789") == "" && isSentenceEnd(s[len(s)-1])
}

func (d *degenGuard) lineIsTableOrFence(t string, i int) bool {
	ln := strings.TrimLeft(t[d.lineStart:i], " \t")
	return strings.HasPrefix(ln, degenFenceMarker) || (ln != "" && ln[0] == degenTableLeadChar)
}

func (d *degenGuard) endLine(t string, i int) {
	line := strings.TrimSpace(t[d.lineStart:i])
	switch {
	case strings.HasPrefix(line, degenFenceMarker):
		d.fence = !d.fence
	case d.fence:
		// kod satırı — sayılmaz
	default:
		d.addUnit(t, d.unitStart, i)
	}
	d.lineStart, d.unitStart = i+1, i+1
	d.scan = i + 1
}

func (d *degenGuard) addUnit(t string, from, to int) {
	if d.tripped || from >= to {
		return
	}
	n := DegenNormalize(t[from:to])
	if n == "" {
		return
	}
	// baştaki boşluğu birimin ofsetinden at (CleanText temiz kesilsin)
	for from < to && (t[from] == ' ' || t[from] == '\t' || t[from] == '\r') {
		from++
	}
	u := degenUnit{norm: n, off: from}
	runes := utf8.RuneCountInString(n)

	d.window = append(d.window, u)
	if len(d.window) > degenWindow {
		d.window = d.window[len(d.window)-degenWindow:]
	}
	if runes >= degenMinRunes {
		cnt, second := 0, -1
		for _, w := range d.window {
			if w.norm == n {
				cnt++
				if cnt == 2 {
					second = w.off
				}
			}
		}
		if cnt >= degenRepeatN {
			d.trip(second)
			return
		}
	}

	if runes < degenTailMinRunes {
		return
	}
	d.tail = append(d.tail, u)
	if len(d.tail) > degenTailN {
		d.tail = d.tail[len(d.tail)-degenTailN:]
	}
	if len(d.tail) < degenTailN {
		return
	}
	distinct := map[string]bool{}
	for _, w := range d.tail {
		distinct[w.norm] = true
	}
	if len(distinct) > degenTailDistinct {
		return
	}
	seen := map[string]bool{}
	for _, w := range d.tail {
		if seen[w.norm] {
			d.trip(w.off)
			return
		}
		seen[w.norm] = true
	}
}

// tokenRun — kural 3 (v0.10.1452): t[from:end] kuyruğunda (from = açık birim/
// satır başı) en çok degenTokPeriodMax*degenTokRepeatN kelime geriye bakılır.
func (d *degenGuard) tokenRun(t string, end int) {
	from := d.unitStart
	if d.lineStart > from {
		from = d.lineStart
	}
	const maxTok = degenTokPeriodMax * degenTokRepeatN
	var toks []string
	var offs []int
	i := end
	for i > from && len(toks) < maxTok {
		for i > from && (t[i-1] == ' ' || t[i-1] == '\t') {
			i--
		}
		j := i
		for i > from && t[i-1] != ' ' && t[i-1] != '\t' {
			i--
		}
		if i < j {
			toks = append(toks, degenToken(t[i:j]))
			offs = append(offs, i)
		}
	}
	// toks ters sırada (son kelime başta): toks[k] == toks[k+p] ⇔ yinelenme.
	for p := 1; p <= degenTokPeriodMax; p++ {
		need := p * degenTokRepeatN
		if len(toks) < need {
			break
		}
		ok, word := true, false
		for k := 0; k < need-p && ok; k++ {
			ok = toks[k] != "" && toks[k] == toks[k+p]
		}
		for k := 0; k < p && ok; k++ {
			if alnumRunes(toks[k]) >= 2 {
				word = true
			}
		}
		if ok && word {
			// öbeğin ikinci görünüşünün başı: ilk görünüş en eski p kelime.
			d.trip(offs[need-p-1])
			return
		}
	}
}

// degenToken — kelime karşılaştırma biçimi: küçük harf, kenar noktalaması atılmış.
func degenToken(s string) string {
	return strings.ToLower(strings.Trim(s, "*_,.;:!?\"'`()[]"))
}

func alnumRunes(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

func (d *degenGuard) trip(cut int) {
	d.tripped = true
	if cut < 0 {
		cut = d.text.Len()
	}
	d.cut = cut
}

// Tripped — muhafız tetiklendi mi.
func (d *degenGuard) Tripped() bool { return d.tripped }

// CleanText — tetiklendiyse ilk yinelemeden önceki metin (sondaki boşluk
// kırpılır); tetiklenmediyse beslenen metnin tamamı.
func (d *degenGuard) CleanText() string {
	t := d.text.String()
	if d.tripped && d.cut <= len(t) {
		t = t[:d.cut]
	}
	return strings.TrimRight(t, " \t\r\n")
}

// Reset — yayın geri alındı (Reset delta'sı): muhafız sıfırdan başlar.
func (d *degenGuard) Reset() { *d = degenGuard{} }

// DegenNormalize — yineleme karşılaştırmasının birimi. Saf: kenar boşluğu,
// Türkçe-duyarlı küçük harf (İ/I/ı → i), boşluk daraltma, markdown madde /
// numara / başlık / alıntı önekleri ve vurgu işaretleri (* _ `) soyulur;
// sondaki noktalama atılır.
func DegenNormalize(s string) string {
	s = strings.TrimSpace(s)
	for {
		t := strings.TrimLeft(s, " \t")
		switch {
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "), strings.HasPrefix(t, "+ "),
			strings.HasPrefix(t, "> "), strings.HasPrefix(t, "#"):
			t = strings.TrimLeft(t, "-*+>#")
		case strings.HasPrefix(t, "• "):
			t = strings.TrimPrefix(t, "•")
		default:
			if j := numberedPrefixLen(t); j > 0 {
				t = t[j:]
			}
		}
		if t == s {
			break
		}
		s = t
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case '*', '_', '`':
			continue
		case 'İ', 'I', 'ı':
			r = 'i'
		case '̇': // birleşik üst nokta (ToLower("İ") artığı)
			continue
		}
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimRight(b.String(), " .,;:!?")
}

// numberedPrefixLen — "12. " / "3) " öneki uzunluğu; yoksa 0.
func numberedPrefixLen(t string) int {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == 0 || i > 3 || i+1 >= len(t) || (t[i] != '.' && t[i] != ')') || t[i+1] != ' ' {
		return 0
	}
	return i + 2
}
