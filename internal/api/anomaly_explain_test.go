package api

import (
	"testing"
	"time"
)

func TestParseExplainTime(t *testing.T) {
	want := time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"1792810800":                want,
		"1792810800000":             want, // ms
		"1792810800000000000":       want, // ns
		"2026-10-24T06:00:00+03:00": want, // yerel saat (operatörün Grafana'sı)
		"2026-10-24T03:00:00Z":      want,
		"":                          {},
		"dün":                       {},
	}
	for in, w := range cases {
		if got := parseExplainTime(in); !got.Equal(w) {
			t.Errorf("%q: %v, beklenen %v", in, got, w)
		}
	}
}
