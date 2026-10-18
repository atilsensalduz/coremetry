package api

// search_sources.go — v0.10.1333 (birleşik arama): kaynak başına TEK
// fonksiyon → sourceResult (satırlar + sourceStatus). Her biri mevcut,
// viewer-okunur bir okuyucuyu çağırır; yeni CH yüzeyi yalnız
// ListOperationHits (operation_summary_5m DISTINCT, MV-first).
//
// Neden kaynak başına fonksiyon ve neden hata DÖNDÜRMÜYORLAR: fan-out
// (search.go runSearchSources) bir kaynağın hatasını kardeşine taşımamalı —
// errgroup ilk hatada ortak ctx'i iptal eder ve operatör "traces yavaş" diye
// servis sonuçlarını da kaybederdi. Her kaynak kendi durumunu (ok /
// unavailable / skipped / timeout) yazar; cevap kısmi ama dürüst.
//
// Daraltma kuralları saf planlarda (search_query.go planTraceSearch /
// planLogSearch); burada yalnız bağlama + durum gerekçesi.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

// searchSourceFn — bir kaynağın çalıştırıcısı; ctx kaynağın bütçesini taşır.
type searchSourceFn func(ctx context.Context) sourceResult

// searchSourceFns — seçilen türler için çalıştırıcılar. Servis okuması
// trace kaynağıyla PAYLAŞILIR (tek-ad daraltması), iki kez koşmaz.
func (s *Server) searchSourceFns(p searchParams, backend string) map[string]searchSourceFn {
	svc := &serviceLookup{}
	return map[string]searchSourceFn{
		"service":   func(ctx context.Context) sourceResult { return s.searchServiceSource(ctx, p, svc) },
		"endpoint":  func(ctx context.Context) sourceResult { return s.searchEndpointSource(ctx, p) },
		"problem":   func(ctx context.Context) sourceResult { return s.searchProblemSource(ctx, p) },
		"exception": func(ctx context.Context) sourceResult { return s.searchExceptionSource(ctx, p) },
		"trace":     func(ctx context.Context) sourceResult { return s.searchTraceSource(ctx, p, svc) },
		"log":       func(ctx context.Context) sourceResult { return s.searchLogSource(ctx, p, backend) },
	}
}

// sourceErrResult — kendi deadline'ımız / iptal → 'timeout' (önbelleğe
// yazılabilir kısmi cevap); gerçek store hatası → 'unavailable' + storeErr
// (cevap önbelleğe YAZILMAZ). Ham hata metni viewer'a gitmez (CH host/port/
// `code: NNN` sızıntısı, api-route skill'i) — yalnız pod loguna.
func sourceErrResult(ctx context.Context, err error) sourceResult {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return sourceResult{Status: sourceStatus{Status: "timeout", Reason: "deadline exceeded"}}
	}
	return sourceResult{Status: sourceStatus{Status: "unavailable", Reason: "query failed"}, storeErr: true, err: err}
}

// windowCapReason — "window capped to 1h" / "24h" (Duration.String'in
// "1h0m0s"ı değil); tavanlar hep tam saat.
func windowCapReason(d time.Duration) string {
	return fmt.Sprintf("window capped to %dh", int(d.Hours()))
}

func skippedSource(reason string) sourceResult {
	return sourceResult{Status: sourceStatus{Status: "skipped", Reason: reason}}
}

func joinReasons(rs ...string) string {
	return strings.Join(nonEmpty(rs...), "; ")
}

// serviceLookup — servis adı okuması, TEK kez (sync.Once): servis kaynağı
// ve trace kaynağının "servis kaynağı tek ad döndürdü mü" sorusu aynı
// cevabı okur. acache (Redis, frekans sıralı) önce, soğuksa CH MV'si.
type serviceLookup struct {
	once  sync.Once
	names []string
	total int
	err   error
}

func (l *serviceLookup) get(ctx context.Context, s *Server, q string, limit int) ([]string, int, error) {
	l.once.Do(func() {
		lctx, cancel := context.WithTimeout(ctx, sourceBudgets()["service"])
		defer cancel()
		if names, total, hit := s.autocomplete.GetServices(lctx, q, limit); hit {
			l.names, l.total = names, total
			return
		}
		l.names, l.total, l.err = s.store.ListServiceNames(lctx, q, limit, 0)
	})
	return l.names, l.total, l.err
}

func (s *Server) searchServiceSource(ctx context.Context, p searchParams, svc *serviceLookup) sourceResult {
	if p.Kind == queryTraceID {
		return skippedSource("trace id query") // palet emsali: hex'te servis araması yok
	}
	names, total, err := svc.get(ctx, s, p.Q, p.Limit)
	if err != nil {
		return sourceErrResult(ctx, err)
	}
	if p.Service != "" {
		// Servis facet'i seçiliyken servis satırı yalnız O servis olabilir.
		kept := []string{}
		for _, n := range names {
			if n == p.Service {
				kept = append(kept, n)
			}
		}
		names, total = kept, len(kept)
	}
	r := sourceResult{Rows: serviceResults(names, p.Env), Count: total, Truncated: total > len(names)}
	if p.Env != "" {
		r.Status.Reason = "env not applied" // ad kataloğu env boyutu taşımaz
	}
	return r
}

func (s *Server) searchEndpointSource(ctx context.Context, p searchParams) sourceResult {
	if p.Kind == queryTraceID {
		return skippedSource("trace id query")
	}
	hits, total, err := s.store.ListOperationHits(ctx, p.Service, p.Q, p.Limit)
	if err != nil {
		return sourceErrResult(ctx, err)
	}
	r := sourceResult{Rows: endpointResults(hits), Count: total, Truncated: total > len(hits)}
	if p.Env != "" {
		r.Status.Reason = "env not applied" // operation_summary_5m env boyutu taşımaz
	}
	return r
}

// searchProblemSource — ListProblems (durum/servis/env/critical SQL'de),
// sonra Go'da alt dize eşleşmesi: problems'ın metin indeksi yok, tarama
// ProblemScanLimit(20, true) = 100 satır (≤ ProblemScanCeiling); tarama dolduysa
// daha eskisi olabilir → truncated.
func (s *Server) searchProblemSource(ctx context.Context, p searchParams) sourceResult {
	f := chstore.ProblemFilter{
		Service: p.Service,
		Env:     p.Env,
		Limit:   chstore.ProblemScanLimit(searchSourceCap, true),
	}
	if f.Limit > chstore.ProblemScanCeiling {
		f.Limit = chstore.ProblemScanCeiling
	}
	if p.Status == "open" {
		f.Status = "open"
	}
	if p.Sev == "critical" {
		f.Severity = "critical" // taban = tepe: tam eşitlik SQL'e iner
	}
	rows, err := s.store.ListProblems(ctx, f)
	if err != nil {
		return sourceErrResult(ctx, err)
	}
	matched := make([]chstore.Problem, 0, len(rows))
	for _, pr := range rows {
		if p.Sev != "" && levelRank(sevLevelOf(pr.Severity)) < levelRank(p.Sev) {
			continue
		}
		if problemMatchesQuery(pr, p.Q) {
			matched = append(matched, pr)
		}
	}
	shown := matched
	if len(shown) > p.Limit {
		shown = shown[:p.Limit]
	}
	out := problemResults(shown)
	if p.Env != "" {
		for i := range out {
			out[i].Env = p.Env // env SQL'de uygulandı (servis kapsamlı semantik)
		}
	}
	return sourceResult{Rows: out, Count: len(matched), Truncated: len(rows) >= f.Limit || len(matched) > len(shown)}
}

// searchExceptionSource — ListExceptionGroups (Search SQL'de ILIKE). Env:
// EnvMemberServices — hata → KISITSIZ (soft-fail, exception-groups ucunun
// duruşu) ve gerekçe "env not applied". Öncelik okuma anında
// (exceptionPriority); sev tabanı Go'da, bu yüzden sev varken 5× okunur.
func (s *Server) searchExceptionSource(ctx context.Context, p searchParams) sourceResult {
	fetch := p.Limit
	if p.Sev != "" {
		fetch = p.Limit * 5
	}
	f := chstore.ExceptionGroupFilter{Search: p.Q, Service: p.Service, Limit: fetch}
	reason, envStamp := "", ""
	if p.Env != "" {
		members, err := s.store.EnvMemberServices(ctx, p.Env)
		switch {
		case err != nil:
			log.Printf("[search] exception env %q çözülemedi (kısıtsız devam): %v", p.Env, err)
			reason = "env not applied"
		case len(members) == 0:
			// Çözüldü ama boş: bu env'de servis yok → dürüst boş. Boş Services
			// ListExceptionGroups'ta "kısıt yok" demek; sorguya göndermeyiz.
			return sourceResult{}
		default:
			f.Services, envStamp = members, p.Env
		}
	}
	rows, err := s.store.ListExceptionGroups(ctx, f)
	if err != nil {
		return sourceErrResult(ctx, err)
	}
	kept := make([]chstore.ExceptionGroup, 0, len(rows))
	for _, g := range rows {
		g.Priority, g.PriorityReason = exceptionPriority(g)
		if p.Sev != "" && levelRank(exceptionLevel(g.Priority)) < levelRank(p.Sev) {
			continue
		}
		kept = append(kept, g)
	}
	shown := kept
	if len(shown) > p.Limit {
		shown = shown[:p.Limit]
	}
	out := exceptionResults(shown)
	for i := range out {
		out[i].Env = envStamp
	}
	r := sourceResult{Rows: out, Count: len(kept), Truncated: len(rows) >= fetch || len(kept) > len(shown)}
	r.Status.Reason = reason
	return r
}

// searchTraceSource — planTraceSearch'in zorunlu daraltması; serbest metinde
// servis facet'i yoksa servis okumasının TEK adı (paylaşılan serviceLookup).
// NarrowedFrom onurlandırılır: yarıya inmiş pencerede top-N başka bir
// cevaptır — gerekçe + truncated.
func (s *Server) searchTraceSource(ctx context.Context, p searchParams, svc *serviceLookup) sourceResult {
	unique := ""
	if p.Kind == queryText && p.Service == "" {
		if names, total, err := svc.get(ctx, s, p.Q, p.Limit); err == nil && total == 1 && len(names) == 1 {
			unique = names[0]
		}
	}
	pl := planTraceSearch(p, unique)
	if pl.Skip {
		return skippedSource(pl.Reason)
	}
	f := pl.Filter
	var narrowed time.Time
	f.NarrowedFrom = &narrowed
	rows, _, hasMore, err := s.store.GetTraces(ctx, f)
	if err != nil {
		return sourceErrResult(ctx, err)
	}
	r := sourceResult{Rows: traceResults(rows, p.Env), Count: len(rows), Truncated: hasMore}
	reasons := []string{}
	if unique != "" {
		reasons = append(reasons, "service "+unique)
	}
	if pl.Capped {
		reasons = append(reasons, windowCapReason(pl.Filter.To.Sub(pl.Filter.From)))
	}
	if !narrowed.IsZero() {
		reasons = append(reasons, "window narrowed")
		r.Truncated = true
	}
	r.Status.Reason = joinReasons(reasons...)
	return r
}

// searchLogSource — s.logs yapılandırılmış arka uçtur (Settings →
// Elasticsearch ise ES; env'den ÇIKARILMAZ). ErrBackendSlow → 'unavailable'
// (zaman-aşımı sınıfı: önbelleğe yazılabilir geri basınç, /api/logs trace
// dalının duruşu); diğer hatalar storeErr.
func (s *Server) searchLogSource(ctx context.Context, p searchParams, backend string) sourceResult {
	pl := planLogSearch(p, backend)
	if pl.Skip {
		if pl.Unavailable {
			return sourceResult{Status: sourceStatus{Status: "unavailable", Reason: pl.Reason}}
		}
		return skippedSource(pl.Reason)
	}
	if s.logs == nil {
		return sourceResult{Status: sourceStatus{Status: "unavailable", Reason: "logs backend not configured"}}
	}
	page, err := logstore.SearchWithTimeout(ctx, s.logs, pl.Filter, sourceBudgets()["log"])
	if err != nil {
		if errors.Is(err, logstore.ErrBackendSlow) {
			log.Printf("[search] logs degraded (backend=%s): %v", backend, err)
			return sourceResult{Status: sourceStatus{Status: "unavailable", Reason: "log backend slow/unreachable"}}
		}
		return sourceErrResult(ctx, err)
	}
	if page == nil {
		return sourceResult{}
	}
	envFallback := ""
	reasons := []string{}
	if p.Env != "" {
		if page.EnvUnapplied {
			reasons = append(reasons, "env not applied")
		} else {
			envFallback = p.Env
		}
	}
	if pl.Capped {
		reasons = append(reasons, windowCapReason(pl.Filter.To.Sub(pl.Filter.From)))
	}
	if page.Partial {
		reasons = append(reasons, "partial results")
	}
	rows := logResults(page.Logs, p.Q, envFallback)
	r := sourceResult{
		Rows:      rows,
		Count:     page.Total,
		Truncated: page.TotalIsLowerBound || page.Partial || page.Total > len(rows),
	}
	r.Status.Reason = joinReasons(reasons...)
	return r
}
