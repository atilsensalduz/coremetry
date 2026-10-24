// @vitest-environment jsdom
// v0.10.1495 (operatör, prod `p99_ms · <svc> · 5 dk pencere`: çizgi uzun
// aralıklarda ~0 ms'e düşüyordu) — sunucunun null noktası (boş pencere ya da
// minSamples tabanı altı) uPlot'a NULL olarak iner (boşluk), asla 0 değil;
// taban altı nokta sayısı ayrıca sayılır.
import { describe, it, expect } from 'vitest';
import type { AlertRuleSeries } from '@/lib/types';
import { spanSeriesToFrames, framesToAligned } from '@/lib/chart/dataFrame';
import { alertBelowFloorCount, alertSeriesState, alertSeriesToSpan } from './alertMetricSeries';

const s = (points: AlertRuleSeries['points']): AlertRuleSeries => ({
  metric: 'p99_ms', service: 'orders-api', windowSec: 300, stepSec: 300, from: 0, to: 4 * 300e9, points,
});

describe('alarm dizisi boşlukları', () => {
  it('null → uPlot verisinde null (0 değil)', () => {
    const data = s([
      { t: 300e9, v: 820 }, { t: 600e9, v: null, belowFloor: true }, { t: 900e9, v: null }, { t: 1200e9, v: 0 },
    ]);
    const aligned = framesToAligned(spanSeriesToFrames(alertSeriesToSpan(data), { unit: 'ms', name: 'p99_ms' }));
    expect(aligned.data[1]).toEqual([820, null, null, 0]); // gerçek 0 kalır, boşluk null
    expect(alertBelowFloorCount(data)).toBe(1);
  });
  it('yalnız taban altı noktalar → "veri yok" (sıfır çizgisi değil)', () => {
    expect(alertSeriesState({ isPending: false, isError: false, data: s([{ t: 1, v: null, belowFloor: true }]) })).toBe('empty');
  });
});
