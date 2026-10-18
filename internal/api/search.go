package api

// search.go — v0.10.1333 (birleşik arama backend'i; operatör onaylı spec,
// varsayılanlar seçili). Ön yüz bu sürümde YOK.
//
//   GET /api/search?q=&types=service,endpoint,problem,exception,trace,log
//       &service=&env=&sev=critical|warning|info&status=open|all
//       &from=&to=&sort=relevance|newest|oldest|severity&limit=20
//   → {results, facets:{type,service,env,sev,scope}, sources, total,
//      queryKind, fromNs, toNs}
//
// NEDEN tek uç: ⌘K paleti bugün servis + endpoint için iki ayrı istek atıyor,
// problem/exception/trace/log hiç aranmıyor. Datadog/Dynatrace'in global
// araması tek istekte tür başına küçük bir dilim + tür facet'i döner; burada
// da her kaynak mevcut, viewer-okunur bir okuyucudur ve ≤20 satır.
//
// NEDEN öteki türlü DEĞİL:
//   - errgroup DEĞİL, WaitGroup + kaynak başına context.WithTimeout: bir
//     kaynağın hatası kardeşlerini iptal etmemeli; yavaş ES servis
//     sonuçlarını götürmesin. Bütün istek 6 sn tavanlı; ctx'i yok sayan bir
//     kaynak bile cevabı bekletmez ('timeout').
//   - Span'ler üstünde daraltmasız tam metin ASLA (1B span/gün): trace kaynağı
//     yalnız 32-hex trace id, kimlik değeri ya da servis (facet / tek servis
//     adı) + ≤1 sa pencereyle koşar; aksi 'skipped'.
//   - Logs s.logs'tan (Settings → Elasticsearch ise ES) — env'den çıkarılmaz;
//     arka uç cache anahtarında, ES↔CH geçişi öbürünün şeklini servis etmez.
//
// Rol kapısı YOK (her kaynak zaten viewer-okunur), audit YOK (yazma değil).
// serveCached 15 sn; anahtar saf searchCacheKey (TÜM girdiler, types küme
// özeti, pencere 15 sn basamakta). Zaman aşımlı kısmi cevap TTL boyunca
// saklanır (geri basınç); zaman-aşımı-dışı store hatasında uncacheable.
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): kayıt init()'te registerRoutesExtra.

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

func init() { registerRoutesExtra("search", (*Server).registerSearchRoutes) }

func (s *Server) registerSearchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/search", s.getSearch)
}

func (s *Server) getSearch(w http.ResponseWriter, r *http.Request) {
	p, err := parseSearchParams(r.URL.Query(), time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	backend := s.searchLogsBackend()
	key := searchCacheKey(p, backend)
	s.serveCached(w, r, key, searchCacheTTL, func(ctx context.Context) (any, error) {
		return s.runSearch(ctx, p, backend)
	})
}

// searchLogsBackend — yapılandırılmış log arka ucu ("" = yok).
func (s *Server) searchLogsBackend() string {
	if s.logs == nil {
		return ""
	}
	return s.logs.Backend()
}

// runSearch — fan-out + birleştirme. İstemci gittiyse (parent ctx) hata
// döner → writeErr 499; kısmi cevap uydurulmaz.
func (s *Server) runSearch(ctx context.Context, p searchParams, backend string) (any, error) {
	res := runSearchSources(ctx, p.Types, s.searchSourceFns(p, backend), sourceBudgets(), searchCeiling)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, cacheable := assembleSearchResponse(p, res)
	if !cacheable {
		return uncacheable(resp), nil
	}
	return resp, nil
}

// searchCeilingGrace — tavan dolunca ctx'e uyan kaynakların iptali gözlemleyip
// durumunu yazması için kısa pay; ctx'i yok sayan kaynak beklenmez.
const searchCeilingGrace = 100 * time.Millisecond

// runSearchSources — yalnız `types`'taki kaynaklar koşar (seçilmeyen tür
// sorgulanmaz). Her kaynak ceiling ctx'inin çocuğu, kendi bütçesiyle;
// bütçeyi aşıp başarısız olan 'timeout', tavanda bitmemiş olan 'timeout'
// ("request ceiling"). Panik eden kaynak süreci düşürmez: 'unavailable'.
func runSearchSources(parent context.Context, types []string, fns map[string]searchSourceFn, budgets map[string]time.Duration, ceiling time.Duration) map[string]sourceResult {
	cctx, cancel := context.WithTimeout(parent, ceiling)
	defer cancel()

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		out     = make(map[string]sourceResult, len(types))
		started = make(map[string]time.Time, len(types))
	)
	for _, t := range types {
		fn, ok := fns[t]
		if !ok {
			continue
		}
		budget := budgets[t]
		if budget <= 0 || budget > ceiling {
			budget = ceiling
		}
		start := time.Now()
		started[t] = start
		wg.Add(1)
		go func(t string, fn searchSourceFn, budget time.Duration, start time.Time) {
			defer wg.Done()
			sctx, scancel := context.WithTimeout(cctx, budget)
			defer scancel()
			r := runSearchSource(sctx, t, fn)
			if r.Status.Status == "" {
				r.Status.Status = "ok"
			}
			if r.storeErr && r.err != nil {
				log.Printf("[search] %s source failed: %v", t, r.err)
			}
			r.Status.Ms = time.Since(start).Milliseconds()
			mu.Lock()
			out[t] = r
			mu.Unlock()
		}(t, fn, budget, start)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-cctx.Done():
		select {
		case <-done:
		case <-time.After(searchCeilingGrace):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	res := make(map[string]sourceResult, len(started))
	for t, start := range started {
		if r, ok := out[t]; ok {
			res[t] = r
			continue
		}
		res[t] = sourceResult{Status: sourceStatus{
			Status: "timeout", Reason: "request ceiling", Ms: time.Since(start).Milliseconds(),
		}}
	}
	return res
}

func runSearchSource(ctx context.Context, t string, fn searchSourceFn) (r sourceResult) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[search] %s source panic: %v", t, rec)
			r = sourceResult{Status: sourceStatus{Status: "unavailable", Reason: "query failed"}, storeErr: true}
		}
	}()
	return fn(ctx)
}
