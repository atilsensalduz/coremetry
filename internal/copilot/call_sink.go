package copilot

// call_sink.go — CoSRE etkileşim günlüğü (cosre_interactions, Faz 1) turun
// HANGİ modelle cevaplandığını ve toplam token'ı bilmeli. Observe yalnız
// anlatım yolunda (Explain/StreamText) ve span kancası onu eziyor; sohbet
// döngüsü (RecordUsage) hiç çağırmıyor. Bu ayrı, zincirlenmeyen bir ctx
// kancası: her iki kayıt yolu da recorder'dan BAĞIMSIZ ve SENKRON çağırır.

import "context"

type callSinkKey struct{}

// CallSink — bir model çağrısı bittiğinde çağrılır (başarılı ya da değil).
type CallSink func(model string, inputTokens, outputTokens uint32)

// WithCallSink — ctx'e kanca bağlar; nil kanca ctx'i değiştirmez.
func WithCallSink(ctx context.Context, f CallSink) context.Context {
	if f == nil {
		return ctx
	}
	return context.WithValue(ctx, callSinkKey{}, f)
}

func notifyCallSink(ctx context.Context, model string, in, out uint32) {
	if ctx == nil {
		return
	}
	if f, ok := ctx.Value(callSinkKey{}).(CallSink); ok && f != nil {
		f(model, in, out)
	}
}
