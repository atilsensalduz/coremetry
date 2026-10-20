import { describe, it, expect } from 'vitest';
import { lttb, downsampleXY, type Point } from './lttb';
import { percentiles, quantileSorted } from './transforms';

describe('lttb', () => {
  const series = (n: number): Point[] =>
    Array.from({ length: n }, (_, i) => ({ x: i, y: Math.sin(i / 5) }));

  it('returns input unchanged when threshold >= n or < 3', () => {
    const s = series(10);
    expect(lttb(s, 10)).toBe(s);
    expect(lttb(s, 20)).toBe(s);
    expect(lttb(s, 2)).toBe(s);
  });

  it('reduces to exactly `threshold` points and keeps first + last', () => {
    const s = series(1000);
    const out = lttb(s, 100);
    expect(out.length).toBe(100);
    expect(out[0]).toBe(s[0]);
    expect(out[out.length - 1]).toBe(s[s.length - 1]);
  });

  it('preserves a sharp spike that naive every-Nth decimation would drop', () => {
    const s = series(1000);
    // Inject a tall spike at an index that is NOT a multiple of the stride.
    s[503] = { x: 503, y: 1000 };
    const out = lttb(s, 50);
    expect(out.some(p => p.y === 1000)).toBe(true);
  });
});

describe('downsampleXY', () => {
  it('preserves null gaps between segments', () => {
    const xs = Array.from({ length: 600 }, (_, i) => i);
    const ys: (number | null)[] = xs.map(i => (i >= 200 && i < 220 ? null : Math.cos(i / 7)));
    const out = downsampleXY(xs, ys, 100);
    expect(out.ys.length).toBeLessThan(ys.length);
    // The gap survives as at least one null break.
    expect(out.ys.some(v => v === null)).toBe(true);
  });
});

describe('quantiles', () => {
  it('quantileSorted interpolates', () => {
    const s = [1, 2, 3, 4];
    expect(quantileSorted(s, 0)).toBe(1);
    expect(quantileSorted(s, 1)).toBe(4);
    expect(quantileSorted(s, 0.5)).toBeCloseTo(2.5);
  });

  it('percentiles ignores non-finite and sorts', () => {
    const [p50, p99] = percentiles([5, 1, NaN, 3, 2, 4], [0.5, 0.99]);
    expect(p50).toBeCloseTo(3);
    expect(p99).toBeGreaterThanOrEqual(4);
  });
});
