import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { SearchQueryParams, SearchResponse } from '@/lib/types';
import { keys } from './keys';

// v0.10.1335 — birleşik arama sayfası (/search) için GET /api/search.
//
// NEDEN bu ayarlar:
//   - enabled q ≥ 2: sunucu daha kısa sorguyu 400 ile reddeder (searchMinQueryLen).
//   - staleTime 15 000 = sunucu serveCached TTL'i (searchCacheTTL); daha sık
//     sormak aynı cevabı tekrar indirir (ES-maliyet disiplini, v0.8.270).
//   - keepPreviousData: her tuş vuruşunda liste iskelete düşmesin; sayfa
//     isPlaceholderData ile "güncelleniyor" der.
//   - refetchOnWindowFocus false + polling YOK: arama bir anlık görüntüdür,
//     canlı liste değil; odak her döndüğünde altı kaynağı yeniden dövmez.
export const SEARCH_MIN_Q = 2;
export const SEARCH_STALE_MS = 15_000;

export function useSearch(params: SearchQueryParams) {
  return useQuery<SearchResponse>({
    queryKey: keys.search.results(params),
    queryFn: ({ signal }) => api.search(params, signal),
    enabled: params.q.trim().length >= SEARCH_MIN_Q,
    staleTime: SEARCH_STALE_MS,
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
  });
}
