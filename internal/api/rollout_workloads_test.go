package api

// rollout_workloads_test.go — v0.10.1420 — operatör (/rollouts Canlı
// sekmesi): "Workload seçilerek filtrelenebilsin."
//
// NE ÇİVİLİYOR (canlı CH yok; sahte depo dikişleri):
//   - ?workload= liste VE Toplu uçlarında depo süzgecine iner (SUNUCU
//     süzgeci — liste 200'de kesilir, istemci süzgeci satır kaçırırdı) ve
//     her iki önbellek anahtarına girer (iki workload aynı girdiye düşmez).
//   - GET /api/rollouts/workloads: seçicinin sunucu araması — desen, cluster,
//     namespace, kelepçeli limit depoya iner; platform dışlaması liste
//     ucuyla aynı; kaynak (v1/v2) seçilir; anahtar TÜM girdileri taşır.
//   - Rota buildMux'ta (registerRoutesExtra) çözülür.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

type fakeWorkloadSearcher struct {
	calls  int
	v2     bool
	f      chstore.RolloutFilter
	q      string
	limit  int
	names  []string
	capped bool
}

func (f *fakeWorkloadSearcher) RolloutWorkloadNames(_ context.Context, v2 bool, fl chstore.RolloutFilter, q string, _, _ time.Time, limit int) ([]string, bool, error) {
	f.calls++
	f.v2, f.f, f.q, f.limit = v2, fl, q, limit
	return f.names, f.capped, nil
}

func newRolloutWorkloadEnv(t *testing.T, src string, fr *fakeRolloutReader, fs *fakeWorkloadSearcher) *http.ServeMux {
	t.Helper()
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats()}
	cfg := rollout.NewSettingsService()
	st := rollout.DefaultSettings()
	st.Enabled, st.Source = true, src
	cfg.Configure(st)
	s.SetRollout(cfg)
	prevR, prevS := rolloutReaderOf, rolloutWorkloadSearcherOf
	rolloutReaderOf = func(*Server) rolloutReader { return fr }
	rolloutWorkloadSearcherOf = func(*Server) rolloutWorkloadSearcher { return fs }
	t.Cleanup(func() { rolloutReaderOf, rolloutWorkloadSearcherOf = prevR, prevS })
	mux := http.NewServeMux()
	s.registerRolloutRoutes(mux)
	s.registerRolloutWorkloadRoutes(mux)
	return mux
}

func TestRolloutWorkloadFilterReachesQueryAndKey(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, c := range []struct {
		name, src, path string
		wantCall        string
		wantWorkload    string
	}{
		{"liste v1", rollout.SourceV1, "/api/rollouts?" + rv2Window + "&workload=orders-api", "v1.list", "orders-api"},
		{"liste v2", rollout.SourceV2, "/api/rollouts?" + rv2Window + "&workload=orders-api", "v2.list", "orders-api"},
		{"liste kırpılır", rollout.SourceV1, "/api/rollouts?" + rv2Window + "&workload=%20orders-api%20", "v1.list", "orders-api"},
		{"liste süzgeçsiz", rollout.SourceV1, "/api/rollouts?" + rv2Window, "v1.list", ""},
		{"toplu v1", rollout.SourceV1, "/api/rollouts/stats?" + rv2Window + "&workload=orders-api", "v1.stats", "orders-api"},
		{"toplu v2", rollout.SourceV2, "/api/rollouts/stats?" + rv2Window + "&workload=orders-api", "v2.stats", "orders-api"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fr := &fakeRolloutReader{v1Stats: &chstore.RolloutStats{}, v2Stats: &chstore.RolloutStats{}}
			mux := newRolloutWorkloadEnv(t, c.src, fr, &fakeWorkloadSearcher{})
			w := rv2Get(mux, c.path)
			if w.Code != http.StatusOK {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if len(fr.calls) == 0 || fr.calls[0] != c.wantCall {
				t.Fatalf("çağrılar %v, want %s", fr.calls, c.wantCall)
			}
			if fr.filter.Workload != c.wantWorkload {
				t.Fatalf("depo süzgeci workload=%q, want %q", fr.filter.Workload, c.wantWorkload)
			}
		})
	}

	// Anahtar: workload liste + Toplu anahtarlarını ayırır; boş ≠ dolu.
	base := chstore.RolloutFilter{ClusterID: "c-1", Namespace: "team-a"}
	withWl := base
	withWl.Workload = "orders-api"
	other := base
	other.Workload = "orders-worker"
	for _, src := range []string{"v1", "v2"} {
		k0, k1, k2 := rolloutsListKey(src, base, 200, from, to), rolloutsListKey(src, withWl, 200, from, to), rolloutsListKey(src, other, 200, from, to)
		if k0 == k1 || k1 == k2 || k0 == k2 {
			t.Fatalf("%s liste anahtarı workload'ı ayırmıyor: %s %s %s", src, k0, k1, k2)
		}
		s0 := rolloutStatsKey(src, "c-1", "team-a", "", nil, 10, from, to)
		s1 := rolloutStatsKey(src, "c-1", "team-a", "orders-api", nil, 10, from, to)
		s2 := rolloutStatsKey(src, "c-1", "team-a", "orders-worker", nil, 10, from, to)
		if s0 == s1 || s1 == s2 || s0 == s2 {
			t.Fatalf("%s Toplu anahtarı workload'ı ayırmıyor", src)
		}
		// ayraç saldırısı: namespace|workload sınırı kaydırılamaz
		if rolloutStatsKey(src, "c-1", "team-a\x00orders", "", nil, 10, from, to) == rolloutStatsKey(src, "c-1", "team-a", "\x00orders", nil, 10, from, to) {
			t.Fatalf("%s Toplu anahtarı NUL kaydırmasıyla çakıştı", src)
		}
	}
}

func TestRolloutWorkloadNamesEndpoint(t *testing.T) {
	t.Run("v1: desen + kapsam + limit depoya iner, gövde names/capped", func(t *testing.T) {
		fs := &fakeWorkloadSearcher{names: []string{"orders-api", "orders-worker"}, capped: true}
		mux := newRolloutWorkloadEnv(t, rollout.SourceV1, &fakeRolloutReader{}, fs)
		w := rv2Get(mux, "/api/rollouts/workloads?"+rv2Window+"&q=%20orders%20&cluster=c-1&namespace=team-a&limit=20")
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if fs.calls != 1 || fs.v2 || fs.q != "orders" || fs.limit != 20 || fs.f.ClusterID != "c-1" || fs.f.Namespace != "team-a" {
			t.Fatalf("depo çağrısı yanlış: %+v", fs)
		}
		// açık namespace platform dışlamasını kaldırır (liste ucuyla aynı kural)
		if len(fs.f.ExcludeNamespaces) != 0 {
			t.Fatalf("açık namespace'te dışlama uygulanmamalı: %v", fs.f.ExcludeNamespaces)
		}
		var body struct {
			Names  []string `json:"names"`
			Capped bool     `json:"capped"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if strings.Join(body.Names, ",") != "orders-api,orders-worker" || !body.Capped {
			t.Fatalf("gövde %s", w.Body)
		}
	})
	t.Run("v2 kaynağı + namespace yokken platform dışlaması + limit kelepçesi", func(t *testing.T) {
		fs := &fakeWorkloadSearcher{}
		mux := newRolloutWorkloadEnv(t, rollout.SourceV2, &fakeRolloutReader{}, fs)
		w := rv2Get(mux, "/api/rollouts/workloads?"+rv2Window+"&limit=99999")
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		if !fs.v2 || fs.limit != chstore.RolloutWorkloadNamesMax || len(fs.f.ExcludeNamespaces) == 0 {
			t.Fatalf("depo çağrısı yanlış: %+v", fs)
		}
		if !strings.Contains(w.Body.String(), `"names":[]`) {
			t.Fatalf("nil ad listesi [] dönmeli: %s", w.Body)
		}
	})
	t.Run("bayrak kapalı → 404 disabled", func(t *testing.T) {
		c, _ := cache.NewNoop()
		s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats()}
		mux := http.NewServeMux()
		s.registerRolloutWorkloadRoutes(mux)
		w := rv2Get(mux, "/api/rollouts/workloads?q=orders")
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), `"disabled":true`) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	})
}

func TestParseRolloutWorkloadSearch(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for _, c := range []struct {
		name      string
		in        map[string]string
		wantQ     string
		wantLimit int
	}{
		{"varsayılan", map[string]string{}, "", rolloutWorkloadNamesDefault},
		{"negatif limit", map[string]string{"limit": "-3"}, "", rolloutWorkloadNamesDefault},
		{"tavan", map[string]string{"limit": "5000"}, "", chstore.RolloutWorkloadNamesMax},
		{"desen kırpılır", map[string]string{"q": "  orders-api "}, "orders-api", rolloutWorkloadNamesDefault},
		{"uzun desen kesilir", map[string]string{"q": strings.Repeat("a", 300)}, strings.Repeat("a", rolloutWorkloadQueryMaxLen), rolloutWorkloadNamesDefault},
	} {
		_, q, limit := parseRolloutWorkloadSearch(get(c.in))
		if q != c.wantQ || limit != c.wantLimit {
			t.Errorf("%s: q=%q limit=%d", c.name, q, limit)
		}
	}
}

func TestRolloutWorkloadNamesKeyCarriesEveryInput(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := chstore.RolloutFilter{ClusterID: "c-1", Namespace: "team-a"}
	k0 := rolloutWorkloadNamesKey("v1", base, "orders", 50, from, to)
	if !strings.HasPrefix(k0, "rollouts:") {
		t.Fatalf("anahtar rollouts: önekiyle başlamalı (PUT/tail süpürmesi): %s", k0)
	}
	for name, k := range map[string]string{
		"kaynak":       rolloutWorkloadNamesKey("v2", base, "orders", 50, from, to),
		"cluster":      rolloutWorkloadNamesKey("v1", chstore.RolloutFilter{ClusterID: "c-2", Namespace: "team-a"}, "orders", 50, from, to),
		"namespace":    rolloutWorkloadNamesKey("v1", chstore.RolloutFilter{ClusterID: "c-1", Namespace: "team-b"}, "orders", 50, from, to),
		"desen":        rolloutWorkloadNamesKey("v1", base, "orders-api", 50, from, to),
		"dışlama":      rolloutWorkloadNamesKey("v1", chstore.RolloutFilter{ClusterID: "c-1", Namespace: "team-a", ExcludeNamespaces: []string{"kube-*"}}, "orders", 50, from, to),
		"limit":        rolloutWorkloadNamesKey("v1", base, "orders", 20, from, to),
		"pencere":      rolloutWorkloadNamesKey("v1", base, "orders", 50, from.Add(time.Hour), to.Add(time.Hour)),
		"NUL kaydırma": rolloutWorkloadNamesKey("v1", chstore.RolloutFilter{ClusterID: "c-1", Namespace: "team-a\x00orders"}, "", 50, from, to),
	} {
		if k == k0 {
			t.Errorf("%s anahtarı ayırmadı", name)
		}
	}
	if rolloutWorkloadNamesKey("v1", base, "orders", 50, from, to) != k0 {
		t.Fatal("anahtar kararlı olmalı")
	}
}

func TestRolloutWorkloadRouteOnBuildMux(t *testing.T) {
	mux := (&Server{}).buildMux()
	if _, pat := mux.Handler(httptest.NewRequest("GET", "/api/rollouts/workloads?q=orders", nil)); pat != "GET /api/rollouts/workloads" {
		t.Fatalf("rota buildMux'ta çözülmüyor: %q", pat)
	}
}
