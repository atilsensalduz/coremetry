package selfobs

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// EndSpan — v0.10.1463 (D1): span'ı son attribute'larıyla kapatır; errMsg
// doluysa durum Error. Tek kapanış noktası: internal/sre span'ları gövde
// (prompt / argüman / çıktı) taşımaz — yalnız sayı ve sınıf attribute'ları
// buradan geçer. Ayrıca sre paketinin "yazan çağrı yok" AST muhafızı
// (TestSRENoMutatingCalls, ^Set…) telemetri attribute'unu yazım saymasın
// diye Set* çağrıları bu pakette kalır.
func EndSpan(span trace.Span, errMsg string, attrs ...attribute.KeyValue) {
	if span == nil {
		return
	}
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	if errMsg != "" {
		span.SetStatus(codes.Error, errMsg)
	}
	span.End()
}

// SampledTraceID — ctx'teki span ÖRNEKLENMİŞSE trace kimliği (32 hex), aksi
// halde "". Örneklenmemiş (oran dışı) ya da noop span'ın kimliği hiçbir
// yerde sorgulanamaz; ai_calls.trace_id'ye yazılırsa /ai "Trace" linki boş
// bir sayfaya giderdi.
func SampledTraceID(sc trace.SpanContext) string {
	if !sc.IsValid() || !sc.IsSampled() {
		return ""
	}
	return sc.TraceID().String()
}
