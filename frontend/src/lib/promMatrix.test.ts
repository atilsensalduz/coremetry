// v0.10.1306 — Thanos konsol cevabı (Prometheus matrix) → editör serisi.
import { describe, it, expect } from 'vitest';
import { promConsoleError, promMatrixToSeries, promSeriesLabel } from './promMatrix';

describe('promMatrixToSeries', () => {
  it('values [ts,"v"] → {time: ns, value}; etiket __name__ hariç', () => {
    const got = promMatrixToSeries([
      { metric: { __name__: 'up', job: 'api', instance: 'a:9090' }, values: [[1760600000, '1'], [1760600015.5, '0.25']] },
    ]);
    expect(got).toEqual([{
      label: 'up{instance="a:9090", job="api"}',
      points: [{ time: 1760600000 * 1e9, value: 1 }, { time: 1760600015500 * 1e6, value: 0.25 }],
    }]);
  });
  it('NaN / Inf / bozuk değer boşluk (null); bozuk zaman atlanır', () => {
    const got = promMatrixToSeries([
      { metric: {}, values: [[1, 'NaN'], [2, '+Inf'], [3, '-Inf'], [4, 'x'], [Number.NaN, '1'], [5, '2']] as [number, string][] },
    ]);
    expect(got[0].label).toBe('{}');
    expect(got[0].points.map(p => p.value)).toEqual([null, null, null, null, 2]);
    expect(got[0].points.map(p => p.time)).toEqual([1e9, 2e9, 3e9, 4e9, 5e9]);
  });
  it('matrix değilse / boşsa []', () => {
    expect(promMatrixToSeries(null)).toEqual([]);
    expect(promMatrixToSeries(undefined)).toEqual([]);
    expect(promMatrixToSeries([])).toEqual([]);
  });
});

describe('promSeriesLabel', () => {
  it('yalnız ad, yalnız etiket, ikisi de yok', () => {
    expect(promSeriesLabel({ __name__: 'up' })).toBe('up');
    expect(promSeriesLabel({ pod: 'p1' })).toBe('{pod="p1"}');
    expect(promSeriesLabel(undefined)).toBe('{}');
  });
});

describe('promConsoleError', () => {
  it('konsol hata zarfı → errorType: error', () => {
    const e = new Error('HTTP 400: {"status":"error","errorType":"bad_data","error":"parse error at 1:3"}');
    expect(promConsoleError(e)).toEqual({ status: 400, message: 'bad_data: parse error at 1:3' });
  });
  it('düz metin 403', () => {
    expect(promConsoleError(new Error('HTTP 403: forbidden\n'))).toEqual({ status: 403, message: 'forbidden' });
  });
  it('HTTP önekisiz hata', () => {
    expect(promConsoleError(new Error('Request timed out'))).toEqual({ status: null, message: 'Request timed out' });
  });
});
