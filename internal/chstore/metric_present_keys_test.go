package chstore

// v0.10.1358 — MetricPresentKeys sertleştirmesi (v0.10.1350 izlemesi).
// Eskiden CH yolu Scan hatasında nil ("hiçbir anahtar yok") dönüyordu ve
// servis kapsamı yoktu. Pinler: (1) SQL servis kapsamlıyken `service_name = ?`
// taşır, filo genelinde taşımaz; zaman sınırı + max_execution_time her iki
// hâlde; (2) ResolvePresentKeys üç hâli (hata/kısmi ⇒ unknown, bilinen-boş
// servis ⇒ filo); (3) pencere ≤ 24 sa.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMetricPresentKeysSQLServiceScope_v0101358(t *testing.T) {
	to := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	keys := []string{"job", "resource.k8s.deployment.name"}

	q, args := metricPresentKeysSQL("m", "cart", keys, from, to)
	for _, want := range []string{
		"countIf(has(attr_keys, ?)) AS k0",
		"countIf(has(res_keys, ?)) AS k1",
		"FROM metric_points WHERE metric = ? AND time >= ? AND time <= ? AND service_name = ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("servis kapsamlı SQL %q içermiyor:\n%s", want, q)
		}
	}
	if len(args) != 6 || args[0] != "job" || args[1] != "k8s.deployment.name" || args[2] != "m" || args[5] != "cart" {
		t.Errorf("args = %v", args)
	}

	q, args = metricPresentKeysSQL("m", "", keys, from, to)
	if strings.Contains(q, "service_name") {
		t.Errorf("filo geneli SQL servis kapsamı taşıyor:\n%s", q)
	}
	if !strings.Contains(q, "time >= ? AND time <= ?") || !strings.Contains(q, "max_execution_time") {
		t.Errorf("filo geneli SQL sınırsız:\n%s", q)
	}
	if len(args) != 5 {
		t.Errorf("filo args = %v", args)
	}
}

func TestResolvePresentKeys_v0101358(t *testing.T) {
	keys := []string{"job", "service_name"}
	type ans struct {
		present []string
		partial bool
		err     error
	}
	cases := []struct {
		name      string
		service   string
		svc, flt  ans
		wantState string
		wantScope string
		wantKeys  int
		wantFleet bool
	}{
		{"servis buldu", "cart", ans{present: []string{"job"}}, ans{}, PresentKeysKnown, PresentKeysScopeService, 1, false},
		{"servis bilinen-boş ⇒ filo buldu", "cart", ans{}, ans{present: []string{"job", "service_name"}}, PresentKeysKnown, PresentKeysScopeFleet, 2, true},
		{"servis bilinen-boş ⇒ filo da boş", "cart", ans{}, ans{}, PresentKeysKnown, PresentKeysScopeFleet, 0, true},
		{"servis hata ⇒ unknown, filo sorulmaz", "cart", ans{err: errors.New("timeout")}, ans{present: keys}, PresentKeysUnknown, PresentKeysScopeService, 0, false},
		{"servis kısmi ⇒ unknown, filo sorulmaz", "cart", ans{partial: true}, ans{present: keys}, PresentKeysUnknown, PresentKeysScopeService, 0, false},
		{"filo hata ⇒ unknown/fleet", "cart", ans{}, ans{err: errors.New("boom")}, PresentKeysUnknown, PresentKeysScopeFleet, 0, true},
		{"servis yok ⇒ doğrudan filo", "  ", ans{present: keys}, ans{present: []string{"job"}}, PresentKeysKnown, PresentKeysScopeFleet, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fleetAsked := false
			got := ResolvePresentKeys(keys, tc.service, func(svc string) ([]string, bool, error) {
				a := tc.svc
				if svc == "" {
					fleetAsked = true
					a = tc.flt
				}
				return a.present, a.partial, a.err
			})
			if got.State != tc.wantState || got.Scope != tc.wantScope || len(got.Keys) != tc.wantKeys || fleetAsked != tc.wantFleet {
				t.Fatalf("got %+v fleetAsked=%v; want state=%s scope=%s keys=%d fleet=%v",
					got, fleetAsked, tc.wantState, tc.wantScope, tc.wantKeys, tc.wantFleet)
			}
			if (got.State == PresentKeysUnknown) != (got.Reason != "") {
				t.Errorf("reason yalnız unknown'da dolu olmalı: %+v", got)
			}
		})
	}

	if got := ResolvePresentKeys(nil, "cart", func(string) ([]string, bool, error) {
		t.Fatal("anahtarsız soru tarama yapmamalı")
		return nil, false, nil
	}); got.State != PresentKeysKnown {
		t.Errorf("boş anahtar = %+v", got)
	}
}

func TestClampPresentKeysWindow_v0101358(t *testing.T) {
	to := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"7 g ⇒ 24 sa", to.Add(-7 * 24 * time.Hour), to.Add(-24 * time.Hour)},
		{"tam 24 sa aynen", to.Add(-24 * time.Hour), to.Add(-24 * time.Hour)},
		{"1 sa aynen", to.Add(-time.Hour), to.Add(-time.Hour)},
		{"sıfır from ⇒ 24 sa", time.Time{}, to.Add(-24 * time.Hour)},
		{"ters pencere ⇒ 24 sa", to.Add(time.Hour), to.Add(-24 * time.Hour)},
	}
	for _, tc := range cases {
		f, gotTo := ClampPresentKeysWindow(tc.from, to)
		if !f.Equal(tc.want) || !gotTo.Equal(to) {
			t.Errorf("%s: (%v, %v), want (%v, %v)", tc.name, f, gotTo, tc.want, to)
		}
	}
	if _, gotTo := ClampPresentKeysWindow(time.Time{}, time.Time{}); gotTo.IsZero() {
		t.Error("sıfır to şimdiye çözülmeli")
	}
}
