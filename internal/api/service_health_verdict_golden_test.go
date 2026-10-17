package api

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)
//
// Servis sağlık hükmü internal/healthverdict'e
// taşındı (service_health_sweep aynı eşikleri okur). Bu golden, taşımadan
// ÖNCEKİ uygulamanın çıktısıdır: serviceHealthVerdict + render bayt-bayt aynı
// kalmalı (operatörün gördüğü HÜKÜM satırı değişmez).

import (
	"errors"
	"testing"
)

func TestServiceHealthVerdictUnchanged(t *testing.T) {
	cases := []struct {
		cur, base aiRED
		err       error
		probs, tr int
		want      string
	}{
		{aiRED{Spans: 900, ErrorRate: 0.6, P99Ms: 210}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Sağlıklı — hata oranı %0.60, p99 210 ms — baseline içinde.\nÖNERİLEN ADIM: Şu an aksiyon gerekmiyor.\n"},
		{aiRED{Spans: 900, ErrorRate: 2.5, P99Ms: 210}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 3, "HÜKÜM (deterministik eşik, model değil): Dikkat — hata oranı %2.50, baseline'dan +2.0 puan.\nÖNERİLEN ADIM: Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın.\n"},
		{aiRED{Spans: 900, ErrorRate: 2.5, P99Ms: 210}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Dikkat — hata oranı %2.50, baseline'dan +2.0 puan.\nÖNERİLEN ADIM: Hatalı trace'ler ve error loglarına bakın.\n"},
		{aiRED{Spans: 900, ErrorRate: 6, P99Ms: 210}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 3, "HÜKÜM (deterministik eşik, model değil): Sorunlu — hata oranı %6.00 (baseline %0.50).\nÖNERİLEN ADIM: Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın.\n"},
		{aiRED{Spans: 900, ErrorRate: 7, P99Ms: 210}, aiRED{Spans: 1000, ErrorRate: 6.5, P99Ms: 200}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Sorunlu — hata oranı %7.00 (baseline %6.50).\nÖNERİLEN ADIM: Hatalı trace'ler ve error loglarına bakın.\n"},
		{aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 260}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Dikkat — p99 260 ms, baseline'dan +%30.\nÖNERİLEN ADIM: \"Overview\" bağlantısında en yavaş operasyona bakın; trace'te zamanın gittiği span'i bulun.\n"},
		{aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 500}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Sorunlu — p99 500 ms (baseline 200 ms, 2.5×).\nÖNERİLEN ADIM: \"Overview\" bağlantısında en yavaş operasyona bakın; trace'te zamanın gittiği span'i bulun.\n"},
		{aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 150}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 60}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Dikkat — p99 150 ms, baseline'dan +%150.\nÖNERİLEN ADIM: \"Overview\" bağlantısında en yavaş operasyona bakın; trace'te zamanın gittiği span'i bulun.\n"},
		{aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 200}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 2, 0, "HÜKÜM (deterministik eşik, model değil): Dikkat — 2 açık problem.\nÖNERİLEN ADIM: Açık problemi açıp kök neden panelini inceleyin.\n"},
		{aiRED{Spans: 900, ErrorRate: 9, P99Ms: 900}, aiRED{Spans: 1000, ErrorRate: 0.5, P99Ms: 200}, nil, 1, 4, "HÜKÜM (deterministik eşik, model değil): Sorunlu — hata oranı %9.00 (baseline %0.50); p99 900 ms (baseline 200 ms, 4.5×); 1 açık problem.\nÖNERİLEN ADIM: Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın.\n"},
		{aiRED{Spans: 900, ErrorRate: 0.5, P99Ms: 300}, aiRED{}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Sağlıklı — hata oranı %0.50, p99 300 ms — baseline içinde.\nÖNERİLEN ADIM: Şu an aksiyon gerekmiyor.\n"},
		{aiRED{}, aiRED{}, nil, 0, 0, "HÜKÜM (deterministik eşik, model değil): Veri yok — bu pencerede span verisi okunamadı.\nÖNERİLEN ADIM: Zaman aralığını genişletin ya da servis adını kontrol edin.\n"},
		{aiRED{Spans: 5}, aiRED{}, errors.New("timeout"), 0, 0, "HÜKÜM (deterministik eşik, model değil): Veri yok — bu pencerede span verisi okunamadı.\nÖNERİLEN ADIM: Zaman aralığını genişletin ya da servis adını kontrol edin.\n"},
		{aiRED{Spans: 900, ErrorRate: 3, P99Ms: 450}, aiRED{Spans: 1000, ErrorRate: 1, P99Ms: 200}, nil, 0, 2, "HÜKÜM (deterministik eşik, model değil): Sorunlu — hata oranı %3.00, baseline'dan +2.0 puan; p99 450 ms (baseline 200 ms, 2.2×).\nÖNERİLEN ADIM: Önce \"Hatalı trace'ler\" bağlantısından en yeni hatalı trace'i açın, ardından o trace'in loglarına bakın.\n"},
	}
	for i, c := range cases {
		l, r, n := serviceHealthVerdict(c.cur, c.base, c.err, c.probs, c.tr)
		if got := renderServiceHealthVerdictTR(l, r, n); got != c.want {
			t.Errorf("#%d:\n got %q\nwant %q", i, got, c.want)
		}
	}
}
