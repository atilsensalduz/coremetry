package chstore

// v0.10.1312 — AI SRE özetine verilen 👎 Faz 5'in "incelemeye değer" listesine
// (ListSREReviewDownvotes) DÜŞER. v0.10.1301'den beri liste hep boştu: anlatım
// çağrısı exchange kimliği taşımıyordu (oyun yüzeyi çözülemiyordu) ve FE oy
// rayı call_id göndermiyordu (birleşim f.call_id = c.id hiçbir satır
// bulmuyordu). Artık narrate ai_calls satırını sabitlenmiş kimlikle +
// exchange_id ile yazar, FE {exchangeId, callId, reason} gönderir.
//
// Bu dosya sorguyu `clickhouse local` ile canlı motorda koşar (ikili yoksa
// atlanır; şekil testi sre_eval_test.go TestSREReviewDownvotesSQL her ortamda):
// aynı fikstürde yalnız sre-* çağrısına call_id'li 👎 gelir; call_id'siz eski
// oy, sohbet 👎'si, 👍 ve 👎→👍 flip'i (FINAL) gelmez.

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSREReviewDownvotesLiveEngine(t *testing.T) {
	bin, err := exec.LookPath("clickhouse")
	if err != nil {
		t.Skip("clickhouse ikilisi yok — canlı motor koşusu atlandı (şekil testi sre_eval_test.go)")
	}
	now := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	ts := func(d time.Duration) string { return chQuote(chDateTime64Arg(now.Add(d))) }

	// Kimlik biçimleri anlatımın bastığıyla aynı: çağrı 24 hex (copilot.NewCallID),
	// exchange 32 hex (16 bayt).
	const (
		sreCall, sreX     = "0a1b2c3d4e5f60718293a4b5", "00112233445566778899aabbccddeeff"
		oldCall, oldX     = "1a1b2c3d4e5f60718293a4b5", "10112233445566778899aabbccddeeff"
		chatCall, chatX   = "2a1b2c3d4e5f60718293a4b5", "20112233445566778899aabbccddeeff"
		upCall, upX       = "3a1b2c3d4e5f60718293a4b5", "30112233445566778899aabbccddeeff"
		flipCall, flipX   = "4a1b2c3d4e5f60718293a4b5", "40112233445566778899aabbccddeeff"
		staleCall, staleX = "5a1b2c3d4e5f60718293a4b5", "50112233445566778899aabbccddeeff"
	)
	call := func(id, surface, xid string, at time.Duration) string {
		return fmt.Sprintf("INSERT INTO ai_calls (id, created_at, surface, exchange_id, prompt_sample, response_sample) VALUES (%s, toDateTime64(%s, 9, 'UTC'), %s, %s, 'girdi', 'Ne oldu: x')",
			chQuote(id), ts(at), chQuote(surface), chQuote(xid))
	}
	vote := func(xid, surface string, verdict int, callID, reason string, at time.Duration, version int) string {
		return fmt.Sprintf("INSERT INTO ai_feedback (exchange_id, surface, verdict, user_email, created_at, version, comment, call_id, reason) VALUES (%s, %s, %d, 'op@x', toDateTime64(%s, 9, 'UTC'), %d, 'yanlış servis', %s, %s)",
			chQuote(xid), chQuote(surface), verdict, ts(at), version, chQuote(callID), chQuote(reason))
	}
	fixture := strings.Join([]string{
		`CREATE TABLE ai_calls (
			id String, created_at DateTime64(9), surface LowCardinality(String), exchange_id String DEFAULT '',
			prompt_sample String DEFAULT '', response_sample String DEFAULT ''
		) ENGINE = MergeTree ORDER BY (created_at, surface)`,
		`CREATE TABLE ai_feedback (
			exchange_id String, surface LowCardinality(String), verdict Int8, user_email String DEFAULT '',
			created_at DateTime64(9), version UInt64, comment String DEFAULT '',
			call_id String DEFAULT '', reason LowCardinality(String) DEFAULT ''
		) ENGINE = ReplacingMergeTree(version) ORDER BY exchange_id`,
		// AI SRE özeti (v0.10.1312 sonrası) + FE'nin {exchangeId, callId, reason} 👎'si.
		call(sreCall, "sre-investigation", sreX, -2*time.Hour),
		vote(sreX, "sre-investigation", -1, sreCall, "wrong", -time.Hour, 1),
		// Eski biçim: çağrı exchange'siz, oy call_id'siz → birleşmez (v0.10.1301 boşluğu).
		call(oldCall, "sre-investigation", "", -3*time.Hour),
		vote(oldX, "", -1, "", "", -time.Hour, 1),
		// Sohbet 👎'si: yüzey sre-* değil.
		call(chatCall, "chat", chatX, -2*time.Hour),
		vote(chatX, "chat", -1, chatCall, "wrong", -time.Hour, 1),
		// SRE özetine 👍: inceleme adayı değil.
		call(upCall, "sre-investigation", upX, -2*time.Hour),
		vote(upX, "sre-investigation", 1, upCall, "", -time.Hour, 1),
		// 👎 sonra 👍 (yüksek sürüm, FINAL son oyu görür).
		call(flipCall, "sre-investigation", flipX, -2*time.Hour),
		vote(flipX, "sre-investigation", -1, flipCall, "wrong", -90*time.Minute, 1),
		vote(flipX, "sre-investigation", 1, flipCall, "", -time.Hour, 2),
		// Pencere dışı oy (9 gün önce).
		call(staleCall, "sre-investigation", staleX, -9*24*time.Hour),
		vote(staleX, "sre-investigation", -1, staleCall, "wrong", -9*24*time.Hour+time.Minute, 1),
	}, ";\n") + ";\n"

	q := sreReviewDownvotesSQL(true)
	sel := inlineDBHealthArgs(t, q, sreReviewDownvotesArgs(true, now.Add(-7*24*time.Hour), now, SREReviewDownvotesMax))
	sel = strings.Replace(sel, "SETTINGS max_execution_time = 10", "FORMAT TSVRaw SETTINGS max_execution_time = 10", 1)
	out, err := exec.Command(bin, "local", "--multiquery", "--query", fixture+sel).CombinedOutput()
	if err != nil {
		t.Fatalf("clickhouse local: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("yalnız SRE özetinin call_id'li 👎'si listelenmeli, gelen %d satır:\n%s", len(lines), out)
	}
	cols := strings.Split(lines[0], "\t")
	if len(cols) != 9 {
		t.Fatalf("9 kolon bekleniyordu: %q", lines[0])
	}
	if cols[0] != sreX || cols[1] != sreCall || cols[2] != "sre-investigation" || cols[5] != "wrong" || cols[6] != "yanlış servis" || cols[8] != "Ne oldu: x" {
		t.Fatalf("satır: %q", cols)
	}
}
