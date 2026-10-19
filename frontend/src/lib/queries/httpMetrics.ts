import { useQueries, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { HTTP_METRICS_POINTS } from '@/lib/httpMetrics';
import { httpEnvScopeView, type HttpEnvScopeView } from '@/lib/httpEnvScopeNote';
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

// useHttpEnvScope — v0.10.1377 (metrik kipi env/cluster süzgeci). env/cluster
// seçiliyken ortam adını VM HTTP histogramının ortam etiketine + canlı
// değer(ler)ine çözer (GET /api/metrics/http-env-scope, sunucu 60 sn
// önbellek). Seçili değilse HİÇ istek yok (state 'off', sorgular bayt bayt
// eski). `to` 10 dk'ya yuvarlanır — sunucunun değer penceresi de öyle; anahtar
// TÜM girdiler (env, cluster, dilim). Yoklama yok; staleTime = sunucu TTL'i;
// retry yok (hata → band söyler, sorgular süzgeçsiz atılır).
export const HTTP_ENV_SCOPE_SNAP_NS = 600e9;

export function useHttpEnvScope(env: string, cluster: string, toNs: number, enabled = true): HttpEnvScopeView {
  const e = env.trim();
  const c = cluster.trim();
  const on = enabled && !!(e || c);
  const bucket = Math.floor(toNs / HTTP_ENV_SCOPE_SNAP_NS);
  const q = useQuery({
    queryKey: ['http-env-scope', e, c, bucket],
    queryFn: ({ signal }) => api.httpEnvScope({ env: e || undefined, cluster: c || undefined, to: bucket * HTTP_ENV_SCOPE_SNAP_NS }, signal),
    enabled: on,
    staleTime: 60_000,
    retry: false,
  });
  return httpEnvScopeView(e, c, { data: q.data, error: (q.error as Error | null) ?? null }, on);
}
