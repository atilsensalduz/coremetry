package copilot

// profile_strict_test.go — v0.10.1456 (C1): ModelProfile.StrictToolArgs blob
// round-trip'i (omitempty: kapalı profil blobu bayt bayt eski), sameProfile
// değişikliği görür (SetProfiles runtime'ı yeniler), callSnapshot anahtarı
// isteğe taşır ve tool'lu istek kurucusu kopyalar.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileStrictRoundTrip(t *testing.T) {
	off, _ := json.Marshal(ModelProfile{ID: "a", Provider: ProviderOpenAI})
	if strings.Contains(string(off), "strict") {
		t.Fatalf("kapalı profil blobu değişti: %s", off)
	}
	on, _ := json.Marshal(ModelProfile{ID: "a", Provider: ProviderOpenAI, StrictToolArgs: true})
	var back ModelProfile
	if err := json.Unmarshal(on, &back); err != nil || !back.StrictToolArgs || !strings.Contains(string(on), `"strictToolArgs":true`) {
		t.Fatalf("round-trip: %s → %+v (%v)", on, back, err)
	}
	if sameProfile(ModelProfile{ID: "a"}, ModelProfile{ID: "a", StrictToolArgs: true}) {
		t.Fatal("sameProfile StrictToolArgs farkını görmeli")
	}
}

func TestCallSnapshotCarriesStrictTools(t *testing.T) {
	s := New(ProviderOpenAI, "k", "qwen3-32b")
	s.Configure(ProviderOpenAI, "k", "qwen3-32b", "http://llm.example.test/v1", false, true)
	_, req, _, _, _ := s.callSnapshot(context.Background())
	if req.StrictTools || chatRequest(context.Background(), req, "", nil, nil).StrictTools {
		t.Fatal("varsayılan profil strict olmamalı")
	}
	ps, def, sm := s.ProfilesSnapshot()
	for i := range ps {
		ps[i].StrictToolArgs = true
	}
	s.SetProfiles(ps, def, sm)
	_, req, _, _, _ = s.callSnapshot(context.Background())
	if !req.StrictTools || !chatRequest(context.Background(), req, "", nil, nil).StrictTools {
		t.Fatal("profil anahtarı isteğe inmedi")
	}
}
