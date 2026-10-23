package sre

// trace.go — v0.10.1463 (CoSRE program D1, karar 7): arka plan araştırmasının
// OTel span ağacı.
//
// Neden: araştırma bugüne dek yalnız metrik bırakıyordu
// (selfobs.ObserveSREInvestigation); drain'in bütçe bağlamı kök span
// açmadığı için okuyucuların clickhouse.query span'ları öksüz kalıyordu ve
// "bu araştırma neden 40 sn sürdü / hangi okuyucu boş döndü / anlatım kaç
// token yaktı" sorusu trace olarak cevaplanamıyordu. Sohbetin ai.chat ağacının
// (internal/api/chat_span.go) arka plan ikizi:
//
//	sre.investigation  (kök, WithNewRoot; selfobs sampler.go HER ZAMAN örnekler)
//	├─ sre.step ×N     (deterministik okuyucu adımı; altında clickhouse.query)
//	└─ sre.narrate     (tek anlatım çağrısı; altında otelhttp "HTTP POST" →
//	                    traceparent vLLM'e)
//
// Attribute sözlüğü (tek yazım; trace_test.go pinler):
//
//	sre.investigation  coremetry.sre.{problem_id, kind, run, attempt, service,
//	                   env, priority, eval_run, case_id} (başta);
//	                   coremetry.sre.{state, outcome_reason, steps, llm_calls,
//	                   wall_ms, budget_exceeded}, gen_ai.usage.input_tokens /
//	                   output_tokens (sonda)
//	sre.step           coremetry.sre.step.{name, state, nodata, findings,
//	                   evidence, duration_ms}
//	sre.narrate        coremetry.sre.step.state, coremetry.ai.call_id,
//	                   gen_ai.system / request.model / usage.*
//
// GÖVDE (prompt / okuyucu argümanı / çıktı / anlatım metni) hiçbir attribute'a
// girmez — yalnız kimlik, sayı ve sınıf (ai_span.go kuralı; TestInvestigation
// SpanTree iğne taraması). outcome_reason ajanın kendi sabit hata cümlesidir.
//
// Self-obs kapalıyken selfobs.Tracer() noop: span yok, trace kimliği yok →
// Investigation.TraceID ve ai_calls.trace_id boş, davranış eskisi gibi.
// Çocuk span'lar kökün TracerProvider'ından açılır (ctx'teki span'ınki), test
// dikişi yalnız kökte (DrainDeps.Tracer).

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

// Span adları — selfobs.DefaultAlwaysSample kökü adıyla tanır.
const (
	spanInvestigation = "sre.investigation"
	spanStep          = "sre.step"
	spanNarrate       = "sre.narrate"
)

// invTally — kök span'ın sonda yazdığı sayaçlar (adım goroutine'leri ve
// anlatımın Observe'u paralel yazar).
type invTally struct {
	mu       sync.Mutex
	steps    int
	llmCalls int
	inTok    int64
	outTok   int64
}

type tallyKey struct{}

func tallyFrom(ctx context.Context) *invTally {
	t, _ := ctx.Value(tallyKey{}).(*invTally)
	return t
}

// childTracer — ctx'teki span'ın sağlayıcısından tracer (kök yoksa noop
// span'ın sağlayıcısı = noop).
func childTracer(ctx context.Context) trace.Tracer {
	return trace.SpanFromContext(ctx).TracerProvider().Tracer("coremetry")
}

// beginInvestigationSpan — kök span'ı açar; dönen ctx adım ve anlatım
// span'larının ebeveynidir. traceID yalnız span ÖRNEKLENMİŞSE dolu.
func beginInvestigationSpan(ctx context.Context, tr trace.Tracer, inv Investigation) (context.Context, string, func(final Investigation, budgetExceeded bool)) {
	if tr == nil {
		tr = selfobs.Tracer()
	}
	started := time.Now()
	tally := &invTally{}
	ctx = context.WithValue(ctx, tallyKey{}, tally)
	ctx, span := tr.Start(ctx, spanInvestigation,
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(investigationStartAttrs(inv)...))
	return ctx, selfobs.SampledTraceID(span.SpanContext()), func(final Investigation, budgetExceeded bool) {
		tally.mu.Lock()
		attrs := []attribute.KeyValue{
			attribute.String("coremetry.sre.state", final.State),
			attribute.Int("coremetry.sre.steps", tally.steps),
			attribute.Int("coremetry.sre.llm_calls", tally.llmCalls),
			attribute.Int64("gen_ai.usage.input_tokens", tally.inTok),
			attribute.Int64("gen_ai.usage.output_tokens", tally.outTok),
			attribute.Int64("coremetry.sre.wall_ms", time.Since(started).Milliseconds()),
			attribute.Bool("coremetry.sre.budget_exceeded", budgetExceeded),
		}
		tally.mu.Unlock()
		if final.Error != "" {
			attrs = append(attrs, attribute.String("coremetry.sre.outcome_reason", truncRunes(final.Error, 200)))
		}
		errMsg := ""
		if final.State == StateError {
			errMsg = "investigation failed"
		}
		selfobs.EndSpan(span, errMsg, attrs...)
	}
}

// investigationStartAttrs — SAF: kökün başlangıç attribute'ları (boş
// alanlar yazılmaz).
func investigationStartAttrs(inv Investigation) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("coremetry.sre.problem_id", inv.ProblemID),
		attribute.String("coremetry.sre.kind", inv.Kind),
		attribute.Int("coremetry.sre.run", inv.Run),
		attribute.Int("coremetry.sre.attempt", inv.Attempts),
		attribute.String("coremetry.sre.service", inv.Service),
	}
	for _, kv := range []struct{ k, v string }{
		{"coremetry.sre.env", inv.Env},
		{"coremetry.sre.priority", inv.Priority},
		{"coremetry.sre.eval_run", inv.EvalRun},
		{"coremetry.sre.case_id", inv.CaseID},
	} {
		if kv.v != "" {
			attrs = append(attrs, attribute.String(kv.k, kv.v))
		}
	}
	return attrs
}

// beginStepSpan — deterministik adımın span'ı; dönen işlev adım sonucunu
// yazar ve kapatır. Metrik ikizi (sre.step.duration) örneklemeden bağımsız.
func beginStepSpan(ctx context.Context, name string) (context.Context, func(o stepOutcome, d time.Duration)) {
	ctx, span := childTracer(ctx).Start(ctx, spanStep,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("coremetry.sre.step.name", name)))
	tally := tallyFrom(ctx)
	return ctx, func(o stepOutcome, d time.Duration) {
		if tally != nil {
			tally.mu.Lock()
			tally.steps++
			tally.mu.Unlock()
		}
		selfobs.ObserveSREStep(name, o.step.State, d)
		errMsg := ""
		if o.step.State == StepError {
			errMsg = "step failed"
		}
		selfobs.EndSpan(span, errMsg,
			attribute.String("coremetry.sre.step.state", o.step.State),
			attribute.Bool("coremetry.sre.step.nodata", o.step.NoData),
			attribute.Int("coremetry.sre.step.findings", len(o.findings)),
			attribute.Int("coremetry.sre.step.evidence", len(o.step.Evidence)),
			attribute.Int64("coremetry.sre.step.duration_ms", d.Milliseconds()))
	}
}

// narrateTraced — anlatım adımını sre.narrate span'ı içinde koşar. Token /
// model, copilot'un CallMeta.Observe kancasından (narrate meta'yı kurarken
// ctx'teki Observe'u taşır); çağrı kimliği özetten.
func narrateTraced(ctx context.Context, run func(context.Context) Step, inv *Investigation) Step {
	ctx, span := childTracer(ctx).Start(ctx, spanNarrate, trace.WithSpanKind(trace.SpanKindInternal))
	tally := tallyFrom(ctx)
	var mu sync.Mutex
	var usage []attribute.KeyValue
	ctx = copilot.WithMeta(ctx, copilot.CallMeta{Observe: func(u copilot.Usage) {
		mu.Lock()
		usage = []attribute.KeyValue{
			attribute.String("gen_ai.system", u.Provider),
			attribute.String("gen_ai.request.model", u.Model),
			attribute.Int("gen_ai.usage.input_tokens", int(u.InputTokens)),
			attribute.Int("gen_ai.usage.output_tokens", int(u.OutputTokens)),
			attribute.Int("gen_ai.usage.cache_read.input_tokens", int(u.CachedTokens)),
		}
		mu.Unlock()
		if tally != nil {
			tally.mu.Lock()
			tally.llmCalls++
			tally.inTok += int64(u.InputTokens)
			tally.outTok += int64(u.OutputTokens)
			tally.mu.Unlock()
		}
	}})
	prev := inv.Summary
	st := run(ctx)
	mu.Lock()
	attrs := append([]attribute.KeyValue{attribute.String("coremetry.sre.step.state", st.State)}, usage...)
	mu.Unlock()
	if inv.Summary != prev && inv.Summary != nil && inv.Summary.CallID != "" { // yalnız BU koşunun çağrısı
		attrs = append(attrs, attribute.String("coremetry.ai.call_id", inv.Summary.CallID))
	}
	errMsg := ""
	if st.State == StepError {
		errMsg = "narration failed"
	}
	selfobs.EndSpan(span, errMsg, attrs...)
	return st
}
