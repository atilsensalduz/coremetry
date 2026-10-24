package anomaly

import (
	"context"
	"testing"
	"time"
)

// v0.10.1495 regresyon testi — problem grafiği (VM yolu): sıfır istekli
// pencerede gecikme/oran BOŞLUK (null), sıfır değil; sayım metrikleri 0 kalır
// (değerlendirici de 0 okur). Her noktada pencerenin istek sayısı (N) taşınır
// — minSamples tabanı çağıranda (api) değerlendiriciyle aynı kuralla uygulanır.
func TestAlertVMSeriesZeroTrafficIsGap(t *testing.T) {
	fake := standardFake()
	base := fake.val
	fake.val = func(fam string, k alertVMKind, svc, phi string, win int) (float64, bool) {
		if svc == "a" && k == akCount {
			return 0, true // aile üyesi, pencerede istek yok
		}
		if svc == "a" && k == akQuant {
			return 0, true // VM histogram yüzdeliği boş pencerede 0 dönse de çizilmez
		}
		return base(fam, k, svc, phi, win)
	}
	r := newTestAlertReader(fake, "auto")
	r.clock = func() time.Time { return alertTestNow }
	vnow := alertTestNow.Add(-vmIngestLag)
	from := vnow.Add(-time.Hour).Truncate(300 * time.Second)
	for _, tc := range []struct {
		svc, metric string
		wantNil     bool
		wantN       float64
	}{
		{"a", "p99_ms", true, 0},
		{"a", "error_rate", true, 0},
		{"a", "avg_ms", true, 0},
		{"a", "request_rate", false, 0},
		{"b", "p99_ms", false, 600},
	} {
		pts, _, used := r.Series(context.Background(), tc.svc, tc.metric, 300, from, alertTestNow, 300)
		if !used || len(pts) == 0 {
			t.Fatalf("%s/%s: VM dizisi bekleniyordu (used=%v n=%d)", tc.svc, tc.metric, used, len(pts))
		}
		for _, p := range pts {
			if (p.V == nil) != tc.wantNil {
				t.Errorf("%s/%s: v=%v, boşluk bekleniyor=%v", tc.svc, tc.metric, p.V, tc.wantNil)
				break
			}
			if p.N == nil || *p.N != tc.wantN {
				t.Errorf("%s/%s: N=%v, want %v", tc.svc, tc.metric, p.N, tc.wantN)
				break
			}
		}
	}
}
