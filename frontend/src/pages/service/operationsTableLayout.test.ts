// operationsTableLayout.test.ts — v0.10.1179.
import { describe, it, expect } from 'vitest';
import { trendSparkWidth, nextOpDetail } from './operationsTableLayout';
import type { OperationRow } from '@/lib/types';

const op = (name: string) => ({ name } as unknown as OperationRow);

describe('operations tablosu', () => {
  it('trend grafiği kolona yayılır, sınırlı', () => {
    expect(trendSparkWidth(100)).toBe(160);
    expect(trendSparkWidth(416)).toBe(400);
    expect(trendSparkWidth(2000)).toBe(640);
  });
  it('aynı satıra ikinci tık kapatır, başka satır açar', () => {
    const a = op('GET /a'), b = op('GET /b');
    expect(nextOpDetail(null, a)).toBe(a);
    expect(nextOpDetail(a, op('GET /a'))).toBeNull();
    expect(nextOpDetail(a, b)).toBe(b);
  });
});
