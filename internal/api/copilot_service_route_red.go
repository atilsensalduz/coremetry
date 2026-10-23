package api

// copilot_service_route_red.go — v0.10.1481: servis sağlığında taramanın
// "sorumlu operasyon" grafiği, VictoriaMetrics'ten.
//
// v0.10.1453'ten beri servis sağlığı cevabı yalnız VM'den çizer ve taramanın
// operasyon grafiği (spanmetrics_1m, örneklenmiş) HER ZAMAN düşer — böylece
// bulgunun hangi uç noktada olduğu grafiksiz kaldı. Bu dosya o grafiği geri
// getirir, ama SPAN'DEN DEĞİL: sorumlu operasyon (mcptools
// SweepResult.ResponsibleOperation — grafik seçimiyle aynı kural) bir HTTP
// sunucu span adıysa ("GET /api/orders/{id}", OTel `{method} {route}`), servis
// RED'inin çözülmüş girdileriyle (servis etiketi, ortam eşleyicisi, durum
// etiketi — readServiceHTTPRED) aynı histogramdan http_route (+ varsa
// http_request_method) süzgeçli üç grafik: istek hızı · 5xx % · p95/p99.
// Eşleşme yoksa (ad rota değil, giden çağrı, gRPC ailesi, histogramda rota
// etiketi yok, VM'de bu rotanın serisi yok, okuma hatası) grafik YOK, tek
// satır dürüst not. Span grafiği hiçbir dalda yok.
//
// Bütçe: tarama bittikten sonra en çok svcRouteREDBudget; üç sorgu paralel,
// her biri svcHTTPREDRunQuery'nin 60 sn önbelleğinden. Salt-okur.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// svcRouteREDBudget — rota grafiklerinin toplam bütçesi (tarama sonrası).
const svcRouteREDBudget = 5 * time.Second

// svcRouteKindPrefix — rota grafiklerinin tür öneki (önbellek + metric_chart argümanı).
const svcRouteKindPrefix = "route_"

// httpRouteFromOperation — SAF (v0.10.1481): HTTP sunucu span adı → (fiil, rota).
// "GET /api/x/{id}" → ("GET", "/api/x/{id}"); çıplak "/api/x" → ("", "/api/x").
// Çıplak fiil ("GET"), sorgu dizgisi taşıyan ad, boşluklu rota ve bilinmeyen
// fiil eşlenmez (http_route şablonu değil).
func httpRouteFromOperation(name string) (method, route string, ok bool) {
	name = strings.TrimSpace(name)
	if v, rest, cut := strings.Cut(name, " "); cut {
		if !httpVerbSet[strings.ToUpper(v)] {
			return "", "", false
		}
		method, name = strings.ToUpper(v), strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(name, "/") || strings.ContainsAny(name, " ?#\t\n") {
		return "", "", false
	}
	return method, name, true
}

// svcRouteMatcher — SAF: ortam eşleyicisi + rota (+ fiil) eşleyicileri tek dizgide
// (metricChartSelector EnvMatcher'ı olduğu gibi ekler).
func svcRouteMatcher(fam redFamily, envMatcher, route, method string) string {
	parts := []string{}
	if m := strings.TrimSpace(envMatcher); m != "" {
		parts = append(parts, m)
	}
	parts = append(parts, fam.RouteLabel+"="+vmetrics.PromStrTS(route))
	if method != "" {
		parts = append(parts, fam.MethodLabel+"="+vmetrics.PromStrTS(method))
	}
	return strings.Join(parts, ", ")
}

// svcRouteREDQueries — SAF (v0.10.1481): rota süzgeçli RED PromQL'leri; gövde
// svcREDFamilyQueries'in aynısı (servis RED'iyle aynı histogram, aynı şablon),
// tür öneki route_ ve başlık rotayı söyler.
func svcRouteREDQueries(fam redFamily, service, svcLabel, envMatcher, statusLabel, route, method string, winS int) []svcHTTPREDQuery {
	qs := svcREDFamilyQueries(fam, service, svcLabel, svcRouteMatcher(fam, envMatcher, route, method), statusLabel, winS)
	op := strings.TrimSpace(method + " " + route)
	for i := range qs {
		what := map[string]string{svcHTTPREDRate: "istek hızı", svcHTTPRED5xx: "5xx oranı", svcHTTPREDLatency: "gecikme p95/p99"}[qs[i].Kind]
		qs[i].Title = svcHTTPREDTitle(service, "sorumlu operasyon "+op+" · "+what)
		qs[i].Kind = svcRouteKindPrefix + qs[i].Kind
	}
	return qs
}

// svcRouteRED — rota grafiklerinin sonucu.
type svcRouteRED struct {
	Op     string // sorumlu operasyon (span adı)
	Method string
	Route  string
	Charts []svcHTTPREDChart
	Note   string // grafik yoksa tek satır gerekçe
}

// svcRouteREDSkipReason — SAF (v0.10.1481): VM sorgusundan ÖNCE eşleme kararı.
// "" → sorgulanabilir. Gerekçe kanıta tek satır yazılır.
func svcRouteREDSkipReason(base svcHTTPREDResult, op, spanKind string) (method, route, reason string) {
	switch {
	case !base.UseVM:
		return "", "", "" // servis RED'i çizilemedi; o not zaten yazıldı
	case base.Family == redKindGRPC:
		return "", "", "servisin VM ailesi gRPC — operasyon http_route'a eşlenmez"
	case strings.EqualFold(spanKind, "CLIENT") || strings.EqualFold(spanKind, "PRODUCER"):
		return "", "", "operasyon giden çağrı (" + strings.ToUpper(spanKind) + ") — servisin gelen HTTP histogramında rotası yok"
	}
	m, r, ok := httpRouteFromOperation(op)
	switch {
	case !ok:
		return "", "", "operasyon adı bir HTTP rotası değil (\"<fiil> /yol\" biçimi bekleniyor)"
	case !base.routeLabelOK:
		return "", "", "HTTP histogramı http_route etiketini taşımıyor"
	}
	if !base.methodLabelOK {
		m = ""
	}
	return m, r, ""
}

// readServiceRouteRED — VM tarafı: eşleme, sonra üç sorgu paralel. Rota serisi
// yoksa grafik yok (not). Emit ETMEZ.
func (s *Server) readServiceRouteRED(ctx context.Context, rd metricChartReader, isVM bool, service, op, spanKind string, base svcHTTPREDResult) svcRouteRED {
	out := svcRouteRED{Op: op}
	method, route, reason := svcRouteREDSkipReason(base, op, spanKind)
	if !base.UseVM {
		return out
	}
	if reason != "" {
		out.Note = reason
		return out
	}
	out.Method, out.Route = method, route
	if !isVM || rd == nil {
		out.Note = "VictoriaMetrics okuyucusu yok"
		return out
	}
	qs := svcRouteREDQueries(base.family(), service, base.svcLabel, base.envMatcher, base.statusLabel, route, method, metricChartWinS(base.Step))
	cctx, cancel := context.WithTimeout(ctx, svcRouteREDBudget)
	defer cancel()
	charts := make([]svcHTTPREDChart, len(qs))
	var wg sync.WaitGroup
	for i := range qs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			charts[i] = s.svcHTTPREDRunQuery(cctx, rd, qs[i], service, base.envMatcher, base.From, base.To, base.Step)
		}(i)
	}
	wg.Wait()
	out.Charts, out.Note = svcRouteREDFinish(charts)
	return out
}

// svcRouteREDFinish — SAF: istek hızı okunamadı / serisiz → grafik YOK + not
// (5xx/gecikme tek başına "rota var" demez).
func svcRouteREDFinish(charts []svcHTTPREDChart) ([]svcHTTPREDChart, string) {
	if len(charts) == 0 {
		return nil, "sorgu yok"
	}
	switch rate := charts[0]; {
	case rate.Err != nil:
		return nil, "VictoriaMetrics sorgusu BAŞARISIZ (okunamadı ≠ yok): " + rate.Err.Error()
	case len(rate.Series) == 0:
		return nil, "VictoriaMetrics'te bu rotanın serisi bu pencerede yok"
	}
	return charts, ""
}

// svcRouteREDNoteTR — SAF: grafiksiz tek satır (span grafiği gösterilmez).
func svcRouteREDNoteTR(op, reason string) string {
	return fmt.Sprintf("Not (sorumlu operasyon grafiği): %s için VictoriaMetrics grafiği çizilmedi — %s; span grafiği gösterilmez.\n", op, reason)
}

// emitServiceRouteRED — bundle goroutine'inde: rota grafikleri servis RED'iyle
// aynı görünüm kurucusundan (svcHTTPREDViews) — metric_chart adımı + kanıt
// çipi + veri grafiği bloğu. Dönen metin kanıta eklenir; drawn = en az bir grafik.
func (s *Server) emitServiceRouteRED(emit func(string, any), service string, rangeS int64, base svcHTTPREDResult, rr svcRouteRED, loc *time.Location) (string, bool) {
	if rr.Note != "" {
		return svcRouteREDNoteTR(rr.Op, rr.Note), false
	}
	if len(rr.Charts) == 0 {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SORUMLU OPERASYON GRAFİKLERİ (%s → %s=%q; taramanın operasyon bulgusu, kaynak: VictoriaMetrics, örneklenmemiş, servis RED'iyle aynı histogram ve pencere):\n",
		rr.Op, base.family().RouteLabel, rr.Route)
	res := base
	res.Charts = rr.Charts
	drawn := false
	for _, v := range svcHTTPREDViews(service, rangeS, res, loc) {
		n := emitGuidedStep(emit, "metric_chart", v.Args)
		b.WriteString(v.Text)
		if v.Err != nil {
			emitGuidedStepResult(emit, n, "metric_chart", v.Text, v.Err)
			continue
		}
		drawn = drawn || v.Drawn
		emitGuidedEvidenceResult(emit, n, "metric_chart", v.Text, v.Ev, v.Chart)
	}
	return b.String(), drawn
}

// svcRouteREDSourcesTR — SAF: kaynak satırına ek (yalnız çizildiyse).
func svcRouteREDSourcesTR(drawn bool) string {
	if !drawn {
		return ""
	}
	return " + sorumlu operasyonun rota RED grafikleri (VictoriaMetrics http_route)"
}
