package devops

// method_usages.go — v0.10.1509 (plan-operation-card, "Operatör kararı
// 2026-10-23 gece" madde 2): kartın "Bu metodu kim kullanıyor?" bölümü.
// Operatör prod Azure DevOps Server'da `ref:metotAdı` aramasının çalıştığını
// doğruladı. Giriş metodunun ADIYLA koleksiyon geneli `ref:<metot> ext:java`
// araması (Feign istemcisi ya da sarmalayıcı çağıran depolarda durur, o
// yüzden depo süzgeci YOK); sonuçlar YALNIZ konum (depo, dosya, satır,
// DevOps linki) — kod satırı/parça (codeSnippet) okunur ama hiçbir alana
// KOPYALANMAZ.
//
// AD BAZLI eşleşme: `ref:` sembol adına bakar, tipe değil — aynı adlı başka
// bir metodun çağrısı da gelir. Bu yüzden her satır "olası" etiketiyle
// çizilir ve sıralama kanıtı tercih eder: trace'te bu operasyonu ÇAĞIRAN
// servislerin deposu önce, sonra dosya adı istemci/sarmalayıcı gibi duranlar
// (…Client / …Feign / …Gateway / …Proxy) ya da giriş sınıfının kökünü
// taşıyanlar, en son geri kalanı. Giriş dosyasının kendisi ve test yolları
// elenir. Çok genel adlar (get, save, handle…) hiç aranmaz: sonuç gürültü
// olurdu (dürüst "skipped" nedeni).
//
// BÜTÇE: TEK arama (≤50 isabet), süre operationDeadline; ham isabetler 10 dk
// önbellekte (anahtar: sunucu/koleksiyon/sorgu), sıralama her istekte (çağıran
// depolar önbellek dışında).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	methodUsageSearchTop = 50
	methodUsageMax       = 10
	methodUsageCacheMax  = 64
)

// genericMethodNames — `ref:` araması anlamsız gürültü döndürecek adlar.
var genericMethodNames = map[string]bool{
	"get": true, "set": true, "list": true, "find": true, "save": true, "update": true, "delete": true,
	"create": true, "handle": true, "process": true, "execute": true, "run": true, "apply": true,
	"call": true, "init": true, "test": true, "index": true, "search": true, "query": true,
	"post": true, "put": true, "patch": true, "remove": true, "add": true, "send": true, "invoke": true,
	"perform": true, "accept": true, "build": true, "convert": true, "map": true, "toString": true,
	"equals": true, "hashCode": true, "main": true, "start": true, "stop": true, "close": true,
}

// MethodUsageSkipReason — SAF: bu ad aranmalı mı? "" = evet.
func MethodUsageSkipReason(method string) string {
	switch {
	case !ValidCodeIdent(method):
		return "metot adı aranabilir biçimde değil"
	case len(method) < 4 || genericMethodNames[method] || genericMethodNames[strings.ToLower(method)]:
		return "metot adı (" + method + ") çok genel — ad bazlı arama gürültü olurdu, aranmadı"
	}
	return ""
}

// MethodUsageRequest — FetchMethodUsages girdisi.
type MethodUsageRequest struct {
	Method    string
	Class     string // giriş sınıfı (sıralama ipucu)
	EntryRepo string
	EntryPath string
	// CallerRepos — trace'te bu operasyonu çağıran servislerin depoları.
	CallerRepos []string
}

// MethodUsage — bir olası kullanım: YALNIZ konum.
type MethodUsage struct {
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	URL      string `json:"url,omitempty"`
	Caller   bool   `json:"caller,omitempty"`   // depo trace'teki bir çağıranın
	SameRepo bool   `json:"sameRepo,omitempty"` // giriş servisinin kendi deposu
}

// MethodUsages — bölümün sonucu.
type MethodUsages struct {
	Method string `json:"method"`
	// Outcome — ok | no_hits | skipped | search-off | error.
	Outcome string        `json:"outcome"`
	Reason  string        `json:"reason,omitempty"`
	Hits    []MethodUsage `json:"hits"`
	// Total — eleme sonrası isabet (Hits ≤ methodUsageMax kesilmiş olabilir).
	Total int `json:"total"`
}

// usageHit — arama isabetinin konum yarısı (parça okunmaz).
type usageHit struct {
	Project, Repo, Path, Branch string
	Line                        int
}

// parseUsageHits — SAF: yanıt → konumlar; TFVC elenir, codeSnippet ATILIR.
func parseUsageHits(raw []byte) ([]usageHit, error) {
	var out searchCodeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("kod arama yanıtı çözümlenemedi: %w", err)
	}
	var hits []usageHit
	for _, r := range out.Results {
		if strings.EqualFold(r.Repository.Type, "tfvc") || isTFVCPath(r.Path) {
			continue
		}
		h := usageHit{Project: r.Project.Name, Repo: r.Repository.Name, Path: "/" + strings.TrimPrefix(r.Path, "/")}
		if len(r.Versions) > 0 {
			h.Branch = strings.TrimPrefix(r.Versions[0].BranchName, "refs/heads/")
		}
		for _, m := range r.Matches.Content {
			if m.Line > 0 {
				h.Line = m.Line
				break
			}
		}
		hits = append(hits, h)
	}
	return hits, nil
}

// RankMethodUsages — SAF, tablo-testli: giriş dosyası + test yolları elenir;
// skor: çağıran depo +4, istemci/sarmalayıcı dosya adı ya da giriş sınıfı
// kökü +2, başka depo +1; eşitlikte depo, yol, satır.
func RankMethodUsages(hits []usageHit, req MethodUsageRequest) []MethodUsage {
	callers := map[string]bool{}
	for _, r := range req.CallerRepos {
		if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
			callers[r] = true
		}
	}
	stem := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(req.Class, "Controller"), "Resource"))
	entryRepo := strings.ToLower(strings.TrimSpace(req.EntryRepo))
	type scored struct {
		u MethodUsage
		s int
	}
	var all []scored
	seen := map[string]bool{}
	for _, h := range hits {
		lp := strings.ToLower(h.Path)
		lr := strings.ToLower(h.Repo)
		if isTestPath(lp) || (lr == entryRepo && strings.EqualFold(h.Path, "/"+strings.TrimPrefix(req.EntryPath, "/"))) {
			continue
		}
		k := lr + "|" + lp + "|" + fmt.Sprint(h.Line)
		if seen[k] {
			continue
		}
		seen[k] = true
		u := MethodUsage{Repo: h.Repo, Path: h.Path, Line: h.Line, Caller: callers[lr], SameRepo: lr == entryRepo}
		sc := 0
		if u.Caller {
			sc += 4
		}
		base := lp[strings.LastIndexByte(lp, '/')+1:]
		for _, w := range []string{"client", "feign", "gateway", "proxy"} {
			if strings.Contains(base, w) {
				sc += 2
				break
			}
		}
		if stem != "" && len(stem) >= 3 && strings.Contains(base, stem) && !strings.Contains(base, "client") {
			sc += 2
		}
		if !u.SameRepo {
			sc++
		}
		all = append(all, scored{u, sc})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].s != all[j].s {
			return all[i].s > all[j].s
		}
		if all[i].u.Repo != all[j].u.Repo {
			return all[i].u.Repo < all[j].u.Repo
		}
		if all[i].u.Path != all[j].u.Path {
			return all[i].u.Path < all[j].u.Path
		}
		return all[i].u.Line < all[j].u.Line
	})
	out := make([]MethodUsage, 0, len(all))
	for _, a := range all {
		out = append(out, a.u)
	}
	return out
}

// ── önbellek (ham isabetler) ────────────────────────────────────────────

type usageCacheEntry struct {
	hits []usageHit
	at   time.Time
}

type usageCacheT struct {
	mu sync.Mutex
	m  map[string]usageCacheEntry
}

var usageCaches sync.Map // *Service → *usageCacheT

func (s *Service) usageCache() *usageCacheT {
	v, _ := usageCaches.LoadOrStore(s, &usageCacheT{})
	return v.(*usageCacheT)
}

// methodUsageCacheKey — SAF: sunucu + koleksiyon + sorgu metni (hepsi).
func methodUsageCacheKey(cfg Settings, query string) string {
	return fmt.Sprintf("usage:v1|%q|%q|%q", cfg.BaseURL, cfg.Collection, query)
}

func (c *usageCacheT) get(k string) ([]usageHit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Since(e.at) > opCardCacheTTL {
		return nil, false
	}
	return append([]usageHit(nil), e.hits...), true
}

func (c *usageCacheT) put(k string, hits []usageHit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]usageCacheEntry{}
	}
	if len(c.m) >= methodUsageCacheMax {
		oldK, oldAt := "", time.Now()
		for kk, e := range c.m {
			if e.at.Before(oldAt) {
				oldK, oldAt = kk, e.at
			}
		}
		delete(c.m, oldK)
	}
	c.m[k] = usageCacheEntry{hits: append([]usageHit(nil), hits...), at: time.Now()}
}

// searchCollectionLocations — koleksiyon geneli Code Search (depo süzgeci
// yok), yalnız konumlar.
func (s *Service) searchCollectionLocations(ctx context.Context, cfg Settings, text string, top int) ([]usageHit, error) {
	cli := s.clientFor(cfg.InsecureSkipVerify)
	body, err := json.Marshal(searchCodeBody{SearchText: text, Top: top})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL(cfg), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coremetry-devops/1.0")
	if cfg.PAT != "" || cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.PAT)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, fileBodyCap))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kod arama http %d: %s", resp.StatusCode, firstLine(string(raw)))
	}
	return parseUsageHits(raw)
}

// FetchMethodUsages — "Bu metodu kim kullanıyor?" (bkz. dosya başı).
// FAIL-OPEN: hata dönmez; Outcome + Reason.
func (s *Service) FetchMethodUsages(ctx context.Context, req MethodUsageRequest) MethodUsages {
	out := MethodUsages{Method: req.Method, Hits: []MethodUsage{}}
	if s == nil || !s.Configured() {
		out.Outcome, out.Reason = "error", "kod entegrasyonu yapılandırılmamış"
		return out
	}
	if !s.CodeSearchEnabled() {
		out.Outcome, out.Reason = "search-off", "kullanım araması için Code Search gerekli (Ayarlar → Kod entegrasyonu)"
		return out
	}
	if why := MethodUsageSkipReason(req.Method); why != "" {
		out.Outcome, out.Reason = "skipped", why
		return out
	}
	q, err := BuildCodeQuery([]CodeTerm{{Kind: CodeQueryRef, Value: req.Method}}, "java")
	if err != nil {
		out.Outcome, out.Reason = "skipped", "metot adı aranabilir biçimde değil"
		return out
	}
	cfg := s.CurrentSettings()
	key := methodUsageCacheKey(cfg, q)
	hits, ok := s.usageCache().get(key)
	if !ok {
		parent := ctx
		c, cancel := context.WithTimeout(ctx, s.operationDeadline())
		defer cancel()
		hits, err = s.searchCollectionLocations(c, cfg, q, methodUsageSearchTop)
		if err != nil {
			out.Outcome = "error"
			out.Reason = "kullanım araması başarısız: " + sanitize(firstLine(err.Error()), cfg)
			if deadlineHit(parent, c) {
				out.Reason = deadlineReason(s.operationDeadline())
			}
			return out
		}
		s.usageCache().put(key, hits)
	}
	ranked := RankMethodUsages(hits, req)
	out.Total = len(ranked)
	if len(ranked) > methodUsageMax {
		ranked = ranked[:methodUsageMax]
	}
	branchOf := map[string]usageHit{}
	for _, h := range hits {
		branchOf[strings.ToLower(h.Repo)+"|"+strings.ToLower(h.Path)] = h
	}
	for i := range ranked {
		h := branchOf[strings.ToLower(ranked[i].Repo)+"|"+strings.ToLower(ranked[i].Path)]
		ranked[i].URL = FileURL(cfg, h.Project, ranked[i].Repo, h.Branch, ranked[i].Path, ranked[i].Line)
	}
	out.Hits = ranked
	if len(ranked) == 0 {
		out.Outcome, out.Reason = "no_hits", "Code Search bu adla başka bir kullanım bulmadı"
		return out
	}
	out.Outcome = "ok"
	return out
}
