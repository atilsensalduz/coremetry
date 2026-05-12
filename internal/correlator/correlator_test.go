package correlator

import (
	"testing"
)

// directSetup builds a Correlator without a backing store and
// populates the neighbor map directly. The public API path
// (Neighbors / AreNeighbors) is what consumers depend on, so
// exercising it bypassing refresh is the high-signal test.
func directSetup(t *testing.T, edges map[string][]string) *Correlator {
	t.Helper()
	c := &Correlator{neighbors: map[string]map[string]struct{}{}}
	for svc, ns := range edges {
		set := map[string]struct{}{}
		for _, n := range ns {
			set[n] = struct{}{}
		}
		c.neighbors[svc] = set
	}
	return c
}

func TestNeighborsReturnsAll(t *testing.T) {
	c := directSetup(t, map[string][]string{
		"api":      {"checkout", "auth"},
		"checkout": {"api", "payment"},
	})
	got := c.Neighbors("api")
	if len(got) != 2 {
		t.Fatalf("expected 2 neighbours, got %d (%v)", len(got), got)
	}
	// Order is map-iteration-dependent — collect into a set for the check.
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	if !seen["checkout"] || !seen["auth"] {
		t.Errorf("missing neighbour: got %v", got)
	}
}

func TestNeighborsUnknownService(t *testing.T) {
	c := directSetup(t, map[string][]string{"api": {"x"}})
	if got := c.Neighbors("ghost"); got != nil {
		t.Errorf("unknown service should return nil, got %v", got)
	}
}

func TestNeighborsLeafReturnsNil(t *testing.T) {
	c := directSetup(t, map[string][]string{"leaf": {}})
	if got := c.Neighbors("leaf"); got != nil {
		t.Errorf("empty set should return nil (caller uses len(nil)==0), got %v", got)
	}
}

func TestAreNeighborsSelfPair(t *testing.T) {
	c := directSetup(t, map[string][]string{})
	// A service is "near" itself — incident-attach uses this as the
	// single "consider related" predicate, so same-service incidents
	// must group.
	if !c.AreNeighbors("api", "api") {
		t.Fatal("self-pair must be neighbours")
	}
}

func TestAreNeighborsKnownEdge(t *testing.T) {
	c := directSetup(t, map[string][]string{
		"api":      {"checkout"},
		"checkout": {"api"},
	})
	if !c.AreNeighbors("api", "checkout") {
		t.Fatal("api ↔ checkout must be neighbours")
	}
	if !c.AreNeighbors("checkout", "api") {
		t.Fatal("relationship must be bidirectional via the map")
	}
}

func TestAreNeighborsUnrelated(t *testing.T) {
	c := directSetup(t, map[string][]string{
		"api":      {"checkout"},
		"checkout": {"api"},
		"reports":  {"warehouse"},
		"warehouse": {"reports"},
	})
	if c.AreNeighbors("api", "warehouse") {
		t.Fatal("two-hop services must not register as neighbours (1-hop only by design)")
	}
}

func TestEnsureCreatesAndReuses(t *testing.T) {
	m := map[string]map[string]struct{}{}
	a := ensure(m, "x")
	a["y"] = struct{}{}
	b := ensure(m, "x")
	if _, ok := b["y"]; !ok {
		t.Fatal("ensure must return the same map on repeat key, not a fresh one")
	}
}
