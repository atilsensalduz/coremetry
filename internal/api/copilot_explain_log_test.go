package api

// v0.10.1291 — ✨ log satırı: satır seçimi (özet eşleşmesi / en yakın), kanıt
// paketi tavanları, gövde doğrulaması. Saf; logstore / model yok.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestExplainLogInput(t *testing.T) {
	const ts = int64(1_700_000_000_000_000_000)
	near := &logstore.LogRecord{Timestamp: ts + 1e6, Body: "başka satır", ServiceName: "checkout"}
	target := &logstore.LogRecord{Timestamp: ts + 400e6, Body: "HikariPool-1 - Connection is not available", ServiceName: "checkout"}
	far := &logstore.LogRecord{Timestamp: ts - 900e6, Body: "HikariPool-1 - Connection is not available", ServiceName: "checkout"}
	hash := mcptools.LogBodyHash(target.Body)

	t.Run("özet eşleşmesi kazanır, eşleşenlerden en yakını", func(t *testing.T) {
		got, exact := pickLogLine([]*logstore.LogRecord{near, far, nil, target}, ts, hash)
		if got != target || !exact {
			t.Fatalf("got %+v exact=%v", got, exact)
		}
	})
	t.Run("eşleşme yoksa en yakın + not", func(t *testing.T) {
		got, exact := pickLogLine([]*logstore.LogRecord{far, near}, ts, "deadbeef")
		if got != near || exact {
			t.Fatalf("got %+v exact=%v", got, exact)
		}
		out := renderLogLineTR(logLineInput{Line: got})
		if !strings.Contains(out, "NOT: tam eşleşme bulunamadı") {
			t.Fatalf("not yok:\n%s", out)
		}
	})
	t.Run("boş girdi", func(t *testing.T) {
		if got, _ := pickLogLine(nil, ts, hash); got != nil {
			t.Fatal("nil beklendi")
		}
	})
	t.Run("tavanlar", func(t *testing.T) {
		attrs := map[string]string{}
		for i := 0; i < 30; i++ {
			attrs[fmt.Sprintf("k%02d", i)] = strings.Repeat("v", 500)
		}
		big := &logstore.LogRecord{Timestamp: ts, ServiceName: "checkout", TraceID: strings.Repeat("a", 32),
			Body: strings.Repeat("ğ", 5000), Attributes: attrs, SeverityText: "error"}
		var ctxRecs []*logstore.LogRecord
		for i := 0; i < 5; i++ {
			ctxRecs = append(ctxRecs, &logstore.LogRecord{Timestamp: ts - int64(i+1)*1e9, Body: strings.Repeat("x", 400)})
		}
		out := renderLogLineTR(logLineInput{Line: big, Pod: "checkout-7f9", Exact: true, Pattern: "Read / write timeout",
			Trace:  &chstore.TraceAggregateStub{RootService: "checkout", RootName: "POST /api/orders", DurationMs: 30412, SpanCount: 48, ErrorCount: 3},
			Before: ctxRecs, After: ctxRecs[:2]})
		if len(out) > logLineInputMax {
			t.Fatalf("paket %d bayt > %d", len(out), logLineInputMax)
		}
		if n := strings.Count(out, "\n- k"); n != logLineAttrMax {
			t.Fatalf("öznitelik satırı %d, want %d", n, logLineAttrMax)
		}
		if !strings.Contains(out, "(+10 anahtar daha") {
			t.Fatalf("kesilen anahtar notu yok")
		}
		body := out[strings.Index(out, "GÖVDE"):strings.Index(out, "DESEN")]
		if len(body) > logLineBodyMaxBytes+64 {
			t.Fatalf("gövde %d bayt — 4 KB tavanı aşıldı", len(body))
		}
		for _, want := range []string{"SATIR: ", "pod=checkout-7f9", "seviye=ERROR", "DESEN: Read / write timeout",
			"TRACE: kök checkout · POST /api/orders · 30412 ms · 48 span · 3 hatalı span", "BAĞLAM (aynı pod, önce 5 / sonra 2)", ">>> (açıklanan satır)"} {
			if !strings.Contains(out, want) {
				t.Errorf("pakette %q yok", want)
			}
		}
		if strings.Contains(out, "NOT: tam eşleşme") {
			t.Error("tam eşleşmede not olmamalı")
		}
	})
	t.Run("bağlam kendini içermez", func(t *testing.T) {
		got := dropSelf([]*logstore.LogRecord{target, near, far}, target, 5)
		if len(got) != 2 || got[0] != near {
			t.Fatalf("dropSelf: %v", got)
		}
		if len(dropSelf([]*logstore.LogRecord{near, far, near}, target, 2)) != 2 {
			t.Fatal("dropSelf ≤n")
		}
	})
}

func TestValidateLogLineBody(t *testing.T) {
	for _, ts := range []string{"", "abc", "-5", "1.7e18"} {
		if _, err := validateLogLineBody(logLineExplainBody{Service: "a", TsNs: ts}); err == nil {
			t.Errorf("tsNs %q kabul edildi", ts)
		}
	}
	if _, err := validateLogLineBody(logLineExplainBody{TsNs: "1"}); err == nil {
		t.Error("servissiz kabul edildi")
	}
	b, err := validateLogLineBody(logLineExplainBody{TsNs: " 1700000000123456789 ", Service: " a ", TraceID: "XYZ", Hash: "ABCD1234"})
	if err != nil || b.Service != "a" || b.TraceID != "" || b.Hash != "abcd1234" || b.tsNs != 1700000000123456789 {
		t.Fatalf("normalize: %+v %v", b, err)
	}
}
