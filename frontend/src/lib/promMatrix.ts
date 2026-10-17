// promMatrix.ts — v0.10.1306: Prometheus HTTP API cevabı → grafik serisi (saf).
//
// /metrics PromQL sekmesinin Thanos yolu (src=thanos) editor+ konsolunu
// (/api/promql/query_range) çağırır; cevap Prometheus'un kendi zarfıdır
// (data.result = matrix: [{metric:{…}, values:[[unixSn, "değer"], …]}]).
// Editörün grafiği TSSeries ister (zaman unix NANO, değer sayı | null) —
// dönüşüm burada, tek yerde, testli (promMatrix.test.ts).

import type { PromMatrixSample } from './types';

export interface PromSeries {
  label: string;
  points: { time: number; value: number | null }[];
}

/**
 * Seri etiketi: `__name__` dışındaki etiketler `k="v"` sıralı, `{…}` içinde;
 * ad varsa önüne (`up{job="x"}`); hiç etiket yoksa ad, o da yoksa `{}`
 * (Prometheus / Grafana gösterimi).
 */
export function promSeriesLabel(metric: Record<string, string> | null | undefined): string {
  const m = metric ?? {};
  const name = m.__name__ ?? '';
  const pairs = Object.keys(m).filter(k => k !== '__name__').sort().map(k => `${k}="${m[k]}"`);
  if (!pairs.length) return name || '{}';
  return `${name}{${pairs.join(', ')}}`;
}

/**
 * promMatrixToSeries — matrix → seri listesi. values [ts, "v"] →
 * {time: ts·1e9, value}; NaN / ±Inf / sayı olmayan değer null (boşluk —
 * sahte sıfır çizilmez). Bozuk kalemler atlanır; matrix değilse [].
 */
export function promMatrixToSeries(result: readonly PromMatrixSample[] | null | undefined): PromSeries[] {
  if (!Array.isArray(result)) return [];
  const out: PromSeries[] = [];
  for (const s of result) {
    if (!s || !Array.isArray(s.values)) continue;
    const points: PromSeries['points'] = [];
    for (const v of s.values) {
      if (!Array.isArray(v) || typeof v[0] !== 'number' || !Number.isFinite(v[0])) continue;
      const n = typeof v[1] === 'string' ? Number(v[1]) : NaN;
      points.push({ time: Math.round(v[0] * 1000) * 1e6, value: Number.isFinite(n) ? n : null });
    }
    out.push({ label: promSeriesLabel(s.metric), points });
  }
  return out;
}

/**
 * promConsoleError — lib/api.ts request() hatası ("HTTP <kod>: <gövde>") →
 * HTTP kodu + okunur mesaj. Konsolun hata zarfı {status:"error", errorType,
 * error, meta} ise `error` alanı; değilse ham metin.
 */
export function promConsoleError(e: unknown): { status: number | null; message: string } {
  const raw = e instanceof Error ? e.message : String(e ?? '');
  const m = /^HTTP (\d{3}): ([\s\S]*)$/.exec(raw);
  if (!m) return { status: null, message: raw };
  const status = Number(m[1]);
  let message = m[2].trim();
  try {
    const body = JSON.parse(message) as { error?: unknown; errorType?: unknown };
    if (typeof body.error === 'string' && body.error) {
      message = typeof body.errorType === 'string' && body.errorType ? `${body.errorType}: ${body.error}` : body.error;
    }
  } catch { /* düz metin gövde (ör. RequireAnyRole 403) */ }
  return { status, message };
}
