package api

// metric_chart_templates_test.go — v0.10.1386: şablon kataloğu + PromQL kurucu
// (altın sorgular), adım/pencere, varyant seçimi, kırılım, durum etiketi.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func mcSel(kind string, names ...string) string {
	return vmetrics.NameSelector(vmetrics.NameCandidates(kind, names...))
}

func TestMetricChartCatalogueShape(t *testing.T) {
	seen := map[string]bool{}
	for _, tpl := range metricChartCatalogue {
		if seen[tpl.ID] {
			t.Errorf("çift şablon %q", tpl.ID)
		}
		seen[tpl.ID] = true
		if tpl.Title == "" || tpl.Phrase == "" || len(tpl.Variants) == 0 {
			t.Errorf("%s: başlık/ifade/varyant eksik", tpl.ID)
		}
		for i, v := range tpl.Variants {
			if v.Probe == "" || len(v.Names) == 0 || !strings.Contains(v.Expr, "{sel}") {
				t.Errorf("%s[%d]: probe/ad/{sel} eksik: %+v", tpl.ID, i, v)
			}
			// Salt-okur ifade: yalnız izinli fonksiyonlar.
			for _, bad := range []string{"label_replace", "label_join", "absent", "predict_linear", "@", "offset"} {
				if strings.Contains(v.Expr, bad) {
					t.Errorf("%s[%d]: ifade %q içermemeli", tpl.ID, i, bad)
				}
			}
		}
		// Phrase kendi başına aynı şablona yönlenmeli (ask/ortam çipleri).
		toks := guidedTokens(normalizeGuidedMsg(tpl.Phrase))
		if !hasMetricChartSignal(toks) || pickMetricChartTemplateID(toks) != tpl.ID {
			t.Errorf("%s: ifade %q kendi şablonuna yönlenmiyor (%q)", tpl.ID, tpl.Phrase, pickMetricChartTemplateID(toks))
		}
	}
	for _, id := range []string{mcJVMHeap, mcJVMGC, mcCPU, mcMemory, mcThreads, mcDBPool, mcKafkaLag, mcHTTP5xx, mcHTTPRPS, mcHTTPP99, mcHTTPP95} {
		if !seen[id] {
			t.Errorf("katalogda %q yok", id)
		}
	}
}

func TestMetricChartStepAndWindow(t *testing.T) {
	for _, c := range []struct {
		rangeS     int64
		step, winS int
	}{
		{300, 15, 60}, {1800, 15, 60}, {3600, 30, 120}, {21600, 180, 720}, {86400, 720, 2880},
	} {
		if got := metricChartStepS(c.rangeS); got != c.step {
			t.Errorf("step(%d)=%d, want %d", c.rangeS, got, c.step)
		}
		if got := metricChartWinS(metricChartStepS(c.rangeS)); got != c.winS {
			t.Errorf("win(%d)=%d, want %d", c.rangeS, got, c.winS)
		}
		if pts := c.rangeS / int64(metricChartStepS(c.rangeS)); pts > metricChartMaxPoints {
			t.Errorf("%d sn: %d nokta > %d", c.rangeS, pts, metricChartMaxPoints)
		}
	}
}

func TestBuildMetricChartPromQLGolden(t *testing.T) {
	heap, _ := metricChartTemplateByID(mcJVMHeap)
	fiveXX, _ := metricChartTemplateByID(mcHTTP5xx)
	p99, _ := metricChartTemplateByID(mcHTTPP99)
	kafka, _ := metricChartTemplateByID(mcKafkaLag)
	gc, _ := metricChartTemplateByID(mcJVMGC)
	heapSel := mcSel("", "jvm.memory.used")
	httpCount := mcSel("count", mcHTTPMetric)
	cases := []struct {
		name string
		in   metricChartQueryInput
		want string
	}{
		{"heap varsayılan etiketler, tek env, pod keşfi",
			metricChartQueryInput{Variant: heap.Variants[0], Service: "orders-api", SvcLabel: "service_name",
				EnvMatcher: `deployment_environment_name="prod-eu-a"`, By: []string{"k8s_pod_name"}, WinS: 120},
			`topk(20, sum by (k8s_pod_name) ({` + heapSel + `, service_name="orders-api", deployment_environment_name="prod-eu-a", jvm_memory_type="heap"}))`},
		{"heap özel LabelMap (servis=app, pod=pod_name), çoklu env",
			metricChartQueryInput{Variant: heap.Variants[1], Service: "orders-api", SvcLabel: "app",
				EnvMatcher: vmetrics.EnvMatcher("cluster_env", []string{"prod-eu-b", "prod-eu-a"}), By: []string{"pod_name"}, WinS: 120},
			`topk(20, sum by (pod_name) ({` + heapSel + `, app="orders-api", cluster_env=~"prod-eu-a|prod-eu-b", area="heap"}))`},
		{"heap kırılımsız, env yok",
			metricChartQueryInput{Variant: heap.Variants[0], Service: "orders-api", SvcLabel: "service_name", WinS: 60},
			`sum ({` + heapSel + `, service_name="orders-api", jvm_memory_type="heap"})`},
		{"5xx oranı servisli, kırılımsız",
			metricChartQueryInput{Variant: fiveXX.Variants[0], Service: "checkout", SvcLabel: "service_name", StatusLabel: "http_response_status_code", WinS: 120},
			`100 * (sum (rate({` + httpCount + `, service_name="checkout", http_response_status_code=~"5.."}[120s])) or 0 * sum (rate({` + httpCount + `, service_name="checkout"}[120s]))) / sum (rate({` + httpCount + `, service_name="checkout"}[120s]))`},
		{"5xx oranı filo, cluster bazında",
			metricChartQueryInput{Variant: fiveXX.Variants[0], By: []string{"deployment_environment_name"}, StatusLabel: "http_status_code", WinS: 60},
			`topk(20, 100 * (sum by (deployment_environment_name) (rate({` + httpCount + `, http_status_code=~"5.."}[60s])) or 0 * sum by (deployment_environment_name) (rate({` + httpCount + `}[60s]))) / sum by (deployment_environment_name) (rate({` + httpCount + `}[60s])))`},
		{"p99 histogram, kırılımsız",
			metricChartQueryInput{Variant: p99.Variants[0], Service: "checkout", SvcLabel: "service_name", WinS: 120},
			`histogram_quantile(0.99, sum by (le) (rate({` + mcSel("bucket", mcHTTPMetric) + `, service_name="checkout"}[120s])))`},
		{"p99 pod kırılımı",
			metricChartQueryInput{Variant: p99.Variants[0], Service: "checkout", SvcLabel: "service_name", By: []string{"k8s_pod_name"}, WinS: 120},
			`topk(20, histogram_quantile(0.99, sum by (le, k8s_pod_name) (rate({` + mcSel("bucket", mcHTTPMetric) + `, service_name="checkout"}[120s]))))`},
		{"kafka-exporter (Fleet): servis süzgeci YOK",
			metricChartQueryInput{Variant: kafka.Variants[1], Service: "orders-api", SvcLabel: "service_name", By: []string{"consumergroup", "topic"}, WinS: 60},
			`topk(20, max by (consumergroup, topic) ({` + mcSel("", "kafka_consumergroup_lag") + `}))`},
		{"gc sum parçası",
			metricChartQueryInput{Variant: gc.Variants[0], Service: "orders-api", SvcLabel: "service_name", WinS: 120},
			`100 * sum (rate({` + mcSel("sum", "jvm.gc.duration") + `, service_name="orders-api"}[120s]))`},
	}
	for _, c := range cases {
		if got := buildMetricChartPromQL(c.in); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
	// Yazım adayları gerçekten çoklu: OTel noktalı ad + VM yazımı birlikte.
	if !strings.Contains(heapSel, `jvm_memory_used_bytes`) || !strings.Contains(heapSel, `jvm\\.memory\\.used`) {
		t.Errorf("heap seçicisi yazım adaylarını taşımalı: %s", heapSel)
	}
	if !strings.Contains(httpCount, "http_server_request_duration_seconds_count") {
		t.Errorf("HTTP count seçicisi _seconds_count yazımını taşımalı: %s", httpCount)
	}
	// Servis değeri kaçışlanır (enjeksiyon yok).
	q := buildMetricChartPromQL(metricChartQueryInput{Variant: heap.Variants[0], Service: `x"} or vector(1) #`, SvcLabel: "service_name", WinS: 60})
	if !strings.Contains(q, `service_name="x\"} or vector(1) #"`) {
		t.Errorf("servis değeri kaçışlanmalı: %s", q)
	}
}

func TestPickMetricChartVariant(t *testing.T) {
	heap, _ := metricChartTemplateByID(mcJVMHeap)
	set := func(ls ...string) map[string]bool {
		m := map[string]bool{}
		for _, l := range ls {
			m[l] = true
		}
		return m
	}
	// OTel jvm_memory_type → varyant 0.
	v, _, ok := pickMetricChartVariant(heap, map[string]map[string]bool{"jvm.memory.used": set("service_name", "jvm_memory_type", "k8s_pod_name")})
	if !ok || v.Match != `jvm_memory_type="heap"` {
		t.Fatalf("OTel heap varyantı: %+v ok=%v", v, ok)
	}
	// micrometer area → varyant 1.
	v, _, ok = pickMetricChartVariant(heap, map[string]map[string]bool{"jvm.memory.used": set("area", "pod")})
	if !ok || v.Match != `area="heap"` {
		t.Fatalf("micrometer heap varyantı: %+v ok=%v", v, ok)
	}
	// Metrik yok (boş küme) → bulunamadı.
	if _, _, ok := pickMetricChartVariant(heap, map[string]map[string]bool{"jvm.memory.used": {}}); ok {
		t.Fatal("boş etiket kümesi = metrik yok")
	}
	// İlk aile yok, ikinci var.
	cpu, _ := metricChartTemplateByID(mcCPU)
	v, _, ok = pickMetricChartVariant(cpu, map[string]map[string]bool{"process.cpu.utilization": set("service_name")})
	if !ok || v.Probe != "process.cpu.utilization" {
		t.Fatalf("cpu ikinci aile: %+v ok=%v", v, ok)
	}
}

func TestMetricChartByLabels(t *testing.T) {
	heap, _ := metricChartTemplateByID(mcJVMHeap)
	pool, _ := metricChartTemplateByID(mcDBPool)
	rps, _ := metricChartTemplateByID(mcHTTPRPS)
	present := map[string]bool{"k8s_pod_name": true, "deployment_environment_name": true, "state": true}
	cases := []struct {
		name         string
		t            metricChartTemplate
		v            metricChartVariant
		groupBy, pod string
		env          string
		want         string
		wantNote     bool
	}{
		{"heap pod keşfedildi", heap, heap.Variants[0], "", "k8s_pod_name", "", "k8s_pod_name", false},
		{"heap pod yapılandırılmış ama metrikte yok", heap, heap.Variants[0], "", "pod_name", "", "", true},
		{"heap ortam bazında", heap, heap.Variants[0], "env", "k8s_pod_name", "deployment_environment_name", "deployment_environment_name", false},
		{"havuz kendi kırılımı (state)", pool, pool.Variants[1], "", "", "", "state", false},
		{"rps kırılımsız", rps, rps.Variants[0], "", "k8s_pod_name", "", "", false},
		{"rps pod bazında", rps, rps.Variants[0], "pod", "k8s_pod_name", "", "k8s_pod_name", false},
		{"rps ortam etiketi yok", rps, rps.Variants[0], "env", "", "cluster_env", "", true},
	}
	for _, c := range cases {
		by, note := metricChartByLabels(c.t, c.v, c.groupBy, c.pod, c.env, present)
		if strings.Join(by, ",") != c.want || (note != "") != c.wantNote {
			t.Errorf("%s: by=%v note=%q", c.name, by, note)
		}
	}
	if got := metricChartStatusLabel(map[string]bool{"http_status_code": true}); got != "http_status_code" {
		t.Errorf("eski durum etiketi: %q", got)
	}
	if got := metricChartStatusLabel(map[string]bool{"http_status_code": true, "http_response_status_code": true}); got != "http_response_status_code" {
		t.Errorf("semconv durum etiketi önce: %q", got)
	}
	if got := metricChartStatusLabel(map[string]bool{"service_name": true}); got != "" {
		t.Errorf("durum etiketi yok: %q", got)
	}
}

func TestMetricChartTriedNames(t *testing.T) {
	cpu, _ := metricChartTemplateByID(mcCPU)
	got := metricChartTriedNames(cpu, 50)
	joined := strings.Join(got, ",")
	for _, w := range []string{"jvm_cpu_recent_utilization", "process_cpu_utilization"} {
		if !strings.Contains(joined, w) {
			t.Errorf("denenen yazımlar %q içermeli: %v", w, got)
		}
	}
	if n := len(metricChartTriedNames(cpu, 3)); n != 3 {
		t.Errorf("tavan 3, %d", n)
	}
}
