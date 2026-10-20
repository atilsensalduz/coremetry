// traceIdJump.ts — v0.10.1423 (operatör, prod Traces ekranı: "Sağ üst
// köşedeki trace ID / function ID kısmına gerek yok artık. Search'ten zaten
// aranabiliyor."). Sağ üstteki "Trace ID veya function_id…" kutusu kalktı;
// operasyon kutusu ("Operation, or an id (function_id / trace ID)…") iki işi
// de yapıyor. SAF; vitest'li (traceIdJump.test.ts).
//
//   · 32-hex trace id → arama DEĞİL atlayış: /trace?id=… (traceIdJump).
//   · başka bir kimlik (function_id gibi) → `?search=` terimi; sunucunun
//     kimlik-önce yolu (v0.10.342) eşitlikle arar.
//
// Eski kutunun yazdığı `?traceId=` derin bağlantıları (paylaşılmış linkler,
// yer imleri) bir kez okunur ve aynı davranışa yönlenir; param düşer
// (legacyTraceIdRedirect, replace ile).

import { traceHref } from '@/lib/traceHref';

const TRACE_ID_RE = /^[0-9a-f]{32}$/i;

/** traceIdJump — terim tam bir 32-hex trace id ise küçük harfli id; değilse null. */
export function traceIdJump(term: string | null | undefined): string | null {
  const t = (term ?? '').trim();
  return TRACE_ID_RE.test(t) ? t.toLowerCase() : null;
}

/**
 * legacyTraceIdRedirect — `/traces` sorgu dizesinde eski `?traceId=` varsa
 * gidilecek yer; yoksa null.
 *
 *   · 32-hex → `/trace?id=…` (sayfanın `range`'i taşınır, traceHref sözleşmesi).
 *   · başka bir değer → `/traces?…&search=<değer>` (ham hâli: kimlik eşitliği
 *     harf duyarlı). Arama kutusu zaten doluysa o kazanır (eski
 *     effectiveTraceSearch kuralı) ve kimlik yalnız düşer.
 *   · boş `?traceId=` → param düşer.
 *
 * Diğer her param sırasıyla korunur.
 */
export function legacyTraceIdRedirect(search: string): string | null {
  const p = new URLSearchParams(search);
  if (!p.has('traceId')) return null;
  const raw = (p.get('traceId') ?? '').trim();
  const tid = traceIdJump(raw);
  if (tid) return traceHref(tid, { pageRange: p.get('range') });
  p.delete('traceId');
  if (raw && !(p.get('search') ?? '').trim()) p.set('search', raw);
  const qs = p.toString();
  return qs ? `/traces?${qs}` : '/traces';
}
