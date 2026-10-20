package api

// chat_explain_cards_test.go — v0.10.1407 (Tek CoSRE adım 4). Operatör:
// "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
// sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//
// Service → Details grafik ✨'ları (charts) ve Logs histogramı ✨'ı
// (log-spike) da sohbet turu; çekmecenin özne kipi kalktı. Pinler:
//   - düğme ↔ sohbet TEK önbellek satırı: HTTP üretir → sohbet cached:true,
//     sıfır LLM, saklanan exchangeId (ve ters yön) — charts'ın v0.9.1031
//     "önbelleksiz" duruşu bilerek tersine döndü (anahtar tüm sinyalleri
//     hash'ler);
//   - cevap ölçülmüş kanıtın tipli bloğunu (charts_signals / log_spike)
//     cevaptan ÖNCE taşır, künyenin renderer'ı "charts" / "log-spike";
//   - kanıt-önce sözleşme: model düşerse `error` değil, blok + dürüst satır;
//     log-spike kanıtı okunamazsa LLM hiç çağrılmaz;
//   - withBlockSeq iki blok türünü sohbetin sıralayıcısına bağlar;
//   - niyet / arşiv: log-spike filo geneli (boş kimlik) geçerli, renderer
//     beyaz listesi kartları tanır.
//
// Sentetik adlar: orders-api, checkout, db.example.test.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

var (
	xcFrom = xrT0.Add(-time.Hour)
	xcTo   = xrT0
)

func xcChartsAsk() chatExplainAsk {
	subj := aiSubject{Kind: "charts", ID: "orders-api", From: xcFrom, To: xcTo, Scope: "err"}
	return chatExplainAsk{Kind: "charts", ID: "orders-api", Subject: formatAiSubject(subj), Src: "button"}
}

func xcChartsHTTP(s *Server, refresh bool) *httptest.ResponseRecorder {
	q := "?service=orders-api&scope=err&from=" + strconv.FormatInt(xcFrom.UnixNano(), 10) + "&to=" + strconv.FormatInt(xcTo.UnixNano(), 10)
	if refresh {
		q += "&refresh=1"
	}
	return xrPost("/api/copilot/explain-charts"+q, "", nil, (*Server).copilotExplainCharts, s)
}

// xcSpike — deterministik log artışı kanıtı (RunLogSpike dikişi).
func xcSpike() mcptools.LogSpikeResult {
	base := xrT0.Add(-10 * time.Minute).UnixMilli()
	return mcptools.LogSpikeResult{
		Tool: "explain_log_spike", OK: true,
		Request: map[string]any{"service": "checkout", "env": "prod", "level": "all"},
		Spike: &mcptools.LogSpikeWindow{FromMs: base, ToMs: base + 7*60000, Source: "detected", Factor: 5.9,
			RatePerMinSpike: 1840, RatePerMinBase: 310, BaseFromMs: base - 21*60000, BaseToMs: base},
		Contributors: []mcptools.SpikeContributor{{Rank: 1, Dim: "pod", Value: "checkout-7f9-abc", SharePct: 62, SpikePct: 71, BasePct: 12,
			Reason: "artışın %62'si pod checkout-7f9-abc kaynaklı"}},
		Dims: []mcptools.LogSpikeDim{{Dim: "pod", State: "ok"}},
	}
}

func xcLogSpikeSeam(t *testing.T, res mcptools.LogSpikeResult) *int {
	t.Helper()
	n := 0
	logSpikeRunSeam = func(context.Context, mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
		n++
		return res, nil
	}
	t.Cleanup(func() { logSpikeRunSeam = nil })
	return &n
}

func xcLogSpikeAsk() chatExplainAsk {
	subj := aiSubject{Kind: "log-spike", ID: "checkout", From: xcFrom, To: xcTo, Mode: "auto", Env: "prod", Q: "timeout", Level: "all"}
	return chatExplainAsk{Kind: "log-spike", Subject: formatAiSubject(subj), Src: "button"}
}

func xcLogSpikeHTTP(s *Server) *httptest.ResponseRecorder {
	body, _ := json.Marshal(logSpikeExplainBody{Service: "checkout", Env: "prod", Query: "timeout", Level: "all", Mode: "auto",
		FromNs: xcFrom.UnixNano(), ToNs: xcTo.UnixNano()})
	return xrPost("/api/copilot/explain-log-spike", string(body), nil, (*Server).copilotExplainLogSpike, s)
}

// xcBlock — olaylardaki tek kanıt bloğunun gövdesi (ham recorder: tür adıyla yayılır).
func xcBlock(t *testing.T, evs []xtEvent, kind string) any {
	t.Helper()
	var out any
	n := 0
	for _, e := range evs {
		if e.kind == kind {
			out, n = e.data, n+1
		}
	}
	if n != 1 {
		t.Fatalf("%s bloğu %d kez (%s)", kind, n, xtKinds(evs))
	}
	return out
}

func TestExplainCardsChatSharesButtonCache(t *testing.T) {
	for _, c := range []struct {
		kind, system, surface, block, renderer, label string
		ask                                           func() chatExplainAsk
		http                                          func(*Server) *httptest.ResponseRecorder
	}{
		{kind: "charts", system: copilot.SystemPromptServiceCharts(), surface: aisurface.ExplainChartsChat,
			block: blocks.TypeChartsSignals, renderer: explainRendererCharts, label: "orders-api",
			ask: xcChartsAsk, http: func(s *Server) *httptest.ResponseRecorder { return xcChartsHTTP(s, false) }},
		{kind: "log-spike", system: copilot.SystemPromptLogSpike(), surface: aisurface.ExplainLogSpikeChat,
			block: blocks.TypeLogSpike, renderer: explainRendererLogSpike, label: "checkout",
			ask: xcLogSpikeAsk, http: xcLogSpikeHTTP},
	} {
		t.Run(c.kind, func(t *testing.T) {
			p := newDefProvider(t, "**Ne oldu:** orders-api ", "hata oranı deploy sonrası arttı.")
			s := xrServer(t, p)
			runs := xcLogSpikeSeam(t, xcSpike())
			// 1) ✨ HTTP düğmesi üretir (buffered) ve kendi anahtarına yazar.
			w := c.http(s)
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			xid, _ := body["exchangeId"].(string)
			sys, user := p.lastMessages(t)
			if sys != c.system || xid == "" || p.requests() != 1 {
				t.Fatalf("düğme: sistem eşleşmedi ya da xid boş (calls=%d) %v", p.requests(), body)
			}
			if _, hit, _ := s.cache.Get(context.Background(), explainCacheKey(c.system, user, "")); !hit {
				t.Fatal("düğme explainCacheKey(system, user, \"\") anahtarına yazmadı")
			}
			// 2) Sohbet turu: isabet, sıfır LLM, saklanan exchangeId, blok cevaptan önce.
			var evs []xtEvent
			ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-chat"})
			ask := c.ask()
			handled, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &ask, time.UTC)
			if !handled || !ok {
				t.Fatalf("handled=%v ok=%v %s", handled, ok, xtKinds(evs))
			}
			if got := xtKinds(evs); got != "step,step-result,"+c.block+",delta,answer" {
				t.Fatalf("olay sırası %s", got)
			}
			ans := xtAnswer(t, evs)
			if p.requests() != 1 || ans["cached"] != true || ans["exchangeId"] != xid {
				t.Fatalf("isabet değil: calls=%d cached=%v xid=%v (düğme %s)", p.requests(), ans["cached"], ans["exchangeId"], xid)
			}
			meta, _ := ans["explain"].(chatExplainMeta)
			if meta.Kind != c.kind || meta.Surface != c.surface || meta.Renderer != c.renderer || !meta.Cached || meta.Label != c.label || meta.Subject != ask.Subject {
				t.Fatalf("künye %+v", meta)
			}
			switch bp := xcBlock(t, evs, c.block).(type) {
			case chartsSignalsBlockPayload:
				if bp.Service != "orders-api" || bp.Scope != "err" || bp.FromNs != xcFrom.UnixNano() || bp.ToNs != xcTo.UnixNano() ||
					bp.Signals.Deploy == nil || len(bp.Signals.Problems) != 1 || len(bp.Signals.Anomalies) != 1 || len(bp.Signals.OpDeltas) != 1 {
					t.Fatalf("charts_signals gövdesi %+v", bp)
				}
			case logSpikeBlockPayload:
				if bp.Service != "checkout" || bp.Env != "prod" || bp.Q != "timeout" || bp.Mode != "auto" || bp.Spike.Spike == nil || len(bp.Spike.Contributors) != 1 {
					t.Fatalf("log_spike gövdesi %+v", bp)
				}
				if *runs != 2 {
					t.Fatalf("kanıt iki yolda da RunLogSpike'tan: %d", *runs)
				}
			default:
				t.Fatalf("blok gövdesi tipi %T", bp)
			}
		})
	}
}

// Ters yön: önce sohbet üretir (akan), sonra ✨ grafik düğmesi isabet eder;
// ?refresh=1 atlar ve yeni üretimi aynı satıra yazar.
func TestExplainChartsButtonHitsChatCache(t *testing.T) {
	p := newDefProvider(t, "özet ", "cevap")
	s := xrServer(t, p)
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-chat-1"})
	ask := xcChartsAsk()
	if h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &ask, time.UTC); !h || !ok || p.requests() != 1 {
		t.Fatalf("sohbet turu: %s calls=%d", xtKinds(evs), p.requests())
	}
	var body map[string]any
	_ = json.Unmarshal(xcChartsHTTP(s, false).Body.Bytes(), &body)
	if body["cached"] != true || body["exchangeId"] != "x-chat-1" || body["explanation"] != "özet cevap" || p.requests() != 1 {
		t.Fatalf("düğme sohbetin satırına isabet etmeli: %v calls=%d", body, p.requests())
	}
	if sg, _ := body["signals"].(map[string]any); sg == nil || sg["deploy"] == nil {
		t.Fatalf("isabette de ölçülmüş sinyaller döner: %v", body["signals"])
	}
	body = nil
	_ = json.Unmarshal(xcChartsHTTP(s, true).Body.Bytes(), &body)
	if body["cached"] == true || p.requests() != 2 {
		t.Fatalf("refresh=1 önbelleği atlamalı: %v calls=%d", body, p.requests())
	}
}

// Kanıt-önce: model düşerse sohbet turu `error` değil, blok + dürüst satır.
func TestExplainChartsNarrationFailureKeepsEvidence(t *testing.T) {
	p := newDefProvider(t, "x")
	s := xrServer(t, p)
	p.srv.Close() // sağlayıcı ulaşılmaz
	var evs []xtEvent
	ask := xcChartsAsk()
	h, ok := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &ask, time.UTC)
	if !h || ok {
		t.Fatalf("h=%v ok=%v", h, ok)
	}
	if strings.Contains(xtKinds(evs), "error") || !strings.HasSuffix(xtKinds(evs), blocks.TypeChartsSignals+",answer") {
		t.Fatalf("olay sırası %s", xtKinds(evs))
	}
	ans := xtAnswer(t, evs)
	if txt, _ := ans["text"].(string); !strings.HasPrefix(txt, "Anlatım üretilemedi (") || !strings.Contains(txt, "ölçülmüş veridir") {
		t.Fatalf("dürüst satır %q", txt)
	}
	if meta, _ := ans["explain"].(chatExplainMeta); meta.Renderer != explainRendererCharts {
		t.Fatalf("künye kartı çizdirmeli: %+v", meta)
	}
}

// Log artışı kanıtı okunamadı → LLM yok, blok + dürüst cevap (HTTP aynı).
// Geçersiz argüman → HTTP 400 JSON (v0.10.1284 gövdesi), sohbette dürüst cevap.
func TestExplainLogSpikeUnexplainedAndBadArgs(t *testing.T) {
	p := newDefProvider(t, "x")
	s := xrServer(t, p)
	var bad mcptools.LogSpikeResult
	_ = json.Unmarshal([]byte(`{"tool":"explain_log_spike","error":{"code":"backend_error","message":"ES zaman aşımı"}}`), &bad)
	xcLogSpikeSeam(t, bad)
	var evs []xtEvent
	ask := xcLogSpikeAsk()
	if h, ok := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &ask, time.UTC); !h || ok {
		t.Fatalf("h=%v ok=%v", h, ok)
	}
	if got := xtKinds(evs); got != "step,step-result,"+blocks.TypeLogSpike+",answer" || p.requests() != 0 {
		t.Fatalf("olay sırası %s calls=%d", got, p.requests())
	}
	if txt, _ := xtAnswer(t, evs)["text"].(string); !strings.Contains(txt, "ES zaman aşımı") {
		t.Fatalf("metin %q", txt)
	}
	var body map[string]any
	_ = json.Unmarshal(xcLogSpikeHTTP(s).Body.Bytes(), &body)
	if body["explanation"] != "" || body["error"] != "ES zaman aşımı" || p.requests() != 0 {
		t.Fatalf("HTTP gövdesi %v calls=%d", body, p.requests())
	}

	w := xrPost("/x", `{"mode":"auto","fromNs":2,"toNs":1}`, nil, (*Server).copilotExplainLogSpike, s)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "fromNs/toNs geçersiz") {
		t.Fatalf("400 bekleniyor: %d %s", w.Code, w.Body.String())
	}
}

// withBlockSeq — iki kart bloğu sohbetin TEK sıralayıcısından geçer.
func TestWithBlockSeqExplainCards(t *testing.T) {
	var seq blocks.Sequencer
	var got []map[string]any
	emit := withBlockSeq(func(k string, v any) {
		if k == "block" {
			got = append(got, v.(map[string]any))
		}
	}, &seq)
	emit(blocks.TypeChartsSignals, chartsSignalsBlockPayload{Service: "orders-api"})
	emit(blocks.TypeLogSpike, logSpikeBlockPayload{Service: "checkout"})
	if len(got) != 2 || got[0]["type"] != "charts_signals" || got[1]["type"] != "log_spike" || got[1]["seq"] != 2 {
		t.Fatalf("bloklar %v", got)
	}
}

func TestNormalizeExplainAskCardKinds(t *testing.T) {
	fleet := "log-spike:*:1000:2000:sel:::all"
	for _, c := range []struct {
		name string
		in   chatExplainAsk
		want chatExplainAsk
		ok   bool
	}{
		{"log-spike filo geneli (boş kimlik)", chatExplainAsk{Kind: "log-spike", Subject: fleet}, chatExplainAsk{Kind: "log-spike", Subject: fleet}, true},
		{"log-spike kodeksiz", chatExplainAsk{Kind: "log-spike", ID: "checkout"}, chatExplainAsk{}, false},
		{"charts kodeksiz", chatExplainAsk{Kind: "charts", ID: "orders-api"}, chatExplainAsk{}, false},
		{"charts kimlik uyuşmaz", chatExplainAsk{Kind: "charts", ID: "checkout", Subject: "charts:orders-api:1000:2000:all"}, chatExplainAsk{}, false},
		{"charts bilinmeyen kapsam → all", chatExplainAsk{Kind: "charts", Subject: "charts:orders-api:1000:2000:zzz"},
			chatExplainAsk{Kind: "charts", ID: "orders-api", Subject: "charts:orders-api:1000:2000:all"}, true},
	} {
		got, ok := normalizeExplainAsk(&c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
	// Arşiv: kart künyesi kalır (blok arşive girmez — istemci ExplainBody'ye düşer).
	out := sanitizeChatMessages([]aiChatMessage{
		{Role: "assistant", Text: "grafik", Explain: &chatExplainMeta{Kind: "charts", ID: "orders-api", Subject: "charts:orders-api:1000:2000:err", Renderer: "charts"}},
		{Role: "assistant", Text: "log", Explain: &chatExplainMeta{Kind: "log-spike", Subject: fleet, Renderer: "log-spike"}},
	})
	if out[0].Explain == nil || out[0].Explain.Renderer != "charts" || out[1].Explain == nil || out[1].Explain.Renderer != "log-spike" {
		t.Fatalf("arşiv künyesi %+v %+v", out[0].Explain, out[1].Explain)
	}
}
