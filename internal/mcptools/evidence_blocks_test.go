package mcptools

// v0.10.1272 — CoSRE adım 3: kanıt çipleri + Explore'da aç (operatör eki:
// sonuç sohbet içinde de görünür). ToolResultBlock: her araç için blok türü +
// yük şekli + tavanlar; veri yok / hata / ref'siz kanıt → blok yok.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

func evBlockContent(t *testing.T, r cosreResult) string {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func evFor(tool string) ChatEvidence {
	return ChatEvidence{Tool: tool, Source: evSourceCH, Ref: "ev3", Href: "/x?range=custom:1000-2000",
		Window: EvidenceWindow{FromMs: 1000, ToMs: 2000}}
}

func TestToolResultBlockTraces(t *testing.T) {
	traces := make([]qtTrace, 25) // tavanın üstü (20)
	for i := range traces {
		traces[i] = qtTrace{TraceID: fmt.Sprintf("%032x", i+1), Root: "GET /a", Service: "checkout", StartMs: 1500, DurationMs: 12.5, Spans: 4, Error: i == 0}
	}
	c := evBlockContent(t, cosreResult{Tool: queryTracesName, OK: true,
		Request: map[string]any{"service": "checkout", "status": "error", "sort_by": "slowest"},
		Data:    map[string]any{"hasMore": false, "traces": traces}})
	b, ok := ToolResultBlock(queryTracesName, c, evFor(queryTracesName))
	if !ok || b.Type != blocks.TypeTraceList {
		t.Fatalf("trace_list bekleniyordu: %+v", b)
	}
	p := b.Payload.(EvTraceListPayload)
	if len(p.Traces) != evBlockMaxTraces || !p.Truncated || p.EvidenceRef != "ev3" || p.DeepLink != "/x?range=custom:1000-2000" {
		t.Fatalf("yük: %d trace, truncated %v, ref %q, link %q", len(p.Traces), p.Truncated, p.EvidenceRef, p.DeepLink)
	}
	r0 := p.Traces[0]
	if r0.StartTime != 1500*1e6 || r0.RootName != "GET /a" || r0.ServiceName != "checkout" || !r0.HasError || r0.SpanCount != 4 || p.Window.FromNs != 1000*1e6 {
		t.Fatalf("satır/pencere eşlemesi: %+v · %+v", r0, p.Window)
	}
	if p.Query != "checkout · hatalı trace'ler · en yavaş" {
		t.Fatalf("başlık %q", p.Query)
	}
	// Tel şekli = ChatTraceList sözleşmesi (traceId/startTime/…/deepLink/truncated).
	raw, _ := json.Marshal(p)
	for _, k := range []string{`"traceId"`, `"startTime"`, `"serviceName"`, `"rootName"`, `"durationMs"`, `"spanCount"`, `"hasError"`, `"deepLink"`, `"truncated"`, `"window":{"fromNs"`, `"evidenceRef":"ev3"`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("trace_list yükünde %s yok", k)
		}
	}
}

func TestToolResultBlockLogs(t *testing.T) {
	logs := make([]qlRow, 60)
	for i := range logs {
		logs[i] = qlRow{TsMs: int64(1000 + i), Severity: "ERROR", Service: "payments", TraceID: "abc", Body: strings.Repeat("x", 500)}
	}
	logs[1].Pod = "payments-7f9"
	c := evBlockContent(t, cosreResult{Tool: queryLogsName, OK: true, Request: map[string]any{"service": "payments", "severity": "error"},
		Data: map[string]any{"hasMore": false, "logs": logs}})
	b, ok := ToolResultBlock(queryLogsName, c, evFor(queryLogsName))
	if !ok || b.Type != blocks.TypeTable {
		t.Fatalf("table bekleniyordu: %+v", b)
	}
	p := b.Payload.(EvTablePayload)
	if len(p.Rows) != evBlockMaxLogs || !p.Truncated || len(p.Columns) != 5 || p.Columns[4].Kind != "trace" || p.Columns[0].Kind != "time" {
		t.Fatalf("yük: %d satır, %+v", len(p.Rows), p.Columns)
	}
	if p.Rows[0][2] != "payments" || p.Rows[1][2] != "payments-7f9" {
		t.Fatalf("pod/servis sütunu: %v / %v", p.Rows[0][2], p.Rows[1][2])
	}
	if body := p.Rows[0][3].(string); len([]rune(body)) > evBlockBodyRunes {
		t.Fatalf("gövde kırpılmadı: %d rune", len([]rune(body)))
	}
	if p.Title != "payments · loglar · error+" {
		t.Fatalf("başlık %q", p.Title)
	}
}

func TestToolResultBlockMetrics(t *testing.T) {
	series := make([]qmSeries, 22)
	for i := range series {
		pts := make([]qmPoint, 130)
		for j := range pts {
			pts[j] = qmPoint{T: int64(1000 + j), V: float64(j)}
		}
		series[i] = qmSeries{Name: "p99", Unit: "ms", Points: pts}
	}
	c := evBlockContent(t, cosreResult{Tool: queryMetricsName, OK: true, Request: map[string]any{"family": "latency", "service": "checkout"},
		Data: map[string]any{"series": series}})
	b, ok := ToolResultBlock(queryMetricsName, c, evFor(queryMetricsName))
	if !ok || b.Type != blocks.TypeChart {
		t.Fatalf("chart bekleniyordu: %+v", b)
	}
	p := b.Payload.(EvChartPayload)
	if len(p.Series) != evBlockMaxSeries || !p.Truncated || len(p.Series[0].Points) != evBlockMaxPoints || p.Series[0].Unit != "ms" {
		t.Fatalf("tavanlar: %d seri, %d nokta", len(p.Series), len(p.Series[0].Points))
	}
	if last := p.Series[0].Points[evBlockMaxPoints-1]; last[0] != 1129 {
		t.Fatalf("son noktalar korunmalı: %v", last)
	}
	// Eski istemci: chartBlocks service+agg ister → veri yükü sorgu spec'i sanılmaz.
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), `"service"`) || strings.Contains(string(raw), `"agg"`) {
		t.Fatalf("veri yükü service/agg taşımamalı: %s", raw)
	}

	// Satırlı aileler (jvm_* / pod_*) → table.
	rows := []qmRow{{Pod: "orders-1", Namespace: "shop", Value: 512, Unit: "MiB", LimitPct: 80}}
	c2 := evBlockContent(t, cosreResult{Tool: queryMetricsName, OK: true, Request: map[string]any{"family": "jvm_heap", "service": "orders"},
		Data: map[string]any{"rows": rows}})
	b2, ok := ToolResultBlock(queryMetricsName, c2, evFor(queryMetricsName))
	if !ok || b2.Type != blocks.TypeTable {
		t.Fatalf("table bekleniyordu: %+v", b2)
	}
	tp := b2.Payload.(EvTablePayload)
	if len(tp.Columns) != 5 || len(tp.Rows) != 1 || tp.Rows[0][0] != "orders-1" || tp.Rows[0][4] != 80.0 {
		t.Fatalf("satır tablosu: %+v", tp)
	}
}

func TestToolResultBlockNone(t *testing.T) {
	empty := evBlockContent(t, cosreResult{Tool: queryTracesName, OK: true, Empty: true, Request: map[string]any{"service": "a"},
		Data: map[string]any{"hasMore": false, "traces": []qtTrace{}}})
	failed := evBlockContent(t, cosreFail(queryLogsName, map[string]any{}, nil, cosreErrBackend, "x"))
	noRef := evFor(queryTracesName)
	noRef.Ref = ""
	full := evBlockContent(t, cosreResult{Tool: queryTracesName, OK: true, Request: map[string]any{"service": "a"},
		Data: map[string]any{"traces": []qtTrace{{TraceID: "t"}}}})
	for name, c := range map[string]struct {
		tool, content string
		ev            ChatEvidence
	}{
		"boş sonuç":    {queryTracesName, empty, evFor(queryTracesName)},
		"hata":         {queryLogsName, failed, evFor(queryLogsName)},
		"ref yok":      {queryTracesName, full, noRef},
		"başka araç":   {"search_traces", full, evFor(queryTracesName)},
		"bozuk içerik": {queryTracesName, "{", evFor(queryTracesName)},
		"seri/satır yok": {queryMetricsName, evBlockContent(t, cosreResult{Tool: queryMetricsName, OK: true,
			Data: map[string]any{"series": []qmSeries{}}}), evFor(queryMetricsName)},
	} {
		if _, ok := ToolResultBlock(c.tool, c.content, c.ev); ok {
			t.Errorf("%s: blok beklenmiyordu", name)
		}
	}
}
