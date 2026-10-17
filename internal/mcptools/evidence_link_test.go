package mcptools

// v0.10.1272 — CoSRE adım 3: kanıt çipleri + Explore'da aç.
//
// EvidenceHref tablo testi: her kaynak (ClickHouse traces, Elasticsearch/CH
// logs, VictoriaMetrics RED, ClickHouse span RED, jvm runtime, Thanos pod_*)
// AYNI sorguyu AYNI pencerede (+ env/cluster) açan href'e eşlenir; eşleme
// kurulamazsa "" (uydurma hedef yok). Href'ler mevcut üreticilerden geçer —
// beklenen değerler de onlardan değil, sayfanın okuduğu parametrelerden
// yazıldı (sözleşme pini).

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestEvidenceHref(t *testing.T) {
	const from, to = int64(1760600000000), int64(1760603600000)
	win := EvidenceWindow{FromMs: from, ToMs: to}
	rng := "custom:1760600000000-1760603600000"
	cases := []struct {
		name string
		tool string
		ev   cosreEvidence
		req  map[string]any
		// path + beklenen parametreler (tam küme; eksik/fazla param hata)
		wantPath   string
		wantParams map[string]string
	}{
		{
			name: "traces · ClickHouse · operation + error + minMs + slowest + env",
			tool: queryTracesName,
			ev:   cosreEvidence{Source: evSourceCH, Window: win, Env: "prod"},
			req: map[string]any{"service": "checkout", "operation": "POST /pay", "status": "error",
				"sort_by": "slowest", "min_duration_ms": 250.0, "env": "prod"},
			wantPath: "/traces",
			wantParams: map[string]string{
				"service": "checkout", "filters": `[{"k":"name","op":"=","v":["POST /pay"]}]`,
				"hasError": "true", "minMs": "250", "env": "prod", "sort": "duration", "order": "desc", "range": rng,
			},
		},
		{
			name:       "traces · varsayılanlar (recent, any)",
			tool:       queryTracesName,
			ev:         cosreEvidence{Source: evSourceCH, Window: win},
			req:        map[string]any{"service": "checkout", "status": "any", "sort_by": "recent"},
			wantPath:   "/traces",
			wantParams: map[string]string{"service": "checkout", "sort": "time", "order": "desc", "range": rng},
		},
		{
			name: "logs · Elasticsearch · error + metin + env + cluster",
			tool: queryLogsName,
			ev:   cosreEvidence{Source: evSourceES, Window: win, Env: "prod", Cluster: "ocp-a"},
			req: map[string]any{"service": "payments", "severity": "error", "text": "connection refused",
				"env": "prod", "cluster": "ocp-a"},
			wantPath: "/logs",
			wantParams: map[string]string{"service": "payments", "severity": "17", "q": "connection refused",
				"env": "prod", "cluster": "ocp-a", "range": rng},
		},
		{
			name:       "logs · ClickHouse · trace_id, warn",
			tool:       queryLogsName,
			ev:         cosreEvidence{Source: evSourceCH, Window: win},
			req:        map[string]any{"trace_id": "4bf92f3577b34da6a3ce929d0e0e4736", "severity": "warn"},
			wantPath:   "/logs",
			wantParams: map[string]string{"traceId": "4bf92f3577b34da6a3ce929d0e0e4736", "severity": "13", "range": rng},
		},
		{
			name:       "metrics · VictoriaMetrics RED → Overview metrik kipi",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Window: win, Env: "prod"},
			req:        map[string]any{"family": "latency", "service": "checkout", "env": "prod"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "checkout", "tab": "overview", "src": "metric", "env": "prod", "range": rng},
		},
		{
			name:       "metrics · ClickHouse span RED → Overview span kipi",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Window: win},
			req:        map[string]any{"family": "red", "service": "checkout", "group_by": "route"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "checkout", "tab": "overview", "src": "span", "range": rng},
		},
		{
			name:       "metrics · jvm_heap (VM) → Infrastructure",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceVM, QueryKind: evKindSummary, Window: win, Env: "uat"},
			req:        map[string]any{"family": "jvm_heap", "service": "orders"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "orders", "tab": "infra", "env": "uat", "range": rng},
		},
		{
			name:       "metrics · Thanos pod_cpu + servis → Infrastructure",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Window: win, Cluster: "ocp-a"},
			req:        map[string]any{"family": "pod_cpu", "service": "orders", "cluster": "ocp-a"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "orders", "tab": "infra", "range": rng},
		},
		{
			name:       "metrics · Thanos pod_memory servissiz → cluster pod listesi",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Window: win, Cluster: "ocp b"},
			req:        map[string]any{"family": "pod_memory", "cluster": "ocp b"},
			wantPath:   "/clusters",
			wantParams: map[string]string{"cluster": "ocp b", "section": "pods", "range": rng},
		},
		// v0.10.1306 — promql kanıtı sorgu metniyle → PromQL editörü.
		{
			name: "metrics · VictoriaMetrics promql → /metrics editör (ilk satır, src=vm)",
			tool: queryMetricsName,
			ev: cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Window: win, Env: "prod",
				Query: "\n  sum(rate(http_server_requests_total{service=\"checkout\"}[5m]))  \nsum(rate(http_server_errors_total[5m]))"},
			req:      map[string]any{"family": "red", "service": "checkout", "env": "prod"},
			wantPath: "/metrics",
			wantParams: map[string]string{"editor": "1", "tab": "promql", "src": "vm", "range": rng,
				"promql": `sum(rate(http_server_requests_total{service="checkout"}[5m]))`},
		},
		{
			name: "metrics · Thanos promql + cluster → /metrics editör (src=thanos&cluster=)",
			tool: queryMetricsName,
			ev: cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Window: win, Cluster: "ocp-a",
				Query: `sum by (pod) (rate(container_cpu_usage_seconds_total{cluster="ocp-a",namespace="shop"}[5m]))`},
			req:      map[string]any{"family": "pod_cpu", "service": "orders", "cluster": "ocp-a"},
			wantPath: "/metrics",
			wantParams: map[string]string{"editor": "1", "tab": "promql", "src": "thanos", "cluster": "ocp-a", "range": rng,
				"promql": `sum by (pod) (rate(container_cpu_usage_seconds_total{cluster="ocp-a",namespace="shop"}[5m]))`},
		},
		{
			name:       "metrics · Thanos promql cluster'sız → eski /service infra",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Window: win, Query: `up{job="x"}`},
			req:        map[string]any{"family": "pod_cpu", "service": "orders"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "orders", "tab": "infra", "range": rng},
		},
		{
			name:       "metrics · Thanos promql > 8 KiB → eski /service infra",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Window: win, Cluster: "ocp-a", Query: "up" + strings.Repeat(" ", 8191) + "+1"},
			req:        map[string]any{"family": "pod_cpu", "service": "orders", "cluster": "ocp-a"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "orders", "tab": "infra", "range": rng},
		},
		{
			name:       "metrics · VM promql ilk satırı > 8 KiB → eski Overview metrik kipi",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Window: win, Query: "sum(" + strings.Repeat("a", 8192) + ")"},
			req:        map[string]any{"family": "red", "service": "checkout"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "checkout", "tab": "overview", "src": "metric", "range": rng},
		},
		{
			name:       "metrics · VM summary türü (jvm) sorgu metniyle bile → Infrastructure",
			tool:       queryMetricsName,
			ev:         cosreEvidence{Source: evSourceVM, QueryKind: evKindSummary, Window: win, Query: "jvm_heap · VictoriaMetrics"},
			req:        map[string]any{"family": "jvm_heap", "service": "orders"},
			wantPath:   "/service",
			wantParams: map[string]string{"name": "orders", "tab": "infra", "range": rng},
		},
		// Kurulamayan eşlemeler: "" (çip tıklanmaz).
		{name: "traces servissiz", tool: queryTracesName, ev: cosreEvidence{Source: evSourceCH, Window: win}, req: map[string]any{}},
		{name: "logs servis/trace yok", tool: queryLogsName, ev: cosreEvidence{Source: evSourceES, Window: win}, req: map[string]any{"text": "x"}},
		{name: "metrics servissiz VM", tool: queryMetricsName, ev: cosreEvidence{Source: evSourceVM, Window: win}, req: map[string]any{"family": "red"}},
		{name: "Thanos servis+cluster yok", tool: queryMetricsName, ev: cosreEvidence{Source: evSourceThanos, Window: win}, req: map[string]any{"family": "pod_cpu"}},
		{name: "bozuk pencere", tool: queryTracesName, ev: cosreEvidence{Source: evSourceCH, Window: EvidenceWindow{FromMs: to, ToMs: from}}, req: map[string]any{"service": "a"}},
		{name: "bilinmeyen araç", tool: "search_traces", ev: cosreEvidence{Source: evSourceCH, Window: win}, req: map[string]any{"service": "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EvidenceHref(c.tool, c.ev, c.req)
			if c.wantPath == "" {
				if got != "" {
					t.Fatalf("href beklenmiyordu: %q", got)
				}
				return
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatalf("href çözülemedi %q: %v", got, err)
			}
			if u.Path != c.wantPath || u.Host != "" || u.Scheme != "" {
				t.Fatalf("yol %q (host %q), beklenen kök-göreli %q", u.Path, u.Host, c.wantPath)
			}
			q := u.Query()
			for k, v := range c.wantParams {
				if q.Get(k) != v {
					t.Errorf("%s = %q, beklenen %q (href %s)", k, q.Get(k), v, got)
				}
			}
			for k := range q {
				if _, ok := c.wantParams[k]; !ok {
					t.Errorf("beklenmeyen param %s=%q (href %s)", k, q.Get(k), got)
				}
			}
		})
	}
}

// Kodlama: değerdeki & / # / boşluk sorgu dizgesini bölmez (url.Values).
func TestEvidenceHrefEncodes(t *testing.T) {
	ev := cosreEvidence{Source: evSourceES, Window: EvidenceWindow{FromMs: 1000, ToMs: 2000}}
	got := EvidenceHref(queryLogsName, ev, map[string]any{"service": "a&b", "text": "x#y z"})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("service") != "a&b" || u.Query().Get("q") != "x#y z" || u.Fragment != "" {
		t.Fatalf("kodlama bozuk: %s", got)
	}
}

// v0.10.1306 — PromQL editör href'i: sorgudaki { } " \n & # + boşluk
// url.Values ile kodlanır; çözülen `promql` değeri sorgunun BAYT-AYNI
// hâli (Thanos'ta satır sonu korunur), href tek parça (fragment yok).
func TestEvidenceHrefPromQLEncodes(t *testing.T) {
	q := "sum by (pod) (\n  rate(x{a=\"b&c\", re=~\"d#e f\"}[5m])\n)"
	ev := cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Query: q, Cluster: "ocp b&c",
		Window: EvidenceWindow{FromMs: 1000, ToMs: 2000}}
	got := EvidenceHref(queryMetricsName, ev, map[string]any{"family": "pod_cpu"})
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/metrics" || u.Fragment != "" || u.Query().Get("promql") != q ||
		u.Query().Get("cluster") != "ocp b&c" || u.Query().Get("range") != "custom:1000-2000" {
		t.Fatalf("kodlama bozuk: %s", got)
	}
	// VM: çok satırlı Query → yalnız ilk dolu satır, kırpılmış.
	ev = cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Query: "  \n\t{__name__=\"a\"} \nb",
		Window: EvidenceWindow{FromMs: 1000, ToMs: 2000}}
	u, err = url.Parse(EvidenceHref(queryMetricsName, ev, map[string]any{"family": "red", "service": "s"}))
	if err != nil || u.Query().Get("promql") != `{__name__="a"}` || u.Query().Get("src") != "vm" || u.Query().Has("cluster") {
		t.Fatalf("VM ilk satır: %v %s", err, u)
	}
}

// ToolResultEvidence: aracın HAM JSON sonucundan kanıt + href; yanlış araç /
// kanıtsız sonuç / bozuk içerik → ok=false.
func TestToolResultEvidence(t *testing.T) {
	rows := 3
	res := cosreResult{
		Tool: queryTracesName, OK: true,
		Request:  map[string]any{"service": "checkout", "status": "error", "sort_by": "recent", "from_ms": 1000, "to_ms": 2000},
		Evidence: &cosreEvidence{Source: evSourceCH, Query: "SELECT 1", QueryKind: evKindSQL, Window: EvidenceWindow{FromMs: 1000, ToMs: 2000}, Rows: &rows, DurationMs: 42},
	}
	raw, _ := json.Marshal(res)
	ev, ok := ToolResultEvidence(queryTracesName, string(raw))
	if !ok {
		t.Fatal("kanıt çözülmedi")
	}
	if ev.Tool != queryTracesName || ev.Source != evSourceCH || ev.Query != "SELECT 1" || ev.QueryKind != evKindSQL ||
		ev.Rows == nil || *ev.Rows != 3 || ev.DurationMs != 42 || ev.Window.FromMs != 1000 {
		t.Fatalf("alanlar taşınmadı: %+v", ev)
	}
	if ev.Href != "/traces?hasError=true&order=desc&range=custom%3A1000-2000&service=checkout&sort=time" {
		t.Fatalf("href %q", ev.Href)
	}
	for name, c := range map[string][2]string{
		"başka araç":       {"search_traces", string(raw)},
		"araç adı uyuşmaz": {queryLogsName, string(raw)},
		"kanıtsız":         {queryTracesName, `{"tool":"query_traces","ok":false,"request":{}}`},
		"bozuk":            {queryTracesName, `not json`},
	} {
		if _, ok := ToolResultEvidence(c[0], c[1]); ok {
			t.Errorf("%s: ok=false bekleniyordu", name)
		}
	}
}
