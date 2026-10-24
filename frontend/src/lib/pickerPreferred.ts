// pickerPreferred — v0.10.1515: sunucu-aramalı seçicinin listesinin başına
// çağıranın elindeki küçük aday listesini koyar (ServicePicker `preferred`;
// ilk kullanıcı CoSRE takım cevabının seçici çipi — components/ai/followupPick.ts).
// Yeni istek yok, tam katalog yok: adaylar çağıranın ZATEN elinde.

/**
 * SAF — seçici listesi: sorguya uyan adaylar ÖNCE (sunucunun sırası — hata
 * oranı azalan), ardından sunucu araması (tüm servisler), tekrar yok. Boş
 * sorguda bütün adaylar; joker (`*`, `?`) sorguda adaylar eklenmez (eşleşmeyi
 * sunucu çözer). Eşleşme büyük/küçük harf duyarsız alt-dize.
 */
export function mergePreferred(preferred: readonly string[] | undefined, query: string, server: readonly string[]): string[] {
  const q = query.trim().toLowerCase();
  const head = !preferred?.length || /[*?]/.test(q) ? [] : preferred.filter(n => !q || n.toLowerCase().includes(q));
  const seen = new Set(head);
  return [...head, ...server.filter(n => !seen.has(n))];
}
