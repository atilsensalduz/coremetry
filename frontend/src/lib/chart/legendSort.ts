// legendSort.ts — v0.10.1248 (HTTP Metrics Grafana paritesi): lejant tablosu
// sütuna göre sıralanabilir (Grafana legend sortBy / sortDesc). SAF: yalnız
// görüntü sırası döner; seri indeksleri (renk, görünürlük) değişmez.
import type { SeriesStat } from './legendStats';

export type LegendSortKey = 'name' | 'last' | 'min' | 'max' | 'mean' | 'sum';
export interface LegendSort { key: LegendSortKey; desc: boolean }

/** Görüntü sırası (orijinal indeksler). sort null → orijinal sıra. Boş değerler sonda. */
export function legendOrder(rows: ReadonlyArray<{ name: string; stat: SeriesStat }>, sort: LegendSort | null): number[] {
  const idx = rows.map((_, i) => i);
  if (!sort) return idx;
  const val = (i: number): number | string | null => {
    if (sort.key === 'name') return rows[i].name;
    const v = rows[i].stat[sort.key];
    return typeof v === 'number' && Number.isFinite(v) ? v : null;
  };
  return idx.sort((a, b) => {
    const va = val(a), vb = val(b);
    if (va === null && vb === null) return a - b;
    if (va === null) return 1;
    if (vb === null) return -1;
    const c = typeof va === 'string' ? va.localeCompare(String(vb)) : (va as number) - (vb as number);
    return (sort.desc ? -c : c) || a - b;
  });
}

/** Başlık tıkı: aynı sütun → yön değişir; yeni sütun → sayılarda azalan, adda artan. */
export function nextLegendSort(cur: LegendSort | null, key: LegendSortKey): LegendSort {
  if (cur && cur.key === key) return { key, desc: !cur.desc };
  return { key, desc: key !== 'name' };
}
