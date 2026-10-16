import { describe, it, expect } from 'vitest';
import { pivotCtxFromSearch, withPivotCtx } from './pivotContext';

// v0.10.1229 — ortak pivot bağlamı (Faz 1-A): taşıma, ezmeme, sabitleme.
const NOW = Date.UTC(2026, 9, 16, 12, 0, 0);

describe('pivotContext', () => {
  it('bağlamı okur, boşları atar', () => {
    expect(pivotCtxFromSearch('range=3h&env=prod&cluster=&q=x')).toEqual({ range: '3h', env: 'prod' });
  });
  it('eksik boyutu ekler, href\'tekini ezmez', () => {
    const h = withPivotCtx('/traces?service=a&range=custom%3A1-2', { range: '3h', env: 'prod', cluster: 'c1' });
    const sp = new URLSearchParams(h.split('?')[1]);
    expect(sp.get('range')).toBe('custom:1-2');
    expect(sp.get('env')).toBe('prod');
    expect(sp.get('cluster')).toBe('c1');
    expect(h.startsWith('/traces?')).toBe(true);
  });
  it('carry yalnız istenen boyutları taşır', () => {
    const h = withPivotCtx('/service?name=a', { range: '1h', env: 'prod', cluster: 'c1' }, { carry: ['range', 'env'] });
    expect(new URLSearchParams(h.split('?')[1]).has('cluster')).toBe(false);
  });
  it('pinAtMs göreli aralığı mutlak pencereye dondurur', () => {
    const h = withPivotCtx('/logs?service=a', { range: '30m' }, { pinAtMs: NOW });
    const r = new URLSearchParams(h.split('?')[1]).get('range') ?? '';
    const m = /^custom:(\d+)-(\d+)$/.exec(r);
    expect(m).not.toBeNull();
    expect(Number(m![2]) - Number(m![1])).toBe(30 * 60_000);
    expect(Number(m![2])).toBe(NOW);
  });
  it('zaten mutlak pencere ve hash korunur', () => {
    expect(withPivotCtx('/trace?id=x#span', { range: 'custom:5-9' }, { pinAtMs: NOW })).toBe('/trace?id=x&range=custom%3A5-9#span');
  });
  it('parametresiz href ve boş bağlam', () => {
    expect(withPivotCtx('/problems', {})).toBe('/problems');
  });
});
