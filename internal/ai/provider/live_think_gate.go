package provider

import "strings"

// live_think_gate.go — v0.10.1304: tek-atış akış yolunun (StreamOpenAI /
// StreamAnthropic → copilot.StreamText → explain/guided/drawer/intent)
// düşünce kapısı.
//
// Eskiden openAIStreamAccum yalnız BAŞTAKİ <think> bloğunu tanıyordu:
// Gemma `<|channel>thought … <channel|>` bloğu ve metnin ortasındaki
// <think> ham akıyordu; anthropicStreamAccum'un hiç kapısı yoktu. Serbest
// döngünün kapısı (tools_stream_gate.go) zaten doğruydu — bu dosya AYNI
// makineyi (thinkStripper, tek etiket tablosu thinkTagPairs) tek-atış yola
// bağlar.
//
// Fark tek: buradaki havuz func(string) — yayınlanmış metin GERİ
// ALINAMAZ. Bir </think> öncesini geri almayı istediğinde (reset) kapı
// yalnız baştaki boşluğu kırpmaya geçer; yayınlanmış kısım ekranda kalır.
// Bu nadir durumda bile ekrana çıkan şey blok ÖNCESİ taslak metindir,
// asla düşüncenin kendisi değil: blok içi soyucuda tutulur ve atılır.
// Cevabın kaynağı zaten `answer` çerçevesidir (frontend/src/lib/api.ts
// explainStream) — biriken delta'lar yalnız ilerleme gösterimi.
//
// Tutma tavanı: etiket öneki olabilecek kuyruk, en uzun açılış etiketinin
// (Gemma kanal açılışı, 17 bayt) boyu − 1 = 16 bayt; akış sonunda Flush
// ile boşalır.

// liveThinkGate — sıfır değeri kullanıma hazır.
type liveThinkGate struct {
	s thinkStripper
	// emitted — en az bir görünür parça döndü. O ana dek baştaki boşluk
	// atılır (buffered StripThinking baştan kırpar; Gemma bloğunun
	// ardındaki "\n\n" boş bir balon açmasın).
	emitted bool
	// trimLead — bir reset (</think>) görüldü: buffered yol öncesini
	// atardı, sonrasını baştan kırpardı. Geri alınamayanı geri almayız;
	// yalnız baştaki boşluğu kırparız.
	trimLead bool
}

// Feed bir content parçasını tüketir ve ŞİMDİ yayınlanabilecek görünür
// metni döndürür ("" = yayınlanacak bir şey yok). Reset yutulur.
func (g *liveThinkGate) Feed(d string) string {
	out, reset := g.s.Feed(d)
	if reset {
		g.trimLead = true
	}
	return g.visible(out)
}

// Flush akış sonunda tutulan etiket-öneki kuyruğunu döndürür (ör. cevabın
// sonundaki "p95 <" → "<"). Kapanmamış bir bloğun içindeysek "" döner.
func (g *liveThinkGate) Flush() string {
	return g.visible(g.s.Flush())
}

func (g *liveThinkGate) visible(out string) string {
	if !g.emitted || g.trimLead {
		out = strings.TrimLeft(out, " \t\r\n")
		if out == "" {
			return ""
		}
		g.trimLead = false
	}
	if out != "" {
		g.emitted = true
	}
	return out
}
