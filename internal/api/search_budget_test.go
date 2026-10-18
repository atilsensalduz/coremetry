package api

// search_budget_test.go — v0.10.1333. Fan-out ve daraltma sözleşmesi:
//   - traces: serbest metin YALNIZ servis facet'i ya da servis kaynağının
//     tek adı varken (Service+Search, pencere ≤1 sa); aksi 'skipped' "needs
//     service or trace id" — span'ler üstünde daraltmasız tam metin ASLA.
//     32-hex → TraceID tam eşitlik; kimlik → Search ham (kimlik-önce yolu).
//   - logs: CH arka ucunda ayrışmayan sözdizimi 'skipped' "unsupported
//     syntax" (rejectLogQuerySyntax'ın saf kararı); pencere ≤24 sa; Limit 20,
//     SoftTimeout 3 sn; arka uç yoksa 'unavailable'.
//   - fan-out: WaitGroup + kaynak başına context.WithTimeout (errgroup DEĞİL):
//     bir kaynağın hatası kardeşini İPTAL ETMEZ; bütçeyi aşan 'timeout';
//     6 sn tavanı ctx'i yok sayan kaynağı da bekletmez; seçilmeyen tür
//     çağrılmaz.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlanTraceSearchNarrowing(t *testing.T) {
	to := time.Unix(1_800_000_000, 0)
	mk := func(q string, kind searchQueryKind, service string, span time.Duration) searchParams {
		p := baseSearchParams()
		p.Q, p.Kind, p.Service = q, kind, service
		p.From, p.To = to.Add(-span), to
		return p
	}
	// Serbest metin, servis yok, tek ad yok → skipped.
	pl := planTraceSearch(mk("checkout", queryText, "", time.Hour), "")
	if !pl.Skip || pl.Reason != "needs service or trace id" {
		t.Fatalf("daraltmasız serbest metin atlanmalı: %+v", pl)
	}
	// Serbest metin + servis facet'i → Service+Search, pencere 1 sa'e kırpılır.
	pl = planTraceSearch(mk("checkout", queryText, "payments", 6*time.Hour), "")
	if pl.Skip || pl.Filter.Service != "payments" || pl.Filter.Search != "checkout" || pl.Filter.TraceID != "" {
		t.Fatalf("servis facet'iyle serbest metin: %+v", pl)
	}
	if pl.Filter.To.Sub(pl.Filter.From) != time.Hour || !pl.Capped {
		t.Fatalf("serbest metin penceresi 1 sa: %v", pl.Filter.To.Sub(pl.Filter.From))
	}
	// Serbest metin + servis kaynağının TEK adı → o servisle daralır.
	pl = planTraceSearch(mk("checkout", queryText, "", time.Hour), "checkout-svc")
	if pl.Skip || pl.Filter.Service != "checkout-svc" {
		t.Fatalf("tek servis adıyla daralmalı: %+v", pl)
	}
	// 32-hex → TraceID tam eşitlik, Search boş.
	pl = planTraceSearch(mk("0fcd70a94ba1f695ea079750e71a7c10", queryTraceID, "", 72*time.Hour), "")
	if pl.Skip || pl.Filter.TraceID != "0fcd70a94ba1f695ea079750e71a7c10" || pl.Filter.Search != "" {
		t.Fatalf("trace id: %+v", pl)
	}
	if pl.Filter.To.Sub(pl.Filter.From) != 24*time.Hour {
		t.Fatalf("trace id penceresi 24 sa: %v", pl.Filter.To.Sub(pl.Filter.From))
	}
	// Kimlik → Search ham, servis gerekmez.
	pl = planTraceSearch(mk("060209Dr8G0037156551", queryIdentity, "", time.Hour), "")
	if pl.Skip || pl.Filter.Search != "060209Dr8G0037156551" || pl.Filter.TraceID != "" {
		t.Fatalf("kimlik: %+v", pl)
	}
	// Her plan: Limit ≤ 20, CountMode skip, env iner.
	p := mk("checkout", queryText, "payments", time.Hour)
	p.Env, p.Limit = "prod", 7
	pl = planTraceSearch(p, "")
	if pl.Filter.Limit != 7 || pl.Filter.CountMode != "skip" || pl.Filter.Env != "prod" {
		t.Fatalf("limit/count/env: %+v", pl.Filter)
	}
}

func TestPlanLogSearch(t *testing.T) {
	to := time.Unix(1_800_000_000, 0)
	p := baseSearchParams()
	p.From, p.To = to.Add(-72*time.Hour), to
	p.Service, p.Env, p.Sev = "payments", "prod", "warning"

	pl := planLogSearch(p, "elasticsearch")
	if pl.Skip {
		t.Fatalf("ES: %+v", pl)
	}
	f := pl.Filter
	if f.Search != "checkout" || f.Service != "payments" || f.Env != "prod" || f.SeverityMin != 13 {
		t.Fatalf("filtre: %+v", f)
	}
	if f.Limit != 20 || f.SoftTimeout != 3*time.Second {
		t.Fatalf("limit 20 / soft 3s: %+v", f)
	}
	if f.To.Sub(f.From) != 24*time.Hour || !pl.Capped {
		t.Fatalf("log penceresi 24 sa: %v", f.To.Sub(f.From))
	}

	// CH arka ucunda ayrışmayan sözdizimi → skipped.
	bad := baseSearchParams()
	bad.Q = `body:"unterminated`
	if pl := planLogSearch(bad, "clickhouse"); !pl.Skip || pl.Reason != "unsupported syntax" {
		t.Fatalf("CH sözdizimi atlanmalı: %+v", pl)
	}
	// Aynı metin ES'te ES'in kendi grameri — atlanmaz.
	if pl := planLogSearch(bad, "elasticsearch"); pl.Skip {
		t.Fatalf("ES'te CH sözdizimi kapısı uygulanmaz: %+v", pl)
	}
	// Arka uç yok.
	if pl := planLogSearch(baseSearchParams(), ""); !pl.Skip || pl.Unavailable != true {
		t.Fatalf("arka uç yoksa unavailable: %+v", pl)
	}
}

func TestSearchSourceBudgets(t *testing.T) {
	b := sourceBudgets()
	want := map[string]time.Duration{
		"service": 2 * time.Second, "endpoint": 2 * time.Second,
		"problem": 3 * time.Second, "exception": 3 * time.Second,
		"trace": 5 * time.Second, "log": 5 * time.Second,
	}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("%s bütçesi %v, beklenen %v", k, b[k], v)
		}
		if b[k] >= searchCeiling {
			t.Errorf("%s bütçesi tavanın (%v) altında olmalı", k, searchCeiling)
		}
	}
	if searchCeiling != 6*time.Second {
		t.Errorf("istek tavanı 6 sn: %v", searchCeiling)
	}
}

func TestRunSearchSourcesTimeouts(t *testing.T) {
	budgets := map[string]time.Duration{
		"service": 50 * time.Millisecond, "endpoint": 50 * time.Millisecond,
		"problem": 300 * time.Millisecond, "exception": 50 * time.Millisecond,
		"trace": 300 * time.Millisecond, "log": 300 * time.Millisecond,
	}
	var logCalled atomic.Bool
	fns := map[string]searchSourceFn{
		// Hızlı, başarılı.
		"service": func(ctx context.Context) sourceResult {
			return sourceResult{Rows: []SearchResult{{Type: "service", ID: "a"}}, Count: 1}
		},
		// ctx'e uyan, bütçeyi aşan → timeout.
		"endpoint": func(ctx context.Context) sourceResult {
			select {
			case <-ctx.Done():
				return sourceErrResult(ctx, ctx.Err())
			case <-time.After(time.Second):
				return sourceResult{}
			}
		},
		// Anında store hatası — kardeşleri İPTAL ETMEMELİ.
		"exception": func(ctx context.Context) sourceResult {
			return sourceErrResult(ctx, errors.New("code: 60, table missing"))
		},
		// Hatalı kardeşten SONRA biten, bütçe içinde → ok.
		"problem": func(ctx context.Context) sourceResult {
			select {
			case <-ctx.Done():
				return sourceErrResult(ctx, ctx.Err())
			case <-time.After(80 * time.Millisecond):
				return sourceResult{Rows: []SearchResult{{Type: "problem", ID: "p"}}, Count: 1}
			}
		},
		// ctx'i YOK SAYAN kaynak — tavan onu beklemez.
		"trace": func(ctx context.Context) sourceResult {
			time.Sleep(2 * time.Second)
			return sourceResult{Count: 99}
		},
		"log": func(ctx context.Context) sourceResult {
			logCalled.Store(true)
			return sourceResult{}
		},
	}
	types := []string{"service", "endpoint", "problem", "exception", "trace"} // log SEÇİLMEDİ
	start := time.Now()
	res := runSearchSources(context.Background(), types, fns, budgets, 400*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 1500*time.Millisecond {
		t.Fatalf("tavan ctx'i yok sayan kaynağı beklememeli: %v", elapsed)
	}
	if logCalled.Load() {
		t.Fatal("seçilmeyen tür sorgulanmamalı")
	}
	if _, ok := res["log"]; ok {
		t.Fatal("seçilmeyen tür sonuçta olmamalı")
	}
	want := map[string]string{
		"service": "ok", "endpoint": "timeout", "exception": "unavailable",
		"problem": "ok", "trace": "timeout",
	}
	for k, v := range want {
		if got := res[k].Status.Status; got != v {
			t.Errorf("%s: status %q, beklenen %q (%+v)", k, got, v, res[k].Status)
		}
	}
	if !res["exception"].storeErr || res["endpoint"].storeErr || res["trace"].storeErr {
		t.Errorf("yalnız zaman-aşımı-dışı hata storeErr: exc=%v ep=%v tr=%v",
			res["exception"].storeErr, res["endpoint"].storeErr, res["trace"].storeErr)
	}
	if strings.Contains(res["exception"].Status.Reason, "code: 60") {
		t.Errorf("ham CH hatası viewer'a sızmamalı: %q", res["exception"].Status.Reason)
	}
	if res["problem"].Count != 1 {
		t.Errorf("problem kardeşin hatasına rağmen tamamlanmalı: %+v", res["problem"])
	}
	for k, r := range res {
		if r.Status.Ms < 0 {
			t.Errorf("%s ms negatif", k)
		}
	}
}

func TestSearchSourceErrClassification(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if r := sourceErrResult(ctx, errors.New("driver: read tcp: i/o timeout")); r.Status.Status != "timeout" || r.storeErr {
		t.Errorf("kendi deadline'ımız → timeout, storeErr değil: %+v", r)
	}
	if r := sourceErrResult(context.Background(), errors.New("code: 47")); r.Status.Status != "unavailable" || !r.storeErr {
		t.Errorf("gerçek store hatası → unavailable + storeErr: %+v", r)
	}
}
