package api

// CoSRE öğrenme Faz 1 — cosre_interactions: kanıtsız-cevap tespiti, emit
// akışından tur satırı (tool/ttft/model/sayfa) ve bloklamayan kuyruk.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestAnswerAdmitsNoEvidence(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// operatör örnekleri, 2026-10-15 (MCA-00029, bsa-atm-core-prod)
		{"drawer neden", "Hata nedeni kanıtlarda belirtilmemiştir; exception grubu için sadece operasyon adı ve hata tipi bilgisi mevcuttur.", true},
		{"drawer düzelt", "Hatanın nedeni ve detayları kanıtlarda yer almadığı için spesifik bir düzeltme önerisi sunulamamaktadır.", true},
		{"en", "The root cause is not mentioned in the evidence provided.", true},
		{"veri yok", "Bu pencerede log verisi yok.", true},
		{"dayanaklı", "Kanıtlarda görüldüğü gibi hata oranı 14:02'de %0.3'ten %5'e çıktı; deploy v1.4.2 ile aynı dakika.", false},
		{"yoksa", "Veri akışı sürüyorsa sorun yoksayılabilir: p95 120 ms.", false},
		{"boş", "", false},
		{"geç uyarı", strings.Repeat("p95 gecikme 840 ms, hata oranı %4 ve db.pool beklemesi artıyor. ", 8) + "Log verisi yok.", false},
	}
	for _, c := range cases {
		if got := answerAdmitsNoEvidence(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestChatExchangeCapturesInteraction(t *testing.T) {
	t0 := time.Now().Add(-500 * time.Millisecond)
	x := newChatExchange("ex1", "Bu neden oluyor?", false, t0)
	x.setInteraction("conv1", 2, "regenerate", &agentctx.PageContext{Page: "exception", ExceptionID: "eg9", Service: "svc-a"}, "ctx-svc", "tr1")
	x.noteModelCall("gemma4-31b", 1657, 174)

	var seen []string
	emit := x.tap(withStepIDs(func(kind string, _ any) { seen = append(seen, kind) }))
	emit("step", map[string]string{"label": "bütçe"}) // etiket çipi: tool değil, kimlik yer
	i := emitStepChipOrigin(emit, "find_exceptions", `{"service":"svc-a"}`, "guided")
	emitStepEvidence(emit, i, "find_exceptions", "MCA-00029 x12", nil)
	j := emitStepChip(emit, "query_logs", `{}`)
	emitStepEvidence(emit, j, "query_logs", "", errors.New("timeout"))
	emit("delta", map[string]string{"text": "Hata"})
	emit("answer", map[string]any{"text": "Hata nedeni kanıtlarda belirtilmemiştir."})

	if i != 2 || j != 3 {
		t.Fatalf("step kimlikleri: %d %d", i, j)
	}
	r := buildCosreInteraction(x, "drawer", copilot.CallMeta{UserEmail: "u@x", Surface: "cosre-turn:drawer"}, "ok", "", time.Now())
	if r.ExchangeID != "ex1" || r.ConversationID != "conv1" || r.Turn != 2 || r.Attempt != "regenerate" {
		t.Errorf("kimlik alanları: %+v", r)
	}
	if r.Page != "exception" || r.ExceptionID != "eg9" || r.Service != "svc-a" || r.TraceID != "tr1" {
		t.Errorf("sayfa bağlamı: page=%q ex=%q svc=%q tr=%q", r.Page, r.ExceptionID, r.Service, r.TraceID)
	}
	if len(r.Tools) != 2 {
		t.Fatalf("tools: %+v", r.Tools)
	}
	if a := r.Tools[0]; a.Name != "find_exceptions" || a.Origin != "guided" || !a.OK || a.Output != "MCA-00029 x12" || a.Bytes != 13 {
		t.Errorf("tool[0]: %+v", a)
	}
	if b := r.Tools[1]; b.OK || b.Error == "" || b.Output != "" {
		t.Errorf("tool[1] hata: %+v", b)
	}
	if r.Model != "gemma4-31b" || r.ModelCalls != 1 || r.InputTokens != 1657 {
		t.Errorf("model: %+v", r)
	}
	if r.TTFTMs < 400 || r.TotalMs < r.TTFTMs {
		t.Errorf("süreler: ttft=%d total=%d", r.TTFTMs, r.TotalMs)
	}
	if !r.NoEvidence || r.EmptyAnswer {
		t.Errorf("bayraklar: noEvidence=%v empty=%v", r.NoEvidence, r.EmptyAnswer)
	}
	if len(seen) == 0 || seen[len(seen)-1] != "answer" {
		t.Errorf("tap olayı değiştirmeden iletmeli: %v", seen)
	}
}

func TestNormalizeChatAttempt(t *testing.T) {
	for in, want := range map[string]string{"retry": "retry", "regenerate": "regenerate", "edit": "edit", "": "", "DROP": ""} {
		if got := normalizeChatAttempt(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}

type fakeCosreSink struct{ rows chan int }

func (f *fakeCosreSink) InsertCosreInteractions(_ context.Context, rows []chstore.CosreInteraction) error {
	f.rows <- len(rows)
	return nil
}

func TestCosreWriterNeverBlocks(t *testing.T) {
	w := newCosreInteractionWriter(&fakeCosreSink{rows: make(chan int, 1)})
	for k := 0; k < cosreQueueCap+5; k++ {
		w.enqueue(chstore.CosreInteraction{ExchangeID: "x"})
	}
	if got := w.dropped.Load(); got != 5 {
		t.Fatalf("düşen satır: %d", got)
	}
}

func TestCosreWriterFlushesBatch(t *testing.T) {
	sink := &fakeCosreSink{rows: make(chan int, 4)}
	w := newCosreInteractionWriter(sink)
	go w.run()
	for k := 0; k < cosreFlushRows; k++ {
		w.enqueue(chstore.CosreInteraction{ExchangeID: "x"})
	}
	select {
	case n := <-sink.rows:
		if n != cosreFlushRows {
			t.Fatalf("batch boyu %d", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("flush olmadı")
	}
}
