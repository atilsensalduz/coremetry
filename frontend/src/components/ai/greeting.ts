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
import type { CosreGreeting } from '@/lib/types';

/** Hitap satırı. Ad yoksa isimsiz — uydurma ada göre hitap etmeyiz. */
export function greetHello(firstName?: string): string {
  const n = (firstName || '').trim();
  return n ? `Merhaba ${n}.` : 'Merhaba 👋';
}

/**
 * Durum satırı — v0.10.1255 (operatör: P1 yerine "son 1 saatteki exception /
 * hata sayısı"). `undefined` = henüz yüklenmedi → BOŞ (yüklenirken "hata yok"
 * demek yanlış iddia olurdu).
 *
 * v0.10.1278 (operatör 2026-10-17: "445.394 hata · 812 exception grubu" —
 * "occurrences mı bu sayı?"). Eski sayı TÜM hatalı span'lerdi (client/db/
 * internal dahil, her ortam) ve grup sayısı resolved'ları da içeriyordu. Yeni:
 * hatalı GİRİŞ isteği (server+consumer) + AÇIK grup (yeni olanlar ayrıca).
 * Ortam etiketi yalnız sunucu ortamı GERÇEKTEN uyguladıysa basılır.
 */
export function greetErrors(stats: CosreGreeting | undefined): string {
  if (!stats) return '';
  if (stats.failedRequests1h === 0 && stats.openExceptionGroups1h === 0) return 'Son 1 saatte hata yok — sistem sakin görünüyor.';
  const n = (v: number) => v.toLocaleString('tr-TR');
  const envTag = stats.env && stats.envApplied ? ` (${stats.env})` : '';
  const fresh = stats.newExceptionGroups1h > 0 ? ` (${n(stats.newExceptionGroups1h)} yeni)` : '';
  return `Son 1 saatte ${n(stats.failedRequests1h)} hatalı istek${envTag} · ${n(stats.openExceptionGroups1h)} açık exception grubu${fresh}`;
}

/** Durum satırının title'ı — sayıların TANIMI (v0.10.1278). */
export function greetTooltip(stats: CosreGreeting | undefined): string {
  if (!stats) return '';
  const lines = [
    "Hatalı istek: giriş span'leri (server+consumer), status=error — başarısız her istek bir kez sayılır; client/db/internal span'leri sayılmaz.",
    "Exception grupları: çözülmemiş (new/acknowledged/regressed), son 1 saatte görülen; \"yeni\" = ilk kez son 1 saatte görülen. Oluşum sayısı değil.",
  ];
  const env = stats.env;
  if (!env) {
    lines.push('Ortam: tümü.');
  } else {
    lines.push(stats.envApplied
      ? `Ortam ${env}: giriş-span MV'sinde ortam boyutu yok — servis başına hatalı istekler ortam MV'sindeki hata payıyla ${env}'a dağıtıldı (tek ortamda koşan serviste kesin).`
      : `Ortam ${env} hatalı istek sayısına uygulanamadı (ortam MV'si son 1 saati henüz kapsamıyor) — sayı tüm ortamlar.`);
    lines.push(stats.exceptionsEnvApplied
      ? `Exception grupları: ${env} ortamında koşan servislerinkiler (Exceptions sayfasıyla aynı kural).`
      : 'Exception grupları: tüm ortamlar (ortam haritası çözülemedi).');
  }
  return lines.join('\n');
}

/** Durum satırının bağlantısı: Exceptions sayfası, ortam taşınır. */
export function greetHref(env: string): string {
  return env ? `/problems?env=${encodeURIComponent(env)}` : '/problems';
}

/**
 * Türkçe göreli zaman. v0.9.1317'de lib/utils.ts'e TAŞINDI (orada
 * fmtDurShort/fmtAgoNs/tsRel ile aynı evde; v0.8.463 konsolidasyonu).
 * Buradan re-export ediliyor: bu modülün kendi kullanımı (greetStatus)
 * ve greeting.test.ts'in './greeting' importu aynen çalışsın diye.
 */
export { agoTR };
