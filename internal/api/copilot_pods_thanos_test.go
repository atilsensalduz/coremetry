package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// v0.10.1250 — pod sorusu Thanos envanterini de okur (Infrastructure ile aynı seçici).
func TestThanosServicePodRegex(t *testing.T) {
	cases := map[[2]string]string{
		{"bsa-mobile-login-prod", ""}: `(bsa-mobile-login-prod|bsa-mobile-login)-.*`,
		{"shop.api-prod", "shop-api"}: `(shop-api|shop\.api-prod|shop\.api)-.*`,
		{"checkout", "checkout"}:      `(checkout)-.*`,
	}
	for in, want := range cases {
		if got := thanosServicePodRegex(in[0], in[1]); got != want {
			t.Errorf("%v → %q, want %q", in, got, want)
		}
	}
}

func TestRenderThanosPodsTR(t *testing.T) {
	if renderThanosPodsTR(thanosPodsResult{}, "s") != "" {
		t.Fatal("Thanos yok/boş → kanıt yok")
	}
	res := thanosPodsResult{Rows: []thanos.PodRow{
		{Cluster: "ocpa", Pod: "s-1", Phase: "Running", Restarts: 0, CPUCores: 0.5, MemBytes: 2 << 30},
		{Cluster: "ocpa", Pod: "s-2", Phase: "Pending", Restarts: 3, CPUCores: 0.1, MemBytes: 512 << 20},
		{Cluster: "ocpb", Pod: "s-3", Restarts: 1, MemBytes: 1 << 30},
	}, Errors: []string{"ocpz"}}
	out := renderThanosPodsTR(res, "s")
	for _, want := range []string{"3 pod, 2 cluster", "- ocpa: 2 pod (1 çalışıyor), restart 3", "- ocpa/s-2: Pending, restart 3", "Okunamayan cluster (Thanos zaman aşımı/hata): ocpz"} {
		if !strings.Contains(out, want) {
			t.Errorf("çıktıda %q yok:\n%s", want, out)
		}
	}
	if !strings.Contains(renderThanosPodsTR(thanosPodsResult{Errors: []string{"x"}}, "s"), "pod bulunamadı") {
		t.Error("yalnız hata → bulunamadı + hata satırı")
	}
}

func TestPodHTTPChartFences(t *testing.T) {
	f := podHTTPChartFences("s", 3600)
	if strings.Count(f, "```chart") != 2 || !strings.Contains(f, `"source":"metric"`) || !strings.Contains(f, `"agg":"rate"`) || !strings.Contains(f, `"agg":"p99"`) {
		t.Fatalf("grafik kartları: %s", f)
	}
}

// v0.10.1250 — deploy sorusu en az 24 saat geriye bakar.
func TestGuidedDeployMinLookback(t *testing.T) {
	if guidedDeployMinLookback.Hours() != 24 {
		t.Fatalf("taban %v", guidedDeployMinLookback)
	}
}

// v0.10.1251 — "bff" önekle tek servise (bff-asset-management) çözülüyordu;
// kelime başka servislerde tam ad parçasıysa aile yoluna düşer.
func TestSharedSegmentTokenIsFamily(t *testing.T) {
	svcs := []string{"bff-asset-management-prod", "mobile-overview-bff-prod", "mobile-crm-bff-prod", "checkout"}
	for _, q := range []string{"bff sorununu incelemek için nereye bakmalıyım?", "bff'ler nasıl?"} {
		r := routeGuidedIntent(q, svcs, nil, nil, "")
		if r.Service == "bff-asset-management-prod" || len(r.Family) < 3 {
			t.Errorf("%q → svc=%q fam=%v, aile bekleniyordu", q, r.Service, r.Family)
		}
	}
	if r := routeGuidedIntent("checkout nasıl", svcs, nil, nil, ""); r.Service != "checkout" {
		t.Errorf("tekil servis bozuldu: %+v", r)
	}
	if serviceSegmentHits("bff", svcs) != 2 || serviceSegmentHits("crm", svcs) != 1 {
		t.Error("segment sayımı")
	}
}
