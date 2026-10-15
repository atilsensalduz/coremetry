import { describe, it, expect } from 'vitest';
import { deltaLabel, errRate, fmtMs, worse } from './operationCardView';
import { operationBlocks } from '@/lib/chatBlocks';

describe('operasyon kartı (CoSRE operasyon tespiti Faz 3)', () => {
  it('fark etiketi', () => {
    expect(deltaLabel(140, 100)).toBe('+%40');
    expect(deltaLabel(88, 100)).toBe('−%12');
    expect(deltaLabel(5, 0)).toBe('yeni');
    expect(deltaLabel(0, 0)).toBe('');
  });
  it('yalnız sapan değer renklenir', () => {
    expect(worse('err', 0.05, 0.01)).toBe(true);
    expect(worse('err', 0.015, 0.01)).toBe(false);
    expect(worse('p99', 900, 600)).toBe(true);
    expect(worse('p99', 700, 600)).toBe(false);
  });
  it('oran ve süre', () => {
    expect(errRate({ calls: 200, errors: 20, p99Ms: 0, measured: true })).toBe(0.1);
    expect(errRate({ calls: 0, errors: 0, p99Ms: 0, measured: false })).toBe(0);
    expect(fmtMs(1500)).toBe('1.50 s');
    expect(fmtMs(42.4)).toBe('42 ms');
  });
  it('geçersiz blok yükü düşer', () => {
    const ok = { operation: 'X', tracesHref: '/traces', current: { calls: 1, errors: 0, p99Ms: 1, measured: true } };
    expect(operationBlocks([
      { id: 'a', type: 'operation', seq: 1, final: true, payload: ok },
      { id: 'b', type: 'operation', seq: 2, final: true, payload: { operation: 'Y' } },
      { id: 'c', type: 'chart', seq: 3, final: true, payload: ok },
    ])).toHaveLength(1);
  });
});
