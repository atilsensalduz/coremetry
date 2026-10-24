// aggResult.ts — v0.10.1505 (operator-reported, prod): /traces Aggregated
// sekmesi gRPC callee servisinde (operasyon + kind + http.method çipleri)
// her denemede "Toplu sorgu hata verdi ya da zaman aşımına uğradı" dedi.
// Sorgu HATA VERMİYORDU: sunucu sıfır grup buldu, Go nil dilimi JSON `null`
// oldu ve sayfa `null`u hata dalına soktu (v0.9.858 "null = hata"). Saf ve
// tablo-testli: boş cevap boş tablo, gerçek hata ise sınıfıyla söylenir.
import type { AggregateRow } from '@/lib/types';

/** Sunucu cevabı → satırlar. `null` (eski sunucu / boş gövde) = sıfır grup, hata DEĞİL. */
export function aggRowsOf(a: AggregateRow[] | null | undefined): AggregateRow[] {
  return a ?? [];
}

export type AggErrorClass = 'timeout' | 'memory' | 'invalid' | 'generic';

/** Hata metninin sınıfı — istemci zaman aşımı, CH 159/241, HTTP 400. */
export function aggErrorClass(msg: string | null | undefined): AggErrorClass {
  const m = (msg ?? '').toLowerCase();
  if (/timed out|timeout exceeded|timeout_exceeded|code: 159|http 504/.test(m)) return 'timeout';
  if (/memory limit|memory_limit_exceeded|code: 241/.test(m)) return 'memory';
  if (/^http 400\b/.test(m)) return 'invalid';
  return 'generic';
}

const HEADLINE: Record<AggErrorClass, string> = {
  timeout: 'Toplu sorgu zaman aşımına uğradı — daha dar bir zaman aralığı ya da daha az grup deneyip yeniden dene.',
  memory: 'Toplu sorgu ClickHouse bellek sınırına takıldı — daha dar bir zaman aralığı ya da daha az grup deneyip yeniden dene.',
  invalid: 'Toplu sorgu geçersiz — süzgeç / HAVING ifadesini kontrol et.',
  generic: 'Toplu sorgu hata verdi — ayrıntı aşağıda; yeniden dene.',
};

/** Hata satırının başlığı (ayrıntı = sunucu metni, ayrıca gösterilir). */
export function aggErrorHeadline(msg: string | null | undefined): string {
  return HEADLINE[aggErrorClass(msg)];
}
