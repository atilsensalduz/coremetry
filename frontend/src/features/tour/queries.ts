// queries.ts — v0.10.1334 (rehberli tur, R1). Yalnız bu feature'ın
// okumaları (features/README: başka feature tüketirse lib/queries'e taşınır).
//
// Karşılama karoları MEVCUT, sınırlı uçlardan: yeni uç YOK.
//   • api.servicesPage — son 5 dk (dakikaya oturtulmuş pencere: sunucu
//     cache anahtarı sınırlı kalır), limit 50, sort spanCount desc,
//     withTotal=1 → toplam servis, Σ spanCount (hasMore ise "ilk 50"
//     etiketi), en yoğun servis. service_summary_5m MV yolu.
//   • api.environments() — parametresiz, tek paylaşılan cache basamağı.
// staleTime 60 s; tur açılmadan fetch YOK (enabled / fetchQuery).
//
// Tur durumu: GET /api/tour/state — kişisel, cache'siz nokta okuma;
// oturum başına bir kez (staleTime sonsuz, PUT sonrası setQueryData).

import { useQuery, type QueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { Service, TourStateResponse } from '@/lib/types';

export const TOUR_TILES_STALE_MS = 60_000;
const NS_PER_MS = 1_000_000;
const MINUTE_MS = 60_000;

export const tourKeys = {
  state: ['tour', 'state'] as const,
  tiles: ['tour', 'tiles'] as const,
};

export interface TourTiles {
  /** Toplam servis (withTotal); MV dışı yolda sayfa uzunluğu. */
  totalServices: number;
  /** Σ spanCount — en çok 50 servis üzerinden. */
  spans: number;
  /** true → Σ yalnız ilk 50 servisi kapsıyor ("ilk 50" etiketi). */
  spansTopOnly: boolean;
  busiest: { name: string; spanCount: number } | null;
  environments: number;
}

/** SAF: son 5 dk, uçlar dakikaya oturtulmuş, unix ns. */
export function tourWindow(nowMs: number): { from: number; to: number } {
  const toMs = Math.floor(nowMs / MINUTE_MS) * MINUTE_MS;
  return { from: (toMs - 5 * MINUTE_MS) * NS_PER_MS, to: toMs * NS_PER_MS };
}

/** SAF: iki yanıttan karo modeli. */
export function summarizeTiles(
  page: { services: Service[] | null | undefined; hasMore?: boolean; total?: number } | null | undefined,
  envs: { environments: string[] | null | undefined; total?: number } | null | undefined,
): TourTiles {
  const services = page?.services ?? [];
  let spans = 0;
  let busiest: TourTiles['busiest'] = null;
  for (const s of services) {
    spans += s.spanCount || 0;
    if (!busiest || s.spanCount > busiest.spanCount) busiest = { name: s.name, spanCount: s.spanCount };
  }
  const envList = envs?.environments ?? [];
  return {
    totalServices: typeof page?.total === 'number' ? page.total : services.length,
    spans,
    spansTopOnly: !!page?.hasMore,
    busiest,
    environments: typeof envs?.total === 'number' ? envs.total : envList.length,
  };
}

export async function fetchTourTiles(nowMs: number = Date.now()): Promise<TourTiles> {
  const [page, envs] = await Promise.all([
    api.servicesPage(tourWindow(nowMs), { limit: 50, sort: 'spanCount', dir: 'desc', withTotal: '1' }),
    api.environments().catch(() => null), // ortam sayısı süs; düşerse 0
  ]);
  return summarizeTiles(page, envs);
}

export const tourTilesQuery = {
  queryKey: tourKeys.tiles,
  queryFn: () => fetchTourTiles(),
  staleTime: TOUR_TILES_STALE_MS,
};

/** Tur başlarken bir kez — hata null (karşılama "okunamadı" notu, tur sürer). */
export function loadTourTiles(qc: QueryClient): Promise<TourTiles | null> {
  return qc.fetchQuery(tourTilesQuery).catch(() => null);
}

/** Karşılama karoları — bağlam çözülürken önbelleğe girmiş veriyi okur. */
export function useTourTiles(enabled: boolean) {
  return useQuery({ ...tourTilesQuery, enabled, retry: false });
}

export function useTourStateQuery(enabled: boolean) {
  return useQuery<TourStateResponse | null>({
    queryKey: tourKeys.state,
    queryFn: ({ signal }) => api.tourState(signal).then(r => r ?? null),
    staleTime: Infinity,
    retry: false,
    enabled,
  });
}
