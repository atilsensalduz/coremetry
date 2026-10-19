package api

// v0.10.1376 (CoSRE küme kıyası, RED metrikten) — adlar SENTETİK.
//
// SÖZLEŞME:
//   - VM yalnız tek servis + http_route hedefinde; yol-yalnız / span adı hedefi
//     notla trace'te kalır; VM yoksa not yok.
//   - üç anlık sorgu frontend windowQueries ile bayt bayt aynı şekil (altın:
//     frontend/src/lib/httpMetrics.envScope.golden.json); seçici sırası
//     service_name, ortam, http_route, http_request_method (yalnız soruda fiil
//     varsa).
//   - parse: eşlenen tüm değerler toplanır; durum etiketi ortamın serilerinde
//     boş-olmayan ilk aday; hiçbiri yok → ErrorsUnknown (0 DEĞİL); NaN yüzdelik
//     → 0; seri yok → Measured=false.
//   - hiç ortam çözülmezse sorgu atılmaz; okuma hatası → err (çağıran trace'e
//     düşer).

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

const ccEnvGolden = "../../frontend/src/lib/httpMetrics.envScope.golden.json"

func TestClusterVMPlan_v0101376(t *testing.T) {
	rt := opTarget{HasRoute: true, Route: "/shop/orders/{id}", Endpoint: true}
	for _, c := range []struct {
		name string
		t    opTarget
		svc  string
		vmOK bool
		ok   bool
		note string
	}{
		{"rota + servis + VM", rt, "orders-api-prod", true, true, ""},
		{"VM yok", rt, "orders-api-prod", false, false, ""},
		{"servis yok", rt, "", true, false, ""},
		{"yol-yalnız", opTarget{SearchPath: "/shop/orders/detail", Endpoint: true}, "orders-api-prod", true, false, clusterVMNoRouteNote},
		{"span adı", opTarget{HasSpan: true, Name: "GET /shop/x", Endpoint: true}, "orders-api-prod", true, false, clusterVMNoRouteNote},
	} {
		ok, note := clusterVMPlan(c.t, c.svc, c.vmOK)
		if ok != c.ok || note != c.note {
			t.Errorf("%s: (%v, %q)", c.name, ok, note)
		}
	}
}

// ccGoldenCase — altın dosyanın bir vakası (frontend v0.10.1377 aynı dosyayı
// windowQueries/serverSel ile doğrular).
type ccGoldenCase struct {
	Name      string   `json:"name"`
	App       string   `json:"app"`
	Route     string   `json:"route"`
	Method    string   `json:"method"`
	EnvLabel  string   `json:"envLabel"`
	EnvValues []string `json:"envValues"`
	WindowSec float64  `json:"windowSec"`
	EnvSel    string   `json:"envSel"`
	clusterVMQuerySet
}

// TestHTTPEnvSelectorMatchesFrontend_v0101376 — çapraz dil pini: Go üreticisi
// altın dosyayla bayt bayt aynı (TS tarafı v0.10.1377'de aynı dosyayı okur).
func TestHTTPEnvSelectorMatchesFrontend_v0101376(t *testing.T) {
	var g struct {
		Comment string         `json:"_comment"`
		Cases   []ccGoldenCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, ccEnvGolden)), &g); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		for i, c := range g.Cases {
			g.Cases[i].EnvSel = vmetrics.EnvMatcher(c.EnvLabel, c.EnvValues)
			g.Cases[i].clusterVMQuerySet = clusterVMQueries(c.App, c.Route, c.Method, c.EnvLabel, c.EnvValues, c.WindowSec)
		}
		b, _ := json.MarshalIndent(g, "", "  ")
		if err := os.WriteFile(ccEnvGolden, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.Cases) < 4 {
		t.Fatalf("altın vakalar: %d", len(g.Cases))
	}
	for _, c := range g.Cases {
		if got := vmetrics.EnvMatcher(c.EnvLabel, c.EnvValues); got != c.EnvSel {
			t.Errorf("%s envSel:\n got %s\nwant %s", c.Name, got, c.EnvSel)
		}
		if got := clusterVMQueries(c.App, c.Route, c.Method, c.EnvLabel, c.EnvValues, c.WindowSec); got != c.clusterVMQuerySet {
			t.Errorf("%s:\n got %+v\nwant %+v", c.Name, got, c.clusterVMQuerySet)
		}
	}
}

func TestClusterVMQueries_v0101376(t *testing.T) {
	q := clusterVMQueries("orders-api-prod", "/shop/orders/{id}", "", "deployment_environment_name", []string{"prod-eu-a", "prod-tr-c"}, 3*3600)
	sel := `{service_name="orders-api-prod", deployment_environment_name=~"prod-eu-a|prod-tr-c", http_route="/shop/orders/{id}"}`
	want := clusterVMQuerySet{
		Sel:        sel,
		TotalBy:    "deployment_environment_name, http_response_status_code, http_status_code",
		QuantileBy: "deployment_environment_name",
		Total:      "sum by (deployment_environment_name, http_response_status_code, http_status_code) (increase(http_server_request_duration_seconds_count" + sel + "[10800s]))",
		P50:        "histogram_quantile(0.5, sum by (le, deployment_environment_name) (increase(http_server_request_duration_seconds_bucket" + sel + "[10800s])))",
		P95:        "histogram_quantile(0.95, sum by (le, deployment_environment_name) (increase(http_server_request_duration_seconds_bucket" + sel + "[10800s])))",
	}
	if q != want {
		t.Fatalf("sorgular:\n got %+v\nwant %+v", q, want)
	}
	// Fiil yalnız verilirse; pencere ≥60 s.
	q = clusterVMQueries("orders-api-prod", "/shop/x", "GET", "deployment_environment_name", []string{"prod-eu-a"}, 12)
	if q.Sel != `{service_name="orders-api-prod", deployment_environment_name="prod-eu-a", http_route="/shop/x", http_request_method="GET"}` || !strings.Contains(q.Total, "[60s]") {
		t.Fatalf("fiil + taban pencere: %+v", q)
	}
}

// ccSeries — anlık seri (GroupKey = vmetrics labelSetGroupKey biçimi).
func ccSeries(v float64, kv ...string) chstore.SpanMetricSeries {
	var gk []string
	for i := 0; i+1 < len(kv); i += 2 {
		gk = append(gk, kv[i]+"="+`"`+kv[i+1]+`"`)
	}
	return chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Time: ccTo.UnixNano(), Value: v}}}
}

const ccEL = "deployment_environment_name"

func TestClusterVMParse_v0101376(t *testing.T) {
	total := []chstore.SpanMetricSeries{
		// eu-a: iki değer (önek belirsizliği), yeni durum etiketi.
		ccSeries(1000.4, ccEL, "prod-eu-a", "http_response_status_code", "200"),
		ccSeries(10, ccEL, "prod-eu-a", "http_response_status_code", "503"),
		ccSeries(5, ccEL, "prod-eu-a", "http_response_status_code", "404"),
		ccSeries(200, ccEL, "dr-eu-a", "http_response_status_code", "200"),
		ccSeries(2.2, ccEL, "dr-eu-a", "http_response_status_code", "500"),
		// tr-c: eski durum etiketi.
		ccSeries(300, ccEL, "prod-tr-c", "http_status_code", "200"),
		ccSeries(4, ccEL, "prod-tr-c", "http_status_code", "502"),
		// eu-b: durum etiketi yok.
		ccSeries(77, ccEL, "prod-eu-b"),
		// NaN çağrı serisi sayılmaz.
		ccSeries(math.NaN(), ccEL, "prod-eu-c"),
	}
	p50 := []chstore.SpanMetricSeries{
		ccSeries(0.12, ccEL, "prod-eu-a"), ccSeries(0.2, ccEL, "dr-eu-a"),
		ccSeries(math.NaN(), ccEL, "prod-tr-c"), ccSeries(0.05, ccEL, "prod-eu-b"),
	}
	p95 := []chstore.SpanMetricSeries{
		ccSeries(0.48, ccEL, "prod-eu-a"), ccSeries(0.3, ccEL, "dr-eu-a"),
		ccSeries(2.1, ccEL, "prod-tr-c"), ccSeries(0.09, ccEL, "prod-eu-b"),
	}
	matches := []vmetrics.EnvMatch{
		{Want: "eu-a", Values: []string{"dr-eu-a", "prod-eu-a"}, Tier: vmetrics.EnvTierPrefix},
		{Want: "tr-c", Values: []string{"prod-tr-c"}, Tier: vmetrics.EnvTierPrefix},
		{Want: "eu-b", Values: []string{"prod-eu-b"}, Tier: vmetrics.EnvTierPrefix},
		{Want: "eu-c", Values: []string{"prod-eu-c"}, Tier: vmetrics.EnvTierPrefix},
		{Want: "us-x", Tier: vmetrics.EnvTierNone},
	}
	got := clusterVMParse(ccEL, total, p50, p95, matches)
	want := map[string]clusterVMRow{
		"eu-a": {RED: chstore.OpRED{Calls: 1218, Errors: 12, P50Ms: 200, P95Ms: 480, Measured: true}},
		"tr-c": {RED: chstore.OpRED{Calls: 304, Errors: 4, P50Ms: 0, P95Ms: 2100, Measured: true}},
		"eu-b": {RED: chstore.OpRED{Calls: 77, P50Ms: 50, P95Ms: 90, Measured: true}, ErrorsUnknown: true},
		"eu-c": {},
	}
	if len(got) != len(want) {
		t.Fatalf("satırlar: %+v", got)
	}
	for k, w := range want {
		g := got[k]
		if g.ErrorsUnknown != w.ErrorsUnknown || g.RED.Calls != w.RED.Calls || g.RED.Errors != w.RED.Errors || g.RED.Measured != w.RED.Measured ||
			math.Abs(g.RED.P50Ms-w.RED.P50Ms) > 1e-6 || math.Abs(g.RED.P95Ms-w.RED.P95Ms) > 1e-6 {
			t.Errorf("%s: %+v, want %+v", k, g, w)
		}
	}
	if _, ok := got["us-x"]; ok {
		t.Error("çözülmeyen ortamın satırı yok")
	}
	// Kaçışlı değer GroupKey'den geri okunur.
	if m := parseVMGroupKey([]string{`a="x\"y\\z"`, "bozuk"}); m["a"] != `x"y\z` || len(m) != 1 {
		t.Errorf("parseVMGroupKey: %v", m)
	}
}

func TestClusterVMRED_v0101376(t *testing.T) {
	from := ccTo.Add(-3 * time.Hour)
	r := &fakeVMReader{
		values: map[string][]string{ccEL: {"prod-eu-a", "prod-tr-c", "prod-eu-b"}},
		total: []chstore.SpanMetricSeries{
			ccSeries(1234, ccEL, "prod-eu-a", "http_response_status_code", "200"),
			ccSeries(310, ccEL, "prod-tr-c", "http_response_status_code", "200"),
		},
		p50: []chstore.SpanMetricSeries{ccSeries(0.12, ccEL, "prod-eu-a"), ccSeries(0.61, ccEL, "prod-tr-c")},
		p95: []chstore.SpanMetricSeries{ccSeries(0.48, ccEL, "prod-eu-a"), ccSeries(2.1, ccEL, "prod-tr-c")},
	}
	tg := opTarget{HasRoute: true, Route: "/shop/orders/{id}", Method: "POST", Endpoint: true} // fiil span adından: süzgece girmez
	res, err := (&Server{}).clusterVMRED(context.Background(), r, tg, "orders-api-prod", []string{"eu-a", "us-x", "tr-c"}, from, ccTo)
	if err != nil || res.Resolved != 2 || res.Label != ccEL || res.LabelSource != httpEnvSourceDefault {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.queries) != 3 {
		t.Fatalf("üç anlık sorgu: %v", r.queries)
	}
	for i, q := range r.queries {
		if strings.Contains(q, "http_request_method") || !strings.Contains(q, `deployment_environment_name=~"prod-eu-a|prod-tr-c"`) || !strings.Contains(q, "[10800s]") || !r.ats[i].Equal(ccTo) {
			t.Errorf("sorgu %d: %s @%v", i, q, r.ats[i])
		}
	}
	if !reflect.DeepEqual(res.Matches[1], vmetrics.EnvMatch{Want: "us-x", Tier: vmetrics.EnvTierNone}) || res.Rows["eu-a"].RED.Calls != 1234 {
		t.Fatalf("eşleme / satır: %+v", res)
	}
	// Soruda fiil varsa süzgeçte.
	r.queries = nil
	tg.MethodAsked = true
	if _, err := (&Server{}).clusterVMRED(context.Background(), r, tg, "orders-api-prod", []string{"eu-a"}, from, ccTo); err != nil || !strings.Contains(r.queries[0], `http_request_method="POST"`) {
		t.Fatalf("fiil süzgeci: %v %v", r.queries, err)
	}
	// Hiç ortam çözülmedi → sorgu yok.
	r.queries = nil
	if res, err := (&Server{}).clusterVMRED(context.Background(), r, tg, "orders-api-prod", []string{"us-x", "us-y"}, from, ccTo); err != nil || res.Resolved != 0 || len(r.queries) != 0 {
		t.Fatalf("çözülmeyen: %+v %v %v", res, err, r.queries)
	}
	// Okuma hataları err döner.
	if _, err := (&Server{}).clusterVMRED(context.Background(), &fakeVMReader{valuesErr: errors.New("boom")}, tg, "orders-api-prod", []string{"eu-a"}, from, ccTo); err == nil {
		t.Fatal("etiket okuma hatası err")
	}
	r.queryErr = context.DeadlineExceeded
	if _, err := (&Server{}).clusterVMRED(context.Background(), r, tg, "orders-api-prod", []string{"eu-a"}, from, ccTo); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("sorgu hatası err: %v", err)
	}
}

// ccMetricLike — ccProdLike'ın metrik kaynaklı hâli: eu-a önek eşleşmesi,
// eu-b metrikte yok, tr-c birebir.
func ccMetricLike() clusterCompareEvidence {
	ev := ccProdLike()
	ev.Source, ev.EnvLabel = clusterSourceMetric, ccEL
	ev.Envs[0].MetricValues = []string{"prod-eu-a"}
	ev.Envs[1].MetricValues = nil
	ev.Envs[2].MetricValues = []string{"tr-c"}
	return ev
}

func TestRenderClusterCompareTR_MetricSource_v0101376(t *testing.T) {
	got := renderClusterCompareTR(ccMetricLike())
	head := "KÜME KIYASI — GET /shop/orders/detail · orders-api-prod · son 3sa (09:00 – 12:00 UTC) · kaynak: metrik (VM, http_server_request_duration_seconds, deployment_environment_name)\n" +
		"- eu-a (metrik: prod-eu-a): 1.234 çağrı · hata %0,1 · p50 120 ms · p95 480 ms\n" +
		"- eu-b: metrikte eşleşen ortam değeri yok\n" +
		"- tr-c: 310 çağrı · hata %0,3 · p50 610 ms · p95 2,1 s ← en yavaş (p95 ×4,4 / eu-a)\n" +
		"En yavaş trace'ler (küme · trace · süre · şekil):\n"
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, ccRuleLine) {
		t.Fatalf("metrik başlığı:\n%s\nwant prefix:\n%s", got, head)
	}
	// Durum etiketi yok → hata bilinmiyor (0 değil); belirsiz eşleşme görünür.
	ev := ccMetricLike()
	ev.Envs[0].ErrorsUnknown, ev.Envs[0].RED.Errors = true, 0
	ev.Envs[0].MetricValues = []string{"dr-eu-a", "prod-eu-a"}
	got = renderClusterCompareTR(ev)
	if !strings.Contains(got, "- eu-a (metrik: dr-eu-a, prod-eu-a): 1.234 çağrı · hata bilinmiyor (durum etiketi yok) · p50 120 ms · p95 480 ms\n") {
		t.Fatalf("hata bilinmiyor satırı:\n%s", got)
	}
	// Eşlenmiş ama pencerede çağrı yok.
	ev.Envs[0].RED = chstore.OpRED{Measured: true}
	if got = renderClusterCompareTR(ev); !strings.Contains(got, "- eu-a (metrik: dr-eu-a, prod-eu-a): veri yok (bu pencerede)\n") {
		t.Fatalf("veri yok satırı:\n%s", got)
	}
	// Zorunlu düşüş: trace başlığı + not; iki kaynak karışmaz (metrik değeri yok).
	ev = ccProdLike()
	ev.Source, ev.SourceNote = clusterSourceTrace, "VM okunamadı: timeout"
	got = renderClusterCompareTR(ev)
	if !strings.HasPrefix(got, "KÜME KIYASI — GET /shop/orders/detail · orders-api-prod · son 3sa (09:00 – 12:00 UTC)"+ccTraceHeader+" (VM okunamadı: timeout)\n") ||
		strings.Contains(got, "metrik:") || strings.Contains(got, "metrikte eşleşen") {
		t.Fatalf("düşüş başlığı:\n%s", got)
	}
	// JSON: kaynak alanları yükte; trace kaynağında metricValues yok.
	b, _ := json.Marshal(ccMetricLike())
	for _, w := range []string{`"source":"metric"`, `"envLabel":"deployment_environment_name"`, `"metricValues":["prod-eu-a"]`} {
		if !strings.Contains(string(b), w) {
			t.Errorf("JSON %s içermeli: %s", w, b)
		}
	}
}

// TestClusterCompareVMUnderModelClamp — azami metrik blok (6 ortam, her biri
// iki uzun metrik değerine eşlenmiş, durum bilinmiyor) model tavanının ALTINDA.
func TestClusterCompareVMUnderModelClamp(t *testing.T) {
	long := strings.Repeat("Ş", 200)
	maxShape := traceShapeStats{Spans: 9999, WallNs: 9e9, DB: shapeShare{Count: 9000, SumNs: 8e9, Share: 0.9},
		Client: shapeShare{Count: 900, SumNs: 5e8, Share: 0.05}, DistinctPeers: 99,
		Repeats: []shapeRepeat{{Name: long, Count: 4000, AvgNs: 2e6}, {Name: long, Count: 3000, AvgNs: 2e6}, {Name: long, Count: 2000, AvgNs: 2e6}, {Name: long, Count: 1000, AvgNs: 2e6}}}
	ev := clusterCompareEvidence{Label: "POST /" + strings.Repeat("segment/", 25) + "detail", Service: "orders-api-prod-" + strings.Repeat("x", 40),
		RangeS: 6 * 3600, Clamped: true, From: ccTo.Add(-6 * time.Hour), To: ccTo, Source: clusterSourceMetric, EnvLabel: strings.Repeat("l", 40)}
	for i := 0; i < envSetMax; i++ {
		env := "env-" + strings.Repeat("e", 30) + string(rune('a'+i))
		rows := []chstore.TraceRow{{TraceID: ccTraceA, DurationMs: 99999, HasError: true}, {TraceID: ccTraceC, DurationMs: 88888, HasError: true}}
		red := chstore.OpRED{Calls: 999999999, Errors: 99999999, P50Ms: 99999, P95Ms: 999999 + float64(i), Measured: true}
		var e clusterEnvEvidence
		if i < clusterDrillEnvMax {
			e = ccEnv(env, red, rows, maxShape, maxShape)
		} else {
			e = ccEnv(env, red, rows)
		}
		e.MetricValues, e.ErrorsUnknown = []string{"prod-" + env, "dr-" + env}, true
		ev.Envs = append(ev.Envs, e)
	}
	txt := renderClusterCompareTR(ev)
	if n := utf8.RuneCountInString(txt); n >= chatToolResultMaxRunes {
		t.Fatalf("metrik küme kıyası metni %d rune, tavan %d", n, chatToolResultMaxRunes)
	}
	if _, cut := clampToolResultForModel(txt); cut {
		t.Fatal("kırpıldı")
	}
}

// TestClusterCompareVMWiring — v0.10.1376 kablolama pini: clusterCompareEvidence
// VM kaynağını span RED sorgusundan ÖNCE sorar, VM okuması span sorgusunun
// goroutine'inde ve ondan önce; 6 sa tavanı iki kaynakta aynı (cfrom).
func TestClusterCompareVMWiring(t *testing.T) {
	src := readRepoFile(t, "chat_cluster_compare.go")
	body := funcBody(src, "clusterCompareEvidence")
	i, j, k := strings.Index(body, "s.metricSource().(vmMetricSource)"), strings.Index(body, "s.clusterVMRED(cctx, vm, t, svc, t.Envs, cfrom, to)"), strings.Index(body, "s.store.RouteREDByEnv(")
	if i < 0 || j < 0 || k < 0 || !(i < j && j < k) {
		t.Fatalf("VM → span sırası bozuk (%d %d %d)", i, j, k)
	}
	if strings.Count(src, "clusterWindow(from, to)") != 2 {
		t.Fatal("6 sa tavanı başlatma ve okuma için aynı yardımcıdan")
	}
}
