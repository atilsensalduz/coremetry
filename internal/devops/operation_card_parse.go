package devops

// operation_card_parse.go — v0.10.1506 (plan-operation-card §2.3.2, R4):
// operasyon kartının kod yarısının SAF ayrıştırıcıları. Ağ yok, saat yok;
// hepsi operation_card_parse_test.go'da tablo-testli (Java fixture'ları
// testdata/opcard/).
//
// Ne çıkarılır (ve neden YALNIZ bunlar — plan §2.5 "değer yok"):
//   - giriş metodunun İMZASI (değiştiriciler + dönüş tipi + ad + parametre
//     tipleri/adları; gövde DEĞİL);
//   - istek/cevap DTO'larının alan ADI + TİPİ (static/final sabitler,
//     ilklendirici DEĞERLERİ atlanır);
//   - çağrı yerleri: dosya:satır, tür (http|grpc|db|messaging), alıcı.metot,
//     ve yalnız ROTA ya da TOPIC biçimli dize sabiti (SQL metni, gövde,
//     serbest dize ASLA — db çağrısının sabiti hiç okunmaz).
// Bunlar tarayıcıya gider (plan §10 soru 2, operatör onaylı); kod satırları
// yalnız CodeWindow.Content'te kalır ve yalnız modele gider (R5).
//
// Ayrıştırma bilinçli olarak SEZGİSEL: maskJava (dize/yorum boşlukları) +
// parantez dengesi + dar regex'ler. Tam bir Java ayrıştırıcısı bu yüzeyin
// sorusu değil; yanlış pozitif "eşleşme" yerine eşleşmeyen satır (-1)
// tercih edilir.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ── rota ────────────────────────────────────────────────────────────────

// routeStopSegments — arama anahtarı olamayacak kadar genel rota parçaları.
var routeStopSegments = map[string]bool{
	"api": true, "rest": true, "v1": true, "v2": true, "v3": true, "v4": true,
	"public": true, "internal": true, "service": true, "services": true,
}

// routeSegments — SAF: rota → parçalar; parametre parçaları "{}" (Spring
// `{id}` / `{id:\d+}`, Express `:id`, joker `*`), sorgu dizesi atılır.
func routeSegments(r string) []string {
	r = strings.TrimSpace(r)
	if i := strings.IndexAny(r, "?#"); i >= 0 {
		r = r[:i]
	}
	var out []string
	for _, s := range strings.Split(r, "/") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "{") || strings.HasPrefix(s, ":") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "$") {
			s = "{}"
		}
		out = append(out, strings.ToLower(s))
	}
	return out
}

// NormRoute — SAF: karşılaştırma biçimi ("/orders/{}/approve").
func NormRoute(r string) string { return "/" + strings.Join(routeSegments(r), "/") }

// RouteSearchSegment — SAF: rotanın Code Search'e gidecek EN AYIRT EDİCİ
// sabit parçası (en uzun, ≥3 karakter, genel parçalar hariç; eşitlikte
// sondaki). Sınıf düzeyi önek + metot düzeyi sonek ayrı yazıldığı için tam
// rota koddaki hiçbir sabite eşit olmayabilir; tek parça her iki yazımda da
// geçer. "" → aranabilir parça yok.
func RouteSearchSegment(route string) string {
	best := ""
	for _, s := range routeSegments(route) {
		if s == "{}" || len(s) < 3 || routeStopSegments[s] || !ValidCodeIdent(strings.NewReplacer("-", "_", ".", "_").Replace(s)) {
			continue
		}
		if len(s) >= len(best) {
			best = s
		}
	}
	return best
}

// routeMatchScore — SAF: koddaki tam yol ↔ telemetri rotası. 2 = birebir,
// 1 = rota kod yolunu SONEK olarak taşıyor (bağlam yolu `/api` gibi bir önek
// telemetride var, kodda yok), 0 = değil.
func routeMatchScore(codePath, route string) int {
	c, r := routeSegments(codePath), routeSegments(route)
	if len(c) == 0 || len(r) == 0 {
		return 0
	}
	if strings.Join(c, "/") == strings.Join(r, "/") {
		return 2
	}
	if len(r) > len(c) && strings.Join(r[len(r)-len(c):], "/") == strings.Join(c, "/") {
		static := 0
		for _, s := range c {
			if s != "{}" {
				static++
			}
		}
		if static > 0 {
			return 1
		}
	}
	return 0
}

// ── anotasyon / dize sabiti yardımcıları ────────────────────────────────

// literalsIn — SAF: masked[a:b] aralığındaki dize sabitlerinin src'deki
// İÇERİĞİ (maskJava ofsetleri korur; tırnaklar maskede durur).
func literalsIn(src, masked string, a, b int) []string {
	if b > len(masked) {
		b = len(masked)
	}
	var out []string
	for i := a; i < b; i++ {
		if masked[i] != '"' {
			continue
		}
		j := i + 1
		for j < b && masked[j] != '"' && masked[j] != '\n' {
			j++
		}
		if j >= b || masked[j] != '"' {
			break
		}
		out = append(out, src[i+1:j])
		i = j
	}
	return out
}

// splitTopLevel — SAF: s'yi (), {}, [], <> dışındaki virgüllerden böler.
func splitTopLevel(s string, angle bool) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case '<':
			if angle {
				depth++
			}
		case '>':
			if angle {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// annotationArgPaths — SAF: bir mapping anotasyonunun argümanlarından yol
// sabitleri (`"x"`, `value = "x"`, `path = {"a", "b"}`); diğer anahtarlar
// (produces, consumes, params, headers, name) atlanır.
func annotationArgPaths(src, masked string, a, b int) []string {
	var out []string
	off := a
	for _, part := range splitTopLevel(masked[a:b], false) {
		pa, pb := off, off+len(part)
		off = pb + 1
		key := ""
		if eq := topLevelIndex(part, '='); eq >= 0 {
			key = strings.TrimSpace(part[:eq])
		}
		if key != "" && key != "value" && key != "path" {
			continue
		}
		out = append(out, literalsIn(src, masked, pa, pb)...)
	}
	return out
}

// topLevelIndex — parantez dışındaki ilk c.
func topLevelIndex(s string, c byte) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		default:
			if s[i] == c && depth == 0 {
				return i
			}
		}
	}
	return -1
}

var (
	mappingAnnoRe = regexp.MustCompile(`@(GetMapping|PostMapping|PutMapping|DeleteMapping|PatchMapping|RequestMapping|Path)\b`)
	jaxVerbRe     = regexp.MustCompile(`@(GET|POST|PUT|DELETE|PATCH)\b`)
	requestVerbRe = regexp.MustCompile(`RequestMethod\s*\.\s*(GET|POST|PUT|DELETE|PATCH)\b`)
	classDeclRe   = regexp.MustCompile(`^(?:(?:public|protected|private|abstract|final|static|sealed)\s+)*(?:class|interface|record)\b`)
	methodNameRe  = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*\($`)
)

// mappingDecl — bir bildirimin (metot ya da tip) mapping anotasyonları.
type mappingDecl struct {
	first   int // ilk anotasyonun ofseti
	decl    int // bildirimin başı (anotasyonlar atlanmış)
	paths   []string
	verbs   map[string]bool
	hasMap  bool // en az bir mapping / JAX-RS fiil anotasyonu
	isClass bool
}

// collectMappings — SAF: kaynaktaki tüm mapping anotasyonlarını
// bildirimlerine göre gruplar. Sınıf düzeyindekiler önek, metot
// düzeyindekiler sonek.
func collectMappings(src, masked string) []mappingDecl {
	byDecl := map[int]*mappingDecl{}
	var order []int
	add := func(at, end int, name string) {
		decl := skipAnnotations(masked, at)
		d, ok := byDecl[decl]
		if !ok {
			d = &mappingDecl{first: at, decl: decl, verbs: map[string]bool{}}
			hdrEnd := decl + 200
			if hdrEnd > len(masked) {
				hdrEnd = len(masked)
			}
			d.isClass = classDeclRe.MatchString(strings.TrimSpace(masked[decl:hdrEnd]))
			byDecl[decl] = d
			order = append(order, decl)
		}
		if at < d.first {
			d.first = at
		}
		d.hasMap = true
		switch name {
		case "GET", "POST", "PUT", "DELETE", "PATCH":
			d.verbs[name] = true
			return
		case "GetMapping":
			d.verbs["GET"] = true
		case "PostMapping":
			d.verbs["POST"] = true
		case "PutMapping":
			d.verbs["PUT"] = true
		case "DeleteMapping":
			d.verbs["DELETE"] = true
		case "PatchMapping":
			d.verbs["PATCH"] = true
		}
		q := end
		for q < len(masked) && (masked[q] == ' ' || masked[q] == '\t' || masked[q] == '\n' || masked[q] == '\r') {
			q++
		}
		if q < len(masked) && masked[q] == '(' {
			if c := matchClose(masked, q); c > q {
				d.paths = append(d.paths, annotationArgPaths(src, masked, q+1, c)...)
				if name == "RequestMapping" {
					for _, m := range requestVerbRe.FindAllStringSubmatch(masked[q+1:c], -1) {
						d.verbs[m[1]] = true
					}
				}
			}
		}
	}
	for _, m := range mappingAnnoRe.FindAllStringSubmatchIndex(masked, -1) {
		add(m[0], m[1], masked[m[2]:m[3]])
	}
	for _, m := range jaxVerbRe.FindAllStringSubmatchIndex(masked, -1) {
		add(m[0], m[1], masked[m[2]:m[3]])
	}
	sort.Ints(order)
	out := make([]mappingDecl, 0, len(order))
	for _, k := range order {
		out = append(out, *byDecl[k])
	}
	return out
}

// joinRoute — önek + sonek.
func joinRoute(prefix, p string) string {
	return "/" + strings.Trim(strings.TrimRight(prefix, "/")+"/"+strings.TrimLeft(p, "/"), "/")
}

// declMethodName — bildirim başlığındaki metot adı ("" = metot değil).
func declMethodName(masked string, decl int) string {
	end := strings.IndexByte(masked[decl:], '(')
	if end < 0 || end > 600 {
		return ""
	}
	if m := methodNameRe.FindStringSubmatch(masked[decl : decl+end+1]); m != nil {
		return m[1]
	}
	return ""
}

// RouteMethodWindow — SAF, tablo-testli: kaynakta HTTP yöntemi + rotası
// telemetriye uyan handler metodu. Spring (@Get/Post/…Mapping,
// @RequestMapping(method=…)) ve JAX-RS (@Path + @GET…); sınıf düzeyi önek
// birleştirilir; `{id}` ↔ `:id` ↔ `{id:\d+}` eşit. Birebir eşleşme sonek
// eşleşmesinden önce gelir; aynı skorda iki aday → ok=false (belirsiz giriş
// metodu göstermek yanlış kanıt olurdu). method "" = yöntem bilinmiyor.
func RouteMethodWindow(src, method, route string, maxLines int) (MethodWindow, bool) {
	if strings.TrimSpace(src) == "" || len(routeSegments(route)) == 0 {
		return MethodWindow{}, false
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	masked := maskJava(src)
	decls := collectMappings(src, masked)
	prefixes := []string{""}
	for _, d := range decls {
		if d.isClass && len(d.paths) > 0 {
			prefixes = d.paths
			break
		}
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	type cand struct {
		d     mappingDecl
		score int
	}
	var best []cand
	top := 0
	for _, d := range decls {
		if d.isClass || !d.hasMap || declMethodName(masked, d.decl) == "" {
			continue
		}
		if method != "" && len(d.verbs) > 0 && !d.verbs[method] {
			continue
		}
		paths := d.paths
		if len(paths) == 0 {
			paths = []string{""}
		}
		score := 0
		for _, pre := range prefixes {
			for _, p := range paths {
				if s := routeMatchScore(joinRoute(pre, p), route); s > score {
					score = s
				}
			}
		}
		if score == 0 {
			continue
		}
		if score > top {
			top, best = score, nil
		}
		if score == top {
			best = append(best, cand{d, score})
		}
	}
	if len(best) != 1 {
		return MethodWindow{}, false
	}
	d := best[0].d
	return methodWindowFrom(src, masked, lineStartOff(masked, d.first), d.decl, maxLines)
}

// ── imza → tipler ───────────────────────────────────────────────────────

// sigMaxRunes — tarayıcıya giden imzanın tavanı.
const sigMaxRunes = 300

var (
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineCommentRe  = regexp.MustCompile(`//[^\n]*`)
)

// MethodSignature — SAF: MethodWindow.Header → tek satırlık imza (yorumlar
// atılır, boşluk tekilleşir, ≤300 rune).
func MethodSignature(header string) string {
	h := lineCommentRe.ReplaceAllString(blockCommentRe.ReplaceAllString(header, " "), " ")
	h = strings.Join(strings.Fields(h), " ")
	if r := []rune(h); len(r) > sigMaxRunes {
		h = string(r[:sigMaxRunes]) + "…"
	}
	return h
}

// TypedParam — imzadaki bir parametre.
type TypedParam struct {
	Name, Type  string
	Annotations []string // "RequestBody", "PathVariable", …
}

var annoNameRe = regexp.MustCompile(`^@([\w.]+)`)

// stripLeadingAnnotations — SAF: baştaki anotasyonları (argümanlarıyla)
// atar, adlarını döner.
func stripLeadingAnnotations(s string) (rest string, names []string) {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "@") {
		m := annoNameRe.FindStringSubmatch(s)
		if m == nil {
			break
		}
		n := m[1]
		if i := strings.LastIndexByte(n, '.'); i >= 0 {
			n = n[i+1:]
		}
		names = append(names, n)
		s = strings.TrimSpace(s[len(m[0]):])
		if strings.HasPrefix(s, "(") {
			if c := matchClose(s, 0); c > 0 {
				s = strings.TrimSpace(s[c+1:])
			} else {
				return "", names
			}
		}
	}
	return s, names
}

// MethodSignatureTypes — SAF, tablo-testli: imza → parametreler, dönüş
// tipi, metot adı. ok=false: imza biçimi tanınmadı.
func MethodSignatureTypes(sig string) (params []TypedParam, ret, name string, ok bool) {
	masked := maskJava(sig)
	open := strings.IndexByte(masked, '(')
	if open < 0 {
		return nil, "", "", false
	}
	cl := matchClose(masked, open)
	if cl < 0 {
		return nil, "", "", false
	}
	pre := strings.TrimRight(masked[:open], " \t\n")
	i := len(pre)
	for i > 0 && isIdentByte(pre[i-1]) {
		i--
	}
	name = pre[i:]
	if name == "" {
		return nil, "", "", false
	}
	pre = strings.TrimRight(pre[:i], " \t\n")
	// Dönüş tipi: sondan geriye, <> dengeli, boşlukta dur.
	depth, j := 0, len(pre)
	for j > 0 {
		c := pre[j-1]
		if c == '>' {
			depth++
		} else if c == '<' {
			depth--
		} else if depth == 0 && (c == ' ' || c == '\t' || c == '\n') {
			break
		}
		j--
	}
	ret = strings.Join(strings.Fields(pre[j:]), "")
	inner := masked[open+1 : cl]
	if strings.TrimSpace(inner) != "" {
		for _, p := range splitTopLevel(inner, true) {
			var annos []string
			rest := strings.TrimSpace(p)
			for {
				r2, a := stripLeadingAnnotations(strings.TrimSpace(strings.TrimPrefix(rest, "final ")))
				annos = append(annos, a...)
				if r2 == rest {
					break
				}
				rest = r2
			}
			k := len(rest)
			for k > 0 && isIdentByte(rest[k-1]) {
				k--
			}
			pn := rest[k:]
			pt := strings.Join(strings.Fields(rest[:k]), "")
			if pn == "" || pt == "" {
				continue
			}
			params = append(params, TypedParam{Name: pn, Type: pt, Annotations: annos})
		}
	}
	return params, ret, name, true
}

// containerTypes — tipi SARAN (DTO olmayan) jenerikler: argümanları açılır.
var containerTypes = map[string]bool{
	"ResponseEntity": true, "HttpEntity": true, "List": true, "ArrayList": true, "LinkedList": true,
	"Set": true, "HashSet": true, "Collection": true, "Iterable": true, "Optional": true,
	"Mono": true, "Flux": true, "CompletableFuture": true, "CompletionStage": true, "Future": true,
	"Page": true, "Slice": true, "Callable": true, "DeferredResult": true, "Stream": true,
	"Uni": true, "Multi": true, "Single": true, "Observable": true, "Maybe": true,
}

// skipTypes — JDK / çerçeve tipleri: DTO değil, açılmaz.
var skipTypes = map[string]bool{
	"String": true, "Object": true, "Integer": true, "Long": true, "Short": true, "Byte": true,
	"Double": true, "Float": true, "Boolean": true, "Character": true, "Number": true, "Void": true,
	"BigDecimal": true, "BigInteger": true, "Date": true, "LocalDate": true, "LocalDateTime": true,
	"LocalTime": true, "Instant": true, "OffsetDateTime": true, "ZonedDateTime": true, "Duration": true,
	"UUID": true, "Timestamp": true, "Calendar": true, "Currency": true, "Locale": true,
	"Map": true, "HashMap": true, "LinkedHashMap": true, "TreeMap": true, "MultiValueMap": true,
	"HttpServletRequest": true, "HttpServletResponse": true, "ServletRequest": true, "ServletResponse": true,
	"HttpSession": true, "Principal": true, "Authentication": true, "HttpHeaders": true,
	"MultipartFile": true, "Pageable": true, "Sort": true, "Model": true, "ModelMap": true,
	"BindingResult": true, "Errors": true, "WebRequest": true, "ServerWebExchange": true,
	"ServerHttpRequest": true, "ServerHttpResponse": true, "UriComponentsBuilder": true,
	"Resource": true, "InputStream": true, "OutputStream": true, "JsonNode": true, "ObjectNode": true,
	"Response": true, "Request": true, "StreamObserver": true, "Exception": true, "Throwable": true,
}

// AppTypes — SAF, tablo-testli: bir tip ifadesindeki UYGULAMA tipleri
// (kapsayıcılar açılır, JDK/çerçeve/ilkel/tek harfli jenerik atlanır).
func AppTypes(t string) []string {
	t = strings.TrimSpace(strings.ReplaceAll(t, "...", ""))
	t = strings.ReplaceAll(t, "[]", "")
	for _, pre := range []string{"? extends ", "? super ", "?extends", "?super"} {
		t = strings.TrimPrefix(t, pre)
	}
	if t == "" || t == "?" {
		return nil
	}
	base, args := t, ""
	if i := strings.IndexByte(t, '<'); i >= 0 && strings.HasSuffix(t, ">") {
		base, args = t[:i], t[i+1:len(t)-1]
	}
	base = strings.TrimSpace(base)
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[i+1:]
	}
	if containerTypes[base] {
		var out []string
		seen := map[string]bool{}
		for _, a := range splitTopLevel(args, true) {
			for _, x := range AppTypes(a) {
				if !seen[x] {
					seen[x] = true
					out = append(out, x)
				}
			}
		}
		return out
	}
	if skipTypes[base] || len(base) < 2 || !ValidCodeIdent(base) || base[0] < 'A' || base[0] > 'Z' {
		return nil
	}
	return []string{base}
}

// RequestResponseTypes — SAF: imza → istek ve cevap DTO tipleri. İstek:
// @RequestBody parametresi varsa YALNIZ o; yoksa yol/sorgu/başlık
// parametresi olmayan uygulama tipleri.
func RequestResponseTypes(params []TypedParam, ret string) (req, resp []string) {
	has := func(p TypedParam, n string) bool {
		for _, a := range p.Annotations {
			if a == n {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	push := func(dst *[]string, ts []string) {
		for _, t := range ts {
			if !seen[t] {
				seen[t] = true
				*dst = append(*dst, t)
			}
		}
	}
	for _, p := range params {
		if has(p, "RequestBody") {
			push(&req, AppTypes(p.Type))
		}
	}
	if len(req) == 0 {
		for _, p := range params {
			if has(p, "PathVariable") || has(p, "RequestParam") || has(p, "RequestHeader") || has(p, "CookieValue") || has(p, "PathParam") || has(p, "QueryParam") || has(p, "HeaderParam") {
				continue
			}
			push(&req, AppTypes(p.Type))
		}
	}
	push(&resp, AppTypes(ret))
	return req, resp
}

// ── DTO alanları ────────────────────────────────────────────────────────

// DTOField — istek/cevap tipinin bir alanı: YALNIZ ad + tip.
type DTOField struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	// Nested — alanın tipi (ya da elemanı) bir uygulama tipi; kart onu bir
	// seviye açar (kendi alanları Owner=o tip ile listelenir).
	Nested bool `json:"nested,omitempty"`
}

// dtoFieldsPerOwner — tip başına alan tavanı.
const dtoFieldsPerOwner = 40

var fieldModRe = regexp.MustCompile(`^(?:(?:public|protected|private|static|final|transient|volatile)\s+)*`)

// cleanType — paket niteleyicisi soyulur (dış tip), boşluklar atılır.
func cleanType(t string) string {
	t = strings.Join(strings.Fields(t), "")
	base, rest := t, ""
	if i := strings.IndexByte(t, '<'); i >= 0 {
		base, rest = t[:i], t[i:]
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		base = base[i+1:]
	}
	return base + rest
}

// fieldFromDecl — SAF: alan bildirimi metni (maskeli, ';' olmadan) → ad, tip.
func fieldFromDecl(stmt string) (name, typ string, ok bool) {
	s, _ := stripLeadingAnnotations(stmt)
	mods := fieldModRe.FindString(s)
	if strings.Contains(mods, "static") {
		return "", "", false
	}
	s = strings.TrimSpace(s[len(mods):])
	if eq := topLevelIndex(s, '='); eq >= 0 {
		s = strings.TrimSpace(s[:eq])
	}
	if s == "" || strings.ContainsAny(s, "(){};") {
		return "", "", false
	}
	k := len(s)
	for k > 0 && isIdentByte(s[k-1]) {
		k--
	}
	name, typ = s[k:], strings.TrimSpace(s[:k])
	if name == "" || typ == "" || strings.Contains(typ, ",") && !strings.Contains(typ, "<") {
		return "", "", false
	}
	if typ == "return" || typ == "package" || typ == "import" {
		return "", "", false
	}
	return name, cleanType(typ), true
}

// DTOFields — SAF, tablo-testli: kaynakta `typ` adlı class/record'un
// alanları (iç içe tip olarak tanımlanmış olsa da). static alanlar,
// metotlar, iç tiplerin alanları atlanır; enum/interface → ok=true, alan yok.
// ok=false: tip bu kaynakta tanımlı değil.
func DTOFields(src, typ string) ([]DTOField, bool) {
	if !ValidCodeIdent(typ) || strings.TrimSpace(src) == "" {
		return nil, false
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	masked := maskJava(src)
	re := regexp.MustCompile(`\b(class|record|enum|interface)\s+` + regexp.QuoteMeta(typ) + `\b`)
	m := re.FindStringSubmatchIndex(masked)
	if m == nil {
		return nil, false
	}
	kind := masked[m[2]:m[3]]
	if kind == "enum" || kind == "interface" {
		return []DTOField{}, true
	}
	p := m[1]
	// Jenerik parametreleri atla.
	for p < len(masked) && (masked[p] == ' ' || masked[p] == '\n' || masked[p] == '\t') {
		p++
	}
	if p < len(masked) && masked[p] == '<' {
		depth := 0
		for ; p < len(masked); p++ {
			if masked[p] == '<' {
				depth++
			} else if masked[p] == '>' {
				depth--
				if depth == 0 {
					p++
					break
				}
			}
		}
	}
	out := []DTOField{}
	addField := func(name, t string) {
		if len(out) >= dtoFieldsPerOwner {
			return
		}
		f := DTOField{Owner: typ, Name: name, Type: t}
		for _, a := range AppTypes(t) {
			if a != typ {
				f.Nested = true
			}
		}
		out = append(out, f)
	}
	if kind == "record" {
		open := strings.IndexByte(masked[p:], '(')
		if open < 0 {
			return out, true
		}
		open += p
		cl := matchClose(masked, open)
		if cl < 0 {
			return out, true
		}
		for _, part := range splitTopLevel(masked[open+1:cl], true) {
			rest, _ := stripLeadingAnnotations(part)
			rest = strings.TrimSpace(rest)
			k := len(rest)
			for k > 0 && isIdentByte(rest[k-1]) {
				k--
			}
			if n, t := rest[k:], strings.TrimSpace(rest[:k]); n != "" && t != "" {
				addField(n, cleanType(t))
			}
		}
		return out, true
	}
	open := strings.IndexByte(masked[p:], '{')
	if open < 0 {
		return out, true
	}
	open += p
	cl := matchClose(masked, open)
	if cl < 0 {
		return out, true
	}
	depth, paren, start := 0, 0, open+1
	for i := open + 1; i < cl; i++ {
		switch masked[i] {
		case '(':
			paren++
		case ')':
			paren--
		case '{':
			if paren == 0 {
				depth++
			}
		case '}':
			if paren == 0 {
				depth--
				if depth == 0 {
					start = i + 1
				}
			}
		case ';':
			if depth == 0 && paren == 0 {
				if n, t, ok := fieldFromDecl(masked[start:i]); ok {
					addField(n, t)
				}
				start = i + 1
			}
		}
	}
	return out, true
}

// ── çağrı yerleri ───────────────────────────────────────────────────────

// Callee — gövdedeki bir alıcı.metot çağrısı.
type Callee struct{ Receiver, Method string }

var calleesRe = regexp.MustCompile(`(?:^|[^\w$.])(?:this\s*\.\s*)?([a-z][\w$]*(?:Manager|Service|Facade|Client|Gateway|Repository|Dao|Producer))\s*\.\s*([a-z][\w$]*)\s*\(`)

// ManagerCallees — SAF (ManagerCallee'nin genellemesi, v0.10.1506): gövdenin
// (maskeli) Manager|Service|Facade|Client|Gateway|Repository|Dao|Producer
// sonekli alıcı çağrıları, ilk görülme sırasıyla, alıcı başına tek, ≤max.
func ManagerCallees(body string, max int) []Callee {
	var out []Callee
	seen := map[string]bool{}
	for _, m := range calleesRe.FindAllStringSubmatch(maskJava(body), -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, Callee{Receiver: m[1], Method: m[2]})
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

// CallSite — çağrı yeri: dosya:satır + tür + alıcı.metot + koddaki LİTERAL
// hedef (rota parçası / topic) + Feign adı. MatchedChild trace yarısının
// Children dizisine indeks (-1 = eşleşmedi).
type CallSite struct {
	Path         string `json:"path"`
	Line         int    `json:"line"`
	Kind         string `json:"kind"` // http | grpc | db | messaging
	Callee       string `json:"callee"`
	Target       string `json:"target,omitempty"`
	Service      string `json:"service,omitempty"` // @FeignClient(name) — servis adı ipucu
	MatchedChild int    `json:"matchedChild"`
	WebURL       string `json:"url,omitempty"`
}

var (
	callExprRe   = regexp.MustCompile(`(?:^|[^\w$.])(?:this\s*\.\s*)?([A-Za-z_$][\w$]*)\s*\.\s*([A-Za-z_$][\w$]*)\s*\(`)
	httpTargetRe = regexp.MustCompile(`^[\w\-./{}:$]{1,160}$`)
	topicRe      = regexp.MustCompile(`^[A-Za-z0-9][\w.\-]{0,159}$`)
)

// classifyReceiver — SAF: alıcı + metot → çağrı türü ("" = çağrı yeri değil).
func classifyReceiver(recv, method string) string {
	l := strings.ToLower(recv)
	hasSuf := func(ss ...string) bool {
		for _, s := range ss {
			if strings.HasSuffix(l, s) {
				return true
			}
		}
		return false
	}
	switch {
	case hasSuf("stub"):
		return "grpc"
	case hasSuf("kafkatemplate", "jmstemplate", "rabbittemplate", "jmsmessagingtemplate", "streambridge", "producer") ||
		(hasSuf("publisher") && !hasSuf("eventpublisher")):
		lm := strings.ToLower(method)
		if strings.HasPrefix(lm, "send") || strings.HasPrefix(lm, "convertandsend") || strings.HasPrefix(lm, "publish") || strings.HasPrefix(lm, "produce") {
			return "messaging"
		}
		return ""
	case hasSuf("resttemplate", "webclient", "httpclient", "restclient", "client"):
		return "http"
	case hasSuf("jdbctemplate", "entitymanager", "repository", "repo", "mapper", "dao") || l == "session" || l == "sqlsession":
		return "db"
	}
	return ""
}

// httpLiteralTarget — SAF: dize sabiti bir rota/URL ise yol kısmı.
func httpLiteralTarget(lit string) string {
	lit = strings.TrimSpace(lit)
	if i := strings.Index(lit, "://"); i >= 0 {
		rest := lit[i+3:]
		j := strings.IndexByte(rest, '/')
		if j < 0 {
			return ""
		}
		lit = rest[j:]
	}
	if i := strings.IndexAny(lit, "?#"); i >= 0 {
		lit = lit[:i]
	}
	if !strings.HasPrefix(lit, "/") || strings.Contains(lit, "${") || !httpTargetRe.MatchString(lit) {
		return ""
	}
	return lit
}

// ClassifyCallSites — SAF, tablo-testli: metot gövdesindeki çağrı yerleri.
// Satır MUTLAK (mw.BodyLine + gövde içi). Literal yalnız http (rota
// biçimli) ve messaging (topic biçimli) için okunur; db çağrısının sabiti
// (SQL metni) ASLA okunmaz.
func ClassifyCallSites(mw MethodWindow, path string) []CallSite {
	body := mw.Body
	masked := maskJava(body)
	var out []CallSite
	seen := map[string]bool{}
	for _, m := range callExprRe.FindAllStringSubmatchIndex(masked, -1) {
		recv, meth := masked[m[2]:m[3]], masked[m[4]:m[5]]
		kind := classifyReceiver(recv, meth)
		if kind == "" {
			continue
		}
		line := mw.BodyLine + strings.Count(body[:m[2]], "\n")
		callee := recv + "." + meth
		if k := callee + "|" + strconv.Itoa(line); seen[k] {
			continue
		} else {
			seen[k] = true
		}
		cs := CallSite{Path: path, Line: line, Kind: kind, Callee: callee, MatchedChild: -1}
		open := m[1] - 1
		cl := matchClose(masked, open)
		if cl < 0 {
			cl = len(masked)
		}
		lits := literalsIn(body, masked, open+1, cl)
		if kind == "http" && len(lits) == 0 {
			// Akıcı istemci (webClient.post().uri("/x")…): ifadenin geri kalanı.
			end := strings.IndexByte(masked[cl:], ';')
			if end >= 0 {
				lits = literalsIn(body, masked, cl, cl+end)
			}
		}
		for _, l := range lits {
			switch kind {
			case "http":
				if t := httpLiteralTarget(l); t != "" && cs.Target == "" {
					cs.Target = t
				}
			case "messaging":
				if topicRe.MatchString(l) && cs.Target == "" {
					cs.Target = l
				}
			}
		}
		out = append(out, cs)
	}
	return out
}

var feignAnnoRe = regexp.MustCompile(`@FeignClient\b`)

// FeignTarget — SAF, tablo-testli: tip kaynağı bir @FeignClient arayüzü ise
// istemci adı (name/value; yoksa url'nin host'u değil — "" ) ve `method`
// metodunun tam mapping yolu. ok=false: Feign istemcisi değil.
func FeignTarget(src, method string) (name, path string, ok bool) {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	masked := maskJava(src)
	loc := feignAnnoRe.FindStringIndex(masked)
	if loc == nil {
		return "", "", false
	}
	q := loc[1]
	for q < len(masked) && (masked[q] == ' ' || masked[q] == '\n' || masked[q] == '\t') {
		q++
	}
	if q < len(masked) && masked[q] == '(' {
		if c := matchClose(masked, q); c > q {
			off := q + 1
			for _, part := range splitTopLevel(masked[q+1:c], false) {
				pa, pb := off, off+len(part)
				off = pb + 1
				key := ""
				if eq := topLevelIndex(part, '='); eq >= 0 {
					key = strings.TrimSpace(part[:eq])
				}
				if key == "" || key == "name" || key == "value" {
					if ls := literalsIn(src, masked, pa, pb); len(ls) > 0 && name == "" && !strings.Contains(ls[0], "${") {
						name = strings.TrimSpace(ls[0])
					}
				}
			}
		}
	}
	prefix := ""
	for _, d := range collectMappings(src, masked) {
		if d.isClass && len(d.paths) > 0 && prefix == "" {
			prefix = d.paths[0]
		}
	}
	for _, d := range collectMappings(src, masked) {
		if d.isClass || declMethodName(masked, d.decl) != method {
			continue
		}
		p := ""
		if len(d.paths) > 0 {
			p = d.paths[0]
		}
		if full := httpLiteralTarget(joinRoute(prefix, p)); full != "" {
			path = full
		}
		break
	}
	return name, path, true
}

// ── çağrı yeri ↔ trace çocuğu ───────────────────────────────────────────

// CardChildHint — trace yarısının bir satırı (chstore.OpCallEdge'in kodla
// eşlenen alt kümesi). Yalnız ParentKey "entry" (giriş servisinin KENDİ
// çağrıları) eşlenir; derin satırlar başka servislerin kodudur.
type CardChildHint struct {
	ParentKey string
	Kind      string // service | db | messaging | external
	Protocol  string // http | grpc | db | kafka | messaging | ""
	Target    string
	Op        string
}

// childCallKind — çocuk satırın çağrı yeri türü karşılığı.
func childCallKind(c CardChildHint) string {
	switch c.Kind {
	case "db":
		return "db"
	case "messaging":
		return "messaging"
	case "service", "external":
		if c.Protocol == "grpc" {
			return "grpc"
		}
		return "http"
	}
	return ""
}

// stripSvcSuffix — servis adı karşılaştırma biçimi.
func stripSvcSuffix(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, suf := range []string{"-api", "-service", "-svc", "-server", "_api", "_service"} {
		s = strings.TrimSuffix(s, suf)
	}
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(s)
}

// childOpPath — "POST /payments/authorize" → "/payments/authorize".
func childOpPath(op string) string {
	op = strings.TrimSpace(op)
	if i := strings.IndexByte(op, ' '); i > 0 && strings.HasPrefix(op[i+1:], "/") {
		return op[i+1:]
	}
	if strings.HasPrefix(op, "/") {
		return op
	}
	return ""
}

// siteChildScore — SAF: bir çağrı yeri ile bir çocuk satırın eşleşme gücü
// (0 = yok). Literal/Feign adı kanıtı; kanıt yoksa 0 (tekillik kuralı
// MatchCallSitesToChildren'da).
func siteChildScore(s CallSite, c CardChildHint) int {
	if childCallKind(c) != s.Kind {
		return 0
	}
	score := 0
	switch s.Kind {
	case "http":
		if s.Target != "" {
			if cp := childOpPath(c.Op); cp != "" {
				score += 2 * routeMatchScore(s.Target, cp)
			}
		}
		if s.Service != "" && c.Kind == "service" && stripSvcSuffix(s.Service) == stripSvcSuffix(c.Target) {
			score++
		}
	case "messaging":
		if s.Target != "" {
			t := strings.ToLower(s.Target)
			if strings.HasSuffix(strings.ToLower(c.Target), ":"+t) || strings.HasPrefix(strings.ToLower(c.Op), t+" ") || strings.EqualFold(c.Op, s.Target) {
				score += 3
			}
		}
	case "grpc":
		meth := s.Callee[strings.LastIndexByte(s.Callee, '.')+1:]
		op := c.Op
		if i := strings.LastIndexByte(op, '/'); i >= 0 {
			op = op[i+1:]
		}
		if meth != "" && strings.EqualFold(op, meth) {
			score += 3
		}
	}
	return score
}

// MatchCallSitesToChildren — SAF, tablo-testli: çağrı yerlerini trace
// çocuklarıyla eşler. (1) Kanıtlı eşleşme: en yüksek skorlu TEK çocuk
// (eşit skorlu iki çocuk → belirsiz, -1). (2) Kanıtsız tekillik: bir türde
// tek eşlenmemiş çağrı yeri ve tek eşlenmemiş çocuk varsa eşlenir (tek db
// çocuğu + tek repository çağrısı). Çıktı dosya:satır sırasında.
func MatchCallSitesToChildren(sites []CallSite, children []CardChildHint) []CallSite {
	out := append([]CallSite(nil), sites...)
	for i := range out {
		out[i].MatchedChild = -1
	}
	direct := func(c CardChildHint) bool { return c.ParentKey == "" || c.ParentKey == "entry" }
	used := map[int]bool{}
	for i := range out {
		best, bestIdx, tie := 0, -1, false
		for j, c := range children {
			if !direct(c) {
				continue
			}
			sc := siteChildScore(out[i], c)
			if sc > best {
				best, bestIdx, tie = sc, j, false
			} else if sc == best && sc > 0 {
				tie = true
			}
		}
		if best > 0 && !tie {
			out[i].MatchedChild = bestIdx
			used[bestIdx] = true
		}
	}
	for _, kind := range []string{"http", "grpc", "db", "messaging"} {
		var si []int
		for i, s := range out {
			if s.Kind == kind && s.MatchedChild < 0 {
				si = append(si, i)
			}
		}
		var cj []int
		for j, c := range children {
			if direct(c) && !used[j] && childCallKind(c) == kind {
				cj = append(cj, j)
			}
		}
		// Tekillik: türün TÜM çağrı yerleri tek (eşleşmiş olan varsa kanıt
		// zaten kullanıldı; kalan tek yer + tek çocuk).
		if len(si) == 1 && len(cj) == 1 {
			out[si[0]].MatchedChild = cj[0]
			used[cj[0]] = true
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Path != out[b].Path {
			return out[a].Path < out[b].Path
		}
		return out[a].Line < out[b].Line
	})
	return out
}
