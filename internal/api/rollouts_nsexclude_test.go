package api

// rollouts_nsexclude_test.go — v0.10.1262 — operatör 2026-10-17: "OpenShift'e
// ait projeler rollout listelemesin". Liste ve Toplu uçları platform
// namespace desenlerini depoya (SQL WHERE) iletir; açık ?namespace= süzgeci
// kazanır (dışlama düşer); [] ayarı dışlamayı kapatır; önbellek anahtarı
// desen kümesini taşır (sıra-bağımsız).

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestRolloutListNamespaceExclude(t *testing.T) {
	def := []string{"kube-*", "openshift", "openshift-*"}
	for _, src := range []string{rollout.SourceV1, rollout.SourceV2} {
		for _, tc := range []struct {
			path        string
			wantFilter  []string
			wantApplied bool
		}{
			{"/api/rollouts?" + rv2Window, def, true},
			{"/api/rollouts?" + rv2Window + "&namespace=openshift-monitoring", nil, false}, // açık süzgeç kazanır
			{"/api/rollouts/stats?" + rv2Window, def, true},
			{"/api/rollouts/stats?" + rv2Window + "&namespace=kube-system", nil, false},
		} {
			st := &chstore.RolloutStats{TopRollback: []chstore.RolloutWorkloadN{}, TopDeploy: []chstore.RolloutWorkloadN{}, ByDay: []chstore.RolloutDayCount{}}
			fr := &fakeRolloutReader{v1Stats: st, v2Stats: st}
			w := rv2Get(newRolloutReadEnv(t, src, fr), tc.path)
			if w.Code != http.StatusOK {
				t.Fatalf("%s %s: %d %s", src, tc.path, w.Code, w.Body)
			}
			if !reflect.DeepEqual(fr.filter.ExcludeNamespaces, tc.wantFilter) {
				t.Errorf("%s %s: depo süzgeci %q, want %q", src, tc.path, fr.filter.ExcludeNamespaces, tc.wantFilter)
			}
			var body struct {
				ExcludeNamespaces []string `json:"excludeNamespaces"`
				ExcludeApplied    bool     `json:"excludeApplied"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if !reflect.DeepEqual(body.ExcludeNamespaces, def) || body.ExcludeApplied != tc.wantApplied {
				t.Errorf("%s %s: gövde %s", src, tc.path, w.Body)
			}
		}
	}
}

func TestRolloutListNamespaceExcludeDisabled(t *testing.T) {
	c := rollout.NewSettingsService()
	st := rollout.DefaultSettings()
	st.Enabled, st.Source = true, rollout.SourceV2
	empty := []string{}
	st.ExcludeNamespaces = &empty
	c.Configure(st)
	s := &Server{}
	s.SetRollout(c)
	if got := s.rolloutExcludeNamespaces(); len(got) != 0 {
		t.Fatalf("[] ayarı dışlamayı kapatmalı: %q", got)
	}
	if got := (&Server{}).rolloutExcludeNamespaces(); !reflect.DeepEqual(got, []string{"kube-*", "openshift", "openshift-*"}) {
		t.Fatalf("bağlanmamış ayar → varsayılan: %q", got)
	}
}

func TestRolloutKeysCarryExclude(t *testing.T) {
	from, to := time.Unix(1790000000, 0), time.Unix(1790003600, 0)
	a := rolloutsListKey("v2", chstore.RolloutFilter{ExcludeNamespaces: []string{"openshift-*", "kube-*"}}, 200, from, to)
	b := rolloutsListKey("v2", chstore.RolloutFilter{ExcludeNamespaces: []string{"kube-*", "openshift-*"}}, 200, from, to)
	c := rolloutsListKey("v2", chstore.RolloutFilter{}, 200, from, to)
	d := rolloutsListKey("v2", chstore.RolloutFilter{ExcludeNamespaces: []string{"kube-*"}}, 200, from, to)
	if a != b {
		t.Error("desen sırası anahtarı değiştirmemeli")
	}
	if a == c || a == d || c == d {
		t.Errorf("farklı desen kümeleri aynı anahtar: %s %s %s", a, c, d)
	}
	if rolloutStatsKey("v2", "", "", []string{"kube-*"}, 10, from, to) == rolloutStatsKey("v2", "", "", nil, 10, from, to) {
		t.Error("stats anahtarı dışlamayı taşımalı")
	}
}

// PUT: geçersiz desen 400 (depo dokunulmaz); geçerli liste saklanır, GET
// resolved'da uygulanan değer döner, audit kaydı düşer.
func TestRolloutSettingsPutExcludeNamespaces(t *testing.T) {
	fs := &rolloutFakeStore{}
	s, mux := newRolloutSettingsEnv(t, fs)
	if w := rolloutAdminDo(mux, "PUT", `{"enabled":true,"excludeNamespaces":["bad|re"]}`); w.Code != http.StatusBadRequest || fs.puts != 0 {
		t.Fatalf("geçersiz desen: %d puts=%d %s", w.Code, fs.puts, w.Body)
	}
	w := rolloutAdminDo(mux, "PUT", `{"enabled":true,"excludeNamespaces":["openshift-*","team-?"]}`)
	if w.Code != http.StatusOK || fs.puts != 1 {
		t.Fatalf("PUT %d %s", w.Code, w.Body)
	}
	var got struct {
		Resolved struct {
			ExcludeNamespaces []string `json:"excludeNamespaces"`
		} `json:"resolved"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !reflect.DeepEqual(got.Resolved.ExcludeNamespaces, []string{"openshift-*", "team-?"}) {
		t.Fatalf("resolved: %s", w.Body)
	}
	if len(s.auditQ) != 1 {
		t.Fatalf("admin PUT audit kaydı yazmalı (kuyruk %d)", len(s.auditQ))
	}
}
