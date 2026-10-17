package mcptools

// evidence_blocks.go — v0.10.1272 (CoSRE adım 3/8, operatör eki 2026-10-17:
// "Explore'da açmak yerine chat içerisine de getirebilirsin").
//
// query_metrics / query_traces / query_logs sonucunun VERİSİ sohbet içinde de
// görünür: aracın zaten sınırlı (≤20 seri × ≤120 nokta, ≤20 trace, ≤50 log
// satırı) sonucundan MEVCUT blok türleri kurulur — ek arka uç sorgusu YOK:
//
//	query_metrics seriler (VM / CH span RED)  → `chart`  (veri taşıyan yük: series + unit + pencere)
//	query_metrics satırlar (jvm_* / pod_*)    → `table`  (pod · namespace · değer · limit %)
//	query_traces                              → `trace_list` (endpoint_traces.go yük şekli)
//	query_logs                                → `table`  (zaman · seviye · pod/servis · gövde · trace)
//
// Her yük `evidenceRef` taşır (= ChatEvidence.Ref): çip ile blok bağlı; çip
// bloğu açar/kapatır. `deepLink` = kanıtın "Explore'da aç" href'i (ikincil bağ).
//
// GERİYE UYUM: veri taşıyan `chart` yükünde `service`/`agg` YOK — eski istemcinin
// chartBlocks süzgeci (service + agg ister) onu yok sayar, yeniden SORGU
// ATMAZ; `table` türünü eski istemci hiç çizmez; `trace_list` eski istemcide
// mevcut ChatTraceList ile çizilir (aynı şekil).

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// Blok tavanları (aracın tavanlarının aynası; yük yine de burada kırpılır —
// tavanı model değil sunucu uygular).
const (
	evBlockMaxSeries = mtMaxSeries
	evBlockMaxPoints = mtMaxPoints
	evBlockMaxTraces = qtMaxLimit
	evBlockMaxLogs   = qlMaxLimit
	evBlockMaxRows   = mtMaxSeries
	evBlockBodyRunes = qlBodyMaxRunes
)

// EvidenceBlock — blok türü + yükü.
type EvidenceBlock struct {
	Type    string
	Payload any
}

// EvBlockWindow — blok penceresi (unix ns; trace_list sözleşmesi).
type EvBlockWindow struct {
	FromNs int64 `json:"fromNs"`
	ToNs   int64 `json:"toNs"`
}

// EvChartSeries — veri taşıyan grafik serisi; nokta [unixMs, değer].
type EvChartSeries struct {
	Name   string       `json:"name"`
	Group  string       `json:"group,omitempty"`
	Unit   string       `json:"unit"`
	Points [][2]float64 `json:"points"`
}

// EvChartRegion — v0.10.1284 (explain_log_spike): grafiğin x-bölgesi (taban /
// artış penceresi). İSTEĞE BAĞLI: alanı tanımayan eski istemci yok sayar.
// Tone: err | warn | muted (FE token eşlemesi; renk sunucudan gelmez).
type EvChartRegion struct {
	FromMs int64  `json:"fromMs"`
	ToMs   int64  `json:"toMs"`
	Label  string `json:"label,omitempty"`
	Tone   string `json:"tone,omitempty"`
}

// EvChartPayload — `chart` bloğunun VERİ kipi (service/agg yok: eski istemci
// sorgu-spec'i sanmaz).
type EvChartPayload struct {
	Title       string          `json:"title"`
	EvidenceRef string          `json:"evidenceRef"`
	DeepLink    string          `json:"deepLink,omitempty"`
	Window      EvBlockWindow   `json:"window"`
	Series      []EvChartSeries `json:"series"`
	Regions     []EvChartRegion `json:"regions,omitempty"`
	Truncated   bool            `json:"truncated,omitempty"`
}

// EvTableColumn — tablo sütunu; kind çizimi seçer: time (unix ms) | num |
// severity | trace (32-hex → trace bağlantısı) | text.
type EvTableColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// EvTablePayload — `table` bloğu (sohbet kanıt tablosu; satır sayısı tavanlı).
type EvTablePayload struct {
	Title       string          `json:"title"`
	EvidenceRef string          `json:"evidenceRef"`
	DeepLink    string          `json:"deepLink,omitempty"`
	Columns     []EvTableColumn `json:"columns"`
	Rows        [][]any         `json:"rows"`
	Truncated   bool            `json:"truncated,omitempty"`
	// RowSubjects — v0.10.1291: Rows ile PARALEL satır başına ✨ öznesi
	// (log_line_subject.go `log:` kodeği; "" = o satırda buton yok). Yalnız
	// log satırı tablolarında (query_logs, explain_log_spike örnekleri);
	// alanı tanımayan eski istemci yok sayar. Tool çıktısına GİRMEZ — blok
	// kurulurken sonuçtan türetilir (model bayt ödemez).
	RowSubjects []string `json:"rowSubjects,omitempty"`
}

// EvTraceRow / EvTraceListPayload — `trace_list` (api/endpoint_traces.go
// traceListPayload ile AYNI tel şekli; FE ChatTraceList) + evidenceRef.
type EvTraceRow struct {
	TraceID     string  `json:"traceId"`
	StartTime   int64   `json:"startTime"`
	ServiceName string  `json:"serviceName"`
	RootName    string  `json:"rootName"`
	DurationMs  float64 `json:"durationMs"`
	SpanCount   uint64  `json:"spanCount"`
	HasError    bool    `json:"hasError"`
}

type EvTraceListPayload struct {
	Query       string        `json:"query"`
	Window      EvBlockWindow `json:"window"`
	Traces      []EvTraceRow  `json:"traces"`
	DeepLink    string        `json:"deepLink"`
	Truncated   bool          `json:"truncated"`
	EvidenceRef string        `json:"evidenceRef"`
}

// ToolResultBlock — kanıt zarflı aracın HAM JSON sonucu + çözülmüş kanıtı →
// sohbet bloğu. Veri yok / hata / kanıtsız → ok=false (boş blok çizilmez).
// SAF, tablo testli (evidence_blocks_test.go).
func ToolResultBlock(tool, content string, ev ChatEvidence) (EvidenceBlock, bool) {
	if !evidenceTools[tool] || ev.Ref == "" {
		return EvidenceBlock{}, false
	}
	var r struct {
		OK      bool            `json:"ok"`
		Request map[string]any  `json:"request"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal([]byte(content), &r) != nil || !r.OK || len(r.Data) == 0 {
		return EvidenceBlock{}, false
	}
	win := EvBlockWindow{FromNs: ev.Window.FromMs * 1e6, ToNs: ev.Window.ToMs * 1e6}
	title := evBlockTitle(tool, r.Request)
	switch tool {
	case queryTracesName:
		var d struct {
			HasMore bool      `json:"hasMore"`
			Traces  []qtTrace `json:"traces"`
		}
		if json.Unmarshal(r.Data, &d) != nil || len(d.Traces) == 0 {
			return EvidenceBlock{}, false
		}
		p := EvTraceListPayload{Query: title, Window: win, DeepLink: ev.Href, Truncated: d.HasMore, EvidenceRef: ev.Ref,
			Traces: make([]EvTraceRow, 0, min(len(d.Traces), evBlockMaxTraces))}
		for _, t := range d.Traces {
			if len(p.Traces) >= evBlockMaxTraces {
				p.Truncated = true
				break
			}
			p.Traces = append(p.Traces, EvTraceRow{TraceID: t.TraceID, StartTime: t.StartMs * 1e6, ServiceName: t.Service,
				RootName: t.Root, DurationMs: t.DurationMs, SpanCount: t.Spans, HasError: t.Error})
		}
		return EvidenceBlock{Type: blocks.TypeTraceList, Payload: p}, true
	case queryLogsName:
		var d struct {
			HasMore bool    `json:"hasMore"`
			Logs    []qlRow `json:"logs"`
		}
		if json.Unmarshal(r.Data, &d) != nil || len(d.Logs) == 0 {
			return EvidenceBlock{}, false
		}
		p := EvTablePayload{Title: title, EvidenceRef: ev.Ref, DeepLink: ev.Href, Truncated: d.HasMore,
			Columns: []EvTableColumn{
				{Key: "time", Label: "Zaman", Kind: "time"}, {Key: "severity", Label: "Seviye", Kind: "severity"},
				{Key: "source", Label: "Pod / servis", Kind: "text"}, {Key: "body", Label: "Mesaj", Kind: "text"},
				{Key: "traceId", Label: "Trace", Kind: "trace"},
			}, Rows: make([][]any, 0, min(len(d.Logs), evBlockMaxLogs))}
		for _, l := range d.Logs {
			if len(p.Rows) >= evBlockMaxLogs {
				p.Truncated = true
				break
			}
			src := l.Pod
			if src == "" {
				src = l.Service
			}
			p.Rows = append(p.Rows, []any{l.TsMs, l.Severity, src, truncateRunes(l.Body, evBlockBodyRunes-1), l.TraceID})
			p.RowSubjects = append(p.RowSubjects, LogLineSubject(l.TsMs*1e6, firstNonEmpty(l.Service, reqString(r.Request, "service")), l.Pod, l.TraceID, l.Body))
		}
		p.RowSubjects = compactRowSubjects(p.RowSubjects)
		return EvidenceBlock{Type: blocks.TypeTable, Payload: p}, true
	case queryMetricsName:
		var d struct {
			Series []qmSeries `json:"series"`
			Rows   []qmRow    `json:"rows"`
		}
		if json.Unmarshal(r.Data, &d) != nil {
			return EvidenceBlock{}, false
		}
		if len(d.Series) > 0 {
			p := EvChartPayload{Title: title, EvidenceRef: ev.Ref, DeepLink: ev.Href, Window: win,
				Series: make([]EvChartSeries, 0, min(len(d.Series), evBlockMaxSeries))}
			for _, s := range d.Series {
				if len(p.Series) >= evBlockMaxSeries {
					p.Truncated = true
					break
				}
				pts := s.Points
				if len(pts) > evBlockMaxPoints {
					pts, p.Truncated = pts[len(pts)-evBlockMaxPoints:], true
				}
				cs := EvChartSeries{Name: s.Name, Group: s.Group, Unit: s.Unit, Points: make([][2]float64, 0, len(pts))}
				for _, pt := range pts {
					cs.Points = append(cs.Points, [2]float64{float64(pt.T), pt.V})
				}
				if len(cs.Points) > 0 {
					p.Series = append(p.Series, cs)
				}
			}
			if len(p.Series) == 0 {
				return EvidenceBlock{}, false
			}
			return EvidenceBlock{Type: blocks.TypeChart, Payload: p}, true
		}
		if len(d.Rows) == 0 {
			return EvidenceBlock{}, false
		}
		hasLimit := false
		for _, row := range d.Rows {
			if row.LimitPct > 0 {
				hasLimit = true
			}
		}
		cols := []EvTableColumn{{Key: "pod", Label: "Pod", Kind: "text"}, {Key: "namespace", Label: "Namespace", Kind: "text"},
			{Key: "value", Label: "Değer", Kind: "num"}, {Key: "unit", Label: "Birim", Kind: "text"}}
		if hasLimit {
			cols = append(cols, EvTableColumn{Key: "limitPct", Label: "Limit %", Kind: "num"})
		}
		p := EvTablePayload{Title: title, EvidenceRef: ev.Ref, DeepLink: ev.Href, Columns: cols,
			Rows: make([][]any, 0, min(len(d.Rows), evBlockMaxRows))}
		for _, row := range d.Rows {
			if len(p.Rows) >= evBlockMaxRows {
				p.Truncated = true
				break
			}
			cells := []any{row.Pod, row.Namespace, row.Value, row.Unit}
			if hasLimit {
				cells = append(cells, row.LimitPct)
			}
			p.Rows = append(p.Rows, cells)
		}
		return EvidenceBlock{Type: blocks.TypeTable, Payload: p}, true
	}
	return EvidenceBlock{}, false
}

// evBlockTitle — bloğun deterministik başlığı (istekten; model metni değil).
func evBlockTitle(tool string, req map[string]any) string {
	scope := reqString(req, "service")
	if scope == "" {
		scope = reqString(req, "cluster")
	}
	if scope == "" {
		scope = reqString(req, "trace_id")
	}
	var what string
	switch tool {
	case queryMetricsName:
		what = reqString(req, "family")
	case queryTracesName:
		what = "trace'ler"
		if reqString(req, "status") == qtStatusError {
			what = "hatalı trace'ler"
		}
		if reqString(req, "sort_by") == qtSortSlowest {
			what += " · en yavaş"
		}
	case queryLogsName:
		what = "loglar"
		if sev := reqString(req, "severity"); sev != "" && sev != qlSeverityAny {
			what += " · " + sev + "+"
		}
	}
	if scope == "" {
		return what
	}
	return scope + " · " + what
}

// compositeBlockMax — v0.10.1275: bileşik aracın sohbet içi blok tavanı.
const compositeBlockMax = 6

// ToolResultBlocks — v0.10.1275 (CoSRE adım 6): kanıt listesi → sohbet blokları.
// Atomik araç → ToolResultBlock (ilk kanıtla; tek blok). Bileşik araç → sonucun
// `data.blocks[{hop,type,payload}]`'ı; her yükün evidenceRef/deepLink'i AYNI
// hop'un kanıtından yeniden yazılır (çip ↔ blok bağı sunucuda kurulur, aracın
// yazdığı değer güvenilmez). Eşleşen kanıtı (ya da ref'i) olmayan blok düşer —
// bağsız blok çipsiz kalır ve hiç açılamazdı. Tavan compositeBlockMax. SAF.
func ToolResultBlocks(tool, content string, evs []ChatEvidence) []EvidenceBlock {
	if evidenceTools[tool] {
		if len(evs) == 0 {
			return nil
		}
		if b, ok := ToolResultBlock(tool, content, evs[0]); ok {
			return []EvidenceBlock{b}
		}
		return nil
	}
	if !compositeEvidenceTools[tool] || len(evs) == 0 {
		return nil
	}
	var r struct {
		OK      bool           `json:"ok"`
		Request map[string]any `json:"request"`
		Data    struct {
			Samples []qlRow `json:"samples"` // v0.10.1291 — explain_log_spike örnek satırları (✨ öznesi)
			Blocks  []struct {
				Hop     string         `json:"hop"`
				Type    string         `json:"type"`
				Payload map[string]any `json:"payload"`
			} `json:"blocks"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(content), &r) != nil {
		return nil
	}
	byHop := make(map[string]ChatEvidence, len(evs))
	for _, ev := range evs {
		if _, seen := byHop[ev.Hop]; !seen && ev.Hop != "" && ev.Ref != "" {
			byHop[ev.Hop] = ev
		}
	}
	var out []EvidenceBlock
	for _, b := range r.Data.Blocks {
		if len(out) >= compositeBlockMax {
			break
		}
		ev, ok := byHop[b.Hop]
		if !ok || b.Payload == nil {
			continue
		}
		switch b.Type {
		case blocks.TypeTraceList, blocks.TypeTable, blocks.TypeChart:
		default:
			continue
		}
		p := make(map[string]any, len(b.Payload)+2)
		for k, v := range b.Payload {
			p[k] = v
		}
		p["evidenceRef"] = ev.Ref
		p["deepLink"] = ev.Href // "" = Explore bağlantısı yok (trace_list alanı zorunlu tutar)
		if tool == ExplainLogSpikeName && b.Hop == logSpikeHopSamples && b.Type == blocks.TypeTable {
			if subj := spikeSampleSubjects(r.Data.Samples, reqString(r.Request, "service")); subj != nil {
				p["rowSubjects"] = subj
			}
		}
		out = append(out, EvidenceBlock{Type: b.Type, Payload: p})
	}
	return out
}

// spikeSampleSubjects — v0.10.1291: log artışı örnek satırlarının ✨ özneleri
// (blok satırlarıyla aynı sıra — logSpikeBlocks örnekleri sırayla yazar).
func spikeSampleSubjects(samples []qlRow, reqService string) []string {
	out := make([]string, 0, len(samples))
	for _, l := range samples {
		out = append(out, LogLineSubject(l.TsMs*1e6, firstNonEmpty(l.Service, reqService), l.Pod, l.TraceID, l.Body))
	}
	return compactRowSubjects(out)
}

// compactRowSubjects — hiçbir satırın öznesi yoksa alan hiç yazılmaz.
func compactRowSubjects(s []string) []string {
	for _, v := range s {
		if v != "" {
			return s
		}
	}
	return nil
}
