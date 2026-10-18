// operationCardView — operasyon ön-getirme kartının saf yardımcıları.
import type { ChatOperationException, ChatOperationPayload, ChatOperationRED } from '@/lib/types';
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
  return exceptionGroupRows(op.exceptions?.groups ?? [], nowMs);
}

/** v0.10.1337 — grup listesinden satırlar (operasyon kartı + servis raporu kartı ortak). SAF. */
export function exceptionGroupRows(groups: ChatOperationException[], nowMs = Date.now()): OpExceptionRow[] {
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

// ── v0.10.1343 — endpoint hedefi (chat_endpoint_prefetch.go) ────────────────

/** SAF: kart başlığı "POST /x · svc"; endpoint değilse null. */
export function opEndpointLabel(op: ChatOperationPayload): string | null {
  if (!op.route && !op.method) return null;
  let path = op.route || op.operation;
  const sp = /^[A-Z]+ (\/.*)$/.exec(op.operation);
  if (!op.route && sp) path = sp[1];
  const head = [op.method, path].filter(Boolean).join(' ');
  const svc = (op.services ?? []).join(', ');
  return svc ? `${head} · ${svc}` : head;
}

/** SAF: "Endpoint sayfası" linki — güvenli uygulama yolu değilse null. */
export function opEndpointHref(op: ChatOperationPayload): string | null {
  return safeAppPath(op.endpointHref);
}

/** SAF: endpoint exception satırları (OpExceptionRow paylaşılır); güvensiz href düşer. */
export function opEndpointExceptionRows(op: ChatOperationPayload, nowMs = Date.now()): OpExceptionRow[] {
  return (op.endpointExceptions ?? []).filter(e => e && typeof e.type === 'string').slice(0, 5).map((e, i) => ({
    key: `${e.type}:${i}`,
    code: e.type || '—',
    count: `toplam ${fmtNum(e.count)}`,
    burst: false,
    lastSeen: e.lastSeenNs > 0 ? agoTR(e.lastSeenNs, nowMs) : '',
    href: safeAppPath(e.href),
    explainHref: null,
    title: e.message ?? '',
  }));
}

/** SAF: endpoint exception durum satırı — okunamadı / yok; satır varken null. */
export function opEndpointExceptionsNote(op: ChatOperationPayload): string | null {
  if (op.endpointExceptionsError) return 'Endpoint exception\'ları okunamadı';
  if (!op.endpointExceptionsChecked) return null;
  return (op.endpointExceptions ?? []).length ? null : 'Bu pencerede endpoint span\'lerinde exception yok';
}

/** SAF: ≥3× kuralıyla geri planda kalan servisler; yoksa null. */
export function opAlternativesLine(op: ChatOperationPayload): string | null {
  const alts = (op.alternatives ?? []).filter(a => a && typeof a.service === 'string');
  if (!alts.length) return null;
  return `Ayrıca şu servislerde de var: ${alts.map(a => `${a.service} (${fmtNum(a.calls)} çağrı)`).join(', ')}`;
}
