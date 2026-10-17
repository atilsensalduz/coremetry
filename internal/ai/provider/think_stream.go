package provider

import "strings"

// think_stream.go — v0.10.1273 (CoSRE adım 4): akan metinden düşünce
// bloklarını PARÇA SINIRINDAN BAĞIMSIZ soyan küçük durum makinesi.
//
// Buffered yolun kuralı StripThinking'dir: SON `</think>` öncesindeki her
// şey düşer. Akışta bunun karşılığı iki parçalı:
//
//   - `<think>…</think>` içi HİÇ yayınlanmaz; etiketin yarısı bir parçada,
//     yarısı ötekinde gelebilir (`<thi` + `nk>`), bu yüzden etiket öneki
//     olabilecek kuyruk karar verilene dek TUTULUR.
//   - bir `</think>` tüketildiğinde ondan ÖNCE yayınlanmış görünür metin
//     varsa buffered yol onu da gizlerdi → reset=true (çağıran önceki
//     parçaları geri alır). Ortadaki tek düşünce bloğu da bu yüzden
//     güvenli: blok kapanınca öncesi geri alınır.
//
// Gemma kanal biçimi (`<|channel>thought … <channel|>`) da gizlenir —
// buffered nihai metin onu soymuyor; burada GİZLEMEK muhafazakâr yön
// (ekrana fazladan bir şey çıkmaz, `answer` metni zaten otoriter).
//
// Kapanmamış blok (akış `<think>…` ortasında bitti): içi gizli kalır.
// Buffered metin onu ham bırakırdı; `answer` otoriter, akışta göstermemek
// yine muhafazakâr yön.

type thinkTagPair struct {
	open, close string
	// resetOnClose — kapanış, buffered StripThinking'in "son </think>
	// sonrası" kuralını tetikler (öncesi geri alınır).
	resetOnClose bool
}

var thinkTagPairs = []thinkTagPair{
	{open: thinkOpen, close: thinkClose, resetOnClose: true},
	{open: "<|channel>thought", close: "<channel|>"},
}

// thinkStripper — sıfır değeri kullanıma hazır.
type thinkStripper struct {
	buf string // henüz karar verilmemiş kuyruk (etiket öneki olabilir)
	in  int    // 0 = dışarıda; aksi hâlde thinkTagPairs indeksi+1
}

// Feed bir içerik parçasını tüketir. out: şimdi yayınlanabilecek görünür
// metin; reset: out'tan ÖNCE yayınlanmış her şey geri alınmalı.
func (t *thinkStripper) Feed(s string) (out string, reset bool) {
	t.buf += s
	var vis strings.Builder
	for {
		if t.in > 0 {
			p := thinkTagPairs[t.in-1]
			if i := strings.Index(t.buf, p.close); i >= 0 {
				t.buf = t.buf[i+len(p.close):]
				t.in = 0
				if p.resetOnClose {
					reset = true
					vis.Reset()
				}
				continue
			}
			// kapanış öneki olabilecek kuyruğu tut, gerisi düşünce — at
			t.buf = t.buf[len(t.buf)-heldPrefixLen(t.buf, []string{p.close}):]
			break
		}
		// dışarıda: en erken açılış (ya da başıboş </think>) etiketini bul
		at, which, stray := -1, 0, false
		for k, p := range thinkTagPairs {
			if i := strings.Index(t.buf, p.open); i >= 0 && (at < 0 || i < at) {
				at, which, stray = i, k+1, false
			}
		}
		if i := strings.Index(t.buf, thinkClose); i >= 0 && (at < 0 || i < at) {
			at, stray = i, true
		}
		if at < 0 {
			keep := heldPrefixLen(t.buf, thinkWatchTags())
			vis.WriteString(t.buf[:len(t.buf)-keep])
			t.buf = t.buf[len(t.buf)-keep:]
			break
		}
		if stray {
			// Açılışsız </think>: buffered StripThinking yine de öncesini atar.
			t.buf = t.buf[at+len(thinkClose):]
			reset = true
			vis.Reset()
			continue
		}
		vis.WriteString(t.buf[:at])
		t.buf = t.buf[at+len(thinkTagPairs[which-1].open):]
		t.in = which
	}
	return vis.String(), reset
}

// Flush akışın sonunda tutulan kuyruğu boşaltır. Blok içindeysek (kapanmamış
// düşünce) hiçbir şey dönmez.
func (t *thinkStripper) Flush() string {
	if t.in > 0 {
		t.buf = ""
		return ""
	}
	out := t.buf
	t.buf = ""
	return out
}

func thinkWatchTags() []string {
	tags := make([]string, 0, len(thinkTagPairs)+1)
	for _, p := range thinkTagPairs {
		tags = append(tags, p.open)
	}
	return append(tags, thinkClose)
}

// heldPrefixLen — s'nin sonunda, verilen etiketlerden birinin ÖZ öneki olan
// en uzun kuyruğun uzunluğu (tam etiket değil; o zaten bulunurdu).
func heldPrefixLen(s string, tags []string) int {
	best := 0
	for _, tag := range tags {
		for n := len(tag) - 1; n > best; n-- {
			if n <= len(s) && strings.HasSuffix(s, tag[:n]) {
				best = n
				break
			}
		}
	}
	return best
}
