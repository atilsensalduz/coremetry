// rolloutSort.test.ts — v0.10.1267 — operatör 2026-10-17: Rollouts sunucu-
// tarafı sıralama (Namespace/Workload/Cluster). URL ↔ sunucu kodeği
// round-trip'i: varsayılan URL'de yok, tanınmayan değer varsayılana düşer,
// API parametreleri sunucu sözlüğüyle (chstore NormalizeRolloutSort) aynı.
import { describe, it, expect } from 'vitest';
import { formatRolloutSort, parseRolloutSort, rolloutSortApiParams, ROLLOUT_SORT_DEFAULT, ROLLOUT_SORT_IDS } from './rolloutSort';

describe('rolloutSort URL kodeği', () => {
  it('parametre yok → varsayılan; varsayılan → URL’ye yazılmaz', () => {
    expect(parseRolloutSort(null)).toEqual(ROLLOUT_SORT_DEFAULT);
    expect(formatRolloutSort(ROLLOUT_SORT_DEFAULT)).toBeNull();
    expect(rolloutSortApiParams(ROLLOUT_SORT_DEFAULT)).toEqual({});
  });

  it.each([
    'namespace.asc', 'namespace.desc', 'workload.asc', 'workload.desc',
    'cluster.asc', 'cluster.desc', 'started.asc',
  ])('round-trip %s', v => {
    const s = parseRolloutSort(v);
    expect(formatRolloutSort(s)).toBe(v);
    const [id, dir] = v.split('.');
    expect(rolloutSortApiParams(s)).toEqual({ sort: id, dir });
  });

  it.each(['started.desc', 'bogus.asc', 'namespace', 'namespace.up', 'cluster_id.asc', '', '.asc'])(
    'geçersiz/varsayılan %j → varsayılan, URL’de yok', v => {
      const s = parseRolloutSort(v);
      expect(s).toEqual(ROLLOUT_SORT_DEFAULT);
      expect(formatRolloutSort(s)).toBeNull();
    });

  it('sözlük sunucuyla aynı (internal/chstore/rollouts.go rolloutSortCols + started)', () => {
    expect([...ROLLOUT_SORT_IDS].sort()).toEqual(['cluster', 'namespace', 'started', 'workload']);
  });
});
