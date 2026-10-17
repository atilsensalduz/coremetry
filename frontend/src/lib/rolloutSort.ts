// rolloutSort.ts — v0.10.1267 (operatör 2026-10-17: Rollouts sunucu-tarafı
// sıralama — Namespace/Workload/Cluster). SAF URL ↔ sunucu kodeği.
//
// Canlı liste SUNUCU sıralı (ORDER BY … LIMIT 200): başlık tıklaması
// useDataTable serverSort kipinde `?sort=<id>.<dir>` yazar (replace, tek
// paylaşılabilir parametre — v1/v2 kolon setleri `sortUrlKey: 'sort'` ile
// aynı parametrede buluşur), varsayılan (started.desc) URL'ye YAZILMAZ.
// Sayfa sorguyu doğrudan URL'den türetir (yerel kopya yok → içe aktarma
// efekti / imza koruması gerekmez; hook'un kendi URL→durum efekti zaten
// farka bekçi). Sözlük sunucuyla aynı: internal/chstore/rollouts.go
// rolloutSortCols + NormalizeRolloutSort (tanınmayan → varsayılan).
import { parseSortParam, formatSortParam, sortIdKnown, type SortState } from './dataTable';

export const ROLLOUT_SORT_URL_KEY = 'sort';
export const ROLLOUT_SORT_IDS = ['started', 'namespace', 'workload', 'cluster'] as const;
export type RolloutSortId = typeof ROLLOUT_SORT_IDS[number];
export const ROLLOUT_SORT_DEFAULT: SortState = { id: 'started', dir: 'desc' };

/** URL değeri → geçerli sıra; yok/bozuk/tanınmayan → varsayılan. */
export function parseRolloutSort(v: string | null): SortState {
  const s = parseSortParam(v);
  return s && s.id && sortIdKnown(s.id, ROLLOUT_SORT_IDS) ? s : ROLLOUT_SORT_DEFAULT;
}

export function isDefaultRolloutSort(s: SortState): boolean {
  return s.id === ROLLOUT_SORT_DEFAULT.id && s.dir === ROLLOUT_SORT_DEFAULT.dir;
}

/** Sıra → URL değeri; varsayılan null (parametre silinir). */
export function formatRolloutSort(s: SortState): string | null {
  return isDefaultRolloutSort(s) ? null : formatSortParam(s);
}

/** Sıra → API parametreleri; varsayılanda boş (sorgu + RQ anahtarı değişmez). */
export function rolloutSortApiParams(s: SortState): { sort?: string; dir?: 'asc' | 'desc' } {
  if (isDefaultRolloutSort(s) || !s.id) return {};
  return { sort: s.id, dir: s.dir };
}
