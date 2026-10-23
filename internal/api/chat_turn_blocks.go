package api

// chat_turn_blocks.go — v0.10.1444: CoSRE turunun zengin blokları arşivde.
//
// Operatör bulgusu: Tek CoSRE'den (v0.10.1400–1407, 1435) beri ✨ açıklaması
// bir sohbet turu; ama charts_signals / log_spike / metric_panel /
// service_report / kanıt grafikleri ve `code` künyesi yalnız canlı SSE'de
// yaşıyordu — Geçmiş'ten (ya da ?chat=<convId>) açılan tur düz metne
// düşüyordu (ChatBubble "blok yoksa ExplainBody").
//
// Karar (docs/DECISIONS.md v0.10.1444): bloklar sohbet blob'unda DEĞİL
// (saved_views 64 KB duvarı, A1 onayı), turun kendi cosre_interactions
// satırında — `blocks` kolonu, sürümlü zarf:
//
//	{"v":1,"at":<unix ms>,"blocks":[wire çerçeveleri],"code":{…}?,"dropped":[…]?,"ds":true?}
//
// DEĞERLE saklanır (anlık görüntü): anlatım o veriyi anlatıyor; yeniden
// sorgu zamanla farklı resim verirdi. Spec grafikleri (`chart`) zaten spec —
// CosreChart canlı çeker. Tavan 256 KiB: önce seriler 60 noktaya indirilir
// (tepe korunur), sığmazsa en büyük blok düşer (`dropped`). `action` blokları
// arşive girmez (eskimiş duruma karşı eylem tekrarı olurdu).
//
// Yakalama: copilotChat'in emit zincirinde withBlockSeq'in ALTINDA
// (turnBlockCapture.wrap) — sıralayıcının ürettiği her `block` çerçevesi
// (guided demet / açıklama kaydı / döngü / ön-getirme) aynı noktadan geçer;
// tap ise blok TÜRÜ adıyla yayılan ham olayı görür, kimlik/sıra görmez.
// Sıkıştırma turun TEK defer'inde (recordChatExchange → buildCosreInteraction)
// akış bittikten SONRA koşar; SSE yoluna ek gecikme yok.

import (
	"bytes"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

const (
	// turnBlocksEnvelopeV — zarf sürümü; bilinmeyen sürüm = blok yok.
	turnBlocksEnvelopeV = 1
	// turnBlocksCapBytes — satır başına zarf tavanı (sıkıştırma sonrası JSON).
	turnBlocksCapBytes = 256 << 10
	// turnBlocksCaptureMax — tur başına ham yakalama tavanı (bellek); aşan
	// çerçeveler sayılır ve yok sayılır (SSE'ye yine gider).
	turnBlocksCaptureMax = 1 << 20
	// turnBlocksPointsMax — tavan aşılınca seri başına nokta sayısı.
	turnBlocksPointsMax = 60
)

// turnBlockFrame — SSE `block` çerçevesinin AYNISI ({id,type,seq,final,payload};
// blocks/block.go). FE ChatTypedBlock şekli → restoreTurns eşleme istemez.
type turnBlockFrame struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Seq     int             `json:"seq"`
	Final   bool            `json:"final"`
	Payload json.RawMessage `json:"payload"`
}

// turnBlocksEnvelope — cosre_interactions.blocks kolonunun gövdesi.
type turnBlocksEnvelope struct {
	V       int                 `json:"v"`
	At      int64               `json:"at"` // unix ms — FE "Arşiv anlık görüntüsü · <zaman>"
	Blocks  []turnBlockFrame    `json:"blocks"`
	Code    *codeContextPayload `json:"code,omitempty"`    // chatExplainMeta.Code (sunucu kaynaklı)
	Dropped []string            `json:"dropped,omitempty"` // tavana sığmayıp düşen blok türleri
	DS      bool                `json:"ds,omitempty"`      // seriler seyreltildi
}

// turnBlocksStats — sıkıştırmanın ölçümü (test + teşhis).
type turnBlocksStats struct {
	Raw         int
	Out         int
	Downsampled bool
	Dropped     []string
}

// ── yakalama ──────────────────────────────────────────────────────────

// turnBlockCapture — bir turun `block` çerçeveleri (kilitli: birden çok
// kademe / gorutin yayınlayabilir).
type turnBlockCapture struct {
	mu     sync.Mutex
	frames []turnBlockFrame
	bytes  int
	over   int
}

// wrap — emit sarmalayıcısı: çerçeveyi DEĞİŞTİRMEDEN önce iletir, sonra
// `block` türündekini kaydeder (frame hiç gecikmez).
func (c *turnBlockCapture) wrap(emit func(string, any)) func(string, any) {
	if c == nil {
		return emit
	}
	return func(kind string, v any) {
		emit(kind, v)
		if kind == "block" {
			c.note(v)
		}
	}
}

// note — çerçeveyi bir kez marshal eder; aynı kimlik yeniden gelirse
// (FE appendChatBlock gibi) yerine yazar. action / final olmayan atlanır.
func (c *turnBlockCapture) note(v any) {
	f, ok := turnBlockFrameOf(v)
	if !ok || f.Type == blocks.TypeAction || !f.Final {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.frames {
		if c.frames[i].ID == f.ID {
			c.bytes += len(f.Payload) - len(c.frames[i].Payload)
			c.frames[i] = f
			return
		}
	}
	if c.bytes+len(f.Payload) > turnBlocksCaptureMax {
		c.over++
		return
	}
	c.bytes += len(f.Payload)
	c.frames = append(c.frames, f)
}

// snapshot — kopya (kilit altında).
func (c *turnBlockCapture) snapshot() []turnBlockFrame {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]turnBlockFrame(nil), c.frames...)
}

// turnBlockFrameOf — SAF: emit yükü → çerçeve. Sequencer map[string]any
// üretir; başka şekiller JSON üzerinden çözülür.
func turnBlockFrameOf(v any) (turnBlockFrame, bool) {
	var f turnBlockFrame
	if m, ok := v.(map[string]any); ok {
		f.ID, _ = m["id"].(string)
		f.Type, _ = m["type"].(string)
		switch n := m["seq"].(type) {
		case int:
			f.Seq = n
		case float64:
			f.Seq = int(n)
		}
		f.Final, _ = m["final"].(bool)
		raw, err := json.Marshal(m["payload"])
		if err != nil {
			return f, false
		}
		f.Payload = raw
	} else {
		raw, err := json.Marshal(v)
		if err != nil || json.Unmarshal(raw, &f) != nil {
			return f, false
		}
	}
	if f.Type == "" || len(f.Payload) == 0 || string(f.Payload) == "null" {
		return f, false
	}
	return f, true
}

// ── sıkıştırma ────────────────────────────────────────────────────────

// compactTurnBlocks — SAF: çerçeveler + kod künyesi → tavana sığan zarf.
// Adımlar: action / final olmayan düşer → sığıyorsa bitti → seriler
// turnBlocksPointsMax noktaya (tepe korunur, ds) → en büyük blok düşer
// (dropped) sığana dek. Asla hata vermez; blok da kod da yoksa "" (kolon
// varsayılanı).
func compactTurnBlocks(frames []turnBlockFrame, code *codeContextPayload, now time.Time, capBytes int) (string, turnBlocksStats) {
	var st turnBlocksStats
	keep := make([]turnBlockFrame, 0, len(frames))
	for _, f := range frames {
		if f.Type == blocks.TypeAction || !f.Final || len(f.Payload) == 0 {
			continue
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 && code == nil {
		return "", st
	}
	env := turnBlocksEnvelope{V: turnBlocksEnvelopeV, At: now.UnixMilli(), Blocks: keep, Code: code}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", st
	}
	st.Raw = len(raw)
	if len(raw) <= capBytes {
		st.Out = len(raw)
		return string(raw), st
	}
	// 2) seyreltme — yalnız değişen bloğun yükü yeniden yazılır.
	for i := range env.Blocks {
		if p, changed := downsampleBlockPayload(env.Blocks[i].Payload, turnBlocksPointsMax); changed {
			env.Blocks[i].Payload = p
			env.DS = true
		}
	}
	st.Downsampled = env.DS
	raw, err = json.Marshal(env)
	if err != nil {
		return "", st
	}
	// 3) en büyük blok düşer (tek dev blok da).
	for len(raw) > capBytes && len(env.Blocks) > 0 {
		big := 0
		for i := range env.Blocks {
			if len(env.Blocks[i].Payload) > len(env.Blocks[big].Payload) {
				big = i
			}
		}
		env.Dropped = append(env.Dropped, env.Blocks[big].Type)
		env.Blocks = append(env.Blocks[:big:big], env.Blocks[big+1:]...)
		if raw, err = json.Marshal(env); err != nil {
			return "", st
		}
	}
	if len(raw) > capBytes { // kod künyesi bile sığmıyor (pratikte ulaşılmaz)
		env.Code = nil
		if raw, err = json.Marshal(env); err != nil || len(raw) > capBytes {
			return "", st
		}
	}
	st.Dropped = env.Dropped
	if len(env.Blocks) == 0 && env.Code == nil {
		return "", st
	}
	st.Out = len(raw)
	return string(raw), st
}

// downsampleBlockPayload — SAF: yükte `series` dizisinin elemanlarındaki
// `points` dizilerini max noktaya indirir (evidence chart, service_report
// httpCharts[].series, metric_panel.chart.series, log_spike iç blokları —
// hepsi aynı şekil). Sayılar json.Number olarak korunur (ns damgaları
// float64'e düşüp bozulmaz). Değişiklik yoksa girdi aynen döner.
func downsampleBlockPayload(p json.RawMessage, max int) (json.RawMessage, bool) {
	if !bytes.Contains(p, []byte(`"points"`)) {
		return p, false
	}
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return p, false
	}
	if !downsampleWalk(v, max) {
		return p, false
	}
	out, err := json.Marshal(v)
	if err != nil {
		return p, false
	}
	return out, true
}

func downsampleWalk(v any, max int) bool {
	changed := false
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if k == "series" {
				if arr, ok := val.([]any); ok {
					for _, s := range arr {
						if sm, ok := s.(map[string]any); ok {
							if pts, ok := sm["points"].([]any); ok && len(pts) > max {
								sm["points"] = downsamplePoints(pts, max)
								changed = true
							}
						}
					}
				}
			}
			if downsampleWalk(val, max) {
				changed = true
			}
		}
	case []any:
		for _, e := range x {
			if downsampleWalk(e, max) {
				changed = true
			}
		}
	}
	return changed
}

// downsamplePoints — SAF: ilk ve son nokta her zaman kalır; aradaki n-2
// nokta tam max-2 kovaya (kesirli sınırlarla, her kova ≥1 nokta) bölünür,
// kova başına DEĞERİ EN BÜYÜK nokta kalır (anlatımın konuştuğu tepe düşmez).
// Sonuç tam max.
func downsamplePoints(pts []any, max int) []any {
	n := len(pts)
	if n <= max || max < 3 {
		return pts
	}
	inner, mid := max-2, n-2
	out := make([]any, 0, max)
	out = append(out, pts[0])
	for b := 0; b < inner; b++ {
		start, end := 1+b*mid/inner, 1+(b+1)*mid/inner
		best, bestV := start, pointValue(pts[start])
		for i := start + 1; i < end; i++ {
			if v := pointValue(pts[i]); v > bestV {
				best, bestV = i, v
			}
		}
		out = append(out, pts[best])
	}
	return append(out, pts[n-1])
}

// pointValue — [t, v] dizisi ya da {v|value} nesnesi; okunamazsa -Inf
// (kovanın ilki kalır).
func pointValue(p any) float64 {
	num := func(x any) (float64, bool) {
		switch n := x.(type) {
		case json.Number:
			f, err := n.Float64()
			return f, err == nil
		case float64:
			return n, true
		}
		return 0, false
	}
	switch x := p.(type) {
	case []any:
		if len(x) >= 2 {
			if f, ok := num(x[1]); ok {
				return f
			}
		}
	case map[string]any:
		for _, k := range []string{"v", "value", "y"} {
			if f, ok := num(x[k]); ok {
				return f
			}
		}
	}
	return math.Inf(-1)
}

// ── okuma ─────────────────────────────────────────────────────────────

// parseTurnBlocksEnvelope — SAF: kolon gövdesi → zarf. "" / bozuk / bilinmeyen
// sürüm → ok=false (tur düz metin çizilir).
func parseTurnBlocksEnvelope(raw string) (turnBlocksEnvelope, bool) {
	var env turnBlocksEnvelope
	if raw == "" || json.Unmarshal([]byte(raw), &env) != nil || env.V != turnBlocksEnvelopeV {
		return turnBlocksEnvelope{}, false
	}
	if len(env.Blocks) == 0 && env.Code == nil {
		return turnBlocksEnvelope{}, false
	}
	return env, true
}
