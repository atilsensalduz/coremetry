package sampling

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

// TestEmptyConfigKeepsEverything locks in the "no config = full
// retention" semantics. Pre-v0.4.84 an empty config was treated as
// `Default=0` which would have silently dropped every probabilistic
// span on a fresh install — a foot-gun for first-boot operators.
func TestEmptyConfigKeepsEverything(t *testing.T) {
	s := New(config.SamplingConfig{})
	span := &chstore.Span{TraceID: "abc", ServiceName: "x", StatusCode: "ok"}
	if !s.Decide(span) {
		t.Fatal("empty config must keep all spans (cold-start safety)")
	}
}

func TestAlwaysKeepErrors(t *testing.T) {
	keepErr, keepRoot := true, false
	s := New(config.SamplingConfig{
		Default:          0.0, // would otherwise drop everything
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	err := &chstore.Span{TraceID: "z", ServiceName: "x", StatusCode: "error", ParentID: "p"}
	if !s.Decide(err) {
		t.Fatal("error span must be kept regardless of probabilistic ratio")
	}
	ok := &chstore.Span{TraceID: "z", ServiceName: "x", StatusCode: "ok", ParentID: "p"}
	if s.Decide(ok) {
		t.Fatal("non-error span at ratio=0 must be dropped")
	}
}

func TestAlwaysKeepRoots(t *testing.T) {
	keepErr, keepRoot := false, true
	s := New(config.SamplingConfig{
		Default:          0.0,
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	root := &chstore.Span{TraceID: "z", ServiceName: "x", StatusCode: "ok", ParentID: ""}
	if !s.Decide(root) {
		t.Fatal("root span must be kept regardless of probabilistic ratio")
	}
}

func TestNilSpanDropped(t *testing.T) {
	s := New(config.SamplingConfig{Default: 1.0})
	if s.Decide(nil) {
		t.Fatal("nil span must be dropped — caller bug guard")
	}
}

func TestServiceOverrideBeatsDefault(t *testing.T) {
	keepErr, keepRoot := false, false
	s := New(config.SamplingConfig{
		Default:          1.0, // keep all by default
		Services:         map[string]float64{"noisy": 0.0},
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	noisy := &chstore.Span{TraceID: "z", ServiceName: "noisy", StatusCode: "ok", ParentID: "p"}
	if s.Decide(noisy) {
		t.Fatal("service override (ratio=0) must drop even when default=1.0")
	}
	other := &chstore.Span{TraceID: "z", ServiceName: "other", StatusCode: "ok", ParentID: "p"}
	if !s.Decide(other) {
		t.Fatal("default=1.0 service must keep")
	}
}

func TestTraceIDStability(t *testing.T) {
	// Critical correctness property: every span with the same trace_id
	// must hit the same keep/drop decision so we never produce partial
	// traces.
	keepErr, keepRoot := false, false
	s := New(config.SamplingConfig{
		Default:          0.5,
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	ids := []string{
		"4bf92f3577b34da6a3ce929d0e0e4736",
		"0000000000000000ffffffffffffffff",
		"deadbeefcafebabe1234567890abcdef",
	}
	for _, id := range ids {
		a := s.Decide(&chstore.Span{TraceID: id, ServiceName: "x", ParentID: "p"})
		b := s.Decide(&chstore.Span{TraceID: id, ServiceName: "x", ParentID: "p"})
		if a != b {
			t.Fatalf("trace_id %q: non-deterministic decision (%v vs %v)", id, a, b)
		}
	}
}

func TestClamp01(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-1.5, 0},
		{0.0, 0},
		{0.5, 0.5},
		{1.0, 1.0},
		{2.0, 1.0},
	}
	for _, c := range cases {
		if got := clamp01(c.in); got != c.want {
			t.Errorf("clamp01(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRatioToThreshold(t *testing.T) {
	if ratioToThreshold(0) != 0 {
		t.Fatal("ratio=0 should give threshold=0 (drop-all)")
	}
	if ratioToThreshold(1.0) != 0xffffffff {
		t.Fatal("ratio=1 should give max threshold (keep-all)")
	}
	mid := ratioToThreshold(0.5)
	const expected = uint32(0x7fffffff)
	// allow ±1 for rounding
	if mid < expected-1 || mid > expected+1 {
		t.Fatalf("ratio=0.5 threshold off: got %x, want ~%x", mid, expected)
	}
}

func TestIsError(t *testing.T) {
	cases := map[string]bool{
		"error":             true,
		"ERROR":             true,
		"Status_Code_Error": true,
		"ok":                false,
		"":                  false,
		"unset":             false,
	}
	for s, want := range cases {
		if got := isError(s); got != want {
			t.Errorf("isError(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestSnapshotRoundtrip(t *testing.T) {
	keepErr, keepRoot := true, false
	cfg := config.SamplingConfig{
		Default:          0.25,
		Services:         map[string]float64{"checkout": 1.0, "noisy": 0.05},
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	}
	s := New(cfg)
	snap := s.Snapshot()
	if snap.Default < 0.24 || snap.Default > 0.26 {
		t.Errorf("Default lost precision: got %v", snap.Default)
	}
	if v := snap.Services["checkout"]; v < 0.99 {
		t.Errorf("checkout service override drifted: got %v, want ~1.0", v)
	}
	if snap.AlwaysKeepErrors == nil || !*snap.AlwaysKeepErrors {
		t.Error("AlwaysKeepErrors lost")
	}
	if snap.AlwaysKeepRoots == nil || *snap.AlwaysKeepRoots {
		t.Error("AlwaysKeepRoots lost")
	}
}

func TestReloadSwapsConfig(t *testing.T) {
	keepErr, keepRoot := false, false
	s := New(config.SamplingConfig{
		Default:          0.0, // drop all
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	ok := &chstore.Span{TraceID: "z", ServiceName: "x", ParentID: "p"}
	if s.Decide(ok) {
		t.Fatal("pre-reload: ratio=0 should drop")
	}
	s.Reload(config.SamplingConfig{
		Default:          1.0,
		AlwaysKeepErrors: &keepErr,
		AlwaysKeepRoots:  &keepRoot,
	})
	if !s.Decide(ok) {
		t.Fatal("post-reload: ratio=1 should keep")
	}
}
