import { describe, it, expect } from 'vitest';
import { legendOrder, nextLegendSort } from './legendSort';

// v0.10.1248 — sıralanabilir lejant (Grafana paritesi).
const st = (last: number | null, mean: number | null) => ({ last, min: null, max: null, mean, sum: 0, count: 1 });
const rows = [
  { name: 'b', stat: st(1, 5) },
  { name: 'a', stat: st(3, null) },
  { name: 'c', stat: st(2, 9) },
];

describe('legendOrder', () => {
  it('null → orijinal sıra', () => expect(legendOrder(rows, null)).toEqual([0, 1, 2]));
  it('sayı azalan / artan, boşlar sonda', () => {
    expect(legendOrder(rows, { key: 'last', desc: true })).toEqual([1, 2, 0]);
    expect(legendOrder(rows, { key: 'mean', desc: true })).toEqual([2, 0, 1]);
    expect(legendOrder(rows, { key: 'mean', desc: false })).toEqual([0, 2, 1]);
  });
  it('ada göre', () => expect(legendOrder(rows, { key: 'name', desc: false })).toEqual([1, 0, 2]));
});

describe('nextLegendSort', () => {
  it('yeni sayı sütunu azalan, ad artan, aynı sütun yön değiştirir', () => {
    expect(nextLegendSort(null, 'max')).toEqual({ key: 'max', desc: true });
    expect(nextLegendSort(null, 'name')).toEqual({ key: 'name', desc: false });
    expect(nextLegendSort({ key: 'max', desc: true }, 'max')).toEqual({ key: 'max', desc: false });
  });
});
