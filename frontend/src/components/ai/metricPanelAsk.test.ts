import { describe, it, expect } from 'vitest';
import { formatAiParam, parseAiParam } from '@/lib/aiSubject';
import { selectedSeriesNames } from '@/lib/chart/legendVisibility';
import { exploreSelectedSeries } from '@/pages/explore/panelAsk';
import { metricPanelSubject } from './metricPanelAsk';

// v0.10.1435 — panel özne kurucusu (pencere + kırpılan brush + ≤3 seri) ve
// seçili seri kuralları (CorePanel lejant izolasyonu, Explore GroupTable).
const W = { fromNs: 1_790_000_000_000_000_000, toNs: 1_790_003_600_000_000_000 };

describe('metricPanelSubject', () => {
  it('brush pencereye kırpılır; tam pencere brush değildir; ≤3 seri; kodek gidiş-dönüş', () => {
    const s = metricPanelSubject('x.A', W, { fromNs: W.fromNs - 5e9, toNs: W.fromNs + 600e9 }, ['a', 'b', 'c', 'd']);
    if (!s || s.kind !== 'metric_panel') throw new Error('özne yok');
    expect(s.brushFromNs).toBe(W.fromNs);
    expect(s.brushToNs).toBe(W.fromNs + 600e9);
    expect(s.series).toEqual(['a', 'b', 'c']);
    expect(parseAiParam(formatAiParam(s))).toEqual(s);
    const full = metricPanelSubject('m', W, W, []);
    expect(full && full.kind === 'metric_panel' ? full.brushFromNs : -1).toBe(0);
    const outside = metricPanelSubject('m', W, { fromNs: W.toNs + 1e9, toNs: W.toNs + 9e9 }, []);
    expect(outside && outside.kind === 'metric_panel' ? outside.brushToNs : -1).toBe(0);
    expect(metricPanelSubject('m', { fromNs: 5, toNs: 5 }, null, [])).toBeNull();
  });
});

describe('seçili seri kuralları', () => {
  it('CorePanel: en az biri gizli ve görünen ≤3', () => {
    expect(selectedSeriesNames(['a', 'b', 'c'], [true, false, false])).toEqual(['a']);
    expect(selectedSeriesNames(['a', 'b', 'c'], [true, true, true])).toEqual([]);
    expect(selectedSeriesNames(['a', 'b', 'c', 'd', 'e'], [true, true, true, true, false])).toEqual([]);
    expect(selectedSeriesNames(['a'], [true])).toEqual([]);
  });
  it('Explore: izolasyon önce, yoksa odak', () => {
    const labels = ['pod=a', 'pod=b', 'pod=c'];
    expect(exploreSelectedSeries('A', labels, new Set(['A:pod=b', 'A:pod=c']), null)).toEqual(['pod=a']);
    expect(exploreSelectedSeries('A', labels, new Set(), 'A:pod=c')).toEqual(['pod=c']);
    expect(exploreSelectedSeries('B', labels, new Set(), 'A:pod=c')).toEqual([]);
  });
});
