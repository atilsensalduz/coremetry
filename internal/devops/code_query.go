package devops

// code_query.go — v0.10.1506 (plan-operation-card R4 + "Operatör kararı
// 2026-10-23 gece"): Azure DevOps Code Search sorgusunun TEK kurucusu, kod
// öğesi filtreleriyle (def: / ref: / class: / interface: / field: / method:).
// Operatör prod Azure DevOps Server'da `ref:metotAdı`nın çalıştığını
// doğruladı; kart giriş metodunu, DTO'larını ve çağrılan metotları düz metin
// yerine bu filtrelerle bulur, filtre sonuç vermezse düz metne düşer.
//
// NEDEN TEK KURUCU. Sorgu metninin parçaları telemetriden (rota, span adı,
// operasyon kodu) ve kaynak koddan (tip adı, metot adı) geliyor. Code Search
// sözdiziminde boşluk VE demektir, `repo:` / `proj:` / `path:` / `ext:`
// filtre, `OR` / `NOT` / parantez işleç: süzülmemiş bir parça aramayı başka
// depoya ya da projeye genişletebilir (kart depo süzgeçli istek atar ama
// metin içindeki `repo:` sunucuda süzgeci ezebilir). Kural:
//   - öğe filtresi (def/ref/class/…) YALNIZ Java/C# tanımlayıcısı taşır
//     (^[A-Za-z_$][A-Za-z0-9_$]{0,127}$) — başka her şey HATA, sorgu kurulmaz;
//   - düz metin her zaman TIRNAKLI ifade; içindeki `"` ve `\` boşluğa
//     çevrilir (Code Search ifade içinde güvenilir bir kaçış tanımlamıyor;
//     ifadeyi kapatamayan metin işleç de filtre de olamaz), kontrol
//     karakterleri boşluk, ≤200 rune;
//   - uzantı yalnız küçük harf [a-z0-9]{1,8}.
// Testler: code_query_test.go (kaçış, enjeksiyon denemeleri, tür listesi).

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// CodeQueryKind — Code Search terim türü.
type CodeQueryKind string

const (
	CodeQueryText      CodeQueryKind = "text"
	CodeQueryDef       CodeQueryKind = "def"
	CodeQueryRef       CodeQueryKind = "ref"
	CodeQueryClass     CodeQueryKind = "class"
	CodeQueryInterface CodeQueryKind = "interface"
	CodeQueryField     CodeQueryKind = "field"
	CodeQueryMethod    CodeQueryKind = "method"
)

// codeElementKinds — sunucuya `tür:ad` diye giden öğe filtreleri.
var codeElementKinds = map[CodeQueryKind]bool{
	CodeQueryDef: true, CodeQueryRef: true, CodeQueryClass: true,
	CodeQueryInterface: true, CodeQueryField: true, CodeQueryMethod: true,
}

// CodeTerm — sorgunun bir terimi.
type CodeTerm struct {
	Kind  CodeQueryKind
	Value string
}

var (
	codeIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]{0,127}$`)
	codeExtRe   = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// codeTextMaxRunes — tırnaklı ifadenin tavanı.
const codeTextMaxRunes = 200

// ValidCodeIdent — SAF: öğe filtresine girebilir mi?
func ValidCodeIdent(s string) bool { return codeIdentRe.MatchString(s) }

// quoteCodeText — SAF: düz metni tırnaklı, kapatılamaz bir ifadeye çevirir;
// boş kalırsa "".
func quoteCodeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteRune(' ')
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	t := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(t); len(r) > codeTextMaxRunes {
		t = strings.TrimSpace(string(r[:codeTextMaxRunes]))
	}
	if t == "" {
		return ""
	}
	return `"` + t + `"`
}

// BuildCodeQuery — SAF, tablo-testli: terimler (VE) + isteğe bağlı uzantı →
// Code Search metni. Geçersiz bir öğe adı, bilinmeyen tür, boş metin ya da
// geçersiz uzantı HATA döner (sessizce düşürmek, daha geniş bir aramaya
// dönüşürdü).
func BuildCodeQuery(terms []CodeTerm, ext string) (string, error) {
	if len(terms) == 0 {
		return "", fmt.Errorf("arama terimi yok")
	}
	parts := make([]string, 0, len(terms)+1)
	for _, t := range terms {
		v := strings.TrimSpace(t.Value)
		switch {
		case t.Kind == CodeQueryText || t.Kind == "":
			q := quoteCodeText(v)
			if q == "" {
				return "", fmt.Errorf("boş arama metni")
			}
			parts = append(parts, q)
		case codeElementKinds[t.Kind]:
			if !ValidCodeIdent(v) {
				return "", fmt.Errorf("%s: filtresi yalnız tanımlayıcı alır", t.Kind)
			}
			parts = append(parts, string(t.Kind)+":"+v)
		default:
			return "", fmt.Errorf("bilinmeyen arama türü %q", t.Kind)
		}
	}
	if ext = strings.TrimSpace(ext); ext != "" {
		if !codeExtRe.MatchString(ext) {
			return "", fmt.Errorf("geçersiz uzantı %q", ext)
		}
		parts = append(parts, "ext:"+ext)
	}
	return strings.Join(parts, " "), nil
}
