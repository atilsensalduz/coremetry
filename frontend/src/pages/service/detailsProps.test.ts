import { describe, it, expect } from 'vitest';
import { runtimeLabel, newestRollout } from './detailsProps';
import type { WorkloadRollout } from '@/lib/types';

// v0.10.1257 — Details şeridi: Java sürümü okunur, son rollout seçimi.
describe('detailsProps', () => {
  it('runtimeLabel', () => {
    expect(runtimeLabel('java', 'OpenJDK Runtime Environment', '17.0.9+9')).toBe('Java 17.0.9');
    expect(runtimeLabel('java')).toBe('Java');
    expect(runtimeLabel('go', 'go', 'go1.22.5')).toBe('go go1.22.5 (go)');
  });
  it('newestRollout', () => {
    const r = (startedAt: number) => ({ startedAt } as WorkloadRollout);
    expect(newestRollout([r(1), r(3), r(2)])?.startedAt).toBe(3);
    expect(newestRollout([])).toBeNull();
  });
});
