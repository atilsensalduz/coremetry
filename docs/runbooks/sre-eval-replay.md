# AI SRE değerlendirmesi — taban koşusu, dondurulmuş küme, karşılaştırma

v0.10.1450 (program 2a). Döngü açık koşular (L, L2) sürüm 2c ile gelir; bu sayfa o zaman genişler.

## Kavramlar
- **Koşu** = `saved_views(page='sre-eval')` satırı (180 gün). Vaka listesi (`cases[]`) koşunun içindedir.
- **Dondurulmuş küme** = taban koşusunun `cases[]`'i; kimliği taban `runId`. Yeni varlık yok.
- **Yeniden koşu** `{rerunOf:"<B>"}`: aynı vakalar, aynı çıpalar (problem `StartedAt` → `AsOf = +15 dk`), yeni `runId`.
  Vakalar yeniden okunmaz; sonradan düzeltilen bir vaka tabanı değiştirmez. Tekrar LLM çağırmaz.
- **Manşet = bağımsız vakalar**: onaylayan CoSRE önerisini değiştirmeden kabul ettiyse (`acceptedSuggestion`) etiket
  ajandan bağımsız değildir; tüm vakalar ikinci satır (kontrol).

## 1. Pencereyi seç
Vakaların `StartedAt`'i, SON planlanan yeniden koşu anında (≈ 1 hafta sonra) hâlâ span VE log saklamasının içinde olmalı.
Pratik: `to = bugün − 1 gün`, `from = bugün − (saklama günü − 7)`. `limit` 10–20.

## 2. Taban koşusu B
UI: `/ai` › "AI SRE doğruluğu — geçmiş problemler" › tarihleri ve "En çok vaka" gir › **Değerlendirme başlat** (admin).
API:
```
curl -sS -X POST -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  https://coremetry.example.test/api/sre/eval/runs \
  -d '{"from":<unix ms>,"to":<unix ms>,"limit":20}'
```
Cevaptaki `runId` = **B**. `state=done` olana kadar bekle (UI 15 s'de bir tazeler). Kontrol: `score.scored`, vaka başına
kapsam; vakaların çoğu "veri yok" ise pencereyi `to`'ya doğru daralt ve yeniden başlat.

## 3. Yeniden koşu (kararlılık / sürüm karşılaştırması)
UI: koşular tablosunda B'yi seç › **Aynı kümeyi yeniden koş**. API: `POST /api/sre/eval/runs {"rerunOf":"B"}` (pencere
verilmez; `loop:true` bu sürümde 400). Seçili koşu yeniden koşuysa altında "Taban → Bu koşu" bloğu çıkar.

## 4. Karşılaştırmayı oku
`GET /api/sre/eval/compare?base=B&cand=<id>` (viewer). 409 = vaka kümesi farklı.
- Kontrol satırında Δ ≠ 0 → veri kayması (`driftCases`, "kayma" rozeti) ya da deterministik yolda gerileme.
- Kalibrasyon: depolanan skor + kapsamdan deterministik sonuç (doğrulandı / olası / belirsiz / sonuçsuz / veri yok).
- Maliyet: tekrar LLM'siz (0 çağrı) + koşu süresi. Uyum: top-1 türü / sonuç aynı mı.

## 5. Tekrarlanabilirlik
Koşu satırları `build` (sunucu sürümü), `rerunOf`, `by`, `createdAt` taşır; sonraki her ikili `{"rerunOf":"B"}` ile aynı
kümeyi yeniden koşabilir (saklama içinde kaldıkça).
