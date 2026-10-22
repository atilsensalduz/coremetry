import { describe, it, expect, beforeEach } from 'vitest';
import {
  getPageOverlay, normalizeOverlay, resetPageOverlayForTest, setPageOverlay, stageMetricPanelAsk,
  subscribePageOverlay, takeMetricPanelAsk, withPageOverlay,
} from './pageContextOverlay';

// v0.10.1435 — sayfanın yayınladığı grafik seçimleri (brush + seçili seri) ve
// ✨ yükünün tek seferlik bırakılması.
beforeEach(() => resetPageOverlayForTest());

describe('normalizeOverlay', () => {
  it('geçersiz brush düşer, seri ≤5 × 120, boş → null', () => {
    expect(normalizeOverlay({ brush: { fromMs: 5, toMs: 5 } })).toBeNull();
    expect(normalizeOverlay({ brush: { fromMs: 0, toMs: 5 } })).toBeNull();
    const n = normalizeOverlay({ brush: { fromMs: 1.4, toMs: 9.2 }, selectedSeries: ['a', ' ', 'ş'.repeat(130), 'c', 'd', 'e', 'f'] });
    expect(n?.brush).toEqual({ fromMs: 1, toMs: 10 });
    expect(n?.selectedSeries).toHaveLength(5);
    expect([...(n?.selectedSeries?.[1] ?? '')].length).toBe(120);
  });
});

describe('mağaza', () => {
  it('aynı içerik dinleyiciyi tetiklemez; withPageOverlay katar', () => {
    let n = 0;
    subscribePageOverlay(() => { n++; });
    setPageOverlay({ selectedSeries: ['pod-a'] });
    setPageOverlay({ selectedSeries: ['pod-a'] });
    expect(n).toBe(1);
    expect(getPageOverlay()).toEqual({ selectedSeries: ['pod-a'] });
    const page = withPageOverlay({ page: 'explore', path: '/explore' }, { brush: { fromMs: 10, toMs: 20 } });
    expect(page).toEqual({ page: 'explore', path: '/explore', brush: { fromMs: 10, toMs: 20 } });
    const same = { page: 'explore' as const, path: '/explore' };
    expect(withPageOverlay(same, null)).toBe(same);
  });
});

describe('✨ yükü', () => {
  it('aynı kodek + ≤60 sn: tek sefer; başka kodek / bayat: yok', () => {
    stageMetricPanelAsk('k1', { src: 'span', agg: 'count' }, 1000);
    expect(takeMetricPanelAsk('k2', 1000)).toBeUndefined();
    expect(takeMetricPanelAsk('k1', 2000)).toEqual({ src: 'span', agg: 'count' });
    expect(takeMetricPanelAsk('k1', 2000)).toBeUndefined();
    stageMetricPanelAsk('k1', { src: 'span' }, 0);
    expect(takeMetricPanelAsk('k1', 61_000)).toBeUndefined();
  });
});
