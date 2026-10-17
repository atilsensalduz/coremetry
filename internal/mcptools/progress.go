package mcptools

// progress.go — v0.10.1275 (CoSRE adım 6): bileşik araçların ilerleme kancası.
//
// pivot_from_trace gibi çok-sekmeli (hop) bir araç 15 sn'ye kadar sürebilir;
// sohbet operatöre "pod'lar okunuyor…" gibi ETİKET satırları gösterebilsin diye
// sunucu, aracın ctx'ine bir geri çağrı koyar (WithProgress). Çağrı yalnız
// etiket taşır — veri değil; sohbet onu `step {label, phase}` olayı olarak
// yayınlar (ekran-bağlamı çipiyle aynı şekil).
//
// GOROUTINE KURALI: agent executor handler'ı SENKRON koşar (tools/executor.go
// runTool) ve sohbetin `emit`'i tek goroutine'den çağrılmak üzere yazılmıştır.
// Bu yüzden progress YALNIZ handler'ın koordinatör goroutine'inden çağrılır —
// hop işçilerinden ASLA. ctx bittiyse ya da kanca yoksa (dış MCP istemcisi,
// SWR arka plan tazelemesi) çağrı no-op'tur.

import "context"

type progressKey struct{}

// ProgressFunc — (evre, etiket). Evre chat_step_phase.go'daki kırıntı dili
// ("Trace", "Altyapı", "Metrik", "Log").
type ProgressFunc func(phase, label string)

// WithProgress — ctx'e ilerleme kancası koyar. nil fn → ctx aynen.
func WithProgress(ctx context.Context, fn ProgressFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, progressKey{}, fn)
}

// reportProgress — kanca varsa ve ctx canlıysa çağırır. Koordinatör
// goroutine'inden çağrılmalı (yukarıdaki kural).
func reportProgress(ctx context.Context, phase, label string) {
	fn, ok := ctx.Value(progressKey{}).(ProgressFunc)
	if !ok || fn == nil || ctx.Err() != nil {
		return
	}
	fn(phase, label)
}
