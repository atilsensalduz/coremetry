package copilot

// v0.10.1463 (CoSRE D1) — sağlayıcı çağrısı self-telemetriye bağlanır:
// traceparent sağlayıcıya (vLLM) gider, ai_calls satırı ÖRNEKLENMİŞ trace
// kimliğini taşır; span yokken ikisi de yok (self-obs kapalı = eski davranış).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func traceTestService(t *testing.T) (*Service, *recSurfaceRec, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("traceparent"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	s := New("openai", "", "test-model")
	s.Configure("openai", "", "test-model", srv.URL, false, true)
	rec := &recSurfaceRec{ch: make(chan CallRecord, 4)}
	s.SetRecorder(rec)
	return s, rec, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func nextRecord(t *testing.T, rec *recSurfaceRec) CallRecord {
	t.Helper()
	select {
	case r := <-rec.ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("kayıt gelmedi")
	}
	return CallRecord{}
}

func TestOtelhttpTransportWraps(t *testing.T) {
	s, rec, seen := traceTestService(t)
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "sre.narrate")
	tid := span.SpanContext().TraceID().String()
	if _, err := s.Explain(WithMeta(ctx, CallMeta{Surface: "sre-investigation"}), "sys", "user"); err != nil {
		t.Fatal(err)
	}
	span.End()
	hdr := seen()
	if len(hdr) != 1 || !strings.HasPrefix(hdr[0], "00-"+tid+"-") {
		t.Fatalf("sağlayıcıya traceparent gitmedi ya da başka trace: %q (want trace %s)", hdr, tid)
	}
	if r := nextRecord(t, rec); r.TraceID != tid {
		t.Fatalf("CallRecord.TraceID = %q, want %q", r.TraceID, tid)
	}
	if baseTransport(s.httpClient()) == nil {
		t.Fatal("sarmal altındaki *http.Transport erişilebilir kalmalı")
	}
}

func TestCallRecordCarriesTraceID(t *testing.T) {
	s, rec, seen := traceTestService(t)
	// Span yok (self-obs kapalı / noop) → başlık yok, kimlik yok.
	if _, err := s.Explain(WithMeta(context.Background(), CallMeta{Surface: "explain-span"}), "sys", "user"); err != nil {
		t.Fatal(err)
	}
	if hdr := seen(); len(hdr) != 1 || hdr[0] != "" {
		t.Fatalf("span yokken traceparent gönderilmemeli: %q", hdr)
	}
	if r := nextRecord(t, rec); r.TraceID != "" {
		t.Fatalf("span yokken TraceID boş olmalı: %q", r.TraceID)
	}
	// Örneklenmemiş span: kimlik geçerli ama sorgulanamaz → yazılmaz.
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("t").Start(context.Background(), "ai.chat")
	s.RecordUsage(WithMeta(ctx, CallMeta{Surface: "chat"}), time.Now(), 1, 1, 0, "ok", "", "q", "a")
	span.End()
	if r := nextRecord(t, rec); r.TraceID != "" {
		t.Fatalf("örneklenmemiş span'ın kimliği yazılmamalı: %q", r.TraceID)
	}
	// Örneklenmiş sohbet span'ı: RecordUsage + RecordExchange bağlanır.
	tp2 := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp2.Shutdown(context.Background()) })
	ctx2, span2 := tp2.Tracer("t").Start(context.Background(), "ai.chat")
	defer span2.End()
	want := span2.SpanContext().TraceID().String()
	s.RecordUsage(WithMeta(ctx2, CallMeta{Surface: "chat"}), time.Now(), 1, 1, 0, "ok", "", "q", "a")
	s.RecordExchange(WithMeta(ctx2, CallMeta{Surface: "cosre-turn:loop"}), time.Now(), "ok", "", "q", "a")
	for i := 0; i < 2; i++ {
		if r := nextRecord(t, rec); r.TraceID != want {
			t.Fatalf("sohbet satırı trace'e bağlanmadı: %q want %q", r.TraceID, want)
		}
	}
}
