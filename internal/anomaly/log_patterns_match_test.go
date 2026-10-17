package anomaly

import "testing"

// v0.10.1291 — ✨ log satırı açıklamasının desen adı: dedektörle aynı tanım.
func TestMatchLogPatternName(t *testing.T) {
	cases := map[string]string{
		"java.sql.SQLException: ORA-00060: deadlock detected": "Oracle errors (ORA-)",
		"dial tcp 10.0.0.1:5432: connect: connection refused": "Connection refused",
		"Exception in thread main java.lang.OutOfMemoryError": "Out of memory",
		"GET /health 200": "",
		"":                "",
	}
	for body, want := range cases {
		if got := MatchLogPatternName(body); got != want {
			t.Errorf("%q → %q, want %q", body, got, want)
		}
	}
}
