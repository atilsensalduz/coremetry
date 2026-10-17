// logLineSubject — v0.10.1291 (CoSRE follow-up D): tek log satırının ✨
// öznesi. Sunucu ikizi internal/mcptools/log_line_subject.go (sohbet log
// tablolarının `rowSubjects`'i oradan gelir); Logs sayfasının açılan satırı
// AYNI kodeği burada üretir. Özet gövdenin İLK 120 kod noktası üzerinden
// FNV-1a 32 (UTF-8 bayt) — sohbet satırı 300 rune'da kırpılır ve FenceSafe
// ``` → ˋˋˋ yapar; ön ek + ˋ→` geri dönüşü iki tarafın özetini eşitler.
import type { AISubject } from './aiSubject';
import type { LogRow } from './types';
import { podOfLog } from './logPod';

export const LOG_BODY_HASH_RUNES = 120;

/** logBodyHash — SAF: gövde önekinin FNV-1a 32 özeti (8 küçük hex). */
export function logBodyHash(body: string): string {
  const prefix = Array.from(body).slice(0, LOG_BODY_HASH_RUNES).map(c => (c === 'ˋ' ? '`' : c)).join('');
  let h = 0x811c9dc5;
  for (const b of new TextEncoder().encode(prefix)) {
    h ^= b;
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h.toString(16).padStart(8, '0');
}

const TRACE_RE = /^[0-9a-fA-F]{32}$/;

/** logLineSubjectOf — SAF: LogRow → `log` öznesi; servis ya da zaman yoksa null. */
export function logLineSubjectOf(l: Pick<LogRow, 'timestamp' | 'serviceName' | 'traceId' | 'body' | 'attributes' | 'resourceAttributes'>): AISubject | null {
  const id = (l.serviceName ?? '').trim();
  if (!id || !(l.timestamp > 0)) return null;
  // Sayı → ondalık metin: Number#toString 1e21 altında üssüz basar (hassasiyet
  // JSON'da zaten ~256 ns; sunucu ±1 sn okur, özetle eşler).
  const tsNs = String(Math.trunc(l.timestamp));
  const traceId = TRACE_RE.test(l.traceId ?? '') ? l.traceId.toLowerCase() : '';
  return { kind: 'log', id, tsNs, pod: podOfLog(l), traceId, hash: logBodyHash(l.body ?? '') };
}
