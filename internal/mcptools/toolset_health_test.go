package mcptools

// toolset_health_test.go — v0.10.1434 (CoSRE araştırma PR 1, §6.1): toolset
// sağlık türetimi — sorgusuz, mevcut problardan; isteğe bağlı logs yoklaması
// yalnız Memo üzerinden ve tek çağrı.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

// healthLogStore — Ping sayan sahte log deposu (gömülü arayüz: yalnız Backend
// ve Ping çağrılır; diğer yöntemler nil-panic ile test hatasını görünür kılar).
type healthLogStore struct {
	logstore.Store
	backend string
	pingErr error
	pings   int
}

func (h *healthLogStore) Backend() string { return h.backend }
func (h *healthLogStore) Ping(context.Context) error {
	h.pings++
	return h.pingErr
}

type healthClusterMetrics struct{ ClusterMetricReader }

func healthOf(t *testing.T, sts []ToolsetState) map[string]ToolsetHealth {
	t.Helper()
	out := map[string]ToolsetHealth{}
	for _, s := range sts {
		out[s.Name] = s.Health
	}
	return out
}

func TestToolsetHealthDerivation(t *testing.T) {
	ctx := context.Background()
	store := &chstore.Store{}
	full := Deps{
		Store: store, LogStore: &healthLogStore{backend: "elasticsearch"},
		MetricsName:     func() string { return "vm" },
		ClusterMetrics:  healthClusterMetrics{},
		EntityEnabled:   func() bool { return true },
		RolloutsEnabled: func() bool { return false },
		RAGReady:        func() bool { return false },
		SourceCode:      goldenConditionalDeps().SourceCode,
		CodeSearch:      goldenConditionalDeps().CodeSearch,
		Wiki:            goldenWiki{},
		OracleOps:       goldenOracle{},
	}
	partial := Deps{Store: store, EntityEnabled: func() bool { return false }, CodeSearch: goldenConditionalDeps().CodeSearch}
	cases := []struct {
		name string
		d    Deps
		want map[string]sourcestate.State
	}{
		{"sıfır Deps", Deps{}, map[string]sourcestate.State{
			ToolsetServices: sourcestate.NotConfigured, ToolsetTraces: sourcestate.NotConfigured, ToolsetProblems: sourcestate.NotConfigured,
			ToolsetChanges: sourcestate.NotConfigured, ToolsetMetrics: sourcestate.NotConfigured, ToolsetLogs: sourcestate.NotConfigured,
			ToolsetK8s: sourcestate.NotConfigured, ToolsetOracle: sourcestate.NotConfigured, ToolsetDevOpsCode: sourcestate.NotConfigured,
			ToolsetDevOpsWiki: sourcestate.NotConfigured, ToolsetKnowledge: sourcestate.NotConfigured, ToolsetMeta: sourcestate.OK,
		}},
		{"tam kurulum", full, map[string]sourcestate.State{
			ToolsetServices: sourcestate.OK, ToolsetTraces: sourcestate.OK, ToolsetProblems: sourcestate.OK,
			ToolsetChanges: sourcestate.OK, ToolsetMetrics: sourcestate.OK, ToolsetLogs: sourcestate.OK,
			ToolsetK8s: sourcestate.OK, ToolsetOracle: sourcestate.OK, ToolsetDevOpsCode: sourcestate.OK,
			ToolsetDevOpsWiki: sourcestate.OK, ToolsetKnowledge: sourcestate.OK, ToolsetMeta: sourcestate.OK,
		}},
		// Store var, Thanos yok → metrics partial; entity bayrağı kapalı → k8s
		// not_configured; yalnız Code Search → devops-code partial.
		{"kısmi kurulum", partial, map[string]sourcestate.State{
			ToolsetServices: sourcestate.OK, ToolsetMetrics: sourcestate.Partial, ToolsetLogs: sourcestate.NotConfigured,
			ToolsetK8s: sourcestate.NotConfigured, ToolsetDevOpsCode: sourcestate.Partial, ToolsetKnowledge: sourcestate.OK,
		}},
	}
	for _, c := range cases {
		got := healthOf(t, ToolsetStates(ctx, c.d, nil, false))
		if len(got) != 12 {
			t.Fatalf("%s: %d sağlık satırı", c.name, len(got))
		}
		for name, want := range c.want {
			h := got[name]
			if h.State != want {
				t.Errorf("%s: %s = %s, beklenen %s (%+v)", c.name, name, h.State, want, h)
			}
			if h.Usable != (sourcestate.Status{State: want}).Usable() {
				t.Errorf("%s: %s usable=%v tutarsız", c.name, name, h.Usable)
			}
		}
	}
	// Ayrıntılar: arka uç adları + notlar (sorgusuz problardan).
	got := healthOf(t, ToolsetStates(ctx, full, nil, false))
	if got[ToolsetLogs].Backend != "elasticsearch" || got[ToolsetMetrics].Backend != "victoriametrics" {
		t.Errorf("arka uç adları: logs=%q metrics=%q", got[ToolsetLogs].Backend, got[ToolsetMetrics].Backend)
	}
	if len(got[ToolsetChanges].Notes) != 1 || len(got[ToolsetKnowledge].Notes) != 1 {
		t.Errorf("rollouts / RAG notları bekleniyordu: %+v %+v", got[ToolsetChanges], got[ToolsetKnowledge])
	}
	// probe=false hiçbir zaman Ping çağırmaz (get_capabilities "zero cost").
	if full.LogStore.(*healthLogStore).pings != 0 {
		t.Error("türetim yoklama yaptı")
	}
}

// fakeMemo — Deps.Memo sahtesi: anahtar başına bir kez hesaplar.
type fakeMemo struct {
	calls int
	got   map[string][]byte
}

func (m *fakeMemo) memo(ctx context.Context, key string, _ time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
	if b, ok := m.got[key]; ok {
		return b, nil
	}
	m.calls++
	v, store, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(v)
	if store {
		if m.got == nil {
			m.got = map[string][]byte{}
		}
		m.got[key] = b
	}
	return b, nil
}

func TestToolsetHealthProbeMemoised(t *testing.T) {
	ctx := context.Background()
	ls := &healthLogStore{backend: "elasticsearch", pingErr: errors.New("dial tcp: connection refused")}
	m := &fakeMemo{}
	d := Deps{LogStore: ls, Memo: m.memo}
	for i := 0; i < 3; i++ {
		h := healthOf(t, ToolsetStates(ctx, d, nil, true))[ToolsetLogs]
		if h.State != sourcestate.Unreachable || h.Usable {
			t.Fatalf("yoklama hatası unreachable olmalı: %+v", h)
		}
		if h.Detail != "liveness probe failed" {
			t.Errorf("ham hata metni sızmamalı: %q", h.Detail)
		}
	}
	if ls.pings != 1 || m.calls != 1 {
		t.Fatalf("yoklama Memo üzerinden TEK kez koşmalı: ping=%d memo=%d", ls.pings, m.calls)
	}
	// Memo yok → yoklama yok, türetilmiş durum.
	ls2 := &healthLogStore{backend: "clickhouse", pingErr: errors.New("boom")}
	if h := healthOf(t, ToolsetStates(ctx, Deps{LogStore: ls2}, nil, true))[ToolsetLogs]; h.State != sourcestate.OK || ls2.pings != 0 {
		t.Fatalf("Memo'suz yoklama yapılmamalı: %+v pings=%d", h, ls2.pings)
	}
	// 401 → unauthorized (sourcestate sözlüğü).
	ls3 := &healthLogStore{backend: "elasticsearch", pingErr: sourcestate.ErrUnauthorized}
	if h := healthOf(t, ToolsetStates(ctx, Deps{LogStore: ls3, Memo: (&fakeMemo{}).memo}, nil, true))[ToolsetLogs]; h.State != sourcestate.Unauthorized {
		t.Fatalf("401 unauthorized olmalı: %+v", h)
	}
}
