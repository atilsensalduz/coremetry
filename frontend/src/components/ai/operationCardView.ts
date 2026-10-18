// operationCardView — operasyon ön-getirme kartının saf yardımcıları.
import type { ChatOperationPayload, ChatOperationRED } from '@/lib/types';
import { safeAppPath } from '@/lib/safeAppPath';
import { agoTR, fmtNum } from '@/lib/utils';

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

// ── v0.10.1331 — "Teknik hatalar" (operasyonun Exceptions grupları) ─────────

export interface OpExceptionRow {
  key: string;
  /** hata kodu (Oracle) ya da exception tipi */
  code: string;
  priority?: string;
  /** "son 1 sa 900 · önce 200" (Oracle, saatlik bilinen) ya da "toplam 12.3K" */
  count: string;
  burst: boolean;
  lastSeen: string;
  /** güvenli uygulama yolu değilse null (bağlantı çizilmez) */
  href: string | null;
  explainHref: string | null;
  title: string;
}

/** SAF: kart satırları — en yeni önce (sunucu sırası), güvensiz href düşer. */
export function opExceptionRows(op: ChatOperationPayload, nowMs = Date.now()): OpExceptionRow[] {
  const groups = op.exceptions?.groups ?? [];
  return groups.filter(g => g && typeof g.fingerprint === 'string').map(g => {
    const code = (g.oracle ? g.code : g.type) || g.type || '—';
    const hourly = g.oracle && g.hourKnown;
    return {
      key: g.fingerprint,
      code,
      priority: g.priority || undefined,
      count: hourly ? `son 1 sa ${fmtNum(g.lastHour)} · önce ${fmtNum(g.prevHour)}` : `toplam ${fmtNum(g.occurrences)}`,
      burst: hourly && g.lastHour > 0 && g.lastHour >= 3 * g.prevHour,
      lastSeen: g.lastSeen > 0 ? agoTR(g.lastSeen, nowMs) : '',
      href: safeAppPath(g.href),
      explainHref: safeAppPath(g.explainHref),
      title: [g.operation, g.sample ?? g.message].filter(Boolean).join(' — '),
    };
  });
}

/** SAF: bölümün durum satırı — okunamadı / grup yok / "+N grup daha"; satır varken ek yoksa null. */
export function opExceptionsNote(op: ChatOperationPayload): string | null {
  const ex = op.exceptions;
  if (!ex) return null;
  if (ex.error) return 'Exceptions okunamadı';
  if (!(ex.groups ?? []).length) return 'Exceptions\'ta bu operasyon için grup yok';
  if (ex.more && ex.more > 0) return `+${ex.moreAtMin ? 'en az ' : ''}${ex.more} grup daha`;
  return null;
}
