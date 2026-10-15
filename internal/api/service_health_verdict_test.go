package api

// Servis sağlık özeti — deterministik hüküm eşikleri (her dal).

import (
	"errors"
	"strings"
	"testing"
)

func TestServiceHealthVerdict(t *testing.T) {
	base := aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}
	cases := []struct {
		name   string
		cur    aiRED
		err    error
		probs  int
		trs    int
		label  string
		nextIn string
	}{
		{"sağlıklı", aiRED{Spans: 900, ErrorRate: 0.6, P99Ms: 210}, nil, 0, 0, "Sağlıklı", "aksiyon gerekmiyor"},
		{"hata +2 puan", aiRED{Spans: 900, ErrorRate: 2.5, P99Ms: 210}, nil, 0, 3, "Dikkat", "Hatalı trace'ler"},
		{"hata mutlak %6", aiRED{Spans: 900, ErrorRate: 6, P99Ms: 210}, nil, 0, 3, "Sorunlu", "Hatalı trace'ler"},
		{"p99 +%30", aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 260}, nil, 0, 0, "Dikkat", "en yavaş operasyon"},
		{"p99 2.5×", aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 500}, nil, 0, 0, "Sorunlu", "en yavaş operasyon"},
		{"açık problem", aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 200}, nil, 2, 0, "Dikkat", "kök neden"},
		{"veri yok", aiRED{}, nil, 0, 0, "Veri yok", "genişletin"},
		{"okuma hatası", aiRED{Spans: 5}, errors.New("timeout"), 0, 0, "Veri yok", "genişletin"},
	}
	for _, c := range cases {
		l, r, n := serviceHealthVerdict(c.cur, base, c.err, c.probs, c.trs)
		if l != c.label || !strings.Contains(n, c.nextIn) || len(r) == 0 {
			t.Errorf("%s: %s %v %q", c.name, l, r, n)
		}
	}
	if out := renderServiceHealthVerdictTR("Dikkat", []string{"a", "b"}, "x"); !strings.HasPrefix(out, "HÜKÜM (deterministik eşik, model değil): Dikkat — a; b.") {
		t.Errorf("%s", out)
	}
}
