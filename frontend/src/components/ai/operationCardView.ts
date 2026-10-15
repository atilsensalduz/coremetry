// operationCardView — operasyon ön-getirme kartının saf yardımcıları.
import type { ChatOperationRED } from '@/lib/types';

export function errRate(r: ChatOperationRED): number {
  return r.calls > 0 ? r.errors / r.calls : 0;
}

/** Önceki eşit pencereye fark: "+%40" / "−%12" / "yeni" / "". */
export function deltaLabel(cur: number, prev: number): string {
  if (prev === 0) return cur === 0 ? '' : 'yeni';
  const d = Math.round(((cur - prev) / prev) * 100);
  if (d === 0) return '±%0';
  return d > 0 ? `+%${d}` : `−%${-d}`;
}

/** Kötüleşme mi (rengi yalnız sapan değer alır): hata oranı ≥ +1 puan, p99 ≥ +%25. */
export function worse(kind: 'err' | 'p99', cur: number, prev: number): boolean {
  if (kind === 'err') return cur - prev >= 0.01;
  return prev > 0 ? (cur - prev) / prev >= 0.25 : false;
}

export function fmtMs(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)} s` : `${Math.round(ms)} ms`;
}
