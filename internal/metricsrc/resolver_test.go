package metricsrc

// v0.10.1270 — CoSRE adım 1: metrik kaynağı çözümleyici.
//
// Contract: ONE pure decision (Decide) maps a metric family + scope +
// backend state to a backend. The table below is the routing table the
// commit message cites; the live Resolver and every caller (anomaly
// investigation, MCP pod health, evaluator GC gate, api metricSource)
// must agree with it because they all call Decide.
//
//	family            VM on      VM off     Thanos matcher
//	pod saturation    thanos     thanos     yes (cluster required)
//	jmx (jvm_/jboss_) thanos     thanos     yes (cluster required)
//	otel runtime      vm         ch         n/a
//	otel infra        vm         ch         n/a
//	app / RED         vm         ch         n/a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

var (
	sharedCluster = thanos.ClusterConfig{ID: "c-prod", Name: "prod", URL: "http://q.invalid",
		ThanosLabelName: "cluster", ThanosLabelValue: "prod-eu", Enabled: true}
	perURLCluster = thanos.ClusterConfig{ID: "c-stg", Name: "stg", URL: "http://stg.invalid", Enabled: true}
)

func TestDecideRoutingTable(t *testing.T) {
	type want struct {
		backend       Backend
		needsCluster  bool
		matcherLabel  string
		matcherValue  string
		clusterScoped bool
	}
	cases := []struct {
		name   string
		family Family
		vm     bool
		sc     Scope
		st     State
		want   want
	}{
		// ── Thanos families ────────────────────────────────────────────
		{"pod sat, vm on, shared cluster", FamilyPodSaturation, true, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true},
			want{backend: BackendThanos, matcherLabel: "cluster", matcherValue: "prod-eu", clusterScoped: true}},
		{"pod sat, vm off, shared cluster", FamilyPodSaturation, false, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true},
			want{backend: BackendThanos, matcherLabel: "cluster", matcherValue: "prod-eu", clusterScoped: true}},
		{"pod sat, per-URL cluster → no matcher, still scoped", FamilyPodSaturation, true, Scope{Cluster: "c-stg"},
			State{Cluster: perURLCluster, ClusterFound: true},
			want{backend: BackendThanos, clusterScoped: true}},
		{"pod sat, vm on, no cluster", FamilyPodSaturation, true, Scope{},
			State{}, want{backend: BackendThanos, needsCluster: true}},
		{"pod sat, vm off, no cluster", FamilyPodSaturation, false, Scope{},
			State{}, want{backend: BackendThanos, needsCluster: true}},
		{"pod sat, unknown cluster", FamilyPodSaturation, true, Scope{Cluster: "nope"},
			State{}, want{backend: BackendNone}},
		{"jmx, vm on, shared cluster", FamilyJMX, true, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true},
			want{backend: BackendThanos, matcherLabel: "cluster", matcherValue: "prod-eu", clusterScoped: true}},
		{"jmx, vm off, shared cluster", FamilyJMX, false, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true},
			want{backend: BackendThanos, matcherLabel: "cluster", matcherValue: "prod-eu", clusterScoped: true}},
		{"jmx, vm on, no cluster", FamilyJMX, true, Scope{}, State{},
			want{backend: BackendThanos, needsCluster: true}},
		{"jmx, vm off, no cluster", FamilyJMX, false, Scope{}, State{},
			want{backend: BackendThanos, needsCluster: true}},
		// ── VM-or-CH families: cluster never changes the backend ────────
		{"otel runtime, vm on, no cluster", FamilyOTelRuntime, true, Scope{}, State{}, want{backend: BackendVM}},
		{"otel runtime, vm off, no cluster", FamilyOTelRuntime, false, Scope{}, State{}, want{backend: BackendCH}},
		{"otel runtime, vm on, cluster", FamilyOTelRuntime, true, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true}, want{backend: BackendVM}},
		{"otel runtime, vm off, cluster", FamilyOTelRuntime, false, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true}, want{backend: BackendCH}},
		{"otel infra, vm on", FamilyOTelInfra, true, Scope{Cluster: "c-prod"}, State{}, want{backend: BackendVM}},
		{"otel infra, vm off", FamilyOTelInfra, false, Scope{}, State{}, want{backend: BackendCH}},
		{"app, vm on, no cluster", FamilyApp, true, Scope{Service: "checkout", Env: "prod"}, State{}, want{backend: BackendVM}},
		{"app, vm off, no cluster", FamilyApp, false, Scope{Service: "checkout"}, State{}, want{backend: BackendCH}},
		{"app, vm on, cluster", FamilyApp, true, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true}, want{backend: BackendVM}},
		{"app, vm off, cluster", FamilyApp, false, Scope{Cluster: "c-prod"},
			State{Cluster: sharedCluster, ClusterFound: true}, want{backend: BackendCH}},
		{"unknown family → app default", FamilyUnknown, true, Scope{}, State{}, want{backend: BackendVM}},
		{"unknown family, vm off", FamilyUnknown, false, Scope{}, State{}, want{backend: BackendCH}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.st.VMConfigured = c.vm
			r := Decide(c.family, c.sc, c.st)
			if r.Backend != c.want.backend {
				t.Fatalf("backend = %q, want %q (reason %q)", r.Backend, c.want.backend, r.Reason)
			}
			if r.NeedsCluster != c.want.needsCluster {
				t.Fatalf("NeedsCluster = %v, want %v", r.NeedsCluster, c.want.needsCluster)
			}
			if r.MatcherLabel != c.want.matcherLabel || r.MatcherValue != c.want.matcherValue {
				t.Fatalf("matcher = %s=%q, want %s=%q", r.MatcherLabel, r.MatcherValue, c.want.matcherLabel, c.want.matcherValue)
			}
			if r.ClusterScoped() != c.want.clusterScoped {
				t.Fatalf("ClusterScoped = %v, want %v", r.ClusterScoped(), c.want.clusterScoped)
			}
			if r.Reason == "" {
				t.Fatal("every route carries a reason")
			}
		})
	}
}

// A Thanos-routed query for a shared-querier cluster carries the cluster
// matcher on EVERY selector; a per-URL cluster sends the expression as is.
// ScopedQuery is the exact text doQueryWith sends (both go through
// thanos.withClusterMatcher via EffectiveThanosLabel).
func TestScopedQueryCarriesClusterMatcher(t *testing.T) {
	expr := `sum(rate(container_cpu_usage_seconds_total{namespace="shop"}[5m])) / sum(kube_pod_container_resource_limits{resource="cpu"})`
	r := Decide(FamilyPodSaturation, Scope{Cluster: "c-prod"}, State{Cluster: sharedCluster, ClusterFound: true})
	got, err := r.ScopedQuery(expr)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, `cluster="prod-eu"`); n != 2 {
		t.Fatalf("matcher count = %d, want 2 (one per selector): %s", n, got)
	}
	per := Decide(FamilyJMX, Scope{Cluster: "c-stg"}, State{Cluster: perURLCluster, ClusterFound: true})
	if got, err := per.ScopedQuery(expr); err != nil || got != expr {
		t.Fatalf("per-URL cluster must send expr verbatim: %q %v", got, err)
	}
	// Non-Thanos and unscoped routes refuse to hand out a query string.
	if _, err := Decide(FamilyApp, Scope{}, State{VMConfigured: true}).ScopedQuery(expr); err == nil {
		t.Fatal("VM route must not produce a Thanos query")
	}
	if _, err := Decide(FamilyPodSaturation, Scope{}, State{}).ScopedQuery(expr); err == nil {
		t.Fatal("Thanos route without a cluster must refuse (unscoped query would read every cluster)")
	}
}

func TestFamilyOf(t *testing.T) {
	cases := []struct {
		name string
		want Family
	}{
		{"", FamilyUnknown},
		{"   ", FamilyUnknown},
		// cAdvisor / KSM / node-exporter / kubelet → Thanos
		{"container_cpu_usage_seconds_total", FamilyPodSaturation},
		{"container_memory_working_set_bytes", FamilyPodSaturation},
		{"kube_pod_container_status_restarts_total", FamilyPodSaturation},
		{"kube_pod_container_resource_limits", FamilyPodSaturation},
		{"node_memory_MemAvailable_bytes", FamilyPodSaturation},
		{"kubelet_volume_stats_used_bytes", FamilyPodSaturation},
		{"machine_cpu_cores", FamilyPodSaturation},
		{"node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate", FamilyPodSaturation},
		{"namespace_workload_pod:kube_pod_owner:relabel", FamilyPodSaturation},
		// JMX exporter → Thanos
		{"jboss_datasources_pool_active_count", FamilyJMX},
		{"jvm_memory_bytes_used", FamilyJMX},
		{"jvm_memory_pool_bytes_used", FamilyJMX},
		{"jvm_gc_collection_seconds_count", FamilyJMX},
		{"jvm_threads_current", FamilyJMX},
		{"jvm_classes_loaded", FamilyJMX},
		// OTel runtime (dotted, and VM-sanitised semconv names) → VM/CH
		{"jvm.memory.used", FamilyOTelRuntime},
		{"jvm.memory.used_after_last_gc", FamilyOTelRuntime},
		{"jvm.gc.duration", FamilyOTelRuntime},
		{"jvm.thread.count", FamilyOTelRuntime},
		{"process.runtime.jvm.memory.usage", FamilyOTelRuntime},
		{"jvm_memory_used", FamilyOTelRuntime},
		{"jvm_memory_used_bytes", FamilyOTelRuntime},
		{"jvm_memory_limit_bytes", FamilyOTelRuntime},
		{"jvm_gc_duration_seconds_sum", FamilyOTelRuntime},
		{"jvm_gc_duration_seconds_count", FamilyOTelRuntime},
		{"jvm_gc_duration_seconds_bucket", FamilyOTelRuntime},
		{"jvm_thread_count", FamilyOTelRuntime},
		{"jvm_cpu_recent_utilization_ratio", FamilyOTelRuntime},
		// OTLP-ingested k8s / host metrics → VM/CH (kubeletstats / hostmetrics, NOT Thanos)
		{"k8s.pod.cpu.usage", FamilyOTelInfra},
		{"container.memory.working_set", FamilyOTelInfra},
		{"system.cpu.utilization", FamilyOTelInfra},
		{"k8s_pod_cpu_usage", FamilyOTelInfra},
		// RED / app → VM/CH
		{"http.server.request.duration", FamilyApp},
		{"http_server_request_duration_seconds_bucket", FamilyApp},
		{"rpc.server.duration", FamilyApp},
		{"db.client.connection.count", FamilyApp},
		{"orders_placed_total", FamilyApp},
		{"checkout.cart.size", FamilyApp},
	}
	for _, c := range cases {
		if got := FamilyOf(c.name); got != c.want {
			t.Errorf("FamilyOf(%q) = %s, want %s", c.name, got, c.want)
		}
	}
}

// ── live Resolver ─────────────────────────────────────────────────────────

type fakeVM struct {
	on  bool
	tag string
}

func (f *fakeVM) Configured() bool { return f != nil && f.on }
func (f *fakeVM) JVMHeapPodUsage(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return []chstore.CapacitySample{{Instance: f.tag}}, nil
}
func (f *fakeVM) JVMGCPodPause(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return []chstore.CapacitySample{{Instance: f.tag}}, nil
}

type fakeCH struct{ tag string }

func (f fakeCH) JVMHeapPodUsage(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return []chstore.CapacitySample{{Instance: f.tag}}, nil
}
func (f fakeCH) JVMGCPodPause(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return []chstore.CapacitySample{{Instance: f.tag}}, nil
}

// RuntimePods decides at CALL time (a Settings flip needs no restart) —
// the behaviour vmetrics.RuntimePodsOr had before it moved here.
func TestRuntimePodsPicksAtCallTime(t *testing.T) {
	vm := &fakeVM{tag: "vm"}
	pods := New(Backends{VM: vm, CH: fakeCH{tag: "ch"}}).RuntimePods()
	now := time.Now()
	if got, _ := pods.JVMHeapPodUsage(context.Background(), now, now); got[0].Instance != "ch" {
		t.Fatalf("VM off → CH, got %q", got[0].Instance)
	}
	vm.on = true
	if got, _ := pods.JVMGCPodPause(context.Background(), now, now); got[0].Instance != "vm" {
		t.Fatalf("VM on → VM, got %q", got[0].Instance)
	}
	// nil VM (typed or untyped) → CH.
	var nilVM *fakeVM
	for _, b := range []Backends{{CH: fakeCH{tag: "ch"}}, {VM: nilVM, CH: fakeCH{tag: "ch"}}} {
		if got, _ := New(b).RuntimePods().JVMHeapPodUsage(context.Background(), now, now); got[0].Instance != "ch" {
			t.Fatalf("nil VM → CH, got %q", got[0].Instance)
		}
	}
}

// End-to-end through the real thanos client: the query that leaves the
// process for a shared-querier cluster carries the cluster matcher.
func TestThanosInstantSendsClusterMatcher(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"pod":"a"},"value":[1700000000,"0.5"]}]}}`))
	}))
	t.Cleanup(srv.Close)
	th := thanos.New()
	c := sharedCluster
	c.URL = srv.URL
	th.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{c}})
	res := New(Backends{Thanos: th})

	samples, route, err := res.ThanosInstant(context.Background(), FamilyPodSaturation, "c-prod",
		`sum by (pod) (container_memory_working_set_bytes{namespace="shop"})`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent, `cluster="prod-eu"`) {
		t.Fatalf("sent query lacks cluster matcher: %s", sent)
	}
	if route.Backend != BackendThanos || len(samples) != 1 || samples[0].Value != 0.5 {
		t.Fatalf("route=%+v samples=%+v", route, samples)
	}
	// Cluster ref by NAME resolves too (legacy ?cluster=<name> URLs).
	if _, r2, err := res.ThanosInstant(context.Background(), FamilyJMX, "prod", `jvm_threads_current`); err != nil || r2.MatcherValue != "prod-eu" {
		t.Fatalf("by-name ref: %+v %v", r2, err)
	}
	// Refusals: no cluster, unknown cluster, non-Thanos family, no Thanos.
	for _, tc := range []struct {
		f   Family
		ref string
		b   Backends
	}{
		{FamilyPodSaturation, "", Backends{Thanos: th}},
		{FamilyPodSaturation, "missing", Backends{Thanos: th}},
		{FamilyApp, "c-prod", Backends{Thanos: th}},
		{FamilyPodSaturation, "c-prod", Backends{}},
	} {
		if _, _, err := New(tc.b).ThanosInstant(context.Background(), tc.f, tc.ref, `up`); err == nil {
			t.Fatalf("expected refusal for family=%s ref=%q", tc.f, tc.ref)
		}
	}
}

// The concrete services satisfy the backend seams (compile-time).
var (
	_ VMBackend     = (*vmetrics.Service)(nil)
	_ ThanosBackend = (*thanos.Service)(nil)
)

// Moved from vmetrics (TestRuntimePodsOrPicksAtCallTime): with the REAL
// *vmetrics.Service, unconfigured → CH; configured → VM answers; nil → CH;
// nil *thanos.Service never panics.
func TestRuntimePodsWithRealVMService(t *testing.T) {
	vm := vmetrics.New()
	pods := New(Backends{VM: vm, CH: fakeCH{tag: "ch"}}).RuntimePods()
	now := time.Now()
	if got, _ := pods.JVMHeapPodUsage(context.Background(), now, now); got[0].Instance != "ch" {
		t.Fatal("unconfigured VM → CH")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
	}))
	t.Cleanup(srv.Close)
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: srv.URL})
	if got, err := pods.JVMGCPodPause(context.Background(), now.Add(-time.Minute), now); err != nil || len(got) != 0 {
		t.Fatalf("configured VM must answer (empty here): %v %v", got, err)
	}
	var nilVM *vmetrics.Service
	var nilTh *thanos.Service
	r := New(Backends{VM: nilVM, CH: fakeCH{tag: "ch"}, Thanos: nilTh})
	if got, _ := r.RuntimePods().JVMHeapPodUsage(context.Background(), now, now); got[0].Instance != "ch" {
		t.Fatal("nil VM → CH")
	}
	if rt := r.Resolve(FamilyPodSaturation, Scope{Cluster: "c-prod"}); rt.Backend != BackendNone {
		t.Fatalf("nil thanos → no backend, got %+v", rt)
	}
}
