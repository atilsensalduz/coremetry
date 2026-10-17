package provider

import (
	"errors"
	"strings"
)

// salvage.go — "model boş içerik döndürdü" kurtarma zinciri.
//
// FAZ 1.2'den beri TEK YAZILIŞ. Faz 1.1'de burası copilot.go'daki
// ikizin bilinçli geçici kopyasıydı; denetim (2026-08-16) zincirin ÜÇ
// kopyasını saymıştı (copilot.go buffered, stream.go SSE, bu paket).
// Faz 1.2'de copilot.go'daki üreticiler silindi ve stream.go/chat.go
// buradaki fonksiyonları çağırıyor.
//
// Kopya sayısı bir YAPISAL kapıyla korunuyor:
// salvage_singleton_test.go. Davranış testi ikinci bir kopyayı
// yakalayamaz — iki kopya bugün aynı şeyi yapar, sonra biri düzeltilir
// öteki unutulur (1024 bütçesi tam böyle ~1000 sürüm yaşadı).
//
// Zincirin geçmişi (v0.8.138 → 155 → 384): yerel reasoning modelleri
// (Qwen3, deepseek-r1, …) cevabı content'e koymayabilir —
// reasoning_content'e, reasoning'e ya da yalnız <think>…</think>
// içine koyar; bütçe düşükse hiç cevap üretmeden "length" ile biter.
// Bunların hepsi kullanıcıya BOŞ panel olarak yansıyordu.

// StripThinking, bazı yerel reasoning modellerinin content'e gömdüğü
// düşünce bloklarını atar ve görünür cevabı (trim'li) döndürür.
//
// v0.10.1285 — kural artık AKIŞ soyucusuyla (think_stream.go
// thinkStripper) TEK yazılış: tam metin tek parça olarak aynı durum
// makinesinden geçer. Önceki yazılış yalnız "son </think> sonrası"ydı;
// Gemma kanal bloğunu (`<|channel>thought … <channel|>`) ve kapanmamış
// bloğu tanımıyordu — akış gizlerken tamponlu yol (ve akışın sonundaki
// `answer` uzlaştırması) düşünceyi nihai cevaba geri koyuyordu.
// Anlam (thinkTagPairs):
//   - <think>…</think> ve <|channel>thought…<channel|> içi düşer;
//   - </think> kapanışı (ya da açılışsız </think>) ÖNCESİNDEKİ her şeyi
//     de atar (eski "son </think> sonrası" kuralı);
//   - kapanmamış blok gizli kalır (cevap bütçe sonunda kesildi).
func StripThinking(s string) string {
	return strings.TrimSpace(thinkVisible(s))
}

// ThinkingContent, İLK düşünce bloğunun (<think> ya da Gemma
// <|channel>thought) İÇİNİ döndürür — StripThinking hiçbir şey
// bırakmadığında son çare. Bazı modeller yalnız düşünce bloğu üretir ve
// o düşünce genelde açıklamanın kendisidir; kurtarmak, isteği başarısız
// saymaktan iyidir.
func ThinkingContent(s string) string {
	at, pair := -1, thinkTagPair{}
	for _, p := range thinkTagPairs {
		if i := strings.Index(s, p.open); i >= 0 && (at < 0 || i < at) {
			at, pair = i, p
		}
	}
	if at < 0 {
		return ""
	}
	rest := s[at+len(pair.open):]
	if c := strings.Index(rest, pair.close); c != -1 {
		rest = rest[:c]
	}
	return strings.TrimSpace(rest)
}

// SalvageAnswer — cevabı modelin koyduğu yerden çeker, öncelik
// sırasıyla:
//
//  1. son </think> sonrasındaki content (normal hâl),
//  2. ayrılmış reasoning alanı (reasoning_content → reasoning),
//  3. son çare: <think> bloğunun İÇİ.
//
// Hiçbiri yoksa boş döner — çağıran EmptyAnswerError ile teşhis eder.
func SalvageAnswer(content, reasoningContent, reasoning string) string {
	if out := StripThinking(content); out != "" {
		return out
	}
	// ⚠ AYRILMIŞ DÜŞÜNCE KANALLARI DA İŞARETLENİYOR (v0.10.66).
	//
	// v0.10.37 yalnız <think> dalını işaretledi ve testi bu tutarsızlığı
	// ÇİVİLEDİ ("yalnız reasoning_content → işaretlenmez"). O karar
	// yanlıştı ve gerekçesi kendi içinde çelişiyordu: 10.37'nin derdi
	// "modelin SPEKÜLASYON fazı nihai cevap kılığında yayına giriyor"du.
	//
	// content BOŞken reasoning_content'ten dönen metin tam olarak odur —
	// aynı madde, yalnız sağlayıcı onu ayrı bir alana koymuş. Hangi ALAN
	// taşıdığı operatörü ilgilendirmiyor; onu ilgilendiren tek şey elindeki
	// metnin nihai cevap mı çalışma notu mu olduğu.
	//
	// Yani ayrım TAŞIYICIDA değil, MADDEDE: content doluysa (1. basamak)
	// model cevabını yazmıştır ve işaret YOK; buraya düştüysek cevap
	// yazılmamış demektir.
	if out := StripThinking(reasoningContent); out != "" {
		return MarkSalvagedThinking(out)
	}
	if out := StripThinking(reasoning); out != "" {
		return MarkSalvagedThinking(out)
	}
	// ⚠ SON ÇARE İŞARETLENİYOR (v0.10.37). Kurtarmanın kendisi makul —
	// bazı modeller yalnız düşünce bloğu üretiyor ve o düşünce genelde
	// açıklamanın kendisi; başarısız saymak daha kötü olurdu.
	//
	// Kusur kurtarma DEĞİL, sonucun gerçek cevaptan AYIRT EDİLEMEZ
	// olmasıydı: metin `answer` olarak çıkıyor, ai_calls'a normal cevap
	// yazılıyor ve bir sonraki turda geçmişe normal cevap gibi biniyordu.
	// Yani modelin SPEKÜLASYON fazı, nihai cevap kılığında yayına
	// giriyordu ve operatörün bunu anlamasının hiçbir yolu yoktu.
	//
	// İşaret sağlayıcı SINIRINDA konuyor: tek yer, ve sohbet/explain/
	// ai_calls/geçmiş hepsi birden kazanıyor.
	return MarkSalvagedThinking(ThinkingContent(content))
}

// SalvagedThinkingPrefix — kurtarılmış düşünce bloğunun önüne konan
// uyarı. Dışa açık, çünkü tüketiciler (arayüz, denetim) bu metni
// TANIYABİLMELİ — dizgeyi kendi kopyalarında tekrar yazmasınlar.
const SalvagedThinkingPrefix = "⚠ Bu metin modelin DÜŞÜNCE bloğundan kurtarıldı — " +
	"nihai cevabı değil, çalışma notu. Çıkarımlar ve sayılar yarım olabilir.\n\n"

// MarkSalvagedThinking — düşünce bloğunu işaretler.
//
// Boş girdi boş kalır: işaretin tek başına gitmesi, olmayan bir cevabı
// varmış gibi göstermek olurdu.
func MarkSalvagedThinking(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	// Zaten işaretliyse tekrarlama — zincir iki kez çağrılırsa uyarı
	// üst üste binerdi.
	if strings.HasPrefix(s, SalvagedThinkingPrefix) {
		return s
	}
	return SalvagedThinkingPrefix + s
}

// IsSalvagedThinking — bu cevap düşünce bloğundan mı kurtarıldı.
//
// Tüketiciler (arayüz rozeti, denetim sorgusu) buna bakıyor; dizge
// karşılaştırmasını kendi içlerinde yazmak, işaret değişince sessizce
// kopmak demekti.
func IsSalvagedThinking(s string) bool {
	return strings.HasPrefix(s, SalvagedThinkingPrefix)
}

// EmptyAnswerError — kurtarılacak hiçbir şey kalmadığında operatöre
// EYLEME DÖNÜK hata. finish_reason="length" ayrı bir teşhistir:
// bütçe düşünme fazında tükenmiştir, çözüm max_tokens'ı yükseltmek
// ya da düşünmeyi kapatmaktır.
//
// Metinler copilot.go'daki ikizleriyle BİREBİR aynı tutuluyor —
// canary sırasında aynı yüzey iki yoldan da aynı cümleyi
// döndürmeli (parity testi bunu pinler).
func EmptyAnswerError(finishReason string) error {
	if finishReason == "length" {
		return errors.New("model returned no answer — token budget exhausted by reasoning; raise max_tokens or disable thinking (e.g. Qwen3 /no_think)")
	}
	return errors.New("openai-compat: model returned empty content — no answer in content/reasoning. Check the model name + endpoint; a reasoning model may need /no_think. See the [copilot] pod log for the raw response")
}
