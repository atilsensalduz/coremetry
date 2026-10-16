// greeting.ts — v0.9.528. CoSRE sohbetinin açılış karşılaması.
//
// Operatör isteği: "login olduktan sonra kim olduğunu anlayıp ismiyle
// hitap etsin, 'sana nasıl yardımcı olabilirim' desin."
//
// İKİ TASARIM KARARI:
//
//  1. Karşılama LLM ÇAĞRISI YAPMAZ. Deterministik, anında ve her
//     açılışta aynı. CoSRE ilkesiyle de tutarlı: kod karar verir,
//     model anlatır. Bir karşılama cümlesi için 26B modeli beklemek
//     hem yavaş hem de "3 açık P1" gibi bir sayıyı uydurma riski.
//
//  2. Cümle CANLI. "Merhaba Fatih" tek başına kibar ama boş; asıl
//     değer operatörün daha soruyu yazmadan durumu görmesi. Sayı
//     zaten sohbet penceresinin poll ettiği veriden gelir.

import { agoTR } from '@/lib/utils';

/** Hitap satırı. Ad yoksa isimsiz — uydurma ada göre hitap etmeyiz. */
export function greetHello(firstName?: string): string {
  const n = (firstName || '').trim();
  return n ? `Merhaba ${n}.` : 'Merhaba 👋';
}

/**
 * Durum satırı — v0.10.1255 (operatör: P1 yerine "son 1 saatteki exception /
 * hata sayısı"). `undefined` = henüz yüklenmedi → BOŞ (yüklenirken "hata yok"
 * demek yanlış iddia olurdu).
 */
export function greetErrors(stats: { errors1h: number; exceptionGroups1h: number } | undefined): string {
  if (!stats) return '';
  if (stats.errors1h === 0 && stats.exceptionGroups1h === 0) return 'Son 1 saatte hata yok — sistem sakin görünüyor.';
  const n = (v: number) => v.toLocaleString('tr-TR');
  return `Son 1 saatte ${n(stats.errors1h)} hata · ${n(stats.exceptionGroups1h)} exception grubu`;
}


/**
 * Türkçe göreli zaman. v0.9.1317'de lib/utils.ts'e TAŞINDI (orada
 * fmtDurShort/fmtAgoNs/tsRel ile aynı evde; v0.8.463 konsolidasyonu).
 * Buradan re-export ediliyor: bu modülün kendi kullanımı (greetStatus)
 * ve greeting.test.ts'in './greeting' importu aynen çalışsın diye.
 */
export { agoTR };
