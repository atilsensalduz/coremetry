import { describe, it, expect } from 'vitest';
import uPlot from 'uplot';
import { httpPanelZeroBase, fillStepGaps, stepSeconds, HTTP_QUANTILE_NOTE } from './httpMetrics';
import { yScaleSoftMin } from './chart/overlays';
import { isolatedPointIdxs } from './chart/gapPolicy';
import type { SpanMetricSeries } from './types';

// v0.10.1355 — operatör-raporlu (prod ekran görüntüleri, /endpoint metrik kipi
// ve Service → HTTP Metrics sekmesindeki aynı paneller):
//  (1) "Latency Yüzdelikleri": p50 2.621–2.653 ms, y ekseni 2.62–2.65 ms'e
//      oturuyordu — 30 µs'lik kova-içi interpolasyon gürültüsü büyük
//      oynaklık gibi ("Küçük ms arasında çok oynak görünüyor grafikte").
//  (2) "5xx Hataları (Route bazında)": iki örnek (19:25 0.0042, 23:00
//      0.0033 req/s) aradaki boşluğu kapatan dev bir üçgen çiziyordu.

// CorePanel y ölçeği @grafana/ui UPlotScaleBuilder üzerinden kurulur:
// softMin null → mode 3 (otomatik, veriye otur); softMin 0 → soft 0 mode 1.
// Burada AYNI uPlot.rangeNum çağrısı kurulur ki kural gerçek aralık
// matematiğiyle sınansın (yScaleSoftMin = CorePanel'in tek karar noktası).
function yRange(unit: string, min: number, max: number): [number, number] {
  const softMin = yScaleSoftMin({ bars: false, zeroBase: httpPanelZeroBase(unit), log: false });
  const r = uPlot.rangeNum(min, max, {
    min: { pad: 0.1, hard: -Infinity, soft: softMin ?? 0, mode: softMin == null ? 3 : 1 },
    max: { pad: 0.1, hard: Infinity, soft: 0, mode: 3 },
  });
  return [r[0] as number, r[1] as number];
}

describe('y aralığı kuralı — HTTP panelleri aşırı zoom yapmaz (v0.10.1355)', () => {
  it('kural öncesi (otomatik aralık) operatör vakası gerçekten aşırı zoom', () => {
    const r = uPlot.rangeNum(0.002621, 0.002653, {
      min: { pad: 0.1, hard: -Infinity, soft: 0, mode: 3 },
      max: { pad: 0.1, hard: Infinity, soft: 0, mode: 3 },
    });
    expect((r[1] as number) - (r[0] as number)).toBeLessThan(0.2 * 0.002653);
  });

  it.each<[string, string, number, number]>([
    ['p50 2.621–2.653 ms (operatör vakası)', 's', 0.002621, 0.002653],
    ['p99 sıçraması 2.6 → 50 ms', 's', 0.0026, 0.05],
    ['ms birimli gecikme', 'ms', 2.621, 2.653],
    ['istek hızı 120.1–120.5 req/s', 'req/s', 120.1, 120.5],
    ['seyrek 5xx 0.0033–0.0042 req/s', 'req/s', 0.0033, 0.0042],
    ['oran %', '%', 0.3, 0.31],
  ])('%s → taban 0, görünen aralık ≥ maksimumun %%20si, veri içeride', (_n, unit, min, max) => {
    expect(httpPanelZeroBase(unit)).toBe(true);
    const [lo, hi] = yRange(unit, min, max);
    expect(lo).toBe(0);
    expect(hi - lo).toBeGreaterThanOrEqual(0.2 * max);
    expect(hi).toBeGreaterThanOrEqual(max);
  });

  it('bilinmeyen birim otomatik aralıkta kalır (işaretli miktar olabilir)', () => {
    expect(httpPanelZeroBase('')).toBe(false);
    expect(httpPanelZeroBase('delta')).toBe(false);
  });
});

const NS = 1e9;
const ser = (key: string, pts: [number, number][]): SpanMetricSeries => ({
  groupKey: [key], points: pts.map(([tSec, value]) => ({ time: tSec * NS, value })),
});
const vals = (s: SpanMetricSeries) => s.points.map(p => (Number.isFinite(p.value) ? p.value : null));
const secs = (s: SpanMetricSeries) => s.points.map(p => p.time / NS);

describe('fillStepGaps — eksik adımlar null, atlanmaz (v0.10.1355)', () => {
  it('operatör vakası: iki örnek arası boşluk null dolu, iki örnek yalıtık nokta kalır', () => {
    // 6h pencere → adım 72 s; 19:25 ve 23:00 arası 3h35m.
    const step = stepSeconds(6 * 3600);
    expect(step).toBe(72);
    const t0 = 19 * 3600 + 25 * 60, t1 = t0 + 179 * step;
    const [out] = fillStepGaps([ser('500 GET /x', [[t0, 0.0042], [t1, 0.0033]])], step);
    expect(out.points).toHaveLength(180);
    const v = vals(out);
    expect(v[0]).toBe(0.0042);
    expect(v[179]).toBe(0.0033);
    expect(v.slice(1, 179).every(x => x === null)).toBe(true);
    // Izgarada: her dolgu damgası t0 + k·adım.
    expect(secs(out).every((t, i) => t === t0 + i * step)).toBe(true);
    expect(isolatedPointIdxs(v)).toEqual([0, 179]);
  });

  it('sürekli seri dokunulmaz (aynı nesne döner)', () => {
    const s = [ser('a', [[0, 1], [10, 2], [20, 3]])];
    expect(fillStepGaps(s, 10)).toBe(s);
  });

  it('tek eksik adım da boşluk (SIKI doktrin); 1.5 çarpanı yalnız yuvarlama payı', () => {
    const [out] = fillStepGaps([ser('a', [[0, 1], [10, 2], [30, 3]])], 10);
    expect(secs(out)).toEqual([0, 10, 20, 30]);
    expect(vals(out)).toEqual([1, 2, null, 3]);
    // Adım tahmini 1 s kaysa (ceil farkı) sürekli seri bozulmaz.
    const s = [ser('b', [[0, 1], [11, 2], [22, 3]])];
    expect(fillStepGaps(s, 10)).toBe(s);
  });

  it('dolgu panel birleşiminin boşluğuna: başka serinin verisi olan ana damga eklenmez', () => {
    const dense = ser('dense', [[0, 1], [10, 1], [20, 1], [30, 1]]);
    const sparse = ser('sparse', [[0, 5], [30, 6]]);
    const out = fillStepGaps([dense, sparse], 10);
    // Birleşim sürekli → dolgu yok; seyrek serinin boşluğu hizalamada null olur.
    expect(out[0]).toBe(dense);
    expect(out[1]).toBe(sparse);
    // Birleşimde boşluk varsa dolgu iki seriye de AYNI damgalarla girer.
    const out2 = fillStepGaps([ser('a', [[0, 1], [10, 1], [40, 1]]), ser('b', [[0, 2], [40, 2]])], 10);
    expect(secs(out2[0])).toEqual([0, 10, 20, 30, 40]);
    expect(secs(out2[1])).toEqual([0, 20, 30, 40]);
    expect(vals(out2[1])).toEqual([2, null, null, 2]);
  });

  it('boş girdi, null points ve geçersiz adım güvenli', () => {
    expect(fillStepGaps([], 10)).toEqual([]);
    const s = [ser('a', [[0, 1], [100, 2]])];
    expect(fillStepGaps(s, 0)).toBe(s);
    const nullPts = [{ groupKey: ['x'], points: null as unknown as SpanMetricSeries['points'] }];
    expect(fillStepGaps(nullPts, 10)[0].groupKey).toEqual(['x']);
  });
});

describe('yüzdelik notu (v0.10.1355)', () => {
  it('tek satır, kova hassasiyetini söyler', () => {
    expect(HTTP_QUANTILE_NOTE).toBe('Yüzdelikler histogram kovalarından hesaplanır; kova içi değerler yaklaşıktır (OTel varsayılan sınırları 0/5/10/25… ms).');
    expect(HTTP_QUANTILE_NOTE).not.toContain('\n');
  });
});
