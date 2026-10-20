package api

// metric_chart_validate_test.go — v0.10.1416 (CoSRE metrik grafiği R2;
// operatör: "VictoriaMetrics sorgulayıp grafik getirse"). Yapıştırılan PromQL
// doğrulayıcısının kabul/ret tablosu (her kural), MetricsQL → yalnız link,
// topk(20, …) sarma, ortam eşleştiricisi enjeksiyonu, en kısa aralık.
// Metrik adları sentetik.

import (
	"strings"
	"testing"
	"time"
)

func TestValidateChartPromQL(t *testing.T) {
	long := "sum(rate(demo_requests_total[5m]))" + strings.Repeat(" + 0", 600)
	longRe := `demo_requests_total{pod=~"` + strings.Repeat("a", 129) + `"}`
	cases := []struct {
		name, q   string
		step      int
		rule      string // "" = kabul
		metricsQL bool
		wrap      bool
		metrics   []string
	}{
		// ── kabul ──
		{name: "sum by rate", q: `sum by (pod) (rate(demo_requests_total{job="api"}[5m]))`, step: 30, wrap: false, metrics: []string{"demo_requests_total"}},
		{name: "çıplak rate → sar", q: `rate(demo_requests_total[5m])`, step: 30, wrap: true, metrics: []string{"demo_requests_total"}},
		{name: "histogram_quantile → sar", q: `histogram_quantile(0.99, sum by (le) (rate(demo_latency_seconds_bucket[5m])))`, step: 30, wrap: true, metrics: []string{"demo_latency_seconds_bucket"}},
		{name: "ad alternasyonu", q: `sum(rate({__name__=~"demo_a_total|demo_b_total", job="api"}[5m]))`, step: 30, metrics: []string{"demo_a_total", "demo_b_total"}},
		{name: "çapalı alternasyon", q: `sum({__name__=~"^(demo_a|demo_b)$"})`, step: 30, metrics: []string{"demo_a", "demo_b"}},
		{name: "ikili işleç", q: `100 * sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total[5m]))`, step: 30, wrap: true, metrics: []string{"demo_errors_total", "demo_requests_total"}},
		{name: "topk 5", q: `topk(5, sum by (pod) (demo_queue_depth))`, step: 30, metrics: []string{"demo_queue_depth"}},
		{name: "quantile", q: `quantile(0.9, demo_queue_depth)`, step: 30, metrics: []string{"demo_queue_depth"}},
		{name: "clamp/round/abs", q: `round(abs(clamp_min(demo_queue_depth, 0)), 0.1)`, step: 30, wrap: true, metrics: []string{"demo_queue_depth"}},
		{name: "offset 1d", q: `sum(demo_queue_depth offset 1d)`, step: 30, metrics: []string{"demo_queue_depth"}},
		{name: "over_time 1h sınırda", q: `max(max_over_time(demo_queue_depth[1h]))`, step: 720, metrics: []string{"demo_queue_depth"}},
		{name: "aralık = adım", q: `sum(rate(demo_requests_total[12m]))`, step: 720, metrics: []string{"demo_requests_total"}},
		{name: "regex süzgeç", q: `sum(rate(demo_requests_total{status=~"5.."}[5m]))`, step: 30, metrics: []string{"demo_requests_total"}},
		// ── ret: her kural ──
		{name: "uzunluk", q: long, step: 30, rule: pqRuleLen},
		{name: "ayrıştırma", q: `sum(rate(demo_requests_total[5m])`, step: 30, rule: pqRuleParse},
		{name: "WITH", q: `WITH (x = demo_requests_total) rate(x[5m])`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "keep_metric_names", q: `rate(demo_requests_total[5m]) keep_metric_names`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "rollup_*", q: `rollup_rate(demo_requests_total[5m])`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "[5i]", q: `rate(demo_requests_total[5i])`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "aralıksız rate", q: `rate(demo_requests_total)`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "histogram_quantiles", q: `histogram_quantiles("phi", 0.9, 0.99, sum by (le) (rate(demo_latency_seconds_bucket[5m])))`, step: 30, rule: pqRuleMetricsQL, metricsQL: true},
		{name: "alt sorgu", q: `max_over_time(rate(demo_requests_total[5m])[1h:1m])`, step: 30, rule: pqRuleSubquery},
		{name: "aralık < adım", q: `rate(demo_requests_total[5m])`, step: 720, rule: pqRuleRange},
		{name: "aralık > 1h", q: `rate(demo_requests_total[2h])`, step: 30, rule: pqRuleRange},
		{name: "çıplak aralık vektörü", q: `demo_requests_total[5m]`, step: 30, rule: pqRuleRange},
		{name: "offset > 7d", q: `demo_queue_depth offset 8d`, step: 30, rule: pqRuleOffset},
		{name: "negatif offset", q: `demo_queue_depth offset -5m`, step: 30, rule: pqRuleOffset},
		{name: "@ zaman", q: `demo_queue_depth @ 1700000000`, step: 30, rule: pqRuleAt},
		{name: "@ end()", q: `sum(demo_queue_depth @ end())`, step: 30, rule: pqRuleAt},
		{name: "7 seçici", q: `a_x + b_x + c_x + d_x + e_x + f_x + g_x`, step: 30, rule: pqRuleSelectors},
		{name: "seçici yok", q: `1 + 1`, step: 30, rule: pqRuleSelectors},
		{name: ">10 farklı ad", q: `sum({__name__=~"m_a|m_b|m_c"}) + sum({__name__=~"m_d|m_e|m_f"}) + sum({__name__=~"m_g|m_h|m_i"}) + sum({__name__=~"m_j|m_k"})`, step: 30, rule: pqRuleSelectors},
		{name: "adsız seçici", q: `sum({job="api"})`, step: 30, rule: pqRuleName},
		{name: "regex ad", q: `sum({__name__=~"demo_.*"})`, step: 30, rule: pqRuleName},
		{name: "__name__ !=", q: `sum({__name__!="demo_x"})`, step: 30, rule: pqRuleName},
		{name: "11 alternasyon", q: `sum({__name__=~"a1|a2|a3|a4|a5|a6|a7|a8|a9|a10|a11"})`, step: 30, rule: pqRuleName},
		{name: "regex .*", q: `sum(demo_queue_depth{pod=~".*"})`, step: 30, rule: pqRuleRegex},
		{name: "regex ^.+$", q: `sum(demo_queue_depth{pod!~"^.+$"})`, step: 30, rule: pqRuleRegex},
		{name: "regex 129", q: longRe, step: 30, rule: pqRuleRegex},
		{name: "regex derlenmez", q: `sum(demo_queue_depth{pod=~"(("})`, step: 30, rule: pqRuleRegex},
		{name: "label_replace", q: `label_replace(demo_queue_depth, "a", "$1", "pod", "(.*)")`, step: 30, rule: pqRuleFunc},
		{name: "label_join", q: `label_join(demo_queue_depth, "a", ",", "pod")`, step: 30, rule: pqRuleFunc},
		{name: "absent", q: `absent(demo_queue_depth)`, step: 30, rule: pqRuleFunc},
		{name: "absent_over_time", q: `absent_over_time(demo_queue_depth[5m])`, step: 30, rule: pqRuleFunc},
		{name: "sort", q: `sort(demo_queue_depth)`, step: 30, rule: pqRuleFunc},
		{name: "sort_desc", q: `sort_desc(demo_queue_depth)`, step: 30, rule: pqRuleFunc},
		{name: "vector", q: `demo_queue_depth + vector(1)`, step: 30, rule: pqRuleFunc},
		{name: "scalar", q: `scalar(demo_queue_depth)`, step: 30, rule: pqRuleFunc},
		{name: "time", q: `time() - demo_last_seen_seconds`, step: 30, rule: pqRuleFunc},
		{name: "predict_linear", q: `predict_linear(demo_queue_depth[1h], 3600)`, step: 30, rule: pqRuleFunc},
		{name: "holt_winters", q: `holt_winters(demo_queue_depth[1h], 0.5, 0.5)`, step: 30, rule: pqRuleFunc},
		{name: "stddev_over_time", q: `stddev_over_time(demo_queue_depth[5m])`, step: 30, rule: pqRuleFunc},
		{name: "izin listesi dışı", q: `changes(demo_queue_depth[5m])`, step: 30, rule: pqRuleFunc},
		{name: "histogram_quantile φ>1", q: `histogram_quantile(1.5, rate(demo_latency_seconds_bucket[5m]))`, step: 30, rule: pqRuleFunc},
		{name: "clamp sabit değil", q: `clamp_min(demo_queue_depth, demo_floor)`, step: 30, rule: pqRuleFunc},
		{name: "count_values", q: `count_values("v", demo_queue_depth)`, step: 30, rule: pqRuleAgg},
		{name: "group", q: `group(demo_queue_depth)`, step: 30, rule: pqRuleAgg},
		{name: "stddev", q: `stddev(demo_queue_depth)`, step: 30, rule: pqRuleAgg},
		{name: "stdvar", q: `stdvar(demo_queue_depth)`, step: 30, rule: pqRuleAgg},
		{name: "topk 50", q: `topk(50, demo_queue_depth)`, step: 30, rule: pqRuleK},
		{name: "topk 0", q: `topk(0, demo_queue_depth)`, step: 30, rule: pqRuleK},
		{name: "topk kesirli", q: `bottomk(2.5, demo_queue_depth)`, step: 30, rule: pqRuleK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validateChartPromQL(c.q, c.step)
			if got.Rule != c.rule {
				t.Fatalf("kural %q bekleniyordu, %q (%s)", c.rule, got.Rule, got.Reason)
			}
			if c.rule != "" {
				if got.Reason == "" {
					t.Error("ret gerekçesiz olmamalı")
				}
				if got.MetricsQL != c.metricsQL {
					t.Errorf("MetricsQL %v bekleniyordu", c.metricsQL)
				}
				return
			}
			if got.NeedsWrap != c.wrap {
				t.Errorf("sarma %v bekleniyordu", c.wrap)
			}
			if strings.Join(got.Metrics, ",") != strings.Join(c.metrics, ",") {
				t.Errorf("metrikler %v, beklenen %v", got.Metrics, c.metrics)
			}
		})
	}
}

func TestPromQLExecQueryWrapAndInject(t *testing.T) {
	// Toplama değil → topk(20, …); operatörün metni aynen (boşluk dahil) korunur.
	chk := validateChartPromQL("rate(demo_requests_total{job=\"api\"}[5m])", 30)
	got, err := promqlExecQuery(chk, nil)
	if err != nil || got != `topk(20, rate(demo_requests_total{job="api"}[5m]))` {
		t.Fatalf("sarma: %q %v", got, err)
	}
	chk = validateChartPromQL("sum  by (pod)(rate(demo_requests_total[5m]))", 30)
	if got, _ := promqlExecQuery(chk, nil); got != "sum  by (pod)(rate(demo_requests_total[5m]))" {
		t.Fatalf("toplama sarılmaz, metin aynen: %q", got)
	}
	// Ortam eşleştiricisi her seçiciye eklenir.
	add, err := promqlMatchersFrom(`deployment_environment_name=~"prod-eu-a|prod-eu-b"`)
	if err != nil {
		t.Fatal(err)
	}
	chk = validateChartPromQL(`sum(rate(demo_errors_total[5m])) / sum(rate(demo_requests_total{job="api"}[5m]))`, 30)
	got, err = promqlExecQuery(chk, add)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, `deployment_environment_name=~"prod-eu-a|prod-eu-b"`) != 2 || !strings.HasPrefix(got, "topk(20, ") || !strings.Contains(got, `job="api"`) {
		t.Errorf("enjeksiyon: %s", got)
	}
	// Yeniden yazılan ifade yine doğrulanır (ayrıştırılabilir, aynı kurallar).
	if v := validateChartPromQL(strings.TrimSuffix(strings.TrimPrefix(got, "topk(20, "), ")"), 30); !v.OK() {
		t.Errorf("yeniden yazılan ifade geçerli olmalı: %s (%s)", got, v.Reason)
	}
	if !promqlHasLabelMatcher(validateChartPromQL(`sum(demo_queue_depth{deployment_environment_name="uat"})`, 30).Expr, "deployment_environment_name") {
		t.Error("var olan ortam eşleştiricisi görülmeli")
	}
}

func TestPromQLMinRange(t *testing.T) {
	for _, c := range []struct {
		q    string
		want time.Duration
	}{
		{`sum(rate(a_total[5m])) / sum(rate(b_total[15m]))`, 5 * time.Minute},
		{`sum(demo_queue_depth)`, 0},
		{`sum(`, -1},
	} {
		if got := promqlMinRange(c.q); got != c.want {
			t.Errorf("%s → %v, beklenen %v", c.q, got, c.want)
		}
	}
}
