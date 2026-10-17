package mcptools

// v0.10.1291 — log satırı ✨ öznesi: kodek + sohbet tablosunun rowSubjects'i.
// İstemci ikizi frontend/src/lib/logLineSubject.ts aynı fixture'ı pinler
// (gövde "connection reset by peer" → aynı 8 hex).

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

func TestLogBodyHash(t *testing.T) {
	// FNV-1a 32 — istemci testindeki sabitle AYNI değer (logLineSubject.test.ts).
	if got := LogBodyHash("connection reset by peer"); got != "ca29aa39" {
		t.Fatalf("hash = %s", got)
	}
	long := strings.Repeat("ab", 200)
	// Kırpık gövde (sohbet 300 rune + …) ile tam gövde aynı özet.
	if LogBodyHash(long) != LogBodyHash(truncateRunes(long, 299)) {
		t.Fatal("ön ek özeti kırpmadan etkilenmemeli")
	}
	// FenceSafe ``` → ˋˋˋ geri dönüşü.
	if LogBodyHash("x ``` y") != LogBodyHash("x ˋˋˋ y") {
		t.Fatal("FenceSafe kaçışı özeti değiştirmemeli")
	}
}

func TestLogLineSubject(t *testing.T) {
	got := LogLineSubject(1700000000123000000, "pay ments", "pod:1", "0AF7651916CD43DD8448EB211C80319C", "connection reset by peer")
	want := "log:1700000000123000000:pay%20ments:pod%3A1:0af7651916cd43dd8448eb211c80319c:ca29aa39"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if LogLineSubject(0, "a", "", "", "x") != "" || LogLineSubject(1, " ", "", "", "x") != "" {
		t.Fatal("zamansız / servissiz satırın öznesi olmamalı")
	}
	if s := LogLineSubject(1, "a", "", "not-hex", "x"); !strings.Contains(s, ":a::") {
		t.Fatalf("geçersiz trace düşmeli: %s", s)
	}
}

func TestToolResultBlockLogsRowSubjects(t *testing.T) {
	logs := []qlRow{
		{TsMs: 1000, Severity: "ERROR", Service: "payments", Pod: "payments-7f9", TraceID: "0af7651916cd43dd8448eb211c80319c", Body: "boom"},
		{TsMs: 2000, Severity: "WARN", Body: "servissiz satır — istekten devralır"},
	}
	c := evBlockContent(t, cosreResult{Tool: queryLogsName, OK: true, Request: map[string]any{"service": "payments"},
		Data: map[string]any{"hasMore": false, "logs": logs}})
	b, ok := ToolResultBlock(queryLogsName, c, evFor(queryLogsName))
	if !ok || b.Type != blocks.TypeTable {
		t.Fatalf("table bekleniyordu: %+v", b)
	}
	p := b.Payload.(EvTablePayload)
	if len(p.RowSubjects) != len(p.Rows) {
		t.Fatalf("rowSubjects satırlarla paralel olmalı: %d / %d", len(p.RowSubjects), len(p.Rows))
	}
	if p.RowSubjects[0] != LogLineSubject(1000*1e6, "payments", "payments-7f9", logs[0].TraceID, "boom") {
		t.Fatalf("satır 0: %s", p.RowSubjects[0])
	}
	if !strings.HasPrefix(p.RowSubjects[1], "log:2000000000:payments::") {
		t.Fatalf("satır 1 istek servisini devralmalı: %s", p.RowSubjects[1])
	}
}

func TestSpikeSampleSubjects(t *testing.T) {
	if spikeSampleSubjects([]qlRow{{TsMs: 5, Body: "x"}}, "") != nil {
		t.Fatal("hiç özne yoksa alan yazılmamalı")
	}
	got := spikeSampleSubjects([]qlRow{{TsMs: 5, Body: "x"}, {TsMs: 6, Service: "a", Body: "y"}}, "")
	if len(got) != 2 || got[0] != "" || !strings.HasPrefix(got[1], "log:6000000:a:") {
		t.Fatalf("got %v", got)
	}
}
