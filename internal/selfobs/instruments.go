package selfobs

// instruments.go — v0.10.1422 — rol başına Coremetry'nin KENDİ RED
// metrikleri (operatör: "Coremetry api, worker ve ingester için yap"):
//
//   - api + ingest (HTTP):  http.server.request.duration (s) — semconv adı,
//     VM'de http_server_request_duration_seconds_* (frontend metrik kipinin
//     okuduğu aile). Etiketler: http.request.method, http.route,
//     http.response.status_code. Kayıt internal/api/http_server_metrics.go.
//   - ingest (OTLP/gRPC):   rpc.server.duration (ms) — rpc.system, rpc.service,
//     rpc.method, rpc.grpc.status_code. internal/otlp'deki interceptor kancası.
//   - worker:               worker.job.duration (s) + worker.job.runs
//     {job, result} — lider-kapılı döngülerin tik başına süresi.
//   - sre:                  sre.investigation.duration (s) + sre.investigations
//     {result}.
//
// Enstrümanlar İLK kayıtta (sync.Once) kurulur: Init'ten önce kurulsalar
// noop meter'a bağlı kalırlardı (op_p99_cache.go'nun aynı gerekçesi).
//
// İSTEK YOLU: Record çağrısı SDK'da kilitsiz-yakın ve bloklamaz; etiket
// kümeleri (attribute.Set) anahtar başına bir kez kurulup önbelleklenir —
// istek başına sıralama/ayırma yok. Önbellek tavanlı (setCacheCap): tavan
// dolunca yeni kümeler yine kaydedilir, yalnız önbelleğe girmez.

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const setCacheCap = 4096

// HTTP semconv kova sınırları (s) — otelhttp/semconv ile aynı.
var httpBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// RPC semconv kova sınırları (ms).
var rpcBuckets = []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}

// İş süresi kovaları (s): saniye-altı tiklerden 5 dk'lık taramalara.
var jobBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// SRE araştırması kovaları (s): bütçe dakikalar mertebesinde.
var sreBuckets = []float64{1, 5, 10, 30, 60, 120, 300, 600, 1200}

type setCache[K comparable] struct {
	mu sync.RWMutex
	m  map[K]attribute.Set
}

func (c *setCache[K]) get(k K, build func() attribute.Set) attribute.Set {
	c.mu.RLock()
	s, ok := c.m[k]
	c.mu.RUnlock()
	if ok {
		return s
	}
	s = build()
	c.mu.Lock()
	if c.m == nil {
		c.m = make(map[K]attribute.Set)
	}
	if len(c.m) < setCacheCap {
		c.m[k] = s
	}
	c.mu.Unlock()
	return s
}

// ── HTTP ───────────────────────────────────────────────────────────

type httpKey struct {
	method, route string
	status        int
}

var (
	httpOnce sync.Once
	httpHist metric.Float64Histogram
	httpSets setCache[httpKey]
)

func httpInstruments() metric.Float64Histogram {
	httpOnce.Do(func() {
		h, err := Meter().Float64Histogram("http.server.request.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Coremetry'nin sunduğu HTTP isteklerinin süresi (API + OTLP/HTTP)"),
			metric.WithExplicitBucketBoundaries(httpBuckets...))
		if err != nil {
			log.Printf("[selfobs] http.server.request.duration: %v", err)
		}
		httpHist = h
	})
	return httpHist
}

// RecordHTTPServer — method normalize edilmiş (bilinen fiil ya da _OTHER),
// route sınırlı şablon (boş = etiket yok). Bloklamaz.
func RecordHTTPServer(method, route string, status int, d time.Duration) {
	h := httpInstruments()
	if h == nil {
		return
	}
	set := httpSets.get(httpKey{method, route, status}, func() attribute.Set {
		kvs := make([]attribute.KeyValue, 0, 3)
		kvs = append(kvs, attribute.String("http.request.method", method),
			attribute.Int("http.response.status_code", status))
		if route != "" {
			kvs = append(kvs, attribute.String("http.route", route))
		}
		return attribute.NewSet(kvs...)
	})
	h.Record(context.Background(), d.Seconds(), metric.WithAttributeSet(set))
}

// ── RPC (OTLP/gRPC) ────────────────────────────────────────────────

type rpcKey struct {
	full string
	code uint32
}

var (
	rpcOnce sync.Once
	rpcHist metric.Float64Histogram
	rpcSets setCache[rpcKey]
)

// RecordRPCServer — fullMethod "/pkg.Service/Method", code gRPC durum kodu.
// internal/otlp kancası (otlp.SetRPCServerObserver) main'de buna bağlanır.
func RecordRPCServer(fullMethod string, code uint32, d time.Duration) {
	rpcOnce.Do(func() {
		h, err := Meter().Float64Histogram("rpc.server.duration",
			metric.WithUnit("ms"),
			metric.WithDescription("Coremetry OTLP/gRPC alıcısının RPC süresi"),
			metric.WithExplicitBucketBoundaries(rpcBuckets...))
		if err != nil {
			log.Printf("[selfobs] rpc.server.duration: %v", err)
		}
		rpcHist = h
	})
	if rpcHist == nil {
		return
	}
	set := rpcSets.get(rpcKey{fullMethod, code}, func() attribute.Set {
		svc, m := SplitGRPCMethod(fullMethod)
		return attribute.NewSet(
			attribute.String("rpc.system", "grpc"),
			attribute.String("rpc.service", svc),
			attribute.String("rpc.method", m),
			attribute.Int64("rpc.grpc.status_code", int64(code)),
		)
	})
	rpcHist.Record(context.Background(), float64(d)/float64(time.Millisecond), metric.WithAttributeSet(set))
}

// SplitGRPCMethod — SAF: "/a.b.Service/Method" → ("a.b.Service", "Method").
func SplitGRPCMethod(full string) (service, method string) {
	full = strings.TrimPrefix(full, "/")
	if i := strings.LastIndexByte(full, '/'); i >= 0 {
		return full[:i], full[i+1:]
	}
	return full, ""
}

// ── Worker işleri ──────────────────────────────────────────────────

type jobKey struct {
	job, result string
}

var (
	jobOnce sync.Once
	jobHist metric.Float64Histogram
	jobRuns metric.Int64Counter
	jobSets setCache[jobKey]
)

// ObserveJob — bir arka plan işi turunun süresi + sonucu. job SABİT bir ad
// olmalı (çağıran yerde literal; kardinalite = iş sayısı). err != nil →
// result="error", yoksa "ok". Kullanım:
//
//	defer selfobs.ObserveJob("evaluator", time.Now(), nil) // ya da err ile
func ObserveJob(job string, started time.Time, err error) {
	jobOnce.Do(func() {
		var e error
		if jobHist, e = Meter().Float64Histogram("worker.job.duration",
			metric.WithUnit("s"),
			metric.WithDescription("Arka plan işi turunun süresi (lider-kapılı döngüler)"),
			metric.WithExplicitBucketBoundaries(jobBuckets...)); e != nil {
			log.Printf("[selfobs] worker.job.duration: %v", e)
		}
		if jobRuns, e = Meter().Int64Counter("worker.job.runs",
			metric.WithDescription("Arka plan işi turları, result=ok|error")); e != nil {
			log.Printf("[selfobs] worker.job.runs: %v", e)
		}
	})
	result := "ok"
	if err != nil {
		result = "error"
	}
	set := jobSets.get(jobKey{job, result}, func() attribute.Set {
		return attribute.NewSet(attribute.String("job", job), attribute.String("result", result))
	})
	opt := metric.WithAttributeSet(set)
	if jobHist != nil {
		jobHist.Record(context.Background(), time.Since(started).Seconds(), opt)
	}
	if jobRuns != nil {
		jobRuns.Add(context.Background(), 1, opt)
	}
}

// ── SRE araştırmaları ──────────────────────────────────────────────

var (
	sreOnce sync.Once
	sreHist metric.Float64Histogram
	sreRuns metric.Int64Counter
	sreSets setCache[string]
)

// ObserveSREInvestigation — result: done | partial | error | requeued.
func ObserveSREInvestigation(result string, d time.Duration) {
	sreOnce.Do(func() {
		var e error
		if sreHist, e = Meter().Float64Histogram("sre.investigation.duration",
			metric.WithUnit("s"),
			metric.WithDescription("AI SRE araştırmasının uçtan uca süresi"),
			metric.WithExplicitBucketBoundaries(sreBuckets...)); e != nil {
			log.Printf("[selfobs] sre.investigation.duration: %v", e)
		}
		if sreRuns, e = Meter().Int64Counter("sre.investigations",
			metric.WithDescription("Biten AI SRE araştırmaları, result=done|partial|error|requeued")); e != nil {
			log.Printf("[selfobs] sre.investigations: %v", e)
		}
	})
	set := sreSets.get(result, func() attribute.Set {
		return attribute.NewSet(attribute.String("result", result))
	})
	opt := metric.WithAttributeSet(set)
	if sreHist != nil {
		sreHist.Record(context.Background(), d.Seconds(), opt)
	}
	if sreRuns != nil {
		sreRuns.Add(context.Background(), 1, opt)
	}
}

// ── SRE araştırma adımları (v0.10.1463, D1) ─────────────────────────

type sreStepKey struct{ step, state string }

// sreStepBuckets — adım tavanları saniyeler mertebesinde (okuyucu cap'i).
var sreStepBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 60}

var (
	sreStepOnce sync.Once
	sreStepHist metric.Float64Histogram
	sreStepSets setCache[sreStepKey]
)

// ObserveSREStep — deterministik adımın süresi (sre.step.duration). step
// planın kapalı adım kümesinden (sweep / deploys / deps / pivot / log_spike /
// events / node / summary), state ok|partial|error|skipped → kardinalite
// sabit. Span ağacının (sre.step) metrik ikizi: örneklemeden bağımsız sayar.
func ObserveSREStep(step, state string, d time.Duration) {
	sreStepOnce.Do(func() {
		var e error
		if sreStepHist, e = Meter().Float64Histogram("sre.step.duration",
			metric.WithUnit("s"),
			metric.WithDescription("AI SRE araştırma adımının süresi, step × state"),
			metric.WithExplicitBucketBoundaries(sreStepBuckets...)); e != nil {
			log.Printf("[selfobs] sre.step.duration: %v", e)
		}
	})
	set := sreStepSets.get(sreStepKey{step, state}, func() attribute.Set {
		return attribute.NewSet(attribute.String("step", step), attribute.String("state", state))
	})
	if sreStepHist != nil {
		sreStepHist.Record(context.Background(), d.Seconds(), metric.WithAttributeSet(set))
	}
}

// ── AI araç argümanı doğrulaması (v0.10.1456, C1) ──────────────────

type toolArgKey struct {
	mode, result string
}

var (
	toolArgOnce sync.Once
	toolArgRuns metric.Int64Counter
	toolArgSets setCache[toolArgKey]
)

// ObserveToolArgValidation — şema doğrulayıcının (SchemaScope) her kararı.
// mode: warn | enforce; result: ok | invalid | gave_up. Araç adı BİLEREK
// yok (kardinalite = kip × sonuç); hangi aracın düştüğü log + ai_calls notunda.
// Sohbet `warn`'dan `enforce`'a geçiş kararı bu sayaçla ölçülür (operatör
// kararı 3, 2026-10-23).
func ObserveToolArgValidation(mode, result string) {
	toolArgOnce.Do(func() {
		var e error
		if toolArgRuns, e = Meter().Int64Counter("ai.tool.args_validation",
			metric.WithDescription("AI araç argümanı şema doğrulaması, mode=warn|enforce result=ok|invalid|gave_up")); e != nil {
			log.Printf("[selfobs] ai.tool.args_validation: %v", e)
		}
	})
	set := toolArgSets.get(toolArgKey{mode, result}, func() attribute.Set {
		return attribute.NewSet(attribute.String("mode", mode), attribute.String("result", result))
	})
	if toolArgRuns != nil {
		toolArgRuns.Add(context.Background(), 1, metric.WithAttributeSet(set))
	}
}
