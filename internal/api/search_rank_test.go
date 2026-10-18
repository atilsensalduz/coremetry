package api

// search_rank_test.go — v0.10.1333. rankSearchResults SAF sözleşmesi:
//   - relevance: tam eşleşme 1000 > önek 500 > içerir 200 (frontend
//     lib/paletteScore.ts ile aynı basamaklar, harf duyarsız); başlık dışı
//     alanlar (servis, açıklama…) en fazla "içerir" sayılır.
//   - eşit skorda tür sırası service → endpoint → problem → exception →
//     trace → log, sonra yenilik (ts DESC; ts'siz sonda).
//   - newest/oldest: ts'ye göre; ts'siz satırlar SONDA ve kendi aralarında
//     relevance sırası korunur (kararlı).
//   - severity: critical/P1/ERROR+ > warning/P2/WARN > info/P3/INFO;
//     loglarda aynı seviyede severity numarası belirler.

import (
	"testing"
)

func searchIDs(rows []SearchResult) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func eqIDs(t *testing.T, label string, got []SearchResult, want ...string) {
	t.Helper()
	g := searchIDs(got)
	if len(g) != len(want) {
		t.Fatalf("%s: %v, beklenen %v", label, g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("%s: %v, beklenen %v", label, g, want)
		}
	}
}

func TestMatchTier(t *testing.T) {
	cases := []struct {
		q, s string
		want int
	}{
		{"checkout", "Checkout", 1000},
		{"check", "checkout", 500},
		{"out", "checkout", 200},
		{"zzz", "checkout", 0},
		{"", "checkout", 0},
	}
	for _, c := range cases {
		if got := matchTier(c.q, c.s); got != c.want {
			t.Errorf("matchTier(%q,%q)=%d beklenen %d", c.q, c.s, got, c.want)
		}
	}
}

func TestRankSearchResultsTiersAndTypeOrder(t *testing.T) {
	rows := []SearchResult{
		{Type: "log", ID: "log-contains", Title: "error in checkout flow", Ts: 50},
		{Type: "endpoint", ID: "ep-prefix", Title: "checkout/submit"},
		{Type: "service", ID: "svc-exact", Title: "checkout"},
		{Type: "trace", ID: "tr-prefix", Title: "checkout-root", Ts: 10},
		{Type: "problem", ID: "pb-desc", Title: "High latency", matchFields: []string{"checkout"}, Ts: 99},
		{Type: "service", ID: "svc-prefix", Title: "checkout-v2"},
	}
	got := rankSearchResults(rows, "checkout", "relevance")
	// 1000: svc-exact | 500: svc-prefix, ep-prefix, tr-prefix (tür sırası) |
	// 200: pb-desc (açıklama tam eşleşse de "içerir" tavanı), log-contains.
	eqIDs(t, "relevance", got, "svc-exact", "svc-prefix", "ep-prefix", "tr-prefix", "pb-desc", "log-contains")
}

func TestRankSearchResultsRecencyTieBreak(t *testing.T) {
	rows := []SearchResult{
		{Type: "log", ID: "old", Title: "x checkout", Ts: 1},
		{Type: "log", ID: "nots", Title: "y checkout"},
		{Type: "log", ID: "new", Title: "z checkout", Ts: 9},
	}
	eqIDs(t, "eş skor + eş tür → yeni önce, ts'siz sonda", rankSearchResults(rows, "checkout", "relevance"), "new", "old", "nots")
}

func TestRankSearchResultsNewestOldestStable(t *testing.T) {
	rows := []SearchResult{
		{Type: "service", ID: "svc-a", Title: "checkout"},
		{Type: "service", ID: "svc-b", Title: "checkout-b"},
		{Type: "log", ID: "l1", Title: "checkout", Ts: 100},
		{Type: "trace", ID: "t1", Title: "checkout", Ts: 300},
		{Type: "problem", ID: "p1", Title: "checkout", Ts: 200},
	}
	eqIDs(t, "newest", rankSearchResults(rows, "checkout", "newest"), "t1", "p1", "l1", "svc-a", "svc-b")
	eqIDs(t, "oldest", rankSearchResults(rows, "checkout", "oldest"), "l1", "p1", "t1", "svc-a", "svc-b")
	// Kararlılık: aynı girdi iki kez aynı çıktı.
	a := searchIDs(rankSearchResults(rows, "checkout", "newest"))
	b := searchIDs(rankSearchResults(rows, "checkout", "newest"))
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("kararsız: %v vs %v", a, b)
		}
	}
}

func TestRankSearchResultsSeverity(t *testing.T) {
	rows := []SearchResult{
		{Type: "service", ID: "svc", Title: "checkout"},
		{Type: "log", ID: "log-warn", Title: "checkout", Severity: "WARN", level: "warning", sevNum: 13},
		{Type: "problem", ID: "pb-info", Title: "checkout", Severity: "info", level: "info"},
		{Type: "exception", ID: "ex-p1", Title: "checkout", Severity: "P1", level: "critical"},
		{Type: "log", ID: "log-fatal", Title: "checkout", Severity: "FATAL", level: "critical", sevNum: 21},
		{Type: "problem", ID: "pb-crit", Title: "checkout", Severity: "critical", level: "critical"},
		{Type: "log", ID: "log-err", Title: "checkout", Severity: "ERROR", level: "critical", sevNum: 17},
		{Type: "exception", ID: "ex-p2", Title: "checkout", Severity: "P2", level: "warning"},
	}
	got := rankSearchResults(rows, "checkout", "severity")
	// critical katmanı: log numarası önce (21 > 17 > 0), eşitlikte relevance
	// (tür sırası: problem → exception); sonra warning, info, seviyesizler.
	eqIDs(t, "severity", got, "log-fatal", "log-err", "pb-crit", "ex-p1", "log-warn", "ex-p2", "pb-info", "svc")
}

func TestSeverityLevels(t *testing.T) {
	for _, c := range []struct {
		num  uint8
		want string
	}{{24, "critical"}, {17, "critical"}, {16, "warning"}, {13, "warning"}, {12, "info"}, {9, "info"}, {5, ""}, {0, ""}} {
		if got := logLevel(c.num); got != c.want {
			t.Errorf("logLevel(%d)=%q beklenen %q", c.num, got, c.want)
		}
	}
	for _, c := range []struct{ p, want string }{{"P1", "critical"}, {"P2", "warning"}, {"P3", "info"}, {"", ""}} {
		if got := exceptionLevel(c.p); got != c.want {
			t.Errorf("exceptionLevel(%q)=%q beklenen %q", c.p, got, c.want)
		}
	}
	for _, c := range []struct {
		sev  string
		want uint8
	}{{"critical", 17}, {"warning", 13}, {"info", 9}, {"", 0}} {
		if got := logSeverityFloor(c.sev); got != c.want {
			t.Errorf("logSeverityFloor(%q)=%d beklenen %d", c.sev, got, c.want)
		}
	}
}
