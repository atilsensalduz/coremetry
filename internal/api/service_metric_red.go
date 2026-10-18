package api

// service_metric_red.go — v0.10.337: Service Overview RED'i METRİKTEN
// (VM ana metrik deposu, dilim 2).
//
// Operatör (2026-09-04): "service overview'da en üstte response throughput
// trace sürelerini de Victoria'ya bağlayabilir miyiz". Response time (avg)
// ve Throughput karoları v0.9.798'den beri metrikten (v0.9.1268 ile VM'den)
// okuyor; P50/P95/P99, latency grafiği ve Failure rate SPAN türevliydi —
// sebep eski bir CH sınırı (metric_points kova sınırı taşımıyordu). VM'de
// `_bucket` serileri var; dikiş (metricsource.go) p50/p95/p99'u
// histogram_quantile ile üretiyor.
//
// GET /api/services/{name}/metric-red → spanMetricBatch ile AYNI seri
// haritası (rate · error_rate · p50 · p95 · p99 · avg) ki Overview aynı
// tüketicilerle çizsin:
//   - rate       req/s (rate modu, rateWindow yumuşatması)
//   - error_rate YÜZDE (span sözleşmesi: 100 × err/total), 5xx =
//                http.response.status_code / http.status_code; etiket yoksa
//                seri YOK + errorsUnknown (0 değil)
//   - p50/p95/p99/avg ms (kaynak birimi ya da `_seconds` adından); birim
//                bilinmiyorsa seriler YOK + latencyUnitKnown=false — tahmin
//                yok, karo span'e düşer ve etiketi söyler
// Kimlik ve ad: /endpoints metrik kipiyle ortak (endpointsMetricIdentity:
// tput bağı → service_name; service.throughput_metric + adaylar). Her
// okuma dikişten; hiçbir s.store.<metric> çağrısı yok. Varsayılan
// DEĞİŞMEDİ: Overview ?src=metric ile bu uca geçer.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("service-metric-red", (*Server).registerServiceMetricREDRoutes) }

// registerServiceMetricREDRoutes — metric-throughput ile aynı duruş (çıplak,
// okuma-yalnız; kimlikli her rol).
func (s *Server) registerServiceMetricREDRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/metric-red", s.getServiceMetricRED)
}

type serviceMetricREDPlan struct {
	Service  string
	From, To time.Time
	Mdp      int
	RateWin  int
	Env      string
}

// v0.10.1350 — v2: zarf statusState/statusReason taşır, hata veri güdümlü.
func serviceMetricREDKey(p serviceMetricREDPlan, srcName, mx string) string {
	return fmt.Sprintf("svc-metric-red:v2:src=%s:%s:%s:mdp%d:rw%d:env=%s:mx=%s",
		srcName, p.Service, cacheBucket(p.From, p.To), p.Mdp, p.RateWin, p.Env, mx)
}

// serviceMetricREDResponse — zarf. `series` anahtarları spanMetricBatch'inki.
type serviceMetricREDResponse struct {
	Service          string                                `json:"service"`
	Source           string                                `json:"source"`
	Metric           string                                `json:"metric,omitempty"`
	RTMetric         string                                `json:"rtMetric,omitempty"`
	Instrument       string                                `json:"instrument,omitempty"`
	MatchedBy        string                                `json:"matchedBy,omitempty"`
	MetricExists     bool                                  `json:"metricExists"`
	StepSeconds      int                                   `json:"stepSeconds"`
	StatusKey        string                                `json:"statusKey,omitempty"`
	ErrorsUnknown    bool                                  `json:"errorsUnknown,omitempty"`
	StatusState      statusLabelState                      `json:"statusState,omitempty"`  // v0.10.1350
	StatusReason     string                                `json:"statusReason,omitempty"` // v0.10.1350
	LatencyUnit      string                                `json:"latencyUnit,omitempty"`
	LatencyUnitKnown bool                                  `json:"latencyUnitKnown"`
	LatencyUnitFrom  string                                `json:"latencyUnitFrom,omitempty"`
	EnvAmbiguous     bool                                  `json:"envAmbiguous,omitempty"`
	Tried            []string                              `json:"tried,omitempty"`
	Partial          []string                              `json:"partial,omitempty"`
	Note             string                                `json:"note"`
	Series           map[string][]chstore.SpanMetricSeries `json:"series"`
}

// seriesStep — noktalar arası en küçük pozitif aralık (sn); tek nokta → 0.
func seriesStep(ser []chstore.SpanMetricSeries) int {
	best := int64(0)
	for _, s := range ser {
		for i := 1; i < len(s.Points); i++ {
			d := (s.Points[i].Time - s.Points[i-1].Time) / 1e9
			if d > 0 && (best == 0 || d < best) {
				best = d
			}
		}
	}
	return int(best)
}

// errorRatePercent — SAF: rate ve 5xx-rate serilerinden YÜZDE serisi
// (zaman damgasıyla hizalı; hata noktası yoksa 0 — "hata yok"; rate 0 ise
// nokta atlanır — 0/0 yüzde değildir).
func errorRatePercent(rate, errs []chstore.SpanMetricSeries) []chstore.SpanMetricSeries {
	if len(rate) == 0 {
		return []chstore.SpanMetricSeries{}
	}
	errAt := map[int64]float64{}
	for _, s := range errs {
		for _, p := range s.Points {
			errAt[p.Time] += p.Value
		}
	}
	// v0.10.842 — Operator-reported: boşta servis (rate hep 0) → hiç nokta
	// eklenmez, nil dilim JSON'a `null` yazılır, Overview .map üstünde düşer.
	// Seri varsa points DİZİDİR (/api-route ev kuralı: nil → []T{}).
	out := chstore.SpanMetricSeries{GroupKey: []string{}, Points: []chstore.SpanMetricPoint{}}
	for _, s := range rate {
		for _, p := range s.Points {
			if p.Value <= 0 || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			e := errAt[p.Time]
			if e < 0 || math.IsNaN(e) || math.IsInf(e, 0) {
				e = 0
			}
			pct := e / p.Value * 100
			if pct > 100 {
				pct = 100
			}
			out.Points = append(out.Points, chstore.SpanMetricPoint{Time: p.Time, Value: pct})
		}
	}
	return []chstore.SpanMetricSeries{out}
}

// sumSeriesByTime — SAF (v0.10.1350): durum kırılımlı serileri zaman
// damgasında toplar → TEK grupsuz seri (eski durumsuz sorgunun şekli).
// NaN/Inf atlanır; seri yoksa boş dilim (JSON `[]`, asla null).
func sumSeriesByTime(ser []chstore.SpanMetricSeries) []chstore.SpanMetricSeries {
	if len(ser) == 0 {
		return []chstore.SpanMetricSeries{}
	}
	at := map[int64]float64{}
	for _, s := range ser {
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			at[p.Time] += p.Value
		}
	}
	out := chstore.SpanMetricSeries{Points: make([]chstore.SpanMetricPoint, 0, len(at))}
	for t, v := range at {
		out.Points = append(out.Points, chstore.SpanMetricPoint{Time: t, Value: v})
	}
	sort.Slice(out.Points, func(i, j int) bool { return out.Points[i].Time < out.Points[j].Time })
	return []chstore.SpanMetricSeries{out}
}

// scaleSeries — değerleri ms'e çevirir (kopya; kaynak serisi mutasyona uğramaz).
func scaleSeries(ser []chstore.SpanMetricSeries, scale float64) []chstore.SpanMetricSeries {
	out := make([]chstore.SpanMetricSeries, 0, len(ser))
	for _, s := range ser {
		c := chstore.SpanMetricSeries{GroupKey: s.GroupKey, Points: make([]chstore.SpanMetricPoint, 0, len(s.Points))}
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			c.Points = append(c.Points, chstore.SpanMetricPoint{Time: p.Time, Value: p.Value * scale})
		}
		out = append(out, c)
	}
	return out
}

// buildServiceMetricRED — SAF (kaynak arayüz): sorgular → seri haritası.
func buildServiceMetricRED(ctx context.Context, src metricSource, id endpointsMetricIdentity, p serviceMetricREDPlan) (serviceMetricREDResponse, error) {
	resp := serviceMetricREDResponse{
		Service: p.Service, Source: src.Name(),
		Metric: id.Metric, RTMetric: id.RTMetric, Instrument: id.Instrument, MatchedBy: id.MatchedBy,
		MetricExists: true,
		Series:       map[string][]chstore.SpanMetricSeries{},
	}
	base := chstore.MetricQueryFilter{
		Name: id.Metric, Service: id.Service, Filters: id.Filters,
		From: p.From, To: p.To, MaxDataPoints: p.Mdp, RateWindowSec: p.RateWin,
	}
	if p.Env != "" {
		_, applied := src.EnvFilterExpr(p.Env)
		resp.EnvAmbiguous = !applied
		base = withEnvFilter(base, p.Env, src)
	}
	unit := src.MetricUnit(ctx, id.RTMetric, p.Service)
	scale, unitKnown, unitFrom := latencyScaleMs(unit, id.RTMetric)
	resp.LatencyUnit, resp.LatencyUnitKnown, resp.LatencyUnitFrom = unit, unitKnown, unitFrom
	if unit == "" && unitKnown {
		resp.LatencyUnit = "s"
		if scale == 1 {
			resp.LatencyUnit = "ms"
		}
	}

	rate := src.QueryMetricRate
	if id.Instrument == "histogram" {
		rate = src.QueryMetricCountRate
	}
	// v0.10.1350 — veri güdümlü durum yoklaması (http_labels.go): rate
	// sorgusu durum adayıyla gruplanır; rate = durumların toplamı, hata =
	// 5xx serileri. Ayrı 5xx sorgusu ve 7 günlük etiket taraması yok.
	probe := probeStatusLabel(func(key string) ([]chstore.SpanMetricSeries, error) {
		f := base
		f.GroupBy = []string{key}
		return statusRowsChecked(rate(ctx, f, "rate"))
	}, 0)
	resp.StatusState, resp.StatusKey = probe.State, probe.Key
	resp.ErrorsUnknown = probe.State != statusLabelPresent
	byStatus := probe.Series
	if probe.State == statusLabelUnknown {
		resp.StatusReason = probe.Reason
		if byStatus == nil { // ilk aday hata verdi: rate durumsuz sorgudan
			var err error
			byStatus, err = rate(ctx, base, "rate")
			if err != nil {
				return resp, err
			}
		}
	}
	rateSer := sumSeriesByTime(byStatus)
	resp.Series["rate"] = rateSer
	resp.StepSeconds = seriesStep(rateSer)
	if probe.State == statusLabelPresent {
		var errSer []chstore.SpanMetricSeries
		for _, sr := range byStatus {
			if len(sr.GroupKey) > 0 && isHTTP5xx(sr.GroupKey[0]) {
				errSer = append(errSer, sr)
			}
		}
		resp.Series["error_rate"] = errorRatePercent(rateSer, errSer)
	}
	if unitKnown {
		for _, ag := range []string{"p50", "p95", "p99", "avg"} {
			vf := base
			vf.Name = id.RTMetric
			vf.Aggregation = ag
			ser, verr := src.QueryMetric(ctx, vf)
			if verr != nil {
				resp.Partial = append(resp.Partial, ag+": "+verr.Error())
				continue
			}
			resp.Series[ag] = scaleSeries(ser, scale)
		}
	}
	resp.Note = serviceMetricREDNote(resp)
	return resp, nil
}

// serviceMetricREDNote — kapsam rozeti + tooltip metni.
func serviceMetricREDNote(r serviceMetricREDResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kaynak: METRİK (%s)", r.Source)
	if !r.MetricExists {
		b.WriteString(" — metrik bulunamadı")
		if len(r.Tried) > 0 {
			b.WriteString(" (denenen: " + strings.Join(r.Tried, ", ") + ")")
		}
		b.WriteString("; karolar ve grafikler span'a düşer.")
		return b.String()
	}
	fmt.Fprintf(&b, " — %s, kimlik %s.", r.Metric, r.MatchedBy)
	b.WriteString(statusLabelNote(r.StatusState, r.StatusKey, r.StatusReason, r.Source, "Failure rate", ""))
	if r.LatencyUnitKnown {
		fmt.Fprintf(&b, " P50/P95/P99 histogram_quantile, birim %s", r.LatencyUnit)
		if r.LatencyUnitFrom == "name" {
			b.WriteString(" (metrik adından)")
		}
		b.WriteString(".")
	} else {
		b.WriteString(" Süre birimi BİLİNMİYOR: P50/P95/P99 metrikten verilmedi, karo span'a düşer.")
	}
	if r.EnvAmbiguous {
		b.WriteString(" env filtresi bu depoda ifade edilemiyor, seriler TÜM ortamları kapsıyor.")
	}
	if len(r.Partial) > 0 {
		b.WriteString(" Eksik: " + strings.Join(r.Partial, "; ") + ".")
	}
	return b.String()
}

// getServiceMetricRED — GET /api/services/{name}/metric-red
func (s *Server) getServiceMetricRED(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeErr(w, fmt.Errorf("%w: service required", errBadRequest))
		return
	}
	from, to := parseFromTo(r, time.Hour)
	mdp, _ := strconv.Atoi(r.URL.Query().Get("maxDataPoints"))
	if mdp < 0 {
		mdp = 0
	}
	if mdp > 4000 {
		mdp = 4000
	}
	rateWin, _ := strconv.Atoi(r.URL.Query().Get("rateWindow"))
	if rateWin <= 0 {
		rateWin = 180
	}
	if rateWin > 600 {
		rateWin = 600
	}
	p := serviceMetricREDPlan{Service: name, From: from, To: to, Mdp: mdp, RateWin: rateWin, Env: strings.TrimSpace(r.URL.Query().Get("env"))}
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	mx := s.store.MetricExclusions().Digest()
	key := serviceMetricREDKey(p, src.Name(), mx)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		id, ok := s.endpointsMetricIdentity(ctx, src, name, p.Env)
		if !ok {
			resp := serviceMetricREDResponse{Service: name, Source: src.Name(), Tried: id.Tried, Series: map[string][]chstore.SpanMetricSeries{}}
			resp.Note = serviceMetricREDNote(resp)
			return resp, nil
		}
		return buildServiceMetricRED(ctx, src, id, p)
	})
}
