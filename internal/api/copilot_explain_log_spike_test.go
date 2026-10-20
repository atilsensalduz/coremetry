package api

// v0.10.1284 — AI SRE Faz 1a: explain_log_spike (log artışını ne açıklıyor).
// ✨ yüzeyinin saf parçaları: gövde → çekirdek isteği, kanıt paketi (≤4 KB,
// sunucu sırası, okunamadı ≠ yok), route kaydı.

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestLogSpikeRequestFromBody(t *testing.T) {
	from := time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC)
	to := from.Add(10 * time.Minute)
	r, err := logSpikeRequestFromBody(logSpikeExplainBody{Service: " checkout ", Mode: "sel", FromNs: from.UnixNano(), ToNs: to.UnixNano()})
	if err != nil || r.Service != "checkout" || !r.SpikeFrom.Equal(from) || !r.SpikeTo.Equal(to) || !r.From.IsZero() {
		t.Fatalf("sel: %+v %v", r, err)
	}
	r, err = logSpikeRequestFromBody(logSpikeExplainBody{Mode: "auto", FromNs: from.UnixNano(), ToNs: to.UnixNano()})
	if err != nil || !r.SpikeFrom.IsZero() || !r.From.Equal(from) || !r.To.Equal(to) {
		t.Fatalf("auto: %+v %v", r, err)
	}
	for _, b := range []logSpikeExplainBody{
		{Mode: "auto", FromNs: to.UnixNano(), ToNs: from.UnixNano()},
		{Mode: "auto"},
		{Mode: "zoom", FromNs: from.UnixNano(), ToNs: to.UnixNano()},
	} {
		if _, err := logSpikeRequestFromBody(b); err == nil {
			t.Errorf("geçersiz gövde kabul edildi: %+v", b)
		}
	}
}

func TestRenderLogSpikeTR(t *testing.T) {
	base := time.Date(2026, 10, 17, 14, 5, 0, 0, time.UTC).UnixMilli()
	res := mcptools.LogSpikeResult{
		OK:      true,
		Request: map[string]any{"service": "checkout", "env": "prod", "level": "all"},
		Spike: &mcptools.LogSpikeWindow{FromMs: base, ToMs: base + 7*60000, Source: "detected", Factor: 5.9,
			RatePerMinSpike: 1840, RatePerMinBase: 310, BaseFromMs: base - 21*60000, BaseToMs: base},
		Contributors: []mcptools.SpikeContributor{
			{Rank: 1, Dim: "pod", Value: "checkout-7f9-abc", SharePct: 62, SpikePct: 71, BasePct: 12, Reason: "artışın %62'si pod checkout-7f9-abc kaynaklı (artışta payı %71, normalde %12)"},
			{Rank: 2, Dim: "pattern", Value: "Connection reset <x>", SharePct: 35, SpikePct: 35, New: true, Reason: "yeni desen «Connection reset <x>» — artışın %35'i (öncesinde görülmedi)"},
		},
		Dims:     []mcptools.LogSpikeDim{{Dim: "pod", State: "ok"}, {Dim: "cluster", State: "unmapped"}, {Dim: "logger", State: "timeout"}},
		Warnings: []string{"env süzgeci uygulanamadı"},
	}
	out := renderLogSpikeTR(res)
	for _, want := range []string{
		"KAPSAM: servis=checkout · env=prod · seviye=tümü",
		"ARTIŞ: 14:05–14:12 UTC (7 dk, otomatik tespit) — dakikada 1840 satır; taban 13:44–14:05 dakikada 310 → ×5,9",
		"1. pod checkout-7f9-abc — artıştaki pay %62 (artışta %71, normalde %12)",
		"2. pattern Connection reset <x> — artıştaki pay %35 (artışta %35, normalde yok (yeni))",
		"BOYUTLAR: cluster eşlenmedi; logger OKUNAMADI (timeout)",
		"UYARI: env süzgeci uygulanamadı",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pakette yok: %q\n---\n%s", want, out)
		}
	}
	if strings.Index(out, "1. pod") > strings.Index(out, "2. pattern") {
		t.Fatal("katkı sırası korunmalı")
	}

	// Tavan: rune sınırında ≤4 KB.
	long := res
	long.Warnings = nil
	for i := 0; i < 200; i++ {
		long.Warnings = append(long.Warnings, strings.Repeat("ğ", 150))
	}
	big := renderLogSpikeTR(long)
	if len(big) > logSpikeExplainMaxBytes || !utf8.ValidString(big) || !strings.HasSuffix(big, "(kanıt kısaltıldı)") {
		t.Fatalf("tavan: %d B valid=%v", len(big), utf8.ValidString(big))
	}
}

func TestLogSpikeRouteRegistered(t *testing.T) {
	b, err := os.ReadFile("ai_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"POST   /api/copilot/explain-log-spike", s.requireCopilot(s.copilotExplainLogSpike)`) {
		t.Fatal("explain-log-spike route'u requireCopilot ile kayıtlı değil")
	}
	h, err := os.ReadFile("copilot_explain_log_spike.go")
	if err != nil {
		t.Fatal(err)
	}
	// v0.10.1407 — istem çifti ortak kurucudan (buildLogSpikeExplain; sohbet turu da kullanır).
	if !strings.Contains(string(h), "s.copilotExplain(r, b.System, b.User)") || !strings.Contains(string(h), "newExplainBuilt(copilot.SystemPromptLogSpike(), renderLogSpikeTR(res))") || strings.Contains(string(h), "s.copilot.Explain(") {
		t.Fatal("yüzey s.copilotExplain üzerinden gitmeli (/ai atfı)")
	}
}
