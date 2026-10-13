// podRingSegments.test.ts — v0.10.1161: halka dilimleri ve merkez etiketi (saf).
import { describe, it, expect } from 'vitest';
import { podRingSegments, podRingLabel } from './podRingSegments';

describe('podRingSegments', () => {
  const keys = (total: number, run: number, fail: number, known = true) =>
    podRingSegments(total, run, fail, known).map(s => `${s.key}:${s.n}`);
  it('hepsi Running → tek nötr dilim', () => { expect(keys(4, 4, 0)).toEqual(['running:4']); });
  it('sapan fazlar ayrı dilim: bekleyen = toplam − running − failing', () => {
    expect(keys(6, 3, 1)).toEqual(['running:3', 'pending:2', 'failed:1']);
  });
  it('faz bilinmiyor / toplam 0 → dilim yok (sağlıklı iddia edilmez)', () => {
    expect(keys(5, 0, 0, false)).toEqual([]);
    expect(keys(0, 0, 0)).toEqual([]);
  });
  it('toplamı aşan girdi kırpılır', () => { expect(keys(3, 9, 9)).toEqual(['running:3']); });
});

describe('podRingLabel', () => {
  it.each([[0, '0'], [7, '7'], [999, '999'], [1000, '1k'], [1240, '1.2k'], [12400, '12k']])('%d → %s', (n, s) => {
    expect(podRingLabel(n)).toBe(s);
  });
});
