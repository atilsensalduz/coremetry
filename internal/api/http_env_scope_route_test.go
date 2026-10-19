package api

// v0.10.1377 — GET /api/metrics/http-env-scope + /metric-red'in VM env
// süzgeci (adlar SENTETİK). SÖZLEŞME: uç yalnız VM'de ve en az bir terimle;
// çözüm 60 sn önbellekli, anahtar TÜM girdileri taşır; etiket okuma hatası
// önbelleğe yazılmaz ve 200 {applied:false, failed:true} döner. /metric-red
// VM'de env'i AYNI çözücüyle süzer (EnvAmbiguous yalnız çözülemeyince).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestHTTPEnvScopeRouteRegistered_v0101377(t *testing.T) {
	if _, ok := extraRouteRegistrars["http-env-scope"]; !ok {
		t.Fatal("http-env-scope defterde değil")
	}
	src := readRepoFile(t, "http_env_scope_route.go")
	// Kayıt satırı parça parça aranır (make audit CHECK 7 test dosyasını da tarar).
	if !strings.Contains(src, "mux.HandleFunc(") || !strings.Contains(src, `"GET /api/metrics/http-env-scope", s.getHTTPEnvScope)`) {
		t.Fatal("rota metot önekli ve kapısız (viewer görür) kayıtlı olmalı")
	}
	if strings.Contains(readRepoFile(t, "api.go"), "http-env-scope") {
		t.Fatal("api.go büyümez")
	}
}

func TestHTTPEnvScopeRouteBadRequests_v0101377(t *testing.T) {
	s := &Server{}
	cases := []struct{ url, want string }{
		{"/api/metrics/http-env-scope", "env ya da cluster parametresi zorunlu"},
		{"/api/metrics/http-env-scope?env=%20&cluster=", "env ya da cluster parametresi zorunlu"},
		{"/api/metrics/http-env-scope?env=" + strings.Repeat("x", 201), "en çok 200"},
		// VM yapılandırılmamış → kaynak ClickHouse → 400 (yüzeyler metricsrc=vm'e sabit).
		{"/api/metrics/http-env-scope?env=eu-a", "yalnız VictoriaMetrics kaynağında (metricsrc=vm)"},
		{"/api/metrics/http-env-scope?cluster=eu-a&metricsrc=ch", "yalnız VictoriaMetrics kaynağında (metricsrc=vm)"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		s.getHTTPEnvScope(w, httptest.NewRequest(http.MethodGet, c.url, nil))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), c.want) {
			t.Errorf("%s: %d %s", c.url, w.Code, w.Body.String())
		}
	}
}

func TestHTTPEnvScopeKey_v0101377(t *testing.T) {
	k0 := httpEnvScopeKey(vmetrics.LabelMap{}, "eu-a", "", ccTo)
	diff := map[string]string{
		"env":         httpEnvScopeKey(vmetrics.LabelMap{}, "eu-b", "", ccTo),
		"cluster":     httpEnvScopeKey(vmetrics.LabelMap{}, "eu-a", "eu-a", ccTo),
		"env↔cluster": httpEnvScopeKey(vmetrics.LabelMap{}, "", "eu-a", ccTo),
		"lm.Env":      httpEnvScopeKey(vmetrics.LabelMap{Env: "environment"}, "eu-a", "", ccTo),
		"lm.Cluster":  httpEnvScopeKey(vmetrics.LabelMap{Cluster: "k8s_cluster_name"}, "eu-a", "", ccTo),
		"to (10 dk)":  httpEnvScopeKey(vmetrics.LabelMap{}, "eu-a", "", ccTo.Add(10*time.Minute)),
	}
	for name, k := range diff {
		if k == k0 {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	// Ayraç kayması çakışmaz: ("a|b","c") ≠ ("a","b|c").
	if httpEnvScopeKey(vmetrics.LabelMap{}, "a|b", "c", ccTo) == httpEnvScopeKey(vmetrics.LabelMap{}, "a", "b|c", ccTo) {
		t.Fatal("serbest metinler ayrı özetlenmeli")
	}
	// Aynı 10 dk dilimi → aynı anahtar (değer penceresi de aynı).
	if httpEnvScopeKey(vmetrics.LabelMap{}, "eu-a", "", ccTo.Add(9*time.Minute)) != k0 {
		t.Fatal("10 dk dilimi içinde anahtar sabit olmalı")
	}
	if !reflect.DeepEqual(httpEnvScopeTerms("eu-a", ""), map[string]string{"env": "eu-a"}) ||
		!reflect.DeepEqual(httpEnvScopeTerms("eu-a", "prod-eu-a"), map[string]string{"env": "eu-a", "cluster": "prod-eu-a"}) {
		t.Fatal("terimler")
	}
}

func TestServeHTTPEnvScope_v0101377(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(16), stats: newCacheStats()}
	r := &fakeVMReader{values: map[string][]string{"deployment_environment_name": {"prod-eu-a", "dr-eu-a", "prod-tr-c"}}}
	get := func(rd httpEnvLabelReader, env, cluster string) (int, httpEnvScope) {
		w := httptest.NewRecorder()
		s.serveHTTPEnvScope(w, httptest.NewRequest(http.MethodGet, "/api/metrics/http-env-scope", nil), rd, env, cluster, ccTo)
		var sc httpEnvScope
		if err := json.Unmarshal(w.Body.Bytes(), &sc); err != nil {
			t.Fatalf("gövde: %v %s", err, w.Body.String())
		}
		return w.Code, sc
	}
	code, sc := get(r, "tr-c", "")
	if code != 200 || !sc.Applied || sc.Matcher != `deployment_environment_name="prod-tr-c"` || sc.Label != "deployment_environment_name" ||
		sc.Source != "default" || len(sc.Terms) != 1 || sc.Terms[0].Tier != vmetrics.EnvTierPrefix {
		t.Fatalf("uygulandı: %d %+v", code, sc)
	}
	calls := len(r.labelCalls)
	if _, again := get(r, "tr-c", ""); !reflect.DeepEqual(again, sc) || len(r.labelCalls) != calls {
		t.Fatalf("ikinci istek önbellekten: %+v (%d→%d çağrı)", again, calls, len(r.labelCalls))
	}
	// Belirsizlik → iki değer birlikte; env ∧ cluster kesişimi.
	if _, sc := get(r, "eu-a", ""); sc.Matcher != `deployment_environment_name=~"dr-eu-a|prod-eu-a"` {
		t.Fatalf("belirsiz: %+v", sc)
	}
	if _, sc := get(r, "eu-a", "prod-eu-a"); !sc.Applied || sc.Matcher != `deployment_environment_name="prod-eu-a"` {
		t.Fatalf("kesişim: %+v", sc)
	}
	// Çözülemeyen ad → 200, applied=false, gerekçe ilk çözülmeyen terimi adlandırır.
	if code, sc := get(r, "us-x", ""); code != 200 || sc.Applied || sc.Failed ||
		sc.Reason != `metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)` {
		t.Fatalf("çözülmedi: %d %+v", code, sc)
	}
	// Okuma hatası → 200 + failed; önbelleğe YAZILMAZ (ikinci istek yeniden okur).
	// Taze sunucu: değer önbelleği (etiket + uç anahtarlı) yukarıdan dolu.
	s = &Server{cache: &fakeCache{}, l1: newL1Cache(16), stats: newCacheStats()}
	bad := &fakeVMReader{valuesErr: errors.New("dial tcp 10.0.0.1:8428: connection refused")}
	for i := 0; i < 2; i++ {
		code, sc := get(bad, "eu-b", "")
		if code != 200 || sc.Applied || !sc.Failed || !strings.HasPrefix(sc.Reason, "VM etiket sorgusu başarısız: ") ||
			strings.Contains(sc.Reason, "10.0.0.1") {
			t.Fatalf("tur %d hata gövdesi: %d %+v", i, code, sc)
		}
	}
	if len(bad.labelCalls) != 2 {
		t.Fatalf("hata önbelleğe yazılmamalı: %d okuma", len(bad.labelCalls))
	}
}

func TestHTTPEnvScopeFilters_v0101377(t *testing.T) {
	sc := httpEnvScope{Applied: true, Terms: []httpEnvTerm{
		{Role: "env", Want: "eu-a", Label: "deployment_environment_name", Values: []string{"dr-eu-a", "prod-eu-a"}, Tier: "prefix"},
		{Role: "cluster", Want: "prod-eu-a", Label: "deployment_environment_name", Values: []string{"prod-eu-a"}, Tier: "exact"},
	}}
	want := []chstore.FilterExpr{{Key: "deployment_environment_name", Op: "=", Values: []string{"prod-eu-a"}}}
	if got := httpEnvScopeFilters(sc); !reflect.DeepEqual(got, want) {
		t.Fatalf("aynı etiket → kesişim: %+v", got)
	}
	sc.Terms[1].Label = "k8s_cluster_name"
	sc.Terms[1].Values = []string{"eu-a-k8s"}
	want = []chstore.FilterExpr{
		{Key: "deployment_environment_name", Op: "=", Values: []string{"dr-eu-a", "prod-eu-a"}},
		{Key: "k8s_cluster_name", Op: "=", Values: []string{"eu-a-k8s"}},
	}
	if got := httpEnvScopeFilters(sc); !reflect.DeepEqual(got, want) {
		t.Fatalf("ayrı etiket → iki ifade: %+v", got)
	}
	sc.Applied = false
	if got := httpEnvScopeFilters(sc); got != nil {
		t.Fatalf("uygulanmamış kapsam süzgeç üretmez: %+v", got)
	}
}

// fakeVMRedSource — metricSource (fakeEPSource) + VM etiket okuyucusu.
type fakeVMRedSource struct {
	*fakeEPSource
	*fakeVMReader
}

func TestServiceMetricREDEnvScopeOnVM_v0101377(t *testing.T) {
	from, to := epFixtureWindow()
	id := httpMetricIdentity{Metric: "http_server_request_duration_seconds_count", RTMetric: "http_server_request_duration_seconds", Instrument: "histogram", Service: "orders-api-prod", MatchedBy: "service_name"}
	live := map[string][]string{"deployment_environment_name": {"prod-eu-a", "prod-tr-c"}}
	s := &Server{}

	// CH kaynağı (etiket okuyucusu yok) → nil kapsam, eski yol.
	if sc := s.serviceMetricREDEnvScope(context.Background(), redFixtureSource("s", nil), "eu-a", to); sc != nil {
		t.Fatalf("CH'de kapsam çözülmez: %+v", sc)
	}
	src := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{values: live}}
	if sc := s.serviceMetricREDEnvScope(context.Background(), src, "", to); sc != nil {
		t.Fatal("env yokken kapsam çözülmez")
	}

	// Çözüldü → süzgeç her sorguda, EnvAmbiguous=false, not etiketi söyler.
	p := serviceMetricREDPlan{Service: "orders-api-prod", From: from, To: to, Mdp: 300, RateWin: 180, Env: "eu-a"}
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src, p.Env, p.To)
	resp, err := buildServiceMetricRED(context.Background(), src, id, p)
	if err != nil {
		t.Fatal(err)
	}
	wantF := chstore.FilterExpr{Key: "deployment_environment_name", Op: "=", Values: []string{"prod-eu-a"}}
	for i, fs := range src.filters {
		if len(fs) != 1 || !reflect.DeepEqual(fs[0], wantF) {
			t.Fatalf("rate sorgusu %d env süzgeci taşımalı: %+v", i, fs)
		}
	}
	for _, q := range src.fakeEPSource.queries {
		if len(q.Filters) != 1 || !reflect.DeepEqual(q.Filters[0], wantF) {
			t.Fatalf("yüzdelik sorgusu env süzgeci taşımalı: %+v", q.Filters)
		}
	}
	if resp.EnvAmbiguous || resp.EnvScope == nil || !resp.EnvScope.Applied ||
		!strings.Contains(resp.Note, `env süzgeci metrikte deployment_environment_name="prod-eu-a" ile uygulandı.`) ||
		strings.Contains(resp.Note, "TÜM ortamları") {
		t.Fatalf("uygulandı: amb=%v not=%s", resp.EnvAmbiguous, resp.Note)
	}

	// Çözülmedi → süzgeç YOK, EnvAmbiguous=true, not gerekçeyi söyler.
	src2 := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{values: live}}
	p.Env = "us-x"
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src2, p.Env, p.To)
	resp, err = buildServiceMetricRED(context.Background(), src2, id, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, fs := range src2.filters {
		if len(fs) != 0 {
			t.Fatalf("çözülmeyen env süzgeç eklememeli: %+v", fs)
		}
	}
	if !resp.EnvAmbiguous || !strings.Contains(resp.Note,
		`env süzgeci uygulanamadı (metrikte bu ortam yok (deployment_environment_name değerleri arasında "us-x" eşleşmedi)) — seriler TÜM ortamları kapsıyor.`) {
		t.Fatalf("çözülmedi: amb=%v not=%s", resp.EnvAmbiguous, resp.Note)
	}

	// Okuma hatası → Failed kapsamı, "çözülemedi" notu.
	src3 := fakeVMRedSource{redFixtureSource("s", []string{"http.response.status_code"}), &fakeVMReader{valuesErr: context.DeadlineExceeded}}
	p.Env = "eu-a"
	p.EnvScope = s.serviceMetricREDEnvScope(context.Background(), src3, p.Env, p.To)
	resp, _ = buildServiceMetricRED(context.Background(), src3, id, p)
	if !resp.EnvAmbiguous || resp.EnvScope == nil || !resp.EnvScope.Failed ||
		!strings.Contains(resp.Note, "env süzgeci çözülemedi (VM etiket sorgusu başarısız: ") {
		t.Fatalf("hata: %+v not=%s", resp.EnvScope, resp.Note)
	}
}

// v0.10.1377 — /metric-red ve MCP query_metrics AYNI çözücüyü çağırır.
func TestServiceMetricREDEnvScopeWiring_v0101377(t *testing.T) {
	red := readRepoFile(t, "service_metric_red.go")
	if !strings.Contains(red, "pp.EnvScope = s.serviceMetricREDEnvScope(ctx, src, p.Env, p.To)") ||
		!strings.Contains(red, "serviceMetricREDKey(p, src.Name(), mx, serviceMetricREDLabelDigest(src))") {
		t.Fatal("/metric-red env kapsamını çözmeli ve anahtara LabelMap özetini koymalı")
	}
	if !strings.Contains(readRepoFile(t, "mcp_deps.go"), "p.EnvScope = s.serviceMetricREDEnvScope(ctx, src, env, to)") {
		t.Fatal("MCP query_metrics aynı env kapsamını çözmeli")
	}
}
