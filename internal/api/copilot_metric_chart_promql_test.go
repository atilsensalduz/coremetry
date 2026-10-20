package api

// copilot_metric_chart_promql_test.go — v0.10.1416 (CoSRE metrik grafiği R2;
// operatör: "VictoriaMetrics sorgulayıp grafik getirse"). Yapıştırılan PromQL:
// looksLikePromQL tablosu (çıplak / çitli / ters tırnaklı / MetricsQL; düzyazıdaki
// "rate" sözcüğü PromQL değil), router (metric_chart + ChartPromQL, ortam yalnız
// sorgu dışı metinden, hata metni / operasyon ön-getirmesi sahiplenmez), pencere,
// demet (sahte VM: tek sorgu + topk sarma, var olmayan metrik reddi, MetricsQL →
// yalnız link, ortam enjeksiyonu, ekran ortamı eklenmez, VM yok, okuma hatası),
// takip çipleri, salt-okur. Metrik adları sentetik.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestLooksLikePromQL(t *testing.T) {
	cases := []struct {
		msg   string
		want  bool
		query string // "" = kontrol etme
	}{
		// ── pozitif ──
		{msg: `sum(rate(demo_http_requests_total{job="api"}[5m]))`, want: true, query: `sum(rate(demo_http_requests_total{job="api"}[5m]))`},
		{msg: `şunu çiz: sum by (pod) (rate(demo_http_requests_total[5m]))`, want: true, query: `sum by (pod) (rate(demo_http_requests_total[5m]))`},
		{msg: `sum(rate(demo_http_requests_total[5m])) grafiğini çiz`, want: true, query: `sum(rate(demo_http_requests_total[5m]))`},
		{msg: `rate(demo_http_requests_total[5m])'yi son 6 saat çiz`, want: true, query: `rate(demo_http_requests_total[5m])`},
		{msg: `chart this please: demo_queue_depth{queue="orders"}`, want: true, query: `demo_queue_depth{queue="orders"}`},
		{msg: "```promql\nsum(rate(demo_x_total[5m]))\n```", want: true, query: `sum(rate(demo_x_total[5m]))`},
		{msg: "```sum(rate(demo_x_total[5m]))```", want: true, query: `sum(rate(demo_x_total[5m]))`},
		{msg: "bu sorguyu çiz `histogram_quantile(0.99, sum by (le) (rate(demo_lat_seconds_bucket[5m])))`", want: true, query: `histogram_quantile(0.99, sum by (le) (rate(demo_lat_seconds_bucket[5m])))`},
		{msg: "```\nWITH (x = demo_x_total) rate(x[5m])\n```", want: true, query: `WITH (x = demo_x_total) rate(x[5m])`},
		{msg: `rate(demo_x_total[5m]) keep_metric_names grafiği`, want: true, query: `rate(demo_x_total[5m]) keep_metric_names`},
		{msg: "`sum(rate(demo_x_total[5m])`", want: true}, // çitli + biçimli ama ayrıştırılamaz → dürüst ret
		// ── negatif: düzyazı ──
		{msg: "checkout error rate nasıl?"},
		{msg: "rate limit hatası alıyoruz"},
		{msg: "5xx rate grafiği çiz"},
		{msg: "orders-api istek rate(rps) grafiği"},
		{msg: "`service_name` etiketi neden boş?"},
		{msg: "p99 (son 1 saat) yüksek mi"},
		{msg: "orders-api'nin JVM heap kullanımı son 6 saat grafik"},
		{msg: "payment_service error rate (son 1 saat)"},
		{msg: "Caused by: java.lang.NullPointerException at com.example.Foo.bar(Foo.java:42)"},
		{msg: "[uyarı] checkout yavaş"},
	}
	for _, c := range cases {
		p, ok := extractPastedPromQL(c.msg)
		if ok != c.want {
			t.Errorf("%q: looksLikePromQL=%v, beklenen %v (sorgu %q)", c.msg, ok, c.want, p.Query)
			continue
		}
		if c.query != "" && p.Query != c.query {
			t.Errorf("%q: sorgu %q, beklenen %q", c.msg, p.Query, c.query)
		}
		// hasGuidedSignal normalize (küçük harf) metinle çalışır — aynı karar.
		if hasSig := looksLikePromQL(normalizeGuidedMsg(c.msg)); hasSig != c.want {
			t.Errorf("%q: normalize metinde %v", c.msg, hasSig)
		}
	}
	p, _ := extractPastedPromQL("`sum(demo_a_total)` ve `sum(demo_b_total)` çiz")
	if p.Query != "sum(demo_a_total)" || p.Extra != 1 {
		t.Errorf("çok sorgu: ilki + Extra: %+v", p)
	}
}

func TestRoutePastedPromQL(t *testing.T) {
	cases := []struct {
		msg, env, promql, template string
	}{
		{msg: `sum(rate(demo_http_requests_total[5m])) prod-eu-a ortamında çiz`, env: "prod-eu-a", promql: `sum(rate(demo_http_requests_total[5m]))`},
		// Sorgudaki etiket değeri ortam anması değil.
		{msg: `sum(rate(demo_http_requests_total{deployment_environment_name="prod-eu-a"}[5m]))`, promql: `sum(rate(demo_http_requests_total{deployment_environment_name="prod-eu-a"}[5m]))`},
		// Etiket değerindeki Exception yapıştırılan hata metni değil.
		{msg: `sum(rate(demo_errors_total{exception="java.lang.NullPointerException"}[5m])) çiz`, promql: `sum(rate(demo_errors_total{exception="java.lang.NullPointerException"}[5m]))`},
		// Servis adı geçse de rota sorgunun kendisi.
		{msg: "checkout için `sum(rate(demo_http_requests_total{service_name=\"checkout\"}[5m]))` grafiği", promql: `sum(rate(demo_http_requests_total{service_name="checkout"}[5m]))`},
		// Düzyazı: R1 şablonu (ChartPromQL boş).
		{msg: "5xx rate grafiği çiz", template: mcHTTP5xx},
		{msg: "checkout istek rate(rps) grafiği", template: mcHTTPRPS},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.msg, mcServices, mcEnvs, nil, "")
		if r.Intent != guidedMetricChart || r.ChartPromQL != c.promql || r.ChartTemplate != c.template {
			t.Errorf("%q → intent %q promql %q template %q", c.msg, r.Intent, r.ChartPromQL, r.ChartTemplate)
			continue
		}
		if c.promql != "" && (r.Env != c.env || r.ChartEnvAsked != (c.env != "")) {
			t.Errorf("%q → env %q asked %v, beklenen %q", c.msg, r.Env, r.ChartEnvAsked, c.env)
		}
	}
	// "rate" sözcüğü taşıyan düzyazı PromQL rotasına savrulmaz.
	for _, q := range []string{"checkout error rate nasıl?", "rate limit hatası alıyoruz", "payments hata oranı (rate) neden yüksek"} {
		if r := routeGuidedIntent(q, mcServices, mcEnvs, nil, ""); r.ChartPromQL != "" {
			t.Errorf("%q PromQL sanıldı: %q", q, r.ChartPromQL)
		}
	}
	if !hasGuidedSignal(normalizeGuidedMsg(`sum(rate(demo_x_total[5m]))`)) {
		t.Error("yapıştırılan sorgu guided sinyalidir (sıfır maliyet kapısı geçmeli)")
	}
	if !promqlVetoesOpPrefetch(`sum(rate(demo_requests_total{http_route="/api/payments"}[5m]))`) || promqlVetoesOpPrefetch("POST /api/payments neden yavaş") {
		t.Error("operasyon ön-getirmesi yalnız yapıştırılan sorguyu bırakır")
	}
	if drawerSuppressesGuided("ekrandaki açıklama", guidedRoute{Intent: guidedMetricChart, ChartPromQL: "sum(demo_x)"}, "sum(demo_x)") {
		t.Error("çekmece bağlamı yapıştırılan sorguyu bastırmaz")
	}
}

func TestPastedPromQLRangeS(t *testing.T) {
	for _, c := range []struct {
		msg      string
		want     int64
		explicit bool
	}{
		{`sum(rate(demo_jobs_per_hour_total[1h]))`, 3600, false},
		{`sum(rate(demo_x_total[5m])) son 6 saat`, 21600, true},
		{`sum(rate(demo_x_total[5m])) son 30 dakika`, 1800, true},
		{`sum(rate(demo_x_total[5m])) son 3 gün`, 86400, true},
		{"son 2 saat ```\nsum(demo_daily_hours)\n```", 7200, true},
	} {
		got, ex := pastedPromQLRangeS(c.msg)
		if got != c.want || ex != c.explicit {
			t.Errorf("%q → %d/%v, beklenen %d/%v", c.msg, got, ex, c.want, c.explicit)
		}
	}
}

func TestMetricChartRunPromQL(t *testing.T) {
	to := time.Date(2026, 10, 20, 12, 0, 30, 0, time.UTC)
	from := to.Add(-time.Hour)
	pts := func(vs ...float64) []chstore.SpanMetricPoint {
		var out []chstore.SpanMetricPoint
		for i, v := range vs {
			out = append(out, chstore.SpanMetricPoint{Time: from.Add(time.Duration(i) * time.Minute).UnixNano(), Value: v})
		}
		return out
	}
	labels := map[string][]string{
		"demo_http_requests_total": {"deployment_environment_name", "job", "pod"},
		"demo_errors_total":        {"job", "pod"},
	}
	envVals := map[string][]string{"deployment_environment_name": {"prod-eu-a", "prod-eu-b"}}
	series := []chstore.SpanMetricSeries{
		{GroupKey: []string{`pod="api-1"`}, Points: pts(10, 11, 12, 40)},
		{GroupKey: []string{`pod="api-2"`}, Points: pts(5, 5, 5, 5)},
	}
	s := &Server{}
	run := func(rd *fakeChartReader, route *guidedRoute, isVM bool) (string, *mcEvents) {
		t.Helper()
		ev := &mcEvents{}
		var reader metricChartReader
		if rd != nil {
			reader = rd
		}
		text, _, err := s.metricChartRunPromQL(context.Background(), ev.emit(), route, reader, isVM, from, to, 3600, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return text, ev
	}

	// Kabul: tek sorgu, toplama değil → topk(20, …), grafik + çip aynı ref.
	rd := &fakeChartReader{labels: labels, envVals: envVals, series: series}
	route := &guidedRoute{Intent: guidedMetricChart, ChartPromQL: `rate(demo_http_requests_total{job="api"}[5m])`}
	text, ev := run(rd, route, true)
	if len(rd.queries) != 1 || rd.queries[0] != `topk(20, rate(demo_http_requests_total{job="api"}[5m]))` || rd.steps[0] != 30 {
		t.Fatalf("TEK sorgu + sarma + adım 30: %v %v", rd.queries, rd.steps)
	}
	for _, w := range []string{"METRİK GRAFİĞİ (yapıştırılan PromQL)", "topk(20, …) ile sarıldı", "GRAFİK ÖZETİ", "api-1", "grafik bloğu YAZMA"} {
		if !strings.Contains(text, w) {
			t.Errorf("kanıt %q içermeli:\n%s", w, text)
		}
	}
	if route.DirectAnswer != "" || len(ev.blocks) != 1 || len(ev.steps) != 1 {
		t.Fatalf("kabulde anlatım yolu: direct=%q blok %d adım %d", route.DirectAnswer, len(ev.blocks), len(ev.steps))
	}
	p := ev.blocks[0].Payload.(mcptools.EvChartPayload)
	ce := ev.steps[0]["evidence"].(mcptools.ChatEvidence)
	if p.EvidenceRef == "" || p.EvidenceRef != ce.Ref || ce.Query != rd.queries[0] || ce.QueryKind != "promql" || *ce.Series != 2 || !strings.HasPrefix(route.ChartHref, "/metrics?") || ce.Href != route.ChartHref {
		t.Errorf("çip/blok: %+v ref %q href %q", ce, p.EvidenceRef, route.ChartHref)
	}

	// Ret senaryoları: sorgu ASLA çalışmaz, cevap gerekçe + Metrics'te aç linki.
	rejects := []struct {
		name  string
		rd    *fakeChartReader
		route guidedRoute
		isVM  bool
		want  []string
	}{
		{name: "var olmayan metrik", rd: &fakeChartReader{labels: labels}, isVM: true,
			route: guidedRoute{ChartPromQL: `sum(rate(demo_missing_total[5m]))`}, want: []string{"bulunamayan metrik", "demo_missing_total"}},
		{name: "MetricsQL → yalnız link", rd: &fakeChartReader{labels: labels}, isVM: true,
			route: guidedRoute{ChartPromQL: `WITH (x = demo_http_requests_total) rate(x[5m])`}, want: []string{"MetricsQL", "PromQL editöründe"}},
		{name: "alt sorgu", rd: &fakeChartReader{labels: labels}, isVM: true,
			route: guidedRoute{ChartPromQL: `max_over_time(rate(demo_http_requests_total[5m])[1h:1m])`}, want: []string{"alt sorgu"}},
		{name: "VM yok", rd: nil, isVM: false,
			route: guidedRoute{ChartPromQL: `sum(rate(demo_http_requests_total[5m]))`}, want: []string{"VictoriaMetrics yapılandırılmamış"}},
		{name: "etiket okuma hatası", rd: &fakeChartReader{labelErr: errors.New("vm down")}, isVM: true,
			route: guidedRoute{ChartPromQL: `sum(rate(demo_http_requests_total[5m]))`}, want: []string{"okunamadı ≠ yok"}},
		{name: "sorulan ortam metrikte yok", rd: &fakeChartReader{labels: labels, envVals: envVals}, isVM: true,
			route: guidedRoute{ChartPromQL: `sum(rate(demo_errors_total[5m]))`, Env: "prod-eu-a", ChartEnvAsked: true}, want: []string{"ortam süzgeci", "demo_errors_total metriği deployment_environment_name"}},
		{name: "sorulan ortam çözülmez", rd: &fakeChartReader{labels: labels, envVals: envVals}, isVM: true,
			route: guidedRoute{ChartPromQL: `sum(rate(demo_http_requests_total[5m]))`, Env: "ap-z", ChartEnvAsked: true}, want: []string{"ap-z"}},
	}
	for _, c := range rejects {
		t.Run(c.name, func(t *testing.T) {
			r := c.route
			r.Intent = guidedMetricChart
			text, ev := run(c.rd, &r, c.isVM)
			if c.rd != nil && len(c.rd.queries) != 0 {
				t.Fatalf("reddedilen sorgu ÇALIŞTI: %v", c.rd.queries)
			}
			if !strings.Contains(text, "ÇALIŞTIRILMADI") || len(ev.blocks) != 0 {
				t.Errorf("kanıt ret demeli, blok yok:\n%s", text)
			}
			for _, w := range append(c.want, "[Metrics'te aç](/metrics?") {
				if !strings.Contains(r.DirectAnswer, w) {
					t.Errorf("cevap %q içermeli:\n%s", w, r.DirectAnswer)
				}
			}
			fu := guidedFollowups(r, nil)
			if len(fu) != 1 || fu[0].Kind != fuKindMetricLink || fu[0].Href != r.ChartHref {
				t.Errorf("ret: yalnız Metrics'te aç çipi: %+v", fu)
			}
		})
	}

	// Sorulan ortam: sorgulanan metriğin etiketine çözülür, her seçiciye eklenir.
	rd = &fakeChartReader{labels: labels, envVals: envVals, series: series}
	route = &guidedRoute{Intent: guidedMetricChart, ChartPromQL: `sum by (pod) (rate(demo_http_requests_total[5m]))`, Env: "prod-eu-a", ChartEnvAsked: true}
	text, ev = run(rd, route, true)
	if len(rd.queries) != 1 || !strings.Contains(rd.queries[0], `deployment_environment_name="prod-eu-a"`) || strings.HasPrefix(rd.queries[0], "topk(") {
		t.Fatalf("ortam enjeksiyonu: %v", rd.queries)
	}
	if !strings.Contains(text, "her seçicisine") || ev.steps[0]["evidence"].(mcptools.ChatEvidence).Env != "prod-eu-a" {
		t.Errorf("ortam notu/çipi:\n%s", text)
	}
	// Ekrandan devralınan ortam sorguya EKLENMEZ ve bu söylenir.
	rd = &fakeChartReader{labels: labels, envVals: envVals, series: series}
	route = &guidedRoute{Intent: guidedMetricChart, ChartPromQL: `sum(rate(demo_http_requests_total[5m]))`, Env: "prod-eu-a"}
	text, _ = run(rd, route, true)
	if len(rd.queries) != 1 || rd.queries[0] != `sum(rate(demo_http_requests_total[5m]))` || !strings.Contains(text, "UYGULANMADI") {
		t.Errorf("ekran ortamı: %v\n%s", rd.queries, text)
	}
	// Sorgu hatası → dürüst doğrudan cevap.
	rd = &fakeChartReader{labels: labels, queryErr: errors.New("bad_data")}
	route = &guidedRoute{Intent: guidedMetricChart, ChartPromQL: `sum(rate(demo_http_requests_total[5m]))`}
	_, ev = run(rd, route, true)
	if !strings.Contains(route.DirectAnswer, "bad_data") || len(ev.steps) != 1 || ev.steps[0]["ok"] != false {
		t.Errorf("sorgu hatası: %q %+v", route.DirectAnswer, ev.steps)
	}
}

func TestPastedPromQLFollowups(t *testing.T) {
	href := "/metrics?editor=1&tab=promql"
	// [15m] ≥ 24 sa adımı (12 dk) → genişletme çipi + link.
	fu := guidedFollowups(guidedRoute{Intent: guidedMetricChart, ChartPromQL: `sum(rate(demo_x_total[15m]))`, ChartRangeS: 3600, ChartHref: href}, nil)
	if len(fu) != 2 || fu[0].Kind != fuKindMetricWindow || fu[1].Href != href {
		t.Errorf("çipler: %+v", fu)
	}
	// [5m] 24 sa'te reddedilirdi → genişletme çipi yok.
	fu = guidedFollowups(guidedRoute{Intent: guidedMetricChart, ChartPromQL: `sum(rate(demo_x_total[5m]))`, ChartRangeS: 3600, ChartHref: href}, nil)
	if len(fu) != 1 || fu[0].Kind != fuKindMetricLink {
		t.Errorf("[5m]: %+v", fu)
	}
	// Bağlam klonu (LastRoute JSON) yapıştırılan sorguyu taşır.
	if !strings.Contains(mustJSON(t, guidedRoute{Intent: guidedMetricChart, ChartPromQL: "sum(demo_x)"}), "sum(demo_x)") {
		t.Error("ChartPromQL JSON'a girmeli (son 24 saate genişlet klonu)")
	}
}

// TestPastedPromQLReadOnly — salt-okur: yazma/denetim/model çağrısı yok; TEK
// range sorgusu çağrı yeri; reddedilen sorgu yürütmeden önce döner.
func TestPastedPromQLReadOnly(t *testing.T) {
	for _, f := range []string{"copilot_metric_chart_promql.go", "metric_chart_validate.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := stripGoCommentsAPI(string(b))
		for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "Feedback", "Exec(", "ALTER ", "DELETE",
			"s.store.", "copilotStreamSurface", "copilotExplain", "s.copilot", "http.Post", "NewRequest", "os.Getenv", "/api/v1/admin", "delete_series"} {
			if strings.Contains(src, w) {
				t.Errorf("%s %q içermemeli (salt-okur)", f, w)
			}
		}
	}
	b, _ := os.ReadFile("copilot_metric_chart_promql.go")
	src := stripGoCommentsAPI(string(b))
	if n := strings.Count(src, ".QueryPromQLRange("); n != 1 {
		t.Errorf("TEK range sorgusu çağrı yeri bekleniyordu, %d", n)
	}
	val, run := strings.Index(src, "validateChartPromQL("), strings.Index(src, ".QueryPromQLRange(")
	if val < 0 || run < val {
		t.Error("doğrulama yürütmeden ÖNCE gelmeli")
	}
	for _, w := range []string{"context.WithTimeout(vmetrics.WithQueryRecorder(ctx, rec), metricChartQueryTimeout)", "s.resolveMetricEnvScope(", "metricChartCacheKey(metricSourceVM, promqlTemplateID, exec,"} {
		if !strings.Contains(src, w) {
			t.Errorf("demet %q taşımalı", w)
		}
	}
}
