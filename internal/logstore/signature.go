package logstore

// signature.go — log mesajı imzası (v0.10.229, Influx D4 audit §4 adım 4).
//
// Aynı şablondan gelen mesajları (farklı UUID / id / zaman / IP / sayı)
// tek imzada toplar: yer tutucu `<x>`, boşluk sıkıştırma, 512 karakter
// tavanı. Bu bir GRUPLAMA anahtarıdır, redaksiyon DEĞİL: örnek mesaj
// verbatim saklanır (feedback-no-redaction). Drain şablonlamasından ayrı:
// burada örnek tabanı yok, tek mesaj → tek imza, deterministik.
//
// v0.10.1496 (operatör, prod ES): `{"request_id":"SPE1015…202610240340413867 00", …}`
// gövdeli 256 satır 164 desene dağılıyordu — harf+rakam karışık uzun
// kimlikler (`\b` sınırı olmadığı için sayı kuralı içlerine giremez)
// maskelenmiyordu. İki ek: (1) KİMLİK BELİRTECİ geçişi (MaskIDTokens —
// Drain maskesiyle ORTAK yüklem), (2) gövde JSON nesnesiyse imza = sıralı
// anahtar kümesi + mesaj alanının maskelenmiş metni; diğer değerler imzaya
// GİRMEZ.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/cespare/xxhash/v2"
)

const signatureMaxLen = 512

// signatureJSONMax — bu boyun üstündeki gövde JSON olarak çözülmez (örnek
// 2000 satır; çözüm maliyeti sınırlı kalsın), maskelenmiş ham metne düşer.
const signatureJSONMax = 64 << 10

var (
	sigUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	sigISO  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)
	sigIP   = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5})?\b`)
	sigHex  = regexp.MustCompile(`(?i)\b[0-9a-f]{16,}\b`)
	sigNum  = regexp.MustCompile(`\b\d{2,}`) // sondaki sınır YOK: "1500ms" → "<x>ms"; "OP12" başında sınır yok, kalır
	sigWS   = regexp.MustCompile(`\s+`)
	// sigQuotedVal — v0.10.1496: çözülemeyen (kesik) JSON'da `"anahtar": "değer"`
	// değeri; değer bütün olarak kimlikse (ayraçlar atılmış hâli IsIDLikeToken)
	// tırnak içi tek `<x>` olur. Anahtar (iki noktadan önceki) dokunulmaz.
	sigQuotedVal = regexp.MustCompile(`(:\s*)"([^"\\\s]{4,256})"`)
	// sigXRun — ardışık yer tutucular ayraçla bağlıysa tek yer tutucu.
	sigXRun = regexp.MustCompile(`<x>(?:[-_.:/+=#]*<x>)+`)
)

// sigMessageKeys — JSON gövdede mesaj sayılan alanlar, öncelik sırasıyla
// (büyük-küçük harf duyarsız; ilk dolu DİZGE değer kazanır).
var sigMessageKeys = []string{"message", "msg", "log", "error", "event"}

// sigLevelKeys — JSON gövdede seviye alanı (desen satırının Seviye
// kolonu, kayıt seviyesizse; (d) maddesi).
var sigLevelKeys = []string{"level", "severity", "log.level", "levelname", "loglevel", "severity_text"}

// NormalizeSignature — mesajın imza şablonu. Sıra önemli: UUID önce (hex
// kuralı parçalarını yakalamasın), ISO zaman IP'den önce (tarih noktaları
// IP değil), hex sayıdan önce (uzun hex'in içindeki rakamlar sayı değil),
// kimlik belirteçleri EN SON (önceki kuralların bıraktığı parçalar üstünde).
func NormalizeSignature(msg string) string {
	return signatureOf(msg).sig
}

// sigInfo — imza + yan ürünler (aynı JSON çözümünden, ikinci kez çözmeden).
type sigInfo struct {
	sig   string
	query string // PatternSearchQuery girdisi: JSON'da mesaj metni, değilse imza
	level string // JSON gövdedeki seviye metni ("" = yok)
}

func signatureOf(msg string) sigInfo {
	s := strings.TrimSpace(msg)
	if s == "" {
		return sigInfo{}
	}
	if s[0] == '{' && len(s) <= signatureJSONMax {
		if info, ok := jsonSignature(s); ok {
			return info
		}
	}
	sig := capSignature(maskSignatureText(s, true))
	return sigInfo{sig: sig, query: sig}
}

// maskSignatureText — düz metin maskesi. quoted=true: JSON-benzeri
// `"k":"v"` değerleri de bütün olarak sınanır.
func maskSignatureText(s string, quoted bool) string {
	s = sigUUID.ReplaceAllString(s, "<x>")
	s = sigISO.ReplaceAllString(s, "<x>")
	s = sigIP.ReplaceAllString(s, "<x>")
	s = sigHex.ReplaceAllString(s, "<x>")
	s = sigNum.ReplaceAllString(s, "<x>")
	if quoted && strings.Contains(s, `"`) {
		s = sigQuotedVal.ReplaceAllStringFunc(s, func(m string) string {
			sub := sigQuotedVal.FindStringSubmatch(m)
			if quotedValueIsID(sub[2]) {
				return sub[1] + `"<x>"`
			}
			return m
		})
	}
	s = MaskIDTokens(s, "<x>")
	s = sigXRun.ReplaceAllString(s, "<x>")
	return strings.TrimSpace(sigWS.ReplaceAllString(s, " "))
}

// quotedValueIsID — boşluksuz bir değer, ayraçları atılınca kimlik mi, ya
// da zaten yer tutuculardan + ≤3 harflik kırıntıdan mı ibaret?
func quotedValueIsID(v string) bool {
	rest := strings.ReplaceAll(v, "<x>", "")
	var b strings.Builder
	for _, r := range rest {
		if isSigAlnum(r) {
			b.WriteRune(r)
		}
	}
	if strings.Contains(v, "<x>") && len([]rune(b.String())) <= 3 {
		return true
	}
	return IsIDLikeToken(b.String())
}

// jsonSignature — gövde bir JSON NESNESİ ise: `{"a":<x>,"msg":"<maskeli>",…}`
// (anahtarlar sıralı, mesaj alanı dışındaki her değer `<x>`). Kimlik
// değerleri böylece imzaya hiç girmez.
func jsonSignature(s string) (sigInfo, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return sigInfo{}, false
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	msgKey, msgText := "", ""
	for _, want := range sigMessageKeys {
		for _, k := range keys {
			if !strings.EqualFold(k, want) {
				continue
			}
			var v string
			if json.Unmarshal(m[k], &v) == nil && strings.TrimSpace(v) != "" {
				msgKey, msgText = k, maskSignatureText(strings.TrimSpace(v), false)
			}
			break
		}
		if msgKey != "" {
			break
		}
	}
	level := ""
	for _, want := range sigLevelKeys {
		for _, k := range keys {
			if strings.EqualFold(k, want) {
				var v string
				if json.Unmarshal(m[k], &v) == nil {
					level = strings.TrimSpace(v)
				}
				break
			}
		}
		if level != "" {
			break
		}
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kk, _ := json.Marshal(MaskIDTokens(k, "<x>"))
		b.Write(kk)
		b.WriteByte(':')
		if k == msgKey {
			b.WriteByte('"')
			b.WriteString(msgText)
			b.WriteByte('"')
		} else {
			b.WriteString("<x>")
		}
	}
	b.WriteByte('}')
	query := msgText
	if msgKey == "" {
		// Mesajsız gövde: arama anahtar adlarıyla (≥3 karakterli olanlar).
		query = strings.Join(keys, " <x> ")
	}
	return sigInfo{sig: capSignature(b.String()), query: query, level: level}, true
}

func capSignature(s string) string {
	if len(s) > signatureMaxLen {
		s = s[:signatureMaxLen]
	}
	return s
}

func isSigAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// MaskIDTokens — v0.10.1496: metindeki harf/rakam koşularından (ayraç =
// harf/rakam dışı her şey; `_` ve `-` dahil) kimlik gibi olanları repl ile
// değiştirir. Doğal dil sözcükleri (rakamsız) ve kısa belirteçler
// (OP12, v2, sha256, log4j2) aynen kalır. Desenler (NormalizeSignature) ve
// Drain maskesi (templater.Mask) bu TEK yüklemi kullanır.
func MaskIDTokens(s, repl string) string {
	var out strings.Builder
	out.Grow(len(s))
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		if tok := s[start:end]; IsIDLikeToken(tok) {
			out.WriteString(repl)
		} else {
			out.WriteString(tok)
		}
		start = -1
	}
	for i, r := range s {
		if isSigAlnum(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		flush(i)
		out.WriteRune(r)
	}
	flush(len(s))
	return out.String()
}

// IsIDLikeToken — tek harf/rakam koşusu kimlik mi? SAF.
//   - yalnız rakam, ≥4 (sınırsız konumdaki sayılar: `order_123456`)
//   - yalnız hex, ≥8, en az bir rakam (`1a2b3c4d`; `deadbeef` sözcük sayılır)
//   - rakam baskın: ≥5 rakam ve rakam > yarısı (`ab12345`; `sha256`, `iso8601` kalır)
//   - uzun karışık: ≥10 ve ≥3 rakam, ya da ≥16 ve ≥2 rakam
//     (`SPE1015060201ZTod…`, ULID, base64 parçaları)
//   - base64-benzeri: ≥16, büyük ve küçük harf her biri ≥¼ ve harf durumu
//     değişimi ≥⅓ (camelCase sınıf adları — `NullPointerException` — kalır)
func IsIDLikeToken(tok string) bool {
	n, digits, letters, upper, lower, flips, hex := 0, 0, 0, 0, 0, 0, true
	prevCase := 0 // 1 upper, 2 lower
	for _, r := range tok {
		n++
		switch {
		case r >= '0' && r <= '9':
			digits++
			prevCase = 0
			continue
		case unicode.IsDigit(r):
			digits++
			hex = false
			prevCase = 0
			continue
		}
		letters++
		if !((r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			hex = false
		}
		c := 0
		if unicode.IsUpper(r) {
			upper++
			c = 1
		} else if unicode.IsLower(r) {
			lower++
			c = 2
		}
		if prevCase != 0 && c != 0 && c != prevCase {
			flips++
		}
		prevCase = c
	}
	switch {
	case n == 0:
		return false
	case n >= 16 && upper*4 >= n && lower*4 >= n && flips*3 >= n:
		return true // base64-benzeri (rakamlı ya da rakamsız)
	case digits == 0:
		return false
	case letters == 0:
		return n >= 4
	case hex && n >= 8:
		return true
	case digits >= 5 && digits*2 > n:
		return true
	case n >= 16 && digits >= 2, n >= 10 && digits >= 3:
		return true
	}
	return false
}

// SignatureHash — imzanın xxhash64'ü (grup anahtarı; UI'da kısa kimlik).
func SignatureHash(sig string) uint64 { return xxhash.Sum64String(sig) }

// severityFromText — v0.10.1496 (d): seviye metninden OTel severity_number
// bandının tabanı (severityBands ile aynı önekler; FATAL ayrı, 21). Metin
// tanınmazsa 0. ES'te severity_number alanı yapılandırılmamış kurulumlarda
// desen satırının Seviye kolonu 0 kalıyordu.
func severityFromText(t string) uint8 {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case t == "":
		return 0
	case strings.HasPrefix(t, "fatal"), strings.HasPrefix(t, "crit"), strings.HasPrefix(t, "panic"):
		return 21
	case strings.HasPrefix(t, "err"):
		return 17
	case strings.HasPrefix(t, "warn"):
		return 13
	case strings.HasPrefix(t, "info"):
		return 9
	case strings.HasPrefix(t, "debug"):
		return 5
	case strings.HasPrefix(t, "trace"):
		return 1
	}
	return 0
}
