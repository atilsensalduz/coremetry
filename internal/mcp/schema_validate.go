package mcp

// schema_validate.go — v0.10.1456 (CoSRE araştırma programı C1, karar 6;
// docs/DECISIONS.md "C1 — araç argümanı şema doğrulaması"): araç
// argümanlarının JSON-Schema ALT KÜMESİ doğrulayıcısı. Bağımlılıksız, saf.
//
// Neden yerli: 74 aracın şemaları yalnız type / properties / required / enum /
// minimum / maximum / items / maxItems / maxLength / additionalProperties
// kullanıyor (HEAD sayımı; pattern / minItems / minLength desteklenir ama
// bugün kullanılmıyor). Bu on bir anahtar ~200 satır; bir JSON-Schema
// kütüphanesi govulncheck / Trivy / CodeQL yüzeyini büyütürdü. Başka her
// anahtar (description, default, oneOf, $ref …) YOK SAYILIR — ileriye
// uyumlu: yeni bir şema anahtarı doğrulayıcıyı kırmaz, yalnız denetlenmez.
//
// Bilinçli yumuşaklıklar (handler'ların Go json.Unmarshal davranışıyla aynı
// hizada; doğrulayıcı handler'dan ÖNCE koşar, onun yumuşatmalarını bozmaz):
//   - `null` değerli alan YOK sayılır (Go sıfır değere çözer); zorunluysa
//     "required" sayılır.
//   - enum karşılaştırması büyük/küçük harf DUYARSIZ — aracın kendi
//     coercion'ı (query_traces.go coerceEnum) normalize eder.
//   - integer: JSON sayısı tam sayıysa (30, 30.0) geçer; "30" (metin) HATA —
//     handler'ın json.Unmarshal'ı da düşürür, ipucu "tırnaksız sayı" der.
//   - boş / "null" gövde = `{}`.
//
// Kullanım yeri Executor Scope dikişi (internal/ai/agent/tools/schema_scope.go):
// sohbet `warn` (yalnız gözlem), AI SRE döngüsü `enforce` (ArgIssuesErr →
// Kind bad_args). Dış MCP teli bu doğrulayıcıyı ÇAĞIRMAZ (dış istemci şemayı
// kendi okur; ileride ayrı kip).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// ArgIssue — tek doğrulama bulgusu. Path alan yolu ("service",
// "labels.env", "names[2]"; kök ""), Rule ihlal edilen anahtar (required,
// type, enum, minimum, maximum, items, maxItems, minItems, minLength,
// maxLength, pattern, additionalProperties, json), Want beklenen, Got gelen
// (kırpılmış).
type ArgIssue struct {
	Path string `json:"path"`
	Rule string `json:"rule"`
	Got  string `json:"got,omitempty"`
	Want string `json:"want,omitempty"`
}

// argIssuesMax — tek çağrının raporlanan bulgu tavanı (hint kısa kalsın;
// küçük model ilk birkaç alanı düzeltir, uzun liste kafa karıştırır).
const argIssuesMax = 8

// argGotMaxRunes — Got alanının tavanı (modelin gönderdiği değer yankılanır).
const argGotMaxRunes = 60

// ValidateArgs — SAF: raw argümanı şemaya karşı denetler; sorun yoksa nil.
// Şema nil/boşsa hiçbir şey denetlenmez (nil).
func ValidateArgs(schema map[string]any, raw json.RawMessage) []ArgIssue {
	if len(schema) == 0 {
		return nil
	}
	v, ok := decodeArgs(raw)
	if !ok {
		return []ArgIssue{{Path: "", Rule: "json", Got: capRunes(strings.TrimSpace(string(raw)), argGotMaxRunes), Want: "JSON nesnesi"}}
	}
	var out []ArgIssue
	validateNode(schema, v, "", &out)
	if len(out) > argIssuesMax {
		out = out[:argIssuesMax]
	}
	return out
}

// decodeArgs — boş / null gövde boş nesnedir; UseNumber tam sayıları kayıpsız tutar.
func decodeArgs(raw json.RawMessage) (any, bool) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return map[string]any{}, true
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if dec.More() {
		return nil, false
	}
	return v, true
}

func addIssue(out *[]ArgIssue, is ArgIssue) {
	if len(*out) < argIssuesMax+1 { // +1: tavanın aşıldığını keserken bilmek yeter
		*out = append(*out, is)
	}
}

func validateNode(schema map[string]any, v any, path string, out *[]ArgIssue) {
	if schema == nil {
		return
	}
	if types := schemaTypes(schema["type"]); len(types) > 0 && !matchesAnyType(types, v) {
		want := strings.Join(types, "|")
		addIssue(out, ArgIssue{Path: path, Rule: "type", Want: want, Got: describeValue(v)})
		return // tipi tutmayan değerde diğer anahtarlar anlamsız
	}
	if enum, ok := schemaEnum(schema["enum"]); ok && !enumContains(enum, v) {
		addIssue(out, ArgIssue{Path: path, Rule: "enum", Want: enumWant(enum), Got: describeValue(v)})
	}
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return
		}
		if mn, ok := schemaNumber(schema["minimum"]); ok && f < mn {
			addIssue(out, ArgIssue{Path: path, Rule: "minimum", Want: "≥ " + fmtNum(mn), Got: x.String()})
		}
		if mx, ok := schemaNumber(schema["maximum"]); ok && f > mx {
			addIssue(out, ArgIssue{Path: path, Rule: "maximum", Want: "≤ " + fmtNum(mx), Got: x.String()})
		}
	case string:
		n := utf8.RuneCountInString(x)
		if mn, ok := schemaNumber(schema["minLength"]); ok && float64(n) < mn {
			addIssue(out, ArgIssue{Path: path, Rule: "minLength", Want: "en az " + fmtNum(mn) + " karakter", Got: fmt.Sprintf("%d karakter", n)})
		}
		if mx, ok := schemaNumber(schema["maxLength"]); ok && float64(n) > mx {
			addIssue(out, ArgIssue{Path: path, Rule: "maxLength", Want: "en çok " + fmtNum(mx) + " karakter", Got: fmt.Sprintf("%d karakter", n)})
		}
		if p, ok := schema["pattern"].(string); ok && p != "" {
			if re := compiledPattern(p); re != nil && !re.MatchString(x) {
				addIssue(out, ArgIssue{Path: path, Rule: "pattern", Want: "desen " + p, Got: describeValue(v)})
			}
		}
	case []any:
		if mn, ok := schemaNumber(schema["minItems"]); ok && float64(len(x)) < mn {
			addIssue(out, ArgIssue{Path: path, Rule: "minItems", Want: "en az " + fmtNum(mn) + " öğe", Got: fmt.Sprintf("%d öğe", len(x))})
		}
		if mx, ok := schemaNumber(schema["maxItems"]); ok && float64(len(x)) > mx {
			addIssue(out, ArgIssue{Path: path, Rule: "maxItems", Want: "en çok " + fmtNum(mx) + " öğe", Got: fmt.Sprintf("%d öğe", len(x))})
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, it := range x {
				if it == nil {
					continue
				}
				validateNode(items, it, fmt.Sprintf("%s[%d]", path, i), out)
			}
		}
	case map[string]any:
		validateObject(schema, x, path, out)
	}
}

func validateObject(schema map[string]any, obj map[string]any, path string, out *[]ArgIssue) {
	props, _ := schema["properties"].(map[string]any)
	for _, name := range schemaStrings(schema["required"]) {
		if val, ok := obj[name]; !ok || val == nil {
			addIssue(out, ArgIssue{Path: joinPath(path, name), Rule: "required", Want: "zorunlu alan", Got: "yok"})
		}
	}
	// Anahtar sırası deterministik: bulgu listesi (ve hint metni) her
	// koşuda aynı olsun.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := obj[k]
		if ps, ok := props[k].(map[string]any); ok {
			if val == nil {
				continue // null = yok (Go sıfır değeri)
			}
			validateNode(ps, val, joinPath(path, k), out)
			continue
		}
		switch ap := schema["additionalProperties"].(type) {
		case bool:
			if !ap {
				addIssue(out, ArgIssue{Path: joinPath(path, k), Rule: "additionalProperties", Want: "tanımsız alan — şemadaki adları kullan", Got: describeValue(val)})
			}
		case map[string]any:
			if val != nil {
				validateNode(ap, val, joinPath(path, k), out)
			}
		}
	}
}

func joinPath(base, k string) string {
	if base == "" {
		return k
	}
	return base + "." + k
}

// schemaTypes — "type" string ya da string dizisi.
func schemaTypes(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	default:
		return schemaStrings(v)
	}
}

func schemaStrings(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func schemaEnum(v any) ([]any, bool) {
	switch t := v.(type) {
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, len(out) > 0
	case []any:
		return t, len(t) > 0
	}
	return nil, false
}

// schemaNumber — Go literal şemalarında int / float64, JSON'dan gelen şemada float64.
func schemaNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func matchesAnyType(types []string, v any) bool {
	for _, t := range types {
		if matchesType(t, v) {
			return true
		}
	}
	// Bilinmeyen tip adı (şema hatası) değeri reddetmez.
	for _, t := range types {
		switch t {
		case "string", "integer", "number", "boolean", "array", "object", "null":
		default:
			return true
		}
	}
	return false
}

func matchesType(t string, v any) bool {
	switch t {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(json.Number)
		return ok
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		if _, err := n.Int64(); err == nil {
			return true
		}
		f, err := n.Float64()
		return err == nil && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "null":
		return v == nil
	}
	return false
}

func enumContains(enum []any, v any) bool {
	for _, e := range enum {
		switch es := e.(type) {
		case string:
			if s, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(s), es) {
				return true
			}
		default:
			if fmt.Sprint(e) == fmt.Sprint(v) {
				return true
			}
		}
	}
	return false
}

func enumWant(enum []any) string {
	parts := make([]string, 0, len(enum))
	for _, e := range enum {
		parts = append(parts, fmt.Sprint(e))
	}
	s := strings.Join(parts, "|")
	return "şunlardan biri: " + capRunes(s, 160)
}

func fmtNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}

// describeValue — Got metni: tür + kırpılmış değer (modelin kendi yazdığı).
func describeValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return "metin " + capRunes(fmt.Sprintf("%q", x), argGotMaxRunes)
	case json.Number:
		return "sayı " + x.String()
	case bool:
		return fmt.Sprintf("boolean %v", x)
	case []any:
		return fmt.Sprintf("dizi (%d öğe)", len(x))
	case map[string]any:
		return "nesne"
	}
	return capRunes(fmt.Sprint(v), argGotMaxRunes)
}

var patternCache sync.Map // desen → *regexp.Regexp (derlenemeyen: nil)

func compiledPattern(p string) *regexp.Regexp {
	if v, ok := patternCache.Load(p); ok {
		re, _ := v.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(p)
	if err != nil {
		re = nil // bozuk desen değeri reddetmez (şema hatası, çağıran hatası değil)
	}
	patternCache.Store(p, re)
	return re
}

// ── modele giden hata sözleşmesi ─────────────────────────────────────

// ArgIssuesErr — doğrulama reddi. ClassifyToolError bunu tanır ve sınıfı
// bad_args, tekrar denenebilir (bir kez), alan-adlı Türkçe ipucuyla yazar.
// Final: aynı araç için ardışık ikinci ret — model aracı BIRAKMALI
// (karar 6 "1 retry, sonra skipped"); retryable=false.
type ArgIssuesErr struct {
	Tool   string
	Issues []ArgIssue
	Final  bool
}

func (e *ArgIssuesErr) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, is := range e.Issues {
		parts = append(parts, issueText(is))
	}
	return "bad_args: " + e.Tool + ": " + strings.Join(parts, "; ")
}

// ArgIssuesError — bulgu yoksa nil; varsa *ArgIssuesErr.
func ArgIssuesError(name string, issues []ArgIssue) error {
	if len(issues) == 0 {
		return nil
	}
	return &ArgIssuesErr{Tool: name, Issues: issues}
}

// argHintIssues — hint'e giren bulgu sayısı (gerisi detail'de).
const argHintIssues = 3

// ToolError — modele giden sözleşme nesnesi (ToolErrorJSON yolu).
func (e *ArgIssuesErr) ToolError() ToolError {
	if e.Final {
		return ToolError{
			Error: ToolErrBadArgs, Retryable: false,
			Hint:   "bu araç art arda iki kez şemaya uymayan argümanla çağrıldı — bu aracı BIRAK; eldeki kanıtla ya da başka bir araçla devam et",
			Detail: capRunes(e.Error(), toolErrDetailMaxRunes),
		}
	}
	n := len(e.Issues)
	if n > argHintIssues {
		n = argHintIssues
	}
	parts := make([]string, 0, n)
	for _, is := range e.Issues[:n] {
		parts = append(parts, issueHint(is))
	}
	return ToolError{
		Error: ToolErrBadArgs, Retryable: true,
		Hint:   strings.Join(parts, "; ") + " — yalnız bu alan(lar)ı düzelt ve aracı BİR kez daha çağır",
		Detail: capRunes(e.Error(), toolErrDetailMaxRunes),
	}
}

func issueText(is ArgIssue) string {
	p := is.Path
	if p == "" {
		p = "(kök)"
	}
	s := p + ": " + is.Rule
	if is.Want != "" {
		s += " (beklenen " + is.Want
		if is.Got != "" {
			s += ", gelen " + is.Got
		}
		s += ")"
	}
	return s
}

// issueHint — "<alan>: <beklenen> bekleniyor (gelen <değer>)" + tip özel öğüt.
func issueHint(is ArgIssue) string {
	p := is.Path
	if p == "" {
		p = "argüman gövdesi"
	}
	if is.Rule == "required" {
		return p + ": zorunlu alan eksik — doldur"
	}
	s := p + ": " + wantTR(is) + " bekleniyor"
	if is.Got != "" {
		s += " (gelen " + is.Got + ")"
	}
	if is.Rule == "type" && (is.Want == "integer" || is.Want == "number") && strings.HasPrefix(is.Got, "metin ") {
		s += " — sayıyı tırnaksız gönder"
	}
	return s
}

var typeNamesTR = map[string]string{
	"string": "metin", "integer": "tam sayı", "number": "sayı", "boolean": "true/false",
	"array": "dizi", "object": "nesne", "null": "null",
}

func wantTR(is ArgIssue) string {
	if is.Rule != "type" {
		return is.Want
	}
	parts := strings.Split(is.Want, "|")
	for i, p := range parts {
		if tr, ok := typeNamesTR[p]; ok {
			parts[i] = tr
		}
	}
	return strings.Join(parts, " ya da ")
}
