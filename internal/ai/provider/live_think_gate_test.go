package provider

import (
	"strings"
	"testing"
)

// live_think_gate_test.go — v0.10.1304 — Explain akışı Gemma düşünce
// bloğunu hiç göstermez.
//
// Kök neden: tek-atış akış yolunun (StreamOpenAI → copilot.StreamText →
// explain/guided/drawer/intent) kapısı openAIStreamAccum.gate yalnız
// BAŞTAKİ <think>'i tanıyordu; Gemma `<|channel>thought … <channel|>`
// bloğu ve ortadaki <think> canlıda ham akıyordu, anthropicStreamAccum'un
// ise hiç kapısı yoktu. Nihai `answer` çerçevesi düşünceyi gizlese de
// operatör düşünceyi akarken görüyordu.
//
// Düzeltme: liveThinkGate — serbest döngünün de kullandığı thinkStripper
// üstünde ince bir kapı. Bu test parite tablosunu (think_parity_test.go)
// HER bölme noktasında kapıdan geçirir.

// playLiveGate — parçaları kapıdan geçirir; func(string) havuzunun
// biriktirdiği metni (geri alma YOK) döndürür.
func playLiveGate(chunks []string) (joined string, pieces []string) {
	var g liveThinkGate
	var b strings.Builder
	for _, c := range chunks {
		if d := g.Feed(c); d != "" {
			pieces = append(pieces, d)
			b.WriteString(d)
		}
	}
	if d := g.Flush(); d != "" {
		pieces = append(pieces, d)
		b.WriteString(d)
	}
	return b.String(), pieces
}

// blockInners — girdideki açık düşünce bloklarının İÇİ (kapanmamış blok
// dâhil). Soyucudan bağımsız, düz tarama: en erken açılış, onun kapanışı.
func blockInners(s string) []string {
	var out []string
	for {
		at, which := -1, -1
		for k, p := range thinkTagPairs {
			if i := strings.Index(s, p.open); i >= 0 && (at < 0 || i < at) {
				at, which = i, k
			}
		}
		if at < 0 {
			return out
		}
		p := thinkTagPairs[which]
		s = s[at+len(p.open):]
		j := strings.Index(s, p.close)
		if j < 0 {
			return append(out, s)
		}
		out = append(out, s[:j])
		s = s[j+len(p.close):]
	}
}

func TestLiveThinkGateNeverEmitsThought(t *testing.T) {
	for _, tc := range thinkParityCases {
		inners := blockInners(tc.in)
		resets := strings.Contains(tc.in, thinkClose)
		for n := 1; n <= len(tc.in); n++ {
			joined, _ := playLiveGate(splitEvery(tc.in, n))
			for _, in := range inners {
				if strings.TrimSpace(in) != "" && strings.Contains(joined, strings.TrimSpace(in)) {
					t.Errorf("%s (parça=%d): düşünce %q yayınlandı: %q", tc.name, n, in, joined)
				}
			}
			if len(inners) > 0 && strings.Contains(joined, "thought") {
				t.Errorf("%s (parça=%d): Gemma etiketi yayınlandı: %q", tc.name, n, joined)
			}
			got := strings.TrimSpace(joined)
			if !resets {
				// Reset yoksa geri alınacak bir şey de yok: canlı = buffered.
				if got != tc.want {
					t.Errorf("%s (parça=%d): canlı %q, beklenen %q", tc.name, n, got, tc.want)
				}
				continue
			}
			// Reset varsa yalnız blok ÖNCESİ taslak kalabilir; son görünen
			// metin daima buffered cevapla biter.
			if !strings.HasSuffix(got, tc.want) {
				t.Errorf("%s (parça=%d): canlı %q buffered %q ile bitmiyor", tc.name, n, got, tc.want)
			}
		}
	}
}

func TestLiveThinkGateMidResetKeepsOnlyDraft(t *testing.T) {
	const in = "Taslak <think>x</think>Son."
	for n := 1; n <= len(in); n++ {
		joined, pieces := playLiveGate(splitEvery(in, n))
		if strings.Contains(joined, "x") {
			t.Fatalf("parça=%d: düşünce yayınlandı: %q", n, pieces)
		}
		// "Taslak " ancak </think> ile AYNI parçada geldiyse düşer.
		if joined != "Taslak Son." && joined != "Son." {
			t.Fatalf("parça=%d: %q (parçalar %q); beklenen \"Taslak \" + \"Son.\"", n, joined, pieces)
		}
	}
	// Taslak kapanıştan önce yayınlandığında iki ayrı parça: taslak, sonra cevap.
	_, pieces := playLiveGate([]string{"Taslak ", "<think>x</think>", "Son."})
	if strings.Join(pieces, "|") != "Taslak |Son." {
		t.Fatalf("parçalar %q; beklenen Taslak |Son.", pieces)
	}
}

func TestLiveThinkGateHoldsAtMostTagPrefix(t *testing.T) {
	// Etiket öneki olabilecek kuyruk tutulur — 16 bayttan (len("<|channel>thought")−1)
	// fazlası asla.
	var g liveThinkGate
	if d := g.Feed("p95 1.8s <|channel>though"); d != "p95 1.8s " {
		t.Fatalf("Feed = %q; beklenen tutulan önek hariç metin", d)
	}
	if len(g.s.buf) > len("<|channel>thought")-1 {
		t.Fatalf("tutulan %d bayt (%q); tavan 16", len(g.s.buf), g.s.buf)
	}
	if d := g.Flush(); d != "<|channel>though" {
		t.Fatalf("Flush = %q; tutulan kuyruk akış sonunda boşalmalı", d)
	}
	// Kapanmamış bloğun içindeyken Flush hiçbir şey döndürmez.
	var h liveThinkGate
	h.Feed("Cevap. <|channel>thought yarım")
	if d := h.Flush(); d != "" {
		t.Fatalf("Flush = %q; kapanmamış düşünce yayınlanmamalı", d)
	}
}
