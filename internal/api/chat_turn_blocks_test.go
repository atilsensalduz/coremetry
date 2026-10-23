package api

// chat_turn_blocks_test.go — v0.10.1444: CoSRE turunun zengin blokları arşivde
// (cosre_interactions.blocks). Pinler:
//   - zarf {v:1, at, blocks, code?, dropped?, ds?} gidiş-dönüş; bilinmeyen
//     sürüm / boş / bozuk = blok yok;
//   - action ve final olmayan çerçeve arşive girmez;
//   - 256 KiB tavan: ÖNCE seriler 60 noktaya (tepe korunur, ds), SONRA en
//     büyük blok düşer (dropped); tek dev blok da düşer; asla hata yok;
//   - yakalayıcı withBlockSeq'in altında çerçeveyi değiştirmeden iletir;
//     tap answer.explain.code'u alır ve önbellek isabetinde turId ekler;
//   - gerçek açıklama turu (charts / trace kod) satırın zarfına ulaşır.
//
// Sentetik adlar: orders-api, checkout.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

var tbNow = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func tbFrame(id, typ string, payload any) turnBlockFrame {
	raw, _ := json.Marshal(payload)
	return turnBlockFrame{ID: id, Type: typ, Seq: 1, Final: true, Payload: raw}
}

// tbChart — evidence chart yükü: nSeries × nPts [ms, v] noktası.
func tbChart(ref string, nSeries, nPts int) map[string]any {
	var series []any
	for s := 0; s < nSeries; s++ {
		pts := make([]any, nPts)
		for i := range pts {
			pts[i] = []any{1761130000000 + int64(i)*60000, 100.1234 + float64((i*7+s)%13)}
		}
		series = append(series, map[string]any{"name": fmt.Sprintf("orders-api-%d", s), "unit": "ms", "points": pts})
	}
	return map[string]any{"title": "p95", "evidenceRef": ref, "window": map[string]any{"fromNs": int64(1761130000000123456), "toNs": int64(1761133600000123456)}, "series": series}
}

func TestTurnBlocksEnvelopeRoundTrip(t *testing.T) {
	code := &codeContextPayload{Repo: "orders", Branch: "main", Reason: ""}
	frames := []turnBlockFrame{tbFrame("b1", blocks.TypeChartsSignals, map[string]any{"service": "orders-api", "fromNs": int64(1761130000000123456)})}
	raw, st := compactTurnBlocks(frames, code, tbNow, turnBlocksCapBytes)
	if raw == "" || st.Out != len(raw) || st.Downsampled || len(st.Dropped) != 0 {
		t.Fatalf("zarf: %q %+v", raw, st)
	}
	env, ok := parseTurnBlocksEnvelope(raw)
	if !ok || env.V != 1 || env.At != tbNow.UnixMilli() || len(env.Blocks) != 1 || env.Code == nil || env.Code.Repo != "orders" {
		t.Fatalf("gidiş-dönüş: %+v", env)
	}
	if env.Blocks[0].ID != "b1" || env.Blocks[0].Type != blocks.TypeChartsSignals || !env.Blocks[0].Final ||
		!strings.Contains(string(env.Blocks[0].Payload), "1761130000000123456") {
		t.Fatalf("çerçeve aynen değil: %+v %s", env.Blocks[0], env.Blocks[0].Payload)
	}
	for name, in := range map[string]string{
		"boş":            "",
		"bozuk":          "{",
		"v2":             strings.Replace(raw, `"v":1`, `"v":2`, 1),
		"blok, kod yok":  `{"v":1,"at":1,"blocks":[]}`,
		"sürümsüz eski?": `{"blocks":[{"id":"b1","type":"chart","seq":1,"final":true,"payload":{}}]}`,
	} {
		if _, ok := parseTurnBlocksEnvelope(in); ok {
			t.Errorf("%s: blok yok sayılmalı", name)
		}
	}
	if got, _ := compactTurnBlocks(nil, nil, tbNow, turnBlocksCapBytes); got != "" {
		t.Errorf("blok/kod yok → kolon varsayılanı: %q", got)
	}
	if got, _ := compactTurnBlocks(nil, code, tbNow, turnBlocksCapBytes); got == "" {
		t.Error("yalnız kod künyesi de saklanır")
	}
}

func TestTurnBlocksCompactDropsActionsAndNonFinal(t *testing.T) {
	notFinal := tbFrame("b3", blocks.TypeChart, map[string]any{"service": "checkout"})
	notFinal.Final = false
	raw, _ := compactTurnBlocks([]turnBlockFrame{
		tbFrame("b1", blocks.TypeAction, map[string]any{"kind": "problem_note"}),
		tbFrame("b2", blocks.TypeEvidence, map[string]any{"service": "orders-api"}),
		notFinal,
	}, nil, tbNow, turnBlocksCapBytes)
	env, ok := parseTurnBlocksEnvelope(raw)
	if !ok || len(env.Blocks) != 1 || env.Blocks[0].ID != "b2" {
		t.Fatalf("yalnız final, action olmayan blok kalmalı: %s", raw)
	}
	if got, _ := compactTurnBlocks([]turnBlockFrame{tbFrame("b1", blocks.TypeAction, map[string]any{"x": 1})}, nil, tbNow, turnBlocksCapBytes); got != "" {
		t.Errorf("yalnız action → boş: %q", got)
	}
}

func TestTurnBlocksCapDownsamplesBeforeDropping(t *testing.T) {
	var charts []turnBlockFrame
	for i := 0; i < 6; i++ {
		charts = append(charts, tbFrame(fmt.Sprintf("b%d", i+1), blocks.TypeChart, tbChart(fmt.Sprintf("ev%d", i), 20, 120)))
	}
	// 1) 6 grafik × 20 seri × 120 nokta > 256 KiB → seyreltme yeter, düşen yok.
	raw, st := compactTurnBlocks(charts, nil, tbNow, turnBlocksCapBytes)
	if st.Raw <= turnBlocksCapBytes {
		t.Fatalf("fixture tavanı aşmalı: %d", st.Raw)
	}
	env, ok := parseTurnBlocksEnvelope(raw)
	if !ok || len(raw) > turnBlocksCapBytes || !env.DS || !st.Downsampled || len(env.Dropped) != 0 || len(env.Blocks) != 6 {
		t.Fatalf("seyreltme yetmeli: %d bayt ds=%v dropped=%v blok=%d", len(raw), env.DS, env.Dropped, len(env.Blocks))
	}
	var p struct {
		EvidenceRef string `json:"evidenceRef"`
		Window      struct {
			FromNs json.Number `json:"fromNs"`
		} `json:"window"`
		Series []struct {
			Unit   string  `json:"unit"`
			Points [][]any `json:"points"`
		} `json:"series"`
	}
	dec := json.NewDecoder(strings.NewReader(string(env.Blocks[0].Payload)))
	dec.UseNumber()
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.EvidenceRef != "ev0" || p.Window.FromNs.String() != "1761130000000123456" || len(p.Series) != 20 || p.Series[0].Unit != "ms" {
		t.Fatalf("alanlar korunmalı (ns hassasiyeti dahil): %+v", p.Window)
	}
	if n := len(p.Series[0].Points); n > turnBlocksPointsMax || n < turnBlocksPointsMax-2 {
		t.Fatalf("nokta sayısı %d", n)
	}

	// 2) + 1 MiB tablo → tablo düşer, grafikler kalır.
	var rows []any
	for i := 0; len(rows) < 9500; i++ {
		rows = append(rows, []any{i, "ERROR", strings.Repeat("gateway timeout ", 6)})
	}
	table := tbFrame("b9", blocks.TypeTable, map[string]any{"columns": []any{"t", "sev", "body"}, "rows": rows})
	if len(table.Payload) < 1<<20 {
		t.Fatalf("tablo fixture'ı küçük: %d", len(table.Payload))
	}
	raw, st = compactTurnBlocks(append(append([]turnBlockFrame(nil), charts...), table), nil, tbNow, turnBlocksCapBytes)
	env, ok = parseTurnBlocksEnvelope(raw)
	if !ok || len(raw) > turnBlocksCapBytes || len(env.Blocks) != 6 || strings.Join(env.Dropped, ",") != blocks.TypeTable || strings.Join(st.Dropped, ",") != blocks.TypeTable {
		t.Fatalf("tablo düşmeli, grafikler kalmalı: %d bayt dropped=%v blok=%d", len(raw), env.Dropped, len(env.Blocks))
	}

	// 3) tek dev blok (seyreltilemez) → düşer; zarf yine geçerli JSON ≤ tavan.
	code := &codeContextPayload{Reason: "kod entegrasyonu yapılandırılmamış"}
	raw, _ = compactTurnBlocks([]turnBlockFrame{table}, code, tbNow, turnBlocksCapBytes)
	env, ok = parseTurnBlocksEnvelope(raw)
	if !ok || !json.Valid([]byte(raw)) || len(raw) > turnBlocksCapBytes || len(env.Blocks) != 0 || env.Code == nil || env.Dropped[0] != blocks.TypeTable {
		t.Fatalf("dev blok düşmeli, kod kalmalı: %d %+v", len(raw), env.Dropped)
	}
	if got, _ := compactTurnBlocks([]turnBlockFrame{table}, nil, tbNow, turnBlocksCapBytes); got != "" {
		t.Errorf("hiçbir şey sığmadıysa kolon varsayılanı: %d bayt", len(got))
	}
}

func TestDownsamplePointsKeepsFirstLastAndPeaks(t *testing.T) {
	pts := make([]any, 120)
	for i := range pts {
		pts[i] = []any{json.Number(fmt.Sprint(1761130000000 + i)), json.Number(fmt.Sprint(i % 5))}
	}
	pts[57] = []any{json.Number("1761130000057"), json.Number("1000")}
	pts[90] = []any{json.Number("1761130000090"), nil} // boşluk: okunamayan değer tepe sayılmaz
	out := downsamplePoints(pts, 60)
	if len(out) > 60 {
		t.Fatalf("%d nokta", len(out))
	}
	if fmt.Sprint(out[0]) != fmt.Sprint(pts[0]) || fmt.Sprint(out[len(out)-1]) != fmt.Sprint(pts[119]) {
		t.Fatal("ilk ve son nokta kalmalı")
	}
	peak := false
	for _, p := range out {
		if pointValue(p) == 1000 {
			peak = true
		}
	}
	if !peak {
		t.Fatal("tepe (1000) seyreltmede kaybolmamalı")
	}
	if got := downsamplePoints(pts[:50], 60); len(got) != 50 {
		t.Errorf("tavan altı aynen: %d", len(got))
	}
	// {t,v} nesne biçimi de okunur.
	if v := pointValue(map[string]any{"t": json.Number("1"), "v": json.Number("7.5")}); v != 7.5 {
		t.Errorf("nesne noktası %v", v)
	}
	// Grafiğin dışındaki "points" (seri değil) dokunulmaz.
	raw := json.RawMessage(`{"points":[1,2,3,4,5,6,7,8,9,10],"meta":{"points":"x"}}`)
	if got, changed := downsampleBlockPayload(raw, 3); changed || string(got) != string(raw) {
		t.Errorf("seri dışı points değişmemeli: %s", got)
	}
}

// Yakalayıcı withBlockSeq'in ALTINDA: sıralayıcının ürettiği çerçeve aynen
// iletilir ve yakalanır; tap answer.explain.code'u alır, isabette turnId ekler.
func TestChatExchangeTapCapturesBlocksAndCode(t *testing.T) {
	const root = "aaaa0000aaaa0000aaaa0000aaaa0000"
	var evs []xtEvent
	xch := newChatExchange(root, "orders-api neden yavaş?", false, tbNow)
	capt := &turnBlockCapture{}
	xch.setBlockCapture(capt)
	var seq blocks.Sequencer
	emit := xch.tap(withBlockSeq(capt.wrap(xtRecorder(&evs)), &seq))

	emit(blocks.TypeChartsSignals, map[string]any{"service": "orders-api"})
	emit("block", seq.Next(blocks.TypeAction, map[string]any{"kind": "x"}))
	emit("block", seq.Next(blocks.TypeChart, map[string]any{"service": "orders-api", "agg": "p95"}))
	code := &codeContextPayload{Repo: "orders", Reason: "dal bulunamadı"}
	emit("answer", map[string]any{"text": "ilk", "exchangeId": root, "explain": chatExplainMeta{Kind: "trace", ID: defTestTrace, Code: code}})

	if got := xtKinds(evs); got != "block,block,block,answer" {
		t.Fatalf("olaylar değişmeden iletilmeli: %s", got)
	}
	if ans := xtAnswer(t, evs); ans["turnId"] != nil {
		t.Fatalf("kökle aynı exchangeId'de turnId yok: %v", ans)
	}
	r := buildCosreInteraction(xch, aisurface.TierGuided, copilot.CallMeta{}, "ok", "", tbNow.Add(time.Second))
	env, ok := parseTurnBlocksEnvelope(r.Blocks)
	if !ok || len(env.Blocks) != 2 || env.Blocks[0].Type != blocks.TypeChartsSignals || env.Blocks[0].ID != "b1" ||
		env.Blocks[1].Type != blocks.TypeChart || env.Code == nil || env.Code.Repo != "orders" {
		t.Fatalf("satır zarfı: %s", r.Blocks)
	}

	// Önbellek isabeti: exchangeId saklanan üretimin → turnId = kök; künyesiz
	// son cevap kod künyesini sıfırlar (son cevap kazanır).
	emit("answer", map[string]any{"text": "ikinci", "exchangeId": "bbbb0000bbbb0000bbbb0000bbbb0000"})
	ans := xtAnswer(t, evs[len(evs)-1:])
	if ans["turnId"] != root || ans["exchangeId"] != "bbbb0000bbbb0000bbbb0000bbbb0000" {
		t.Fatalf("isabette turnId: %v", ans)
	}
	r = buildCosreInteraction(xch, aisurface.TierGuided, copilot.CallMeta{}, "ok", "", tbNow)
	if env, _ := parseTurnBlocksEnvelope(r.Blocks); env.Code != nil {
		t.Fatalf("künyesiz son cevap kodu taşımamalı: %s", r.Blocks)
	}
	if got := answerWithTurnID(map[string]string{"text": "x"}, root).(map[string]string); got["turnId"] != root {
		t.Fatalf("map[string]string cevap: %v", got)
	}

	// Yakalama tavanı: aşan çerçeve sayılır, yok sayılır (SSE'ye yine gider).
	big := &turnBlockCapture{}
	n := 0
	bigEmit := big.wrap(func(string, any) { n++ })
	blob := strings.Repeat("x", 300<<10)
	for i := 0; i < 5; i++ {
		bigEmit("block", map[string]any{"id": fmt.Sprintf("b%d", i), "type": blocks.TypeTable, "seq": i, "final": true, "payload": blob})
	}
	if n != 5 || len(big.snapshot()) != 3 || big.over != 2 || big.bytes > turnBlocksCaptureMax {
		t.Fatalf("tavan: iletilen %d, yakalanan %d, aşan %d, bayt %d", n, len(big.snapshot()), big.over, big.bytes)
	}
	// Aynı kimlik yeniden gelirse yerine yazılır (FE appendChatBlock gibi).
	re := &turnBlockCapture{}
	reEmit := re.wrap(func(string, any) {})
	reEmit("block", map[string]any{"id": "op-prefetch", "type": blocks.TypeOperation, "seq": 1000, "final": true, "payload": map[string]any{"v": 1}})
	reEmit("block", map[string]any{"id": "op-prefetch", "type": blocks.TypeOperation, "seq": 1000, "final": true, "payload": map[string]any{"v": 2}})
	if f := re.snapshot(); len(f) != 1 || string(f[0].Payload) != `{"v":2}` || f[0].Seq != 1000 {
		t.Fatalf("yerine yazma: %+v", f)
	}
}

// Gerçek açıklama turları satırın zarfına ulaşır: charts turu charts_signals,
// trace kod turu (önbellek isabeti dahil) code künyesi.
func TestExplainChartsTurnBlocksReachInteractionRow(t *testing.T) {
	run := func(t *testing.T, s *Server, ask chatExplainAsk, root string) (map[string]any, string) {
		t.Helper()
		var evs []xtEvent
		xch := newChatExchange(root, "açıkla", false, tbNow)
		capt := &turnBlockCapture{}
		xch.setBlockCapture(capt)
		var seq blocks.Sequencer
		emit := xch.tap(withBlockSeq(capt.wrap(xtRecorder(&evs)), &seq))
		ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: root})
		if h, ok := s.chatExplainTurn(ctx, emit, &ask, time.UTC); !h || !ok {
			t.Fatalf("tur işlenmedi: %s", xtKinds(evs))
		}
		r := buildCosreInteraction(xch, aisurface.TierGuided, copilot.CallMeta{}, "ok", "", time.Now())
		return xtAnswer(t, evs), r.Blocks
	}
	t.Run("charts", func(t *testing.T) {
		s := xrServer(t, newDefProvider(t, "**Ne oldu:** orders-api ", "hata oranı arttı."))
		_, raw := run(t, s, xcChartsAsk(), "cccc0000cccc0000cccc0000cccc0000")
		env, ok := parseTurnBlocksEnvelope(raw)
		if !ok || len(env.Blocks) != 1 || env.Blocks[0].Type != blocks.TypeChartsSignals ||
			!strings.Contains(string(env.Blocks[0].Payload), `"service":"orders-api"`) {
			t.Fatalf("charts_signals zarfta olmalı: %s", raw)
		}
	})
	t.Run("trace kod (isabet → turnId)", func(t *testing.T) {
		s := defServer(t, newDefProvider(t, "**Kök neden:** payments card declined."))
		ask := chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}
		first, raw := run(t, s, ask, "dddd0000dddd0000dddd0000dddd0000")
		if env, ok := parseTurnBlocksEnvelope(raw); !ok || env.Code == nil || env.Code.Reason == "" || first["turnId"] != nil {
			t.Fatalf("kod künyesi zarfta olmalı: %s", raw)
		}
		hit, raw := run(t, s, ask, "eeee0000eeee0000eeee0000eeee0000")
		if hit["cached"] != true || hit["exchangeId"] != "dddd0000dddd0000dddd0000dddd0000" || hit["turnId"] != "eeee0000eeee0000eeee0000eeee0000" {
			t.Fatalf("isabet cevabı turun kimliğini taşımalı: %v", hit)
		}
		if env, ok := parseTurnBlocksEnvelope(raw); !ok || env.Code == nil {
			t.Fatalf("isabet turunun satırı da kod künyesini taşır: %s", raw)
		}
	})
}
