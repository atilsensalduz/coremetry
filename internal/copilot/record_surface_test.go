package copilot

// v0.10.1316 — kayıt yüzeyi öneki: işaretsiz ctx'te yüzey aynen (üretim),
// işaretli ctx'te önek bir kez eklenir; RecordUsage / RecordExchange satırı
// öneki taşır, profil yönlendirmesi etiketi okumaya devam eder.

import (
	"context"
	"testing"
	"time"
)

type recSurfaceRec struct{ ch chan CallRecord }

func (r *recSurfaceRec) RecordCall(_ context.Context, c CallRecord) { r.ch <- c }

func TestRecordSurfacePrefix(t *testing.T) {
	bg := context.Background()
	if WithRecordSurfacePrefix(bg, "") != bg {
		t.Fatal("boş önek ctx'i değiştirmemeli")
	}
	for _, tc := range []struct {
		ctx  context.Context
		in   string
		want string
	}{
		{bg, "chat-guided", "chat-guided"},
		{WithRecordSurfacePrefix(bg, "evalset-"), "chat-guided", "evalset-chat-guided"},
		{WithRecordSurfacePrefix(bg, "evalset-"), "evalset-ChatLoop", "evalset-ChatLoop"},
		{nil, "chat", "chat"},
	} {
		if got := recordSurface(tc.ctx, tc.in); got != tc.want {
			t.Errorf("recordSurface(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	s := New(ProviderOpenAI, "k", "m")
	rec := &recSurfaceRec{ch: make(chan CallRecord, 4)}
	s.SetRecorder(rec)
	ctx := WithMeta(WithRecordSurfacePrefix(bg, "evalset-"), CallMeta{Surface: "chat-intent-none", ExchangeID: "ev/1:chat-intent-none"})
	s.RecordUsage(ctx, time.Now(), 1, 1, 0, "ok", "", "q", "a")
	s.RecordExchange(WithMeta(WithRecordSurfacePrefix(bg, "evalset-"), CallMeta{Surface: "cosre-turn:loop"}), time.Now(), "ok", "", "q", "a")
	for i := 0; i < 2; i++ {
		select {
		case r := <-rec.ch:
			if r.Surface != "evalset-chat-intent-none" && r.Surface != "evalset-cosre-turn:loop" {
				t.Errorf("kayıt yüzeyi önek taşımıyor: %q", r.Surface)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("kayıt gelmedi")
		}
	}
}
