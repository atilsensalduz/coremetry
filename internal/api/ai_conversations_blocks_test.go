package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// v0.10.1444 — Geçmiş'ten açılan turun zengin blokları: blob yalnız asistan
// exchangeId'sini taşır (istemci → sunucu), bloklar YALNIZ sunucudan gelir
// (cosre_interactions.blocks zarfı), eski satırlar bayt-bayt eski kalır.

const (
	cbX1 = "aaaa0000aaaa0000aaaa0000aaaa0000"
	cbX2 = "bbbb0000bbbb0000bbbb0000bbbb0000"
)

func TestSanitizeChatMessagesExchangeIDAndBlocks(t *testing.T) {
	in := []aiChatMessage{
		{Role: "user", Text: "q", ExchangeID: cbX1, Blocks: json.RawMessage(`[{"id":"b1"}]`)},
		{Role: "assistant", Text: "a", ExchangeID: "AAAA0000AAAA0000AAAA0000AAAA0000", Blocks: json.RawMessage(`[{"id":"b1"}]`), BlocksAt: 9, BlocksDropped: []string{"table"}},
		{Role: "assistant", Text: "b", ExchangeID: cbX1[:31]},
		{Role: "assistant", Text: "c", ExchangeID: "zzzz0000aaaa0000aaaa0000aaaa0000"},
	}
	out := sanitizeChatMessages(in)
	if len(out) != 4 {
		t.Fatalf("%d mesaj", len(out))
	}
	if out[0].ExchangeID != "" {
		t.Errorf("kullanıcı mesajı exchangeId taşımaz: %q", out[0].ExchangeID)
	}
	if out[1].ExchangeID != cbX1 {
		t.Errorf("32-hex korunur (küçültülür): %q", out[1].ExchangeID)
	}
	if out[2].ExchangeID != "" || out[3].ExchangeID != "" {
		t.Errorf("31 karakter / hex olmayan düşer: %q %q", out[2].ExchangeID, out[3].ExchangeID)
	}
	for i, m := range out {
		if m.Blocks != nil || m.BlocksAt != 0 || m.BlocksDropped != nil {
			t.Errorf("mesaj %d: istemciden gelen bloklar her zaman düşer", i)
		}
	}
}

func TestConversationBlocksDecorateOldRowsTextOnly(t *testing.T) {
	env := `{"v":1,"at":1761130000000,"blocks":[{"id":"b1","type":"charts_signals","seq":1,"final":true,"payload":{"service":"orders-api"}}],"code":{"repo":"orders","reason":"dal yok"},"dropped":["table"]}`
	msgs := func() []aiChatMessage {
		return []aiChatMessage{
			{Role: "user", Text: "q"},
			{Role: "assistant", Text: "a", ExchangeID: cbX1, Explain: &chatExplainMeta{Kind: "trace", ID: "t1"}},
			{Role: "assistant", Text: "b", ExchangeID: cbX2},
		}
	}

	// Eski satır: exchangeId yok → okuma yapılmaz, JSON bayt-bayt aynı.
	old := []aiChatMessage{{Role: "user", Text: "q"}, {Role: "assistant", Text: "a"}}
	before, _ := json.Marshal(old)
	called := false
	fetch := func(context.Context, []string, time.Time, time.Time) (map[string]string, error) {
		called = true
		return map[string]string{cbX1: env}, nil
	}
	decorateTurnBlocks(context.Background(), old, 0, time.Now().UnixNano(), true, fetch)
	after, _ := json.Marshal(old)
	if called || string(before) != string(after) {
		t.Fatalf("eski satır değişmemeli (called=%v): %s", called, after)
	}

	// Kolon bayrağı kapalı → okuma hiç denenmez.
	m := msgs()
	decorateTurnBlocks(context.Background(), m, 0, time.Now().UnixNano(), false, fetch)
	if called || m[1].Blocks != nil {
		t.Fatal("bayrak kapalıyken okuma yapılmamalı")
	}

	// Dolu zarf → blocks/blocksAt/blocksDropped + explain.code (sunucu kaynaklı).
	var gotIDs []string
	m = msgs()
	decorateTurnBlocks(context.Background(), m, 0, time.Now().UnixNano(), true,
		func(_ context.Context, ids []string, _, _ time.Time) (map[string]string, error) {
			gotIDs = ids
			return map[string]string{cbX1: env, cbX2: `{"v":2,"blocks":[{"id":"x"}]}`}, nil
		})
	if len(gotIDs) != 2 || gotIDs[0] != cbX1 || gotIDs[1] != cbX2 {
		t.Fatalf("kimlikler: %v", gotIDs)
	}
	var frames []turnBlockFrame
	if err := json.Unmarshal(m[1].Blocks, &frames); err != nil || len(frames) != 1 || frames[0].Type != "charts_signals" {
		t.Fatalf("blocks: %s", m[1].Blocks)
	}
	if m[1].BlocksAt != 1761130000000 || len(m[1].BlocksDropped) != 1 || m[1].Explain.Code == nil || m[1].Explain.Code.Repo != "orders" {
		t.Fatalf("zarf alanları: at=%d dropped=%v code=%v", m[1].BlocksAt, m[1].BlocksDropped, m[1].Explain.Code)
	}
	if m[2].Blocks != nil || m[2].Explain != nil {
		t.Fatalf("bilinmeyen sürüm → düz metin: %s", m[2].Blocks)
	}

	// Okuma hatası → soft-fail, transkript aynen.
	m = msgs()
	decorateTurnBlocks(context.Background(), m, 0, time.Now().UnixNano(), true,
		func(context.Context, []string, time.Time, time.Time) (map[string]string, error) {
			return nil, errors.New("ch down")
		})
	if m[1].Blocks != nil || m[1].Explain.Code != nil || m[1].Text != "a" {
		t.Fatal("hata düz metne düşmeli")
	}
}

func TestConversationBlocksWindow(t *testing.T) {
	first := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	upd := first.Add(2 * time.Hour)
	from, to := turnBlocksWindow(first.UnixNano(), upd.UnixNano())
	if !from.Equal(first.Add(-15*time.Minute)) || !to.Equal(upd.Add(20*time.Minute)) {
		t.Fatalf("pencere: %v .. %v", from, to)
	}
	from, to = turnBlocksWindow(0, upd.UnixNano())
	if to.Sub(from) != 180*24*time.Hour {
		t.Fatalf("firstAt yok → 180 g: %v", to.Sub(from))
	}
	// Taşıma: yeni satır now, var olan blob değerini korur, eski blob 0.
	if got := carriedFirstAt("", 42); got != 42 {
		t.Errorf("yeni satır: %d", got)
	}
	if got := carriedFirstAt(`{"firstAt":7,"messages":[]}`, 42); got != 7 {
		t.Errorf("taşınmalı: %d", got)
	}
	if got := carriedFirstAt(`{"messages":[]}`, 42); got != 0 {
		t.Errorf("eski satır uydurulmaz: %d", got)
	}
}
