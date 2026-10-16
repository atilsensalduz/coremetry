import { describe, it, expect } from 'vitest';
import { readUrlParams, writeUrlParams } from './urlParams';
import { SERVICES_URL_SPEC, SERVICES_FILTERS_EMPTY } from './servicesUrlSpec';

// v0.10.1229 — /services filtre sözleşmesi (v0.9.1336 K5 + v0.10.1216
// regresyonlarının yerine): param adları sabit, round-trip kayıpsız,
// yabancılar (page, s_*, ai, env, range) korunur.
describe('SERVICES_URL_SPEC', () => {
  it('eski derin link aynen okunur', () => {
    const v = readUrlParams(new URLSearchParams('q=pay&err=1&minSpans=100&minP99=250&ownerTeam=t1&sreTeam=s1&cluster=c1&namespace=ns'), SERVICES_URL_SPEC);
    expect(v).toEqual({ q: 'pay', err: true, minSpans: '100', minP99: '250', ownerTeam: 't1', sreTeam: 's1', cluster: 'c1', namespace: 'ns' });
  });
  it('yazım yabancıları korur, boşları siler', () => {
    const prev = new URLSearchParams('page=3&s_services=p99:desc&ai=1&env=prod&range=6h&q=old&err=1');
    const sp = writeUrlParams(prev, SERVICES_URL_SPEC, { ...SERVICES_FILTERS_EMPTY, q: 'new' });
    expect(Object.fromEntries(sp)).toEqual({ page: '3', s_services: 'p99:desc', ai: '1', env: 'prod', range: '6h', q: 'new' });
  });
  it('okunan değer geri yazılınca URL değişmez (silme döngüsü yok)', () => {
    const qs = 'q=a&err=1&minSpans=5&cluster=c';
    const sp = new URLSearchParams(qs);
    expect(writeUrlParams(sp, SERVICES_URL_SPEC, readUrlParams(sp, SERVICES_URL_SPEC)).toString()).toBe(qs);
  });
});
