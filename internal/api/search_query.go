package api

// search_query.go — v0.10.1333 (birleşik arama, GET /api/search): SAF
// çekirdek. Parametre ayrıştırma, sorgu sınıflandırma, cache anahtarı,
// kaynak planları (trace/log daraltması), sıralama ve facet sayımı burada;
// I/O yok. Testler: search_query_test / search_key_test / search_rank_test /
// search_facets_test / search_budget_test.
//
// Neden ayrı dosya: tdd skill'inin "önce dikiş" kuralı — her karar (trace
// ne zaman koşar, log ne zaman atlanır, hangi satır önce gelir) tablo-testli
// bir fonksiyondur; handler yalnız bunları bağlar.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	searchMinQueryLen = 2
	// searchSourceCap — kaynak başına satır tavanı (≤20); cevap ≤ 6×20 = 120.
	searchSourceCap     = 20
	searchDefaultWindow = time.Hour
	// searchMaxWindow — genel pencere tavanı; kaynaklar ayrıca kendi
	// tavanlarını uygular (trace metni 1 sa, trace id/kimlik ve log 24 sa).
	searchMaxWindow          = 7 * 24 * time.Hour
	searchTraceTextWindowCap = time.Hour
	searchTraceIDWindowCap   = 24 * time.Hour
	searchLogWindowCap       = 24 * time.Hour
	searchLogSoftTimeout     = 3 * time.Second
	searchCacheTTL           = 15 * time.Second
	// searchCeiling — bütün isteğin tavanı; her kaynak bütçesi bunun altında.
	searchCeiling = 6 * time.Second
	// searchScopeShown — service/env/sev facet'leri DÖNEN satırlar üstünde
	// sayılır; istemci bunu "gösterilenler içinde" diye yazar.
	searchScopeShown = "within shown results"
)

// searchTypeOrder — kanonik tür sırası: fan-out, anahtar, eşit skorda
// sıralama (paletin servis → endpoint önceliği, v0.10.126) hepsi bunu okur.
var searchTypeOrder = []string{"service", "endpoint", "problem", "exception", "trace", "log"}

func searchTypeRank(t string) int {
	for i, x := range searchTypeOrder {
		if x == t {
			return i
		}
	}
	return len(searchTypeOrder)
}

type searchQueryKind string

const (
	queryTraceID  searchQueryKind = "trace_id"
	queryIdentity searchQueryKind = "identity"
	queryText     searchQueryKind = "text"
)

type searchParams struct {
	Q       string
	Kind    searchQueryKind
	Types   []string // kanonik sırada, tekrarsız
	Service string
	Env     string
	Sev     string // "" | critical | warning | info — TABAN
	Status  string // open | all (problem kaynağı)
	Sort    string // relevance | newest | oldest | severity
	From    time.Time
	To      time.Time
	Limit   int // kaynak başına, ≤ searchSourceCap
}

func (p searchParams) wants(t string) bool {
	for _, x := range p.Types {
		if x == t {
			return true
		}
	}
	return false
}

var (
	searchSevValues    = map[string]bool{"critical": true, "warning": true, "info": true}
	searchStatusValues = map[string]bool{"open": true, "all": true}
	searchSortValues   = map[string]bool{"relevance": true, "newest": true, "oldest": true, "severity": true}
)

// parseSearchParams — SAF (now dışarıdan). Kapalı sözlüklerde bilinmeyen
// değer 400: sessizce düşürmek operatörün sorusunu başka bir soruya çevirir.
func parseSearchParams(q url.Values, now time.Time) (searchParams, error) {
	p := searchParams{
		Q:       strings.TrimSpace(q.Get("q")),
		Service: strings.TrimSpace(q.Get("service")),
		Env:     strings.TrimSpace(q.Get("env")),
		Sev:     strings.ToLower(strings.TrimSpace(q.Get("sev"))),
		Status:  strings.ToLower(strings.TrimSpace(q.Get("status"))),
		Sort:    strings.ToLower(strings.TrimSpace(q.Get("sort"))),
	}
	if len([]rune(p.Q)) < searchMinQueryLen {
		return p, fmt.Errorf("q must be at least %d characters", searchMinQueryLen)
	}
	p.Kind = classifyQuery(p.Q)
	if p.Kind == queryTraceID {
		p.Q = strings.ToLower(p.Q) // trace id küçük harf; kimlik HAM kalır (v0.10.674)
	}

	types, err := parseSearchTypes(q.Get("types"))
	if err != nil {
		return p, err
	}
	p.Types = types

	if p.Sev != "" && !searchSevValues[p.Sev] {
		return p, fmt.Errorf("sev must be one of critical, warning, info")
	}
	if p.Status == "" {
		p.Status = "open"
	}
	if !searchStatusValues[p.Status] {
		return p, fmt.Errorf("status must be open or all")
	}
	if p.Sort == "" {
		p.Sort = "relevance"
	}
	if !searchSortValues[p.Sort] {
		return p, fmt.Errorf("sort must be one of relevance, newest, oldest, severity")
	}

	p.Limit = parseInt(q.Get("limit"), searchSourceCap)
	if p.Limit <= 0 || p.Limit > searchSourceCap {
		p.Limit = searchSourceCap
	}

	from, to := parseTime(q.Get("from")), parseTime(q.Get("to"))
	if to.IsZero() {
		to = now
	}
	if from.IsZero() || !to.After(from) {
		from = to.Add(-searchDefaultWindow)
	}
	from, to, _ = capWindow(from, to, searchMaxWindow)
	// Canlı pencere TTL basamağına (15 sn) oturur, uzunluk korunur; mutlak
	// pencere aynen (snapLogsWindow emsali, v0.10.446). Hem anahtar hem
	// sorgu bu pencereyle koşar.
	if a, b, ok := snapLogsKeyWindow(from, to, now, searchCacheTTL); ok {
		from, to = a, b
	}
	p.From, p.To = from, to
	return p, nil
}

func parseSearchTypes(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string{}, searchTypeOrder...), nil
	}
	want := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		if searchTypeRank(t) == len(searchTypeOrder) {
			return nil, fmt.Errorf("unknown type %q (allowed: %s)", t, strings.Join(searchTypeOrder, ","))
		}
		want[t] = true
	}
	if len(want) == 0 {
		return append([]string{}, searchTypeOrder...), nil
	}
	out := make([]string, 0, len(want))
	for _, t := range searchTypeOrder {
		if want[t] {
			out = append(out, t)
		}
	}
	return out, nil
}

// classifyQuery — 32-hex → trace id (isTraceIDHex); paletin IDENTITY_RE'si
// (lib/paletteIdentity.ts: tek parça, ≥8, [A-Za-z0-9._:-], en az bir rakam)
// → kimlik (function_id gibi; GetTraces'in kimlik-önce yolu); gerisi metin.
func classifyQuery(q string) searchQueryKind {
	t := strings.TrimSpace(q)
	if isTraceIDHex(strings.ToLower(t)) {
		return queryTraceID
	}
	if len(t) >= 8 {
		digit, ok := false, true
		for _, r := range t {
			switch {
			case r >= '0' && r <= '9':
				digit = true
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '_', r == ':', r == '-':
			default:
				ok = false
			}
		}
		if ok && digit {
			return queryIdentity
		}
	}
	return queryText
}

// capWindow — sağ ucu koruyarak pencereyi max'a kırpar.
func capWindow(from, to time.Time, max time.Duration) (time.Time, time.Time, bool) {
	if to.Sub(from) > max {
		return to.Add(-max), to, true
	}
	return from, to, false
}

// searchCacheKey — SAF; cache_key_test kanonu (v0.5.187): serbest metinler
// fnvStr ile AYRI AYRI özetlenir (ayraç birleşmesi yok), types KÜME olarak
// sıralı FNV (excludeKeyDigest), pencere snap'lenmiş ns. Logs arka ucu
// anahtarda: Settings → Elasticsearch geçişi CH şeklini 15 sn servis etmesin.
func searchCacheKey(p searchParams, logsBackend string) string {
	set := make(map[string]bool, len(p.Types))
	for _, t := range p.Types {
		set[t] = true
	}
	return fmt.Sprintf("search:v1:%s:types=%s:lim=%d:w=%d_%d",
		fnvStr(p.Q, string(p.Kind), p.Service, p.Env, p.Sev, p.Status, p.Sort, logsBackend),
		excludeKeyDigest(set), p.Limit, p.From.UnixNano(), p.To.UnixNano())
}

// sourceBudgets — kaynak başına context.WithTimeout süresi.
func sourceBudgets() map[string]time.Duration {
	return map[string]time.Duration{
		"service":   2 * time.Second,
		"endpoint":  2 * time.Second,
		"problem":   3 * time.Second,
		"exception": 3 * time.Second,
		"trace":     5 * time.Second,
		"log":       5 * time.Second,
	}
}

// ── Kaynak planları ─────────────────────────────────────────────────────

type tracePlan struct {
	Skip   bool
	Reason string
	Capped bool // pencere kaynağın tavanına kırpıldı
	Filter chstore.TraceFilter
}

// planTraceSearch — ZORUNLU daraltma: span'ler üstünde daraltmasız tam metin
// ASLA. 32-hex → TraceID tam eşitlik; kimlik → Search ham (kimlik-önce yolu,
// indeksli eşitlik); serbest metin yalnız servis facet'i ya da servis
// kaynağının TEK adı (uniqueService) varken, pencere ≤1 sa.
func planTraceSearch(p searchParams, uniqueService string) tracePlan {
	f := chstore.TraceFilter{
		Service:   p.Service,
		Env:       p.Env,
		Limit:     p.Limit,
		CountMode: "skip",
	}
	var capped bool
	switch p.Kind {
	case queryTraceID:
		f.TraceID = p.Q
		f.From, f.To, capped = capWindow(p.From, p.To, searchTraceIDWindowCap)
	case queryIdentity:
		f.Search = p.Q
		f.From, f.To, capped = capWindow(p.From, p.To, searchTraceIDWindowCap)
	default:
		if f.Service == "" {
			f.Service = uniqueService
		}
		if f.Service == "" {
			return tracePlan{Skip: true, Reason: "needs service or trace id"}
		}
		f.Search = p.Q
		f.From, f.To, capped = capWindow(p.From, p.To, searchTraceTextWindowCap)
	}
	return tracePlan{Filter: f, Capped: capped}
}

type logPlan struct {
	Skip        bool
	Unavailable bool // arka uç yok — 'unavailable', 'skipped' değil
	Reason      string
	Capped      bool
	Filter      logstore.Filter
}

// planLogSearch — backend s.logs.Backend()'den gelir (Settings → ES ise
// "elasticsearch"); env'den ÇIKARILMAZ. CH arka ucunda ayrışmayan arama
// metni atlanır (logQuerySyntaxReject — /api/logs'un 400'ünün saf kararı).
func planLogSearch(p searchParams, backend string) logPlan {
	if backend == "" {
		return logPlan{Skip: true, Unavailable: true, Reason: "logs backend not configured"}
	}
	if _, bad := logQuerySyntaxReject(backend, p.Q); bad {
		return logPlan{Skip: true, Reason: "unsupported syntax"}
	}
	from, to, capped := capWindow(p.From, p.To, searchLogWindowCap)
	return logPlan{Capped: capped, Filter: logstore.Filter{
		Search:      p.Q,
		Service:     p.Service,
		Env:         p.Env,
		From:        from,
		To:          to,
		SeverityMin: logSeverityFloor(p.Sev),
		Limit:       p.Limit,
		SoftTimeout: searchLogSoftTimeout,
	}}
}

// ── Seviye merdiveni (sev TABANDIR: critical ⊂ warning ⊂ info) ──────────

func levelRank(l string) int {
	switch l {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

// logSeverityFloor — OTel SeverityNumber tabanı: critical → ERROR (17),
// warning → WARN (13), info → INFO (9).
func logSeverityFloor(sev string) uint8 {
	switch sev {
	case "critical":
		return 17
	case "warning":
		return 13
	case "info":
		return 9
	}
	return 0
}

func logLevel(num uint8) string {
	switch {
	case num >= 17:
		return "critical"
	case num >= 13:
		return "warning"
	case num >= 9:
		return "info"
	}
	return ""
}

func exceptionLevel(priority string) string {
	switch priority {
	case "P1":
		return "critical"
	case "P2":
		return "warning"
	case "P3":
		return "info"
	}
	return ""
}

// ── Sonuç şekli ─────────────────────────────────────────────────────────

// SearchResult — bir arama satırı. Sunucu DÜZ METİN döner; vurgulama
// istemcide. Payload tür başına hafif alt küme.
type SearchResult struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Service  string `json:"service"`
	Env      string `json:"env,omitempty"`
	Severity string `json:"severity,omitempty"`
	Ts       int64  `json:"ts,omitempty"` // unix ns
	Href     string `json:"href"`
	Payload  any    `json:"payload,omitempty"`

	// sıralama/facet girdileri — tele girmez
	matchFields []string // başlık dışı eşleşme alanları ("içerir" tavanı)
	level       string   // critical | warning | info | ""
	sevNum      int      // log SeverityNumber (aynı seviyede ince sıra)
}

type sourceStatus struct {
	Status    string `json:"status"` // ok | unavailable | skipped | timeout
	Ms        int64  `json:"ms"`
	Reason    string `json:"reason,omitempty"`
	Truncated bool   `json:"truncated"`
}

type typeFacet struct {
	Count     int  `json:"count"`
	Truncated bool `json:"truncated"`
}

type searchFacets struct {
	Type    map[string]typeFacet `json:"type"`
	Service map[string]int       `json:"service"`
	Env     map[string]int       `json:"env"`
	Sev     map[string]int       `json:"sev"`
	Scope   string               `json:"scope"`
}

type searchResponse struct {
	Results   []SearchResult          `json:"results"`
	Facets    searchFacets            `json:"facets"`
	Sources   map[string]sourceStatus `json:"sources"`
	Total     int                     `json:"total"`
	QueryKind searchQueryKind         `json:"queryKind"`
	FromNs    int64                   `json:"fromNs"`
	ToNs      int64                   `json:"toNs"`
}

// sourceResult — bir kaynağın çıktısı. Count okuyucunun toplamı (gösterilen
// satırdan büyük olabilir); storeErr = zaman-aşımı-DIŞI store hatası →
// cevap önbelleğe yazılmaz.
type sourceResult struct {
	Rows      []SearchResult
	Count     int
	Truncated bool
	Status    sourceStatus
	storeErr  bool
	err       error // yalnız pod loguna (viewer'a ham hata sızmaz)
}

// ── Sıralama ────────────────────────────────────────────────────────────

// matchTier — frontend lib/paletteScore.ts basamakları (harf duyarsız):
// tam 1000 > önek 500 > içerir 200.
func matchTier(q, s string) int {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return 0
	}
	l := strings.ToLower(s)
	switch {
	case l == q:
		return 1000
	case strings.HasPrefix(l, q):
		return 500
	case strings.Contains(l, q):
		return 200
	}
	return 0
}

func searchScore(r SearchResult, q string) int {
	s := matchTier(q, r.Title)
	for _, f := range r.matchFields {
		t := matchTier(q, f)
		if t > 200 {
			t = 200 // başlık dışı alan en fazla "içerir"
		}
		if t > s {
			s = t
		}
	}
	return s
}

// rankSearchResults — SAF, kararlı. relevance: skor → tür sırası → yenilik
// (ts'siz sonda). newest/oldest: ts (ts'siz sonda, relevance sırası korunur).
// severity: seviye → log numarası → relevance.
func rankSearchResults(rows []SearchResult, q, sortMode string) []SearchResult {
	out := append([]SearchResult{}, rows...)
	sc := make([]int, len(out))
	idx := make([]int, len(out))
	for i := range out {
		sc[i] = searchScore(out[i], q)
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := idx[a], idx[b]
		if sc[x] != sc[y] {
			return sc[x] > sc[y]
		}
		tx, ty := searchTypeRank(out[x].Type), searchTypeRank(out[y].Type)
		if tx != ty {
			return tx < ty
		}
		return newerFirst(out[x].Ts, out[y].Ts)
	})
	ranked := make([]SearchResult, len(out))
	for i, j := range idx {
		ranked[i] = out[j]
	}
	switch sortMode {
	case "newest", "oldest":
		asc := sortMode == "oldest"
		sort.SliceStable(ranked, func(a, b int) bool {
			ta, tb := ranked[a].Ts, ranked[b].Ts
			if (ta > 0) != (tb > 0) {
				return ta > 0
			}
			if ta == 0 || ta == tb {
				return false
			}
			if asc {
				return ta < tb
			}
			return ta > tb
		})
	case "severity":
		sort.SliceStable(ranked, func(a, b int) bool {
			la, lb := levelRank(ranked[a].level), levelRank(ranked[b].level)
			if la != lb {
				return la > lb
			}
			return ranked[a].sevNum > ranked[b].sevNum
		})
	}
	return ranked
}

func newerFirst(a, b int64) bool {
	if (a > 0) != (b > 0) {
		return a > 0
	}
	return a > b
}

// ── Facet'ler ───────────────────────────────────────────────────────────

type facetSelection struct {
	Service, Env, Sev string
}

// facetPasses — satır, `exclude` DIŞINDAKİ aktif facet'lerden geçiyor mu.
// Boyutu olmayan satır (servis satırının env'i yok) o facet'e takılmaz:
// bilinmeyen ≠ uyuşmayan.
func facetPasses(r SearchResult, sel facetSelection, exclude string) bool {
	if exclude != "service" && sel.Service != "" && r.Service != "" && r.Service != sel.Service {
		return false
	}
	if exclude != "env" && sel.Env != "" && r.Env != "" && r.Env != sel.Env {
		return false
	}
	if exclude != "sev" && sel.Sev != "" && r.level != "" && levelRank(r.level) < levelRank(sel.Sev) {
		return false
	}
	return true
}

// facetCounts — SAF: service/env/sev, dönen satırlar üstünde; her facet sorgu
// + DİĞER aktif facet'lere göre (kendi seçimi hariç).
func facetCounts(rows []SearchResult, sel facetSelection) searchFacets {
	f := searchFacets{
		Type:    map[string]typeFacet{},
		Service: map[string]int{},
		Env:     map[string]int{},
		Sev:     map[string]int{},
		Scope:   searchScopeShown,
	}
	for _, r := range rows {
		if r.Service != "" && facetPasses(r, sel, "service") {
			f.Service[r.Service]++
		}
		if r.Env != "" && facetPasses(r, sel, "env") {
			f.Env[r.Env]++
		}
		if r.level != "" && facetPasses(r, sel, "sev") {
			f.Sev[r.level]++
		}
	}
	return f
}

// assembleSearchResponse — SAF: kaynak çıktıları → cevap. Kaynak başına
// ≤ p.Limit satır; type facet okuyucunun toplamından (count + truncated);
// total = tür sayımlarının toplamı. cacheable=false yalnız zaman-aşımı-DIŞI
// store hatasında (kısmi zaman-aşımı cevabı TTL boyunca saklanır — boğulan
// arka uç her tuş vuruşunda yeniden dövülmesin).
func assembleSearchResponse(p searchParams, res map[string]sourceResult) (searchResponse, bool) {
	resp := searchResponse{
		Results:   []SearchResult{},
		Sources:   map[string]sourceStatus{},
		QueryKind: p.Kind,
		FromNs:    p.From.UnixNano(),
		ToNs:      p.To.UnixNano(),
	}
	cacheable := true
	types := map[string]typeFacet{}
	var rows []SearchResult
	for _, t := range p.Types {
		r, ok := res[t]
		if !ok {
			continue
		}
		shown := r.Rows
		if len(shown) > p.Limit {
			shown = shown[:p.Limit]
		}
		count := r.Count
		if count < len(r.Rows) {
			count = len(r.Rows)
		}
		trunc := r.Truncated || count > len(shown)
		types[t] = typeFacet{Count: count, Truncated: trunc}
		st := r.Status
		if st.Status == "" {
			st.Status = "ok"
		}
		st.Truncated = trunc
		resp.Sources[t] = st
		resp.Total += count
		rows = append(rows, shown...)
		if r.storeErr {
			cacheable = false
		}
	}
	resp.Facets = facetCounts(rows, facetSelection{Service: p.Service, Env: p.Env, Sev: p.Sev})
	resp.Facets.Type = types
	if ranked := rankSearchResults(rows, p.Q, p.Sort); len(ranked) > 0 {
		resp.Results = ranked
	}
	return resp, cacheable
}

// ── Satır kurucular (saf) ───────────────────────────────────────────────

// isSafeSearchHref — uygulama içi yol: "/" ile başlar, "//" ya da "/\" ile
// değil (protokole göreli dış adres), şema taşımaz.
func isSafeSearchHref(h string) bool {
	if !strings.HasPrefix(h, "/") || strings.HasPrefix(h, "//") || strings.HasPrefix(h, `/\`) {
		return false
	}
	return !strings.ContainsAny(h, " \t\n\r")
}

func appHref(path string, v url.Values) string {
	if len(v) == 0 {
		return path
	}
	return path + "?" + v.Encode()
}

func searchTrunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func serviceResults(names []string, env string) []SearchResult {
	out := make([]SearchResult, 0, len(names))
	for _, n := range names {
		v := url.Values{"name": {n}}
		if env != "" {
			v.Set("env", env)
		}
		out = append(out, SearchResult{
			Type: "service", ID: n, Title: n, Subtitle: "Service", Service: n,
			Href: appHref("/service", v),
		})
	}
	return out
}

type searchFilterExpr struct {
	K  string   `json:"k"`
	Op string   `json:"op"`
	V  []string `json:"v"`
}

// endpointResults — href operationTracesHref'in (lib/pivotHref.ts) aynası:
// kesin span-adı süzgeci, rootOnly=false (operasyon kök olmak zorunda değil),
// liste görünümü; pencereyi istemci taşır.
func endpointResults(hits []chstore.OperationHit) []SearchResult {
	out := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		fj, _ := json.Marshal([]searchFilterExpr{{K: "name", Op: "=", V: []string{h.Name}}})
		v := url.Values{"filters": {string(fj)}, "rootOnly": {"false"}, "view": {"list"}}
		if h.Service != "" {
			v.Set("service", h.Service)
		}
		out = append(out, SearchResult{
			Type: "endpoint", ID: h.Service + "\x1f" + h.Name, Title: h.Name, Subtitle: h.Service,
			Service: h.Service, Href: appHref("/traces", v), matchFields: []string{h.Service},
			Payload: h,
		})
	}
	return out
}

type searchProblemLite struct {
	ID          string `json:"id"`
	DisplayID   string `json:"displayId"`
	RuleName    string `json:"ruleName"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	Service     string `json:"service"`
	Kind        string `json:"kind,omitempty"`
	Description string `json:"description"`
	StartedAt   int64  `json:"startedAt"`
}

// problemMatchesQuery — SAF: harf duyarsız alt dize; RuleName / Service /
// Description / görüntü kimliği (P-xxxxx).
func problemMatchesQuery(p chstore.Problem, q string) bool {
	lq := strings.ToLower(strings.TrimSpace(q))
	if lq == "" {
		return false
	}
	disp := p.DisplayID
	if disp == "" {
		disp = chstore.ProblemDisplayID(p.ID)
	}
	for _, f := range []string{p.RuleName, p.Service, p.Description, disp} {
		if strings.Contains(strings.ToLower(f), lq) {
			return true
		}
	}
	return false
}

func problemResults(rows []chstore.Problem) []SearchResult {
	out := make([]SearchResult, 0, len(rows))
	for _, p := range rows {
		disp := p.DisplayID
		if disp == "" {
			disp = chstore.ProblemDisplayID(p.ID)
		}
		sub := strings.TrimSpace(strings.Join(nonEmpty(disp, p.Service, p.Status), " · "))
		out = append(out, SearchResult{
			Type: "problem", ID: p.ID, Title: p.RuleName, Subtitle: sub, Service: p.Service,
			Severity: p.Severity, Ts: p.StartedAt,
			Href:        appHref("/problems", url.Values{"problem": {p.ID}}),
			matchFields: []string{disp, p.Service, p.Description},
			level:       sevLevelOf(p.Severity),
			Payload: searchProblemLite{
				ID: p.ID, DisplayID: disp, RuleName: p.RuleName, Severity: p.Severity, Status: p.Status,
				Service: p.Service, Kind: p.Kind, Description: searchTrunc(p.Description, 500), StartedAt: p.StartedAt,
			},
		})
	}
	return out
}

func sevLevelOf(s string) string {
	s = strings.ToLower(s)
	if levelRank(s) > 0 {
		return s
	}
	return ""
}

type searchExceptionLite struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	Message     string `json:"message"`
	Service     string `json:"service"`
	State       string `json:"state"`
	FirstSeen   int64  `json:"firstSeen"`
	LastSeen    int64  `json:"lastSeen"`
	Occurrences uint64 `json:"occurrences"`
	Priority    string `json:"priority,omitempty"`
}

// exceptionResults — Priority alanı çağıran tarafından (exceptionPriority)
// doldurulmuş olmalı; boşsa seviye yok.
func exceptionResults(rows []chstore.ExceptionGroup) []SearchResult {
	out := make([]SearchResult, 0, len(rows))
	for _, g := range rows {
		title := g.Type
		if g.Message != "" {
			if title != "" {
				title += ": "
			}
			title += searchTrunc(firstLine(g.Message), 200)
		}
		out = append(out, SearchResult{
			Type: "exception", ID: g.Fingerprint, Title: title,
			Subtitle: strings.Join(nonEmpty(g.Service, g.State, fmt.Sprintf("%d occurrences", g.Occurrences)), " · "),
			Service:  g.Service, Severity: g.Priority, Ts: g.LastSeen,
			Href:        appHref("/problems", url.Values{"exc": {g.Fingerprint}}),
			matchFields: []string{g.Type, g.Message, g.Service},
			level:       exceptionLevel(g.Priority),
			Payload: searchExceptionLite{
				Fingerprint: g.Fingerprint, Type: g.Type, Message: searchTrunc(g.Message, 500), Service: g.Service,
				State: g.State, FirstSeen: g.FirstSeen, LastSeen: g.LastSeen, Occurrences: g.Occurrences, Priority: g.Priority,
			},
		})
	}
	return out
}

type searchTraceLite struct {
	TraceID     string  `json:"traceId"`
	RootName    string  `json:"rootName"`
	RootRoute   string  `json:"rootRoute,omitempty"`
	ServiceName string  `json:"serviceName"`
	StartTime   int64   `json:"startTime"`
	DurationMs  float64 `json:"durationMs"`
	SpanCount   uint64  `json:"spanCount"`
	HasError    bool    `json:"hasError"`
	ErrorSpans  uint64  `json:"errorSpans,omitempty"`
}

// traceResults — env: sorgu env ile daraltıldıysa satıra damgalanır (satırın
// kendi env kolonu yok).
func traceResults(rows []chstore.TraceRow, env string) []SearchResult {
	out := make([]SearchResult, 0, len(rows))
	for _, t := range rows {
		title := t.RootName
		if t.RootRoute != "" && !strings.Contains(title, t.RootRoute) {
			title = strings.TrimSpace(title + " " + t.RootRoute)
		}
		if title == "" {
			title = t.TraceID
		}
		sub := fmt.Sprintf("%s · %.0f ms · %d spans", t.ServiceName, t.DurationMs, t.SpanCount)
		if t.HasError {
			sub += " · error"
		}
		out = append(out, SearchResult{
			Type: "trace", ID: t.TraceID, Title: title, Subtitle: sub, Service: t.ServiceName, Env: env,
			Ts: t.StartTime, Href: appHref("/trace", url.Values{"id": {t.TraceID}}),
			matchFields: []string{t.TraceID, t.ServiceName},
			Payload: searchTraceLite{
				TraceID: t.TraceID, RootName: t.RootName, RootRoute: t.RootRoute, ServiceName: t.ServiceName,
				StartTime: t.StartTime, DurationMs: t.DurationMs, SpanCount: t.SpanCount, HasError: t.HasError, ErrorSpans: t.ErrorSpans,
			},
		})
	}
	return out
}

type searchLogLite struct {
	Timestamp    int64  `json:"timestamp"`
	Severity     uint8  `json:"severity"`
	SeverityText string `json:"severityText"`
	Body         string `json:"body"`
	ServiceName  string `json:"serviceName"`
	TraceID      string `json:"traceId,omitempty"`
	SpanID       string `json:"spanId,omitempty"`
}

func logEnvOf(rec *logstore.LogRecord) string {
	for _, k := range []string{"deployment.environment.name", "deployment.environment"} {
		if v := rec.ResourceAttributes[k]; v != "" {
			return v
		}
	}
	return ""
}

// logResults — href /logs'a sorguyla (+ servis) iner; tek kaydın detay
// rotası yok. envFallback: env süzgeci UYGULANDIYSA kaydın kendi env
// özniteliği yoksa seçili env damgalanır.
func logResults(recs []*logstore.LogRecord, q, envFallback string) []SearchResult {
	out := make([]SearchResult, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		env := logEnvOf(rec)
		if env == "" {
			env = envFallback
		}
		id := fmt.Sprintf("%d", rec.ID)
		if rec.ID == 0 {
			id = fnvStr(fmt.Sprintf("%d", rec.Timestamp), rec.ServiceName, rec.Body)
		}
		v := url.Values{"q": {q}}
		if rec.ServiceName != "" {
			v.Set("service", rec.ServiceName)
		}
		sev := rec.SeverityText
		out = append(out, SearchResult{
			Type: "log", ID: id, Title: searchTrunc(firstLine(rec.Body), 200),
			Subtitle: strings.Join(nonEmpty(sev, rec.ServiceName), " · "),
			Service:  rec.ServiceName, Env: env, Severity: sev, Ts: rec.Timestamp,
			Href:        appHref("/logs", v),
			matchFields: []string{rec.Body, rec.ServiceName},
			level:       logLevel(rec.Severity),
			sevNum:      int(rec.Severity),
			Payload: searchLogLite{
				Timestamp: rec.Timestamp, Severity: rec.Severity, SeverityText: rec.SeverityText,
				Body: searchTrunc(rec.Body, 1000), ServiceName: rec.ServiceName, TraceID: rec.TraceID, SpanID: rec.SpanID,
			},
		})
	}
	return out
}

func nonEmpty(parts ...string) []string {
	out := parts[:0:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}
