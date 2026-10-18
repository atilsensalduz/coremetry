import { useQueries } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { HTTP_METRICS_POINTS } from '@/lib/httpMetrics';
import type { SpanMetricSeries } from '@/lib/types';

// useHttpPromql — v0.10.1352. Service → HTTP Metrics sekmesinin VM PromQL
// okuması tek kancada; endpoint sayfasının metrik kipi AYNI kancayı
// kullanır, böylece iki yüzey aynı uçtan (/api/metrics/promql?metricsrc=vm,
// api.httpMetricsPromql), aynı anahtarla (aynı ifade + pencere ⇒ aynı RQ
// önbellek girdisi), aynı staleTime (30 sn = sunucu serveCached TTL) ve
// retry'sız okur. Yoklama YOK (refetchInterval verilmez); `enabled` false
// iken hiç istek atılmaz (endpoint sayfası yalnız metrik kipinde açar).
// Instant (anlık, `to` anında) sorgular sunucuda yalnız VM'de var.

export interface HttpPromqlItem { id: string; expr: string; instant: boolean }

export interface HttpPromqlResult {
  data: Record<string, SpanMetricSeries[] | null | undefined>;
  pending: (id: string) => boolean;
  error: (id: string) => Error | null;
}

export function useHttpPromql(
  items: readonly HttpPromqlItem[], win: { from: number; to: number }, enabled = true,
): HttpPromqlResult {
  const results = useQueries({
    queries: items.map(q => ({
      queryKey: ['http-metrics', q.expr, win.from, win.to, q.instant],
      queryFn: () => api.httpMetricsPromql({
        query: q.expr, from: win.from, to: win.to, maxDataPoints: HTTP_METRICS_POINTS,
        ...(q.instant ? { instant: '1' as const } : {}),
      }),
      staleTime: 30_000,
      retry: false,
      enabled,
    })),
  });
  const data: Record<string, SpanMetricSeries[] | null | undefined> = {};
  const idx = new Map<string, number>();
  items.forEach((q, i) => { data[q.id] = results[i]?.data; idx.set(q.id, i); });
  return {
    data,
    pending: id => { const i = idx.get(id); return i === undefined ? false : !!results[i]?.isPending; },
    error: id => { const i = idx.get(id); return i === undefined ? null : ((results[i]?.error as Error | null) ?? null); },
  };
}
