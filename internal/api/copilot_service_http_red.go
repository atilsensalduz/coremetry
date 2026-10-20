package api

// copilot_service_http_red.go — v0.10.1391 (operatör, prod ekranı, "… sağlığı
// nasıl?" cevabı: "Prod sağlığı güzel ama VictoriaMetrics getirmedi, en
// azından HTTP metriclerimi getirebilirdi. Onun yerine gRPC getirdi. Aslında
// metrikler OTel collector'dan geliyor.").
//
// KÖK NEDEN: guided servis sağlığı (guidedServiceHealthBundle) grafiklerini
// YALNIZ span verisinden kuruyordu — iki ```chart``` çiti (servis error_rate /
// p99, span rollup) ve tarama (mcptools service_health_sweep) operasyon
// bulgusunun spanmetrics_1m grafiği. Tarama operasyonları operation_summary_5m'den
// sıralar (kind boyutu YOK); servisin GİDEN gRPC istemci çağrısı (CLIENT) gelen
// HTTP uç noktasıyla aynı listede yarışıp bulgu oldu ve grafiğe o çıktı. Prod %1
// örnekliyor: o grafiğin count paneli (0–7) örneklenmiş span sayısıydı ve başlığı
// "p50/p99" diyordu. Oysa VictoriaMetrics'te OTel collector'ın ÖRNEKLENMEMİŞ
// HTTP sunucu histogramı var (http_server_request_duration_seconds_*; Service →
// HTTP Metrics sekmesinin metriği).
//
// DÜZELTME: VM yapılandırılmış ve servisin bu histogramı varsa servis sağlığı
// cevabı servisin GELEN HTTP RED'ini VM'den çizer — istek hızı (req/s), 5xx
// oranı (%), gecikme p95/p99 — üç veri grafiği bloğu (kanıt çipi: PromQL kopyala
// + "Metrics'te aç"), cevapla AYNI pencere. Span grafikleri (iki çit + taramanın
// operasyon grafiği) bunların YERİNE geçer; cevaptaki grafik sayısı artmaz. Her
// grafiğin deterministik özeti (metricChartStatsTR) kanıta yazılır, kaynak
// açıkça "VictoriaMetrics, örneklenmemiş". VM yok / histogram yok / okunamadı →
// bugünkü span grafikleri kalır ve kanıt dürüst notu taşır (span = örneklenmiş).
//
// Yeniden kullanım (v0.10.1386 metric_chart makinesi): şablonlar
// (mcHTTPRPS / mcHTTP5xx / mcHTTPP99 varyantı + buildMetricChartPromQL),
// etiket keşfi önbelleği (metricChartLabelSet), ortam çözümü
// (resolveHTTPEnvScope), sorgu önbelleği (metricChartCacheKey), kanıt + grafik
// bloğu (emitGuidedEvidenceResult). Salt-okur: yazma, denetim, model çağrısı yok.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// v0.10.1391 — servis HTTP RED grafiklerinin türleri (sıra = grafik sırası).
const (
	svcHTTPREDRate    = "rate"
	svcHTTPRED5xx     = "5xx"
	svcHTTPREDLatency = "latency"
)

// svcHTTPREDBudget — VM tarafının toplam bütçesi (etiket keşfi + üç sorgu).
// Tarama (15 s) ile PARALEL koşar; cevabı tek başına uzatmaz.
const svcHTTPREDBudget = 8 * time.Second

// svcHTTPREDLatencyExpr — p95 + p99 TEK sorguda: MetricsQL histogram_quantiles
// (yalnız VM okuyucusunda koşar — metricChartReaderFor vmMetricSource ister);
// her yüzdelik ayrı seri, {phi="0.95"} / {phi="0.99"}. Seçici ve kırılım
// mcHTTPP99 şablonunun varyantından (aynı histogram, aynı yazım adayları).
const svcHTTPREDLatencyExpr = `histogram_quantiles("phi", 0.95, 0.99, sum by ({byle}) (rate({sel}[{win}])))`

// svcHTTPREDQuery — tek grafiğin SAF tanımı.
type svcHTTPREDQuery struct {
	Kind  string
	Title string
	Unit  string
	Name  string // tek serinin lejant adı (kırılımsız ifade)
	Query string
}

// svcHTTPREDTitle — SAF: grafik başlığı; kaynak açıkça yazılır.
func svcHTTPREDTitle(service, what string) string {
	return service + " · " + what + " · VictoriaMetrics (örneklenmemiş)"
}

// svcHTTPREDQueries — SAF (v0.10.1391): servisin gelen HTTP RED'inin PromQL'leri.
// statusLabel boşsa 5xx grafiği YOK (0 sanılmasın — çağıran notu yazar).
func svcHTTPREDQueries(service, svcLabel, envMatcher, statusLabel string, winS int) []svcHTTPREDQuery {
	rps, _ := metricChartTemplateByID(mcHTTPRPS)
	fiveXX, _ := metricChartTemplateByID(mcHTTP5xx)
	p99, _ := metricChartTemplateByID(mcHTTPP99)
	in := func(v metricChartVariant) metricChartQueryInput {
		return metricChartQueryInput{Variant: v, Service: service, SvcLabel: svcLabel, EnvMatcher: envMatcher, StatusLabel: statusLabel, WinS: winS}
	}
	out := []svcHTTPREDQuery{{Kind: svcHTTPREDRate, Title: svcHTTPREDTitle(service, "HTTP istek hızı"), Unit: "req/s", Name: "req/s",
		Query: buildMetricChartPromQL(in(rps.Variants[0]))}}
	if statusLabel != "" {
		out = append(out, svcHTTPREDQuery{Kind: svcHTTPRED5xx, Title: svcHTTPREDTitle(service, "HTTP 5xx oranı"), Unit: "%", Name: "5xx %",
			Query: buildMetricChartPromQL(in(fiveXX.Variants[0]))})
	}
	lat := p99.Variants[0]
	lat.Expr = svcHTTPREDLatencyExpr
	out = append(out, svcHTTPREDQuery{Kind: svcHTTPREDLatency, Title: svcHTTPREDTitle(service, "HTTP gecikme p95/p99"), Unit: "s", Name: "gecikme",
		Query: buildMetricChartPromQL(in(lat))})
	return out
}

// svcHTTPREDCacheKey — SAF: grafik sonucu önbelleği; metricChartCacheKey'in
// TÜM girdileri + tür (CLAUDE.md "cache key hashes ALL inputs").
func svcHTTPREDCacheKey(kind, query, service, envMatcher string, from, to time.Time, step int) string {
	return metricChartCacheKey(metricSourceVM, "svc_http_red/"+kind, query, service, envMatcher, "", from, to, step)
}

// svcHTTPREDProbe — kaynak kararının girdileri (okuma sırasıyla dolar).
type svcHTTPREDProbe struct {
	VM            bool
	LabelErr      error
	MetricPresent bool
	SvcLabelOK    bool
	RateErr       error
	RateSeries    int
}

// pickServiceChartSource — SAF (v0.10.1391): servis sağlığı grafiklerinin kaynağı.
// true → VictoriaMetrics HTTP RED; false → span grafikleri (yedek) + dürüst gerekçe.
// Okunamadı ≠ yok: etiket/sorgu hatası "histogram yok" diye söylenmez.
func pickServiceChartSource(p svcHTTPREDProbe) (bool, string) {
	switch {
	case !p.VM:
		return false, "VictoriaMetrics yapılandırılmamış"
	case p.LabelErr != nil:
		return false, "VictoriaMetrics etiket okuması BAŞARISIZ (okunamadı ≠ yok): " + p.LabelErr.Error()
	case !p.MetricPresent:
		return false, "OTel HTTP sunucu histogramı (http_server_request_duration_seconds_*) VictoriaMetrics'te bulunamadı (son 24 sa)"
	case !p.SvcLabelOK:
		return false, "HTTP histogramında servis etiketi bulunamadı (service_name / LabelMap) — servise süzülemez"
	case p.RateErr != nil:
		return false, "VictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): " + p.RateErr.Error()
	case p.RateSeries == 0:
		return false, "bu servisin HTTP sunucu histogramı serisi bu pencerede yok (servis HTTP sunucusu değil ya da metrik başka adla)"
	}
	return true, ""
}

// svcHTTPREDChart — okunmuş tek grafik.
type svcHTTPREDChart struct {
	svcHTTPREDQuery
	Href       string
	Series     []mcptools.EvChartSeries
	Truncated  bool
	DurationMs int64
	Err        error
}

// svcHTTPREDResult — VM tarafının sonucu (goroutine'de dolar, emit ETMEZ).
type svcHTTPREDResult struct {
	UseVM    bool
	Reason   string // UseVM=false: span yedeğinin gerekçesi
	Notes    []string
	From, To time.Time
	Step     int
	Env      string // uygulanan ortam (kanıt çipi)
	Charts   []svcHTTPREDChart
}

// readServiceHTTPRED — VM tarafı: etiket keşfi (5 dk önbellek), ortam çözümü,
// önce istek hızı sorgusu (servisin histogramı var mı — 60 sn önbellekli
// varlık yoklaması), sonra 5xx + gecikme paralel. Hiçbir okuma hatası demeti
// düşürmez; UseVM=false + gerekçe döner.
func (s *Server) readServiceHTTPRED(ctx context.Context, rd metricChartReader, isVM bool, service, env string, from, to time.Time, rangeS int64) svcHTTPREDResult {
	qTo := to.Truncate(time.Minute)
	qFrom := qTo.Add(-to.Sub(from))
	step := metricChartStepS(rangeS)
	res := svcHTTPREDResult{From: qFrom, To: qTo, Step: step}
	probe := svcHTTPREDProbe{VM: isVM && rd != nil}
	fallback := func() svcHTTPREDResult {
		res.UseVM, res.Reason = pickServiceChartSource(probe)
		res.Charts = nil
		return res
	}
	if !probe.VM || service == "" {
		return fallback()
	}
	labels, err := s.metricChartLabelSet(ctx, rd, mcHTTPMetric, qTo)
	if probe.LabelErr = err; err != nil {
		return fallback()
	}
	probe.MetricPresent = len(labels) > 0
	if !probe.MetricPresent {
		return fallback()
	}
	lm := rd.MetricLabelMap().Normalized()
	sr := vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, labels, true)
	if probe.SvcLabelOK = sr.Applied(); !probe.SvcLabelOK {
		return fallback()
	}
	envMatcher := ""
	if env != "" {
		sc, eerr := s.resolveHTTPEnvScope(ctx, rd, map[string]string{vmetrics.RoleEnv: env}, qTo)
		switch {
		case eerr != nil:
			res.Notes = append(res.Notes, fmt.Sprintf("ortam %q süzgeci UYGULANMADI (ortam etiketi okunamadı: %v) — seriler TÜM ortamları kapsıyor", env, eerr))
		case !sc.Applied:
			res.Notes = append(res.Notes, fmt.Sprintf("ortam %q süzgeci UYGULANMADI (%s) — seriler TÜM ortamları kapsıyor", env, sc.Reason))
		case !labels[sc.Label]:
			res.Notes = append(res.Notes, fmt.Sprintf("ortam %q süzgeci UYGULANMADI (histogram %s etiketini taşımıyor) — seriler TÜM ortamları kapsıyor", env, sc.Label))
		default:
			envMatcher, res.Env = sc.Matcher, env
		}
	}
	status := metricChartStatusLabel(labels)
	if status == "" {
		res.Notes = append(res.Notes, fmt.Sprintf("HTTP histogramında durum kodu etiketi yok (%s) — 5xx oranı ölçülemez, grafiği YOK (0 sanılmasın)", strings.Join(mcStatusLabels, " / ")))
	}
	qs := svcHTTPREDQueries(service, sr.Label, envMatcher, status, metricChartWinS(step))
	run := func(q svcHTTPREDQuery) svcHTTPREDChart {
		return s.svcHTTPREDRunQuery(ctx, rd, q, service, envMatcher, qFrom, qTo, step)
	}
	rate := run(qs[0])
	probe.RateErr, probe.RateSeries = rate.Err, len(rate.Series)
	if ok, _ := pickServiceChartSource(probe); !ok {
		return fallback()
	}
	charts := make([]svcHTTPREDChart, len(qs))
	charts[0] = rate
	var wg sync.WaitGroup
	for i := 1; i < len(qs); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			charts[i] = run(qs[i])
		}(i)
	}
	wg.Wait()
	res.UseVM, res.Charts = true, charts
	return res
}

// svcHTTPREDRunQuery — tek grafik: 60 sn önbellek, yoksa TEK range sorgusu
// (kaydedici VM'e giden ifadeyi yakalar). Gecikme serileri phi → p95/p99.
func (s *Server) svcHTTPREDRunQuery(ctx context.Context, rd metricChartReader, q svcHTTPREDQuery, service, envMatcher string, from, to time.Time, step int) svcHTTPREDChart {
	out := svcHTTPREDChart{svcHTTPREDQuery: q}
	out.Href = mcptools.PromQLEditorHref(q.Query, "vm", "", from.UnixMilli(), to.UnixMilli())
	key := svcHTTPREDCacheKey(q.Kind, q.Query, service, envMatcher, from, to, step)
	var c metricChartCached
	if !s.metricChartCacheGet(ctx, key, &c) {
		rec := &vmetrics.QueryRecorder{}
		qctx, cancel := context.WithTimeout(vmetrics.WithQueryRecorder(ctx, rec), metricChartQueryTimeout)
		start := time.Now()
		raw, err := rd.QueryPromQLRange(qctx, q.Query, from, to, step, metricChartMaxPoints)
		cancel()
		if err != nil {
			out.Err = err
			return out
		}
		c.Series, c.Truncated = metricChartSeries(raw, q.Unit, q.Name)
		c.DurationMs = time.Since(start).Milliseconds()
		c.Query = q.Query
		if rq := rec.Queries(); len(rq) > 0 {
			c.Query = rq[len(rq)-1]
		}
		s.metricChartCacheSet(ctx, key, c, metricChartCacheTTL)
	}
	if c.Query != "" {
		out.Query = c.Query
	}
	out.Series, out.Truncated, out.DurationMs = svcHTTPREDNameSeries(c.Series), c.Truncated, c.DurationMs
	return out
}

// svcHTTPREDNameSeries — SAF: histogram_quantiles'ın {phi="0.95"} adları → p95/p99.
func svcHTTPREDNameSeries(in []mcptools.EvChartSeries) []mcptools.EvChartSeries {
	out := make([]mcptools.EvChartSeries, len(in))
	for i, s := range in {
		switch s.Name {
		case "0.95":
			s.Name = "p95"
		case "0.99":
			s.Name = "p99"
		}
		out[i] = s
	}
	return out
}

// svcHTTPREDHeadTR — SAF: kanıt bloğunun başlığı (kaynak açık).
func svcHTTPREDHeadTR(service string, rangeS int64, step int) string {
	return fmt.Sprintf("SERVİS HTTP RED GRAFİKLERİ (%s, son %s; kaynak: VictoriaMetrics, örneklenmemiş — OTel collector'ın HTTP sunucu histogramı "+
		"http_server_request_duration_seconds_*, Service → HTTP Metrics sekmesiyle aynı metrik; adım %ds):\n", service, fmtAgoTR(rangeS), step)
}

// svcHTTPREDSpanNoteTR — SAF: span yedeğinin dürüst notu.
func svcHTTPREDSpanNoteTR(reason string) string {
	return "Not (grafik kaynağı): servis grafikleri span verisinden — örneklenmiş trace'ler (sayımlar örnekleme oranı kadar düşük); " +
		"VictoriaMetrics HTTP RED çizilmedi: " + reason + ".\n"
}

// emitServiceHTTPRED — bundle goroutine'inde: grafik başına bir metric_chart
// adımı (kanıt çipi: PromQL + pencere + seri + süre + "Metrics'te aç") ve aynı
// ref'le veri grafiği bloğu. Dönen metin kanıta eklenir: kaynak başlığı, notlar,
// grafik başına deterministik özet.
func (s *Server) emitServiceHTTPRED(emit func(string, any), service string, rangeS int64, res svcHTTPREDResult, loc *time.Location) string {
	var b strings.Builder
	b.WriteString(svcHTTPREDHeadTR(service, rangeS, res.Step))
	for _, nt := range res.Notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	// v0.10.1415 — grafik başına görünüm ORTAK kurucudan (service_vm_red_charts.go;
	// servis raporu da aynısını kullanır). Metin/kanıt/blok 1391 ile bayt bayt aynı.
	drawn := 0
	for _, v := range svcHTTPREDViews(service, rangeS, res, loc) {
		n := emitGuidedStep(emit, "metric_chart", v.Args)
		b.WriteString(v.Text)
		if v.Err != nil {
			emitGuidedStepResult(emit, n, "metric_chart", v.Text, v.Err)
			continue
		}
		if v.Drawn {
			drawn++
		}
		emitGuidedEvidenceResult(emit, n, "metric_chart", v.Text, v.Ev, v.Chart)
	}
	if drawn > 0 {
		b.WriteString("GRAFİK ÖZETİ yukarıda (deterministik; model noktaları yeniden sayma). Bu sayılar ÖRNEKLENMEMİŞ metrikten: istek hacmi/oranı " +
			"için bunları esas al; span türevli SAYIMLAR (span/s, hata sayısı) örneklenmiş trace'lerden ve örnekleme oranı kadar düşük. " +
			"Grafikler cevabın altında kanıt çiplerinde çizildi (sunucu verisi; grafik bloğu YAZMA).\n")
	}
	return b.String()
}

// startServiceHTTPRED — v0.10.1391: VM tarafını arka planda başlatır (goroutine
// emit ETMEZ); dönen fonksiyon sonucu bekler (bütçe ya da ctx sonunda yedek).
func (s *Server) startServiceHTTPRED(ctx context.Context, service, env string, from, to time.Time, rangeS int64) func() svcHTTPREDResult {
	rd, isVM := s.metricChartReaderFor()
	ch := make(chan svcHTTPREDResult, 1)
	vctx, cancel := context.WithTimeout(ctx, svcHTTPREDBudget)
	go func() {
		ch <- s.readServiceHTTPRED(vctx, rd, isVM, service, env, from, to, rangeS)
	}()
	return func() svcHTTPREDResult {
		defer cancel()
		select {
		case r := <-ch:
			return r
		case <-vctx.Done():
			_, reason := pickServiceChartSource(svcHTTPREDProbe{VM: isVM, LabelErr: errors.New("süre bütçesi doldu")})
			return svcHTTPREDResult{Reason: reason}
		}
	}
}

// svcHTTPREDSourcesTR — SAF: cevabın kaynak satırına ek.
func svcHTTPREDSourcesTR(res svcHTTPREDResult) string {
	if res.UseVM {
		return " + servis HTTP RED grafikleri (VictoriaMetrics, örneklenmemiş: istek hızı · 5xx oranı · p95/p99)"
	}
	return " + grafikler (span, örneklenmiş)"
}
