package sre

// v0.10.1463 (CoSRE D1, karar 7) — arka plan araştırmasının span ağacı:
// sre.investigation kökü → sre.step ×N + sre.narrate; kimlik kayda; gövde
// hiçbir attribute'a girmez; self-obs kapalıyken (noop) iz de kimlik de yok.

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// usageNarrator — fakeNarrator + copilot'un Observe sözleşmesi (gerçek
// Service recordNarration'da SENKRON çağırır).
type usageNarrator struct{ *fakeNarrator }

func (n usageNarrator) Explain(ctx context.Context, sys, user string) (string, error) {
	out, err := n.fakeNarrator.Explain(ctx, sys, user)
	if obs := copilot.MetaFromContext(ctx).Observe; obs != nil {
		obs(copilot.Usage{Provider: "openai", Model: "gemma-test", Status: "ok", InputTokens: 812, OutputTokens: 140})
	}
	return out, err
}

const narrationSecret = "GİZLİ_ANLATIM_GÖVDESİ"

func runTracedDrain(t *testing.T, withTracer bool) (Investigation, *tracetest.InMemoryExporter, *fakeNarrator) {
	t.Helper()
	SetSettings(DefaultSettings())
	st := newFakeStore()
	if _, err := Enqueue(context.Background(), st, EnqueueRequest{ProblemID: "p1", Service: "checkout"}, CurrentSettings()); err != nil {
		t.Fatal(err)
	}
	fn := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: " + narrationSecret}
	rd := okReaders(&calls{}, usageNarrator{fn})
	exp := tracetest.NewInMemoryExporter()
	deps := DrainDeps{Store: st, Lock: newFakeLock(), PodID: "sre-1", Mode: "sre", Poll: time.Millisecond,
		Runner: func(ctx context.Context, inv *Investigation, cp func(*Investigation)) {
			runInvestigation(ctx, rd, inv, cp, func() time.Time { return t0 })
		}}
	if withTracer {
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		deps.Tracer = tp.Tracer("test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := StartDrain(ctx, deps)
	waitFor(t, "araştırma bitsin", func() bool {
		s := st.get(t, "p1").State
		return s == StateDone || s == StatePartial || s == StateError
	})
	cancel()
	<-done
	return st.get(t, "p1"), exp, fn
}

func TestInvestigationSpanTree(t *testing.T) {
	inv, exp, fn := runTracedDrain(t, true)
	spans := exp.GetSpans()
	var root tracetest.SpanStub
	byName := map[string][]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = append(byName[s.Name], s)
		if s.Name == spanInvestigation {
			root = s
		}
	}
	if len(byName[spanInvestigation]) != 1 {
		t.Fatalf("tek kök beklenir: %v", byName)
	}
	if root.Parent.IsValid() {
		t.Fatal("kök WithNewRoot ile açılmalı (ebeveynsiz)")
	}
	if inv.TraceID != root.SpanContext.TraceID().String() {
		t.Fatalf("kayıt trace kimliğini taşımalı: %q vs %s", inv.TraceID, root.SpanContext.TraceID())
	}
	ra := attrMapSRE(root.Attributes)
	for k, want := range map[string]string{
		"coremetry.sre.problem_id": "p1", "coremetry.sre.service": "checkout", "coremetry.sre.kind": KindAuto,
		"coremetry.sre.state": inv.State,
	} {
		if ra[k] != want {
			t.Errorf("kök %s = %q, want %q", k, ra[k], want)
		}
	}
	if ra["coremetry.sre.llm_calls"] != "1" || ra["gen_ai.usage.input_tokens"] != "812" || ra["gen_ai.usage.output_tokens"] != "140" ||
		ra["coremetry.sre.budget_exceeded"] != "false" || ra["coremetry.sre.wall_ms"] == "" {
		t.Errorf("kök sayaçları: %v", ra)
	}
	steps := byName[spanStep]
	if len(steps) == 0 || ra["coremetry.sre.steps"] != strconv.Itoa(len(steps)) {
		t.Fatalf("adım span'ları: %d, kök steps=%s", len(steps), ra["coremetry.sre.steps"])
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if s.Parent.SpanID() != root.SpanContext.SpanID() {
			t.Errorf("%s kökün çocuğu olmalı", s.Name)
		}
		a := attrMapSRE(s.Attributes)
		seen[a["coremetry.sre.step.name"]] = true
		if a["coremetry.sre.step.state"] == "" || a["coremetry.sre.step.duration_ms"] == "" {
			t.Errorf("adım attribute'ları eksik: %v", a)
		}
	}
	for _, n := range []string{stepSweep, stepDeploys, stepDeps, stepPivot, stepLogSpike, stepEvents} {
		if !seen[n] {
			t.Errorf("adım span'ı yok: %s (%v)", n, seen)
		}
	}
	nar := byName[spanNarrate]
	if len(nar) != 1 || nar[0].Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatalf("tek sre.narrate, kökün çocuğu: %v", nar)
	}
	na := attrMapSRE(nar[0].Attributes)
	if na["gen_ai.request.model"] != "gemma-test" || na["gen_ai.usage.input_tokens"] != "812" ||
		na["coremetry.ai.call_id"] == "" || na["coremetry.ai.call_id"] != fn.callID || na["coremetry.sre.step.state"] != StepOK {
		t.Errorf("anlatım span'ı: %v (callID %q)", na, fn.callID)
	}
	// Gövde iğne taraması: anlatım metni / prompt hiçbir attribute değerine,
	// gövde adlı hiçbir anahtar sözlüğe girmez.
	bodyKey := regexp.MustCompile(`(?i)prompt|args|body|content`)
	for _, s := range spans {
		for _, kv := range s.Attributes {
			if bodyKey.MatchString(string(kv.Key)) {
				t.Errorf("%s: gövde anahtarı %s", s.Name, kv.Key)
			}
			v := kv.Value.String()
			if strings.Contains(v, narrationSecret) || (len(fn.user) > 40 && strings.Contains(v, fn.user[:40])) {
				t.Errorf("%s: %s gövde taşıyor", s.Name, kv.Key)
			}
		}
	}
}

func TestStepSpanErrorStatus(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer func() { _ = tp.Shutdown(context.Background()) }()
	ctx, root := tp.Tracer("t").Start(context.Background(), spanInvestigation)
	_, end := beginStepSpan(ctx, stepDeploys)
	end(stepOutcome{step: Step{State: StepError}}, 3*time.Second)
	root.End()
	for _, s := range exp.GetSpans() {
		if s.Name == spanStep && (s.Status.Code != codes.Error || attrMapSRE(s.Attributes)["coremetry.sre.step.duration_ms"] != "3000") {
			t.Fatalf("hata adımı span durumu: %+v", s.Status)
		}
	}
}

func TestNoopWhenSelfObsOff(t *testing.T) {
	inv, exp, _ := runTracedDrain(t, false) // Tracer nil → selfobs.Tracer() (testte noop)
	if inv.TraceID != "" {
		t.Fatalf("self-obs kapalıyken trace kimliği yazılmamalı: %q", inv.TraceID)
	}
	if len(exp.GetSpans()) != 0 {
		t.Fatal("noop tracer span üretmez")
	}
	if inv.Summary == nil || inv.Summary.CallID == "" {
		t.Fatal("anlatım yine koşmalı (span'sız)")
	}
}

func attrMapSRE(kvs []attribute.KeyValue) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.String()
	}
	return m
}
