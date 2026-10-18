package api

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// http_labels_test.go — v0.10.1350, Operator-reported (prod): /endpoint
// "Kaynak: metrik" "Hata BİLİNMİYOR: metrikte durum-kodu etiketi yok" derken
// aynı servisin HTTP Metrics sekmesi (VM) 200/400 çiziyordu. Kök neden:
// etiket varlığı filo genelinde 7 g /api/v1/labels taramasıyla soruluyordu,
// hata/kısmi cevap "yok" okunuyordu. Yeni seam veri güdümlü, üç hâlli.

func TestResolveStatusLabel_v0101350(t *testing.T) {
	cases := []struct {
		name  string
		ser   []chstore.SpanMetricSeries
		idx   int
		state statusLabelState
		key   string
	}{
		{"kod var", []chstore.SpanMetricSeries{{GroupKey: []string{"/a", ""}}, {GroupKey: []string{"/a", "200"}}}, 1, statusLabelPresent, "k"},
		{"hepsi boş", []chstore.SpanMetricSeries{{GroupKey: []string{"/a", ""}}, {GroupKey: []string{"/b", ""}}}, 1, statusLabelAbsent, ""},
		{"kısa tuple", []chstore.SpanMetricSeries{{GroupKey: []string{"/a"}}}, 1, statusLabelAbsent, ""},
		{"seri yok", nil, 0, statusLabelAbsent, ""},
		{"route değil durum konumu", []chstore.SpanMetricSeries{{GroupKey: []string{"/a", ""}}}, 1, statusLabelAbsent, ""},
	}
	for _, c := range cases {
		st, key := resolveStatusLabel(c.ser, c.idx, "k")
		if st != c.state || key != c.key {
			t.Errorf("%s: (%s,%q) beklenen (%s,%q)", c.name, st, key, c.state, c.key)
		}
	}
}

func TestIsHTTP5xx_v0101350(t *testing.T) {
	for v, want := range map[string]bool{"500": true, "503": true, "599": true, "400": false, "404": false, "200": false, "5000": false, "5xx": false, "": false} {
		if isHTTP5xx(v) != want {
			t.Errorf("isHTTP5xx(%q) != %v", v, want)
		}
	}
}

func TestProbeStatusLabel_v0101350(t *testing.T) {
	withCode := []chstore.SpanMetricSeries{{GroupKey: []string{"/a", "200"}}}
	empty := []chstore.SpanMetricSeries{{GroupKey: []string{"/a", ""}}}
	var asked []string
	q := func(answers map[string]func() ([]chstore.SpanMetricSeries, error)) func(string) ([]chstore.SpanMetricSeries, error) {
		asked = nil
		return func(k string) ([]chstore.SpanMetricSeries, error) {
			asked = append(asked, k)
			return answers[k]()
		}
	}
	ok := func(s []chstore.SpanMetricSeries) func() ([]chstore.SpanMetricSeries, error) {
		return func() ([]chstore.SpanMetricSeries, error) { return s, nil }
	}
	fail := func() ([]chstore.SpanMetricSeries, error) { return nil, errors.New("boom") }

	p := probeStatusLabel(q(map[string]func() ([]chstore.SpanMetricSeries, error){"http.response.status_code": ok(withCode)}), 1)
	if p.State != statusLabelPresent || p.Key != "http.response.status_code" || len(asked) != 1 {
		t.Fatalf("ilk aday tutar, ikinci SORULMAZ: %+v %v", p, asked)
	}
	p = probeStatusLabel(q(map[string]func() ([]chstore.SpanMetricSeries, error){"http.response.status_code": ok(empty), "http.status_code": ok(withCode)}), 1)
	if p.State != statusLabelPresent || p.Key != "http.status_code" || len(asked) != 2 {
		t.Fatalf("ıskada ikinci aday: %+v %v", p, asked)
	}
	p = probeStatusLabel(q(map[string]func() ([]chstore.SpanMetricSeries, error){"http.response.status_code": ok(empty), "http.status_code": ok(empty)}), 1)
	if p.State != statusLabelAbsent || p.Key != "" || len(p.Series) != 1 {
		t.Fatalf("hepsi boş ⇒ absent, seri yine çağrı serisi: %+v", p)
	}
	p = probeStatusLabel(q(map[string]func() ([]chstore.SpanMetricSeries, error){"http.response.status_code": fail}), 1)
	if p.State != statusLabelUnknown || p.Reason != "boom" || p.Series != nil || len(asked) != 1 {
		t.Fatalf("hata ⇒ unknown, absent DEĞİL: %+v %v", p, asked)
	}
	p = probeStatusLabel(q(map[string]func() ([]chstore.SpanMetricSeries, error){"http.response.status_code": ok(empty), "http.status_code": fail}), 1)
	if p.State != statusLabelUnknown || len(p.Series) != 1 {
		t.Fatalf("ikinci aday hatası ⇒ unknown, ilk başarılı seri korunur: %+v", p)
	}
	if _, err := statusRowsChecked([]chstore.SpanMetricSeries{{Points: make([]chstore.SpanMetricPoint, chstore.SpanMetricRowCap)}}, nil); !errors.Is(err, errStatusRowsCapped) {
		t.Fatalf("tavan ⇒ hata: %v", err)
	}
}

// Not metni — üç hâl × iki özne; "unknown" ASLA "etiket yok" demez.
func TestStatusLabelNote_v0101350(t *testing.T) {
	cases := []struct {
		state            statusLabelState
		key, reason, be  string
		subject, tail    string
		want, mustNotSay string
	}{
		{statusLabelPresent, "http.response.status_code", "", "vm", "Hata", "", " Hata = http_response_status_code 5xx (4xx hata sayılmaz).", "BİLİNMİYOR"},
		{statusLabelPresent, "http.response.status_code", "", "ch", "Failure rate", "", " Failure rate = http.response.status_code 5xx (4xx hata sayılmaz).", "BİLİNMİYOR"},
		{statusLabelAbsent, "", "", "vm", "Hata", " — Errors ve Rate boş", " Hata BİLİNMİYOR: metrikte durum-kodu etiketi yok (http.response.status_code / http.status_code) — Errors ve Rate boş.", "başarısız"},
		{statusLabelAbsent, "", "", "vm", "Failure rate", "", " Failure rate BİLİNMİYOR: metrikte durum-kodu etiketi yok (http.response.status_code / http.status_code).", "başarısız"},
		{statusLabelUnknown, "", "timeout", "vm", "Hata", " — Errors ve Rate boş", " Hata BİLİNMİYOR: durum sorgusu başarısız (timeout) — etiket yok DEĞİL.", "durum-kodu etiketi yok"},
		{statusLabelUnknown, "", "timeout", "vm", "Failure rate", "", " Failure rate BİLİNMİYOR: durum sorgusu başarısız (timeout) — etiket yok DEĞİL.", "durum-kodu etiketi yok"},
		{"", "", "", "vm", "Hata", "", "", "x"},
	}
	for _, c := range cases {
		got := statusLabelNote(c.state, c.key, c.reason, c.be, c.subject, c.tail)
		if got != c.want || (got != "" && strings.Contains(got, c.mustNotSay)) {
			t.Errorf("%s/%s/%s:\n got %q\nwant %q", c.state, c.be, c.subject, got, c.want)
		}
	}
}

// Çapraz dil pini: Go aday anahtarlarının VM sanitizasyonu == frontend
// lib/httpMetrics.ts HTTP_LABELS (sekme ile metrik kipi aynı etiketi okur).
func TestHTTPLabelsMatchFrontend_v0101350(t *testing.T) {
	src := readRepoFile(t, "../../frontend/src/lib/httpMetrics.ts")
	block := regexp.MustCompile(`(?s)export const HTTP_LABELS = \{(.*?)\} as const;`).FindStringSubmatch(src)
	if block == nil {
		t.Fatal("HTTP_LABELS bloğu bulunamadı")
	}
	got := map[string]string{}
	for _, m := range regexp.MustCompile(`(\w+):\s*'([^']+)'`).FindAllStringSubmatch(block[1], -1) {
		got[m[1]] = m[2]
	}
	want := map[string]string{
		"service": vmetrics.PromLabel("service.name"),
		"route":   vmetrics.PromLabel("http.route"),
		"method":  vmetrics.PromLabel("http.request.method"),
		"status":  vmetrics.PromLabel(httpStatusKeyCandidates[0]),
	}
	if len(got) != len(want) {
		t.Fatalf("HTTP_LABELS anahtarları: %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("HTTP_LABELS.%s = %q, Go sanitizasyonu %q", k, got[k], v)
		}
	}
}
