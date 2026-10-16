import { describe, it, expect } from 'vitest';
import { readUrlParams, writeUrlParams, strParam, boolParam, intParam, enumParam, csvParam, type ParamSpec } from './urlParams';

// v0.10.1229 — ortak URL↔state yardımcısı (Faz 1-A).
interface V { q: string; err: boolean; page: number; view: 'list' | 'agg'; svcs: string[] }
const SPEC: ParamSpec<V> = {
  q: strParam(), err: boolParam(), page: intParam(0), view: enumParam(['list', 'agg'] as const, 'list'), svcs: csvParam(),
};

describe('urlParams', () => {
  it('round-trip: yaz → oku aynı değer', () => {
    const v: V = { q: 'pay', err: true, page: 3, view: 'agg', svcs: ['a', 'b'] };
    const sp = writeUrlParams(new URLSearchParams('range=1h&s_x=y'), SPEC, v);
    expect(readUrlParams(sp, SPEC)).toEqual(v);
    expect(sp.get('range')).toBe('1h');   // yabancılar korunur
    expect(sp.get('s_x')).toBe('y');
  });
  it('varsayılan / boş değer paramı siler', () => {
    const sp = writeUrlParams(new URLSearchParams('q=a&err=1&page=2&view=agg&svcs=x'), SPEC,
      { q: '', err: false, page: 0, view: 'list', svcs: [] });
    expect(sp.toString()).toBe('');
  });
  it('bilinmeyen / bozuk değer varsayılana düşer', () => {
    const v = readUrlParams(new URLSearchParams('page=-4&view=nope&err=maybe&svcs=,,'), SPEC);
    expect(v).toEqual({ q: '', err: false, page: 0, view: 'list', svcs: [] });
    expect(readUrlParams(new URLSearchParams('page=1e9'), SPEC).page).toBe(0);
  });
  it('spec dışı yama anahtarı yok sayılır', () => {
    const sp = writeUrlParams(new URLSearchParams(), SPEC, { nope: 'x' } as unknown as Partial<V>);
    expect(sp.toString()).toBe('');
  });
});
