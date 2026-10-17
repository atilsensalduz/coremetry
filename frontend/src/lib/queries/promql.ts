import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';

// usePromqlConsoleRange — v0.10.1306: /metrics PromQL sekmesinin Thanos yolu
// (src=thanos). Uç editor+ konsolu /api/promql/query_range: sunucuda
// önbellek YOK (her koşu audit + kullanıcı başına limiter, bilinçli —
// promql_console_routes.go), bu yüzden istemci disiplini sıkı:
//   • enabled yalnız sekme açık + kaynak thanos + cluster + sorgu varken
//     (çağıran hesaplar) — fetch-on-open;
//   • staleTime 30 sn (VM yolu /api/metrics/promql serveCached TTL'iyle aynı),
//     odakta yeniden çekme yok, yoklama yok;
//   • retry YOK: sözdizimi / 403 / limit hatası operatör hatasıdır, geçici değil;
//   • signal iletilir (cancellation.test.ts HEAVY): yeni yazım ya da pencere
//     değişince uçuştaki Thanos sorgusu kesilir.
// Anahtar TÜM girdileri taşır (cluster, sorgu, pencere, adım).

export interface PromqlConsoleRangeArgs {
  query: string;
  cluster: string;
  /** unix saniye */
  start: number;
  /** unix saniye */
  end: number;
  /** saniye; yoksa sunucu pencereden hesaplar (promqlEffectiveStep) */
  step?: number;
}

export function usePromqlConsoleRange(a: PromqlConsoleRangeArgs, enabled: boolean) {
  return useQuery({
    queryKey: ['promql-console-range', a.cluster, a.query, a.start, a.end, a.step ?? 0] as const,
    queryFn: ({ signal }) => api.promqlQueryRange(
      { query: a.query, start: a.start, end: a.end, step: a.step, cluster: a.cluster }, signal),
    enabled,
    staleTime: 30_000,
    retry: false,
    refetchOnWindowFocus: false,
  });
}
