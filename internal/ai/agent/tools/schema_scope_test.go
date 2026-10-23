package tools

// schema_scope_test.go — v0.10.1456 (C1, karar 6): SchemaScope kipleri
// Executor üzerinden. warn: çağrı koşar, gözlenir, modele giden içerik
// doğrulamasız koşuyla BAYT BAYT aynı. enforce: handler koşmaz, Kind
// bad_args, alan-adlı ipucu; aynı aracın ardışık ikinci reddi Final
// ("bırak"); Next zinciri korunur. Fake "sre-loop" yüzeyi (2b henüz yok)
// karar 6'nın "1 retry, sonra skipped" muhasebesini sürer.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcp"
)

func scopeTestSchemas() map[string]map[string]any {
	return map[string]map[string]any{
		"query_traces": {
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string"},
				"range_s": map[string]any{"type": "integer", "minimum": 60, "maximum": 86400},
			},
			"required": []string{"service"},
		},
	}
}

func scopeTestHandlers(runs *int) map[string]Handler {
	return map[string]Handler{
		"query_traces": func(_ context.Context, _ json.RawMessage) (any, error) {
			*runs++
			return map[string]any{"rows": 3}, nil
		},
		"ext_tool": func(_ context.Context, _ json.RawMessage) (any, error) { *runs++; return "ext", nil },
	}
}

func TestSchemaScopeModes(t *testing.T) {
	bad := json.RawMessage(`{"range_s":"3600"}`)
	ctx := context.Background()

	// Referans: doğrulamasız (bugünkü) koşunun modele giden içeriği.
	var refRuns int
	ref := NewExecutor(scopeTestHandlers(&refRuns), nil, Hooks{}).Call(ctx, "query_traces", bad)

	t.Run("off", func(t *testing.T) {
		var runs int
		sc := NewSchemaScope(scopeTestSchemas(), ArgModeOff, nil)
		observed := 0
		sc.Observe = func(string, string, []mcp.ArgIssue) { observed++ }
		oc := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(sc).Call(ctx, "query_traces", bad)
		if !oc.Executed || runs != 1 || observed != 0 || oc.Content != ref.Content {
			t.Fatalf("off: %+v runs=%d observed=%d", oc, runs, observed)
		}
	})

	t.Run("unknown mode = off", func(t *testing.T) {
		var runs int
		oc := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(NewSchemaScope(scopeTestSchemas(), "strict-ish", nil)).Call(ctx, "query_traces", bad)
		if !oc.Executed || runs != 1 {
			t.Fatalf("unknown mode: %+v", oc)
		}
	})

	t.Run("warn runs and observes, model content identical", func(t *testing.T) {
		var runs int
		var seen [][]mcp.ArgIssue
		sc := NewSchemaScope(scopeTestSchemas(), ArgModeWarn, nil)
		sc.Observe = func(name, mode string, is []mcp.ArgIssue) {
			if name != "query_traces" || mode != ArgModeWarn {
				t.Errorf("observe %s %s", name, mode)
			}
			seen = append(seen, is)
		}
		x := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(sc)
		oc := x.Call(ctx, "query_traces", bad)
		if !oc.Executed || oc.IsError || oc.Kind != KindOK || runs != 1 {
			t.Fatalf("warn: %+v runs=%d", oc, runs)
		}
		if oc.Content != ref.Content || oc.IsError != ref.IsError || oc.Kind != ref.Kind {
			t.Fatalf("warn changed model content: %q vs %q", oc.Content, ref.Content)
		}
		if len(seen) != 1 || IssuesSummary(seen[0]) != "service:required,range_s:type" {
			t.Fatalf("observed = %+v", seen)
		}
		// Geçerli çağrı da gözlenir (issues nil).
		x.Call(ctx, "query_traces", json.RawMessage(`{"service":"orders-api"}`))
		if len(seen) != 2 || seen[1] != nil {
			t.Fatalf("valid observe = %+v", seen)
		}
	})

	t.Run("enforce rejects with bad_args", func(t *testing.T) {
		var runs int
		oc := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(NewSchemaScope(scopeTestSchemas(), ArgModeEnforce, nil)).Call(ctx, "query_traces", bad)
		if oc.Executed || !oc.IsError || oc.Kind != KindBadArgs || runs != 0 {
			t.Fatalf("enforce: %+v runs=%d", oc, runs)
		}
		var te mcp.ToolError
		if err := json.Unmarshal([]byte(oc.Content), &te); err != nil {
			t.Fatal(err)
		}
		if te.Error != mcp.ToolErrBadArgs || !te.Retryable || !strings.Contains(te.Hint, "range_s") || !strings.Contains(te.Hint, "service") {
			t.Fatalf("content = %s", oc.Content)
		}
	})

	t.Run("no schema = not validated (external tool)", func(t *testing.T) {
		var runs int
		oc := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(NewSchemaScope(scopeTestSchemas(), ArgModeEnforce, nil)).Call(ctx, "ext_tool", json.RawMessage(`{"anything":"goes"}`))
		if !oc.Executed || runs != 1 {
			t.Fatalf("ext: %+v", oc)
		}
	})

	t.Run("Next chained after validation", func(t *testing.T) {
		var runs int
		sc := NewSchemaScope(scopeTestSchemas(), ArgModeEnforce, denyScope{})
		byName := scopeTestHandlers(&runs)
		byName["search_traces"] = func(context.Context, json.RawMessage) (any, error) { runs++; return "x", nil }
		x := NewExecutor(byName, nil, Hooks{}).WithScope(sc)
		if oc := x.Call(ctx, "search_traces", json.RawMessage(`{}`)); oc.Kind != KindScope || runs != 0 {
			t.Fatalf("next deny: %+v", oc)
		}
		if oc := x.Call(ctx, "query_traces", json.RawMessage(`{"service":"a"}`)); !oc.Executed || runs != 1 {
			t.Fatalf("next allow: %+v", oc)
		}
		// warn kipinde de Next koşar
		sw := NewSchemaScope(scopeTestSchemas(), ArgModeWarn, denyScope{})
		if oc := NewExecutor(byName, nil, Hooks{}).WithScope(sw).Call(ctx, "search_traces", json.RawMessage(`{}`)); oc.Kind != KindScope {
			t.Fatalf("warn next deny: %+v", oc)
		}
	})
}

// fakeLoopStep — 2b döngüsünün (henüz yok) muhasebe sözleşmesinin asgari
// ikizi: her çağrı bir hak yakar; bad_args Final ise adım "skipped".
type fakeLoopStep struct {
	State   string // done | bad_args | skipped
	Content string
}

func fakeLoopRun(x *Executor, calls []struct{ name, args string }) (steps []fakeLoopStep, slots int) {
	for _, c := range calls {
		slots++
		oc := x.Call(context.Background(), c.name, json.RawMessage(c.args))
		st := "done"
		if oc.Kind == KindBadArgs {
			st = "bad_args"
			var ae *mcp.ArgIssuesErr
			if errors.As(oc.Err, &ae) && ae.Final {
				st = "skipped"
			}
		}
		steps = append(steps, fakeLoopStep{State: st, Content: oc.Content})
	}
	return steps, slots
}

func TestLoopBadArgsOneRetryThenSkipped(t *testing.T) {
	var runs int
	sc := NewSchemaScope(scopeTestSchemas(), ArgModeEnforce, nil)
	x := NewExecutor(scopeTestHandlers(&runs), nil, Hooks{}).WithScope(sc)
	steps, slots := fakeLoopRun(x, []struct{ name, args string }{
		{"query_traces", `{"range_s":"3600"}`},                   // 1. ret: retryable ipucu
		{"query_traces", `{"service":"a","range_s":10}`},         // 2. ardışık ret: Final → skipped
		{"query_traces", `{"service":"orders-api"}`},             // geçerli: koşar, sayaç sıfırlanır
		{"query_traces", `{"service":"orders-api","range_s":1}`}, // yeniden ilk ret
	})
	want := []string{"bad_args", "skipped", "done", "bad_args"}
	for i, s := range steps {
		if s.State != want[i] {
			t.Fatalf("step %d = %s, want %s (%s)", i, s.State, want[i], s.Content)
		}
	}
	if slots != 4 || runs != 1 {
		t.Fatalf("slots=%d runs=%d — her çağrı hak yakar, yalnız geçerli olan koşar", slots, runs)
	}
	var te mcp.ToolError
	_ = json.Unmarshal([]byte(steps[1].Content), &te)
	if te.Retryable || !strings.Contains(te.Hint, "BIRAK") {
		t.Fatalf("final content = %s", steps[1].Content)
	}
	_ = json.Unmarshal([]byte(steps[0].Content), &te)
	if !te.Retryable {
		t.Fatalf("first content = %s", steps[0].Content)
	}
}
