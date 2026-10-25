package api

// problem_standard_test.go — v0.10.1529 (Problem standardı R2). Çivilenenler:
// ref ayrıştırması (InboxItem.ID ile aynı biçim), önbellek anahtarının tek
// girdisi, D10 bölmesi (düşük güven altta; Güven sıralamasında bölme yok;
// std'siz satır low sayılmaz) ve mapper'ların kaynak nesneyi taşıması.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestParseStdRef(t *testing.T) {
	cases := []struct {
		in       string
		kind, id string
		ok       bool
	}{
		{"problem:abc", "problem", "abc", true},
		{"exception:fp:with:colons", "exception", "fp:with:colons", true},
		{"anomaly:1", "anomaly", "1", true},
		{"incident:x", "incident", "x", true},
		{" problem:abc ", "problem", "abc", true},
		{"problem:", "", "", false},
		{"widget:1", "", "", false},
		{"abc", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		k, id, ok := parseStdRef(c.in)
		if k != c.kind || id != c.id || ok != c.ok {
			t.Errorf("parseStdRef(%q) = %q,%q,%v; want %q,%q,%v", c.in, k, id, ok, c.kind, c.id, c.ok)
		}
	}
}

func TestProblemStandardKeyCarriesRef(t *testing.T) {
	if problemStandardKey("problem:a") == problemStandardKey("problem:b") {
		t.Fatal("farklı ref aynı anahtar — çapraz zehirlenme (v0.5.187)")
	}
	if problemStandardKey("problem:a") == problemStandardKey("exception:a") {
		t.Fatal("tür anahtarda değil")
	}
}

func stdItem(id, prio, conf string, started int64) InboxItem {
	it := InboxItem{ID: id, Priority: prio, StartedAt: started, LastSeen: started}
	if conf != "" {
		it.Std = &chstore.ProblemStandard{Confidence: chstore.StdConfidence{Level: conf}}
	}
	return it
}

func stdIDs(items []InboxItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func TestInboxSortAndCapLowConfidenceLast(t *testing.T) {
	mk := func() []InboxItem {
		return []InboxItem{
			stdItem("a", "P1", "low", 400),
			stdItem("b", "P1", "high", 300),
			stdItem("c", "P1", "", 200), // std yok = low DEĞİL
			stdItem("d", "P1", "medium", 100),
		}
	}
	cases := []struct {
		name, sortID, dir string
		limit             int
		want              []string
	}{
		{"firstSeen desc: low sona", "firstSeen", "desc", 0, []string{"b", "c", "d", "a"}},
		{"tavan low'u keser", "firstSeen", "desc", 3, []string{"b", "c", "d"}},
		{"confidence desc: bölme yok, kolon sırası", "confidence", "desc", 0, []string{"b", "c", "d", "a"}},
		{"confidence asc: low başta", "confidence", "asc", 0, []string{"a", "c", "d", "b"}},
	}
	for _, c := range cases {
		got, total := inboxSortAndCap(mk(), c.sortID, c.dir, c.limit)
		if total != 4 {
			t.Errorf("%s: total=%d", c.name, total)
		}
		g := stdIDs(got)
		if len(g) != len(c.want) {
			t.Fatalf("%s: got %v want %v", c.name, g, c.want)
		}
		for i := range g {
			if g[i] != c.want[i] {
				t.Errorf("%s: got %v want %v", c.name, g, c.want)
				break
			}
		}
	}
}

func TestNormalizeInboxSortConfidence(t *testing.T) {
	if id, _ := normalizeInboxSort("confidence", "asc"); id != "confidence" {
		t.Fatalf("confidence sıralaması tanınmadı: %q", id)
	}
}

func TestInboxMappersCarryStdSrc(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	p := problemToInbox(chstore.Problem{ID: "p1", Service: "svc", RuleID: "anomaly:svc:error_rate", Metric: "error_rate",
		Value: 5, Threshold: 1, StartedAt: now.Add(-time.Hour).UnixNano(), Status: "open"})
	e := exceptionToInbox(chstore.ExceptionGroup{Fingerprint: "fp", Service: "svc", Type: "NPE", Occurrences: 10,
		FirstSeen: now.Add(-time.Hour).UnixNano(), LastSeen: now.UnixNano()})
	a := anomalyToInbox(chstore.AnomalyEvent{ID: "a1", Service: "svc", Kind: "trace_op", Pattern: "GET /x", PeakRatio: 3})
	inc := incidentToInbox(chstore.Incident{ID: "i1", Title: "t", Status: "open"})
	for _, it := range []InboxItem{p, e, a, inc} {
		if it.stdSrc == nil {
			t.Fatalf("%s: stdSrc yok", it.ID)
		}
		std := standardizeInboxSrc(it.stdSrc, chstore.StdFacts{Now: now}, 0)
		if std == nil || std.Headline == "" || std.Confidence.Level == "" {
			t.Fatalf("%s: standart eksik: %+v", it.ID, std)
		}
	}
	// Exception kopyası liste merdiveninin önceliğini taşır (büyüklük kolu).
	if e.stdSrc.exception.Priority != e.Priority {
		t.Fatalf("exception kopyası öncelik taşımıyor: %q vs %q", e.stdSrc.exception.Priority, e.Priority)
	}
}

func TestStandardizeInboxSrcNil(t *testing.T) {
	if standardizeInboxSrc(nil, chstore.StdFacts{}, 0) != nil {
		t.Fatal("nil kaynak nil dönmeli")
	}
	if standardizeInboxSrc(&inboxStdSrc{}, chstore.StdFacts{}, 0) != nil {
		t.Fatal("boş kaynak nil dönmeli")
	}
}
