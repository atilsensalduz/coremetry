import { describe, it, expect } from 'vitest';
import {
  listQueries, endpointListRows, endpointListScope, routeSubstringRegex, ENDPOINT_LIST_POOL_MAX,
  windowQueries, httpQueries, mergeTable, endpointHttpQueries, endpointRed,
  type EndpointListId,
} from './httpMetrics';
import type { SpanMetricSeries } from './types';

// v0.10.1359 — Operator-reported: /endpoints LİSTESİ "Kaynak: metrik" kipinde
// /api/endpoints/metric okuyordu (çağrı = adım artışlarının toplamı, yüzdelik
// = adım değerlerinin çağrı-ağırlıklı ortalaması) ve sayılar Service → HTTP
// Metrics sekmesiyle tutmuyordu. Karar: liste sekmenin AYNI pencere
// üreticisini (windowQueries, by = method, route) okur, `and on (BY) topk(N,
// …)` ile havuza süzülü. Bu dosya: (1) her sorgunun bayt bayt pini (W=10800,
// N=300), (2) ölçü = sekme üreticisi + havuz sarmalı, (3) indirgeyici tablosu,
// (4) tek fikstürle liste satırı == sekme satırı == detay karoları.

const C = 'http_server_request_duration_seconds_count';
const B = 'http_server_request_duration_seconds_bucket';
const S = 'http_server_request_duration_seconds_sum';
const POOL = `topk(300, sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s])))`;
const AND = ' and on (http_request_method, http_route) ';

const PINS: Record<EndpointListId, string> = {
  total: POOL,
  err5: `(sum by (http_request_method, http_route) (increase(${C}{service_name="shop", http_response_status_code=~"5.."}[10800s])) or (0 * sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s]))))${AND}${POOL}`,
  avg: `(sum by (http_request_method, http_route) (increase(${S}{service_name="shop"}[10800s])) / sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s])))${AND}${POOL}`,
  p50: `(histogram_quantile(0.5, sum by (le, http_request_method, http_route) (increase(${B}{service_name="shop"}[10800s]))))${AND}${POOL}`,
  p95: `(histogram_quantile(0.95, sum by (le, http_request_method, http_route) (increase(${B}{service_name="shop"}[10800s]))))${AND}${POOL}`,
  p99: `(histogram_quantile(0.99, sum by (le, http_request_method, http_route) (increase(${B}{service_name="shop"}[10800s]))))${AND}${POOL}`,
  cls: `(sum by (http_request_method, http_route, cls) (label_replace(increase(${C}{service_name="shop"}[10800s]), "cls", "\${1}xx", "http_response_status_code", "([0-9]).*")))${AND}${POOL}`,
  pTotal: `(sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s] offset 10800s)))${AND}${POOL}`,
  pErr5: `(sum by (http_request_method, http_route) (increase(${C}{service_name="shop", http_response_status_code=~"5.."}[10800s] offset 10800s)) or (0 * sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s] offset 10800s))))${AND}${POOL}`,
  pAvg: `(sum by (http_request_method, http_route) (increase(${S}{service_name="shop"}[10800s] offset 10800s)) / sum by (http_request_method, http_route) (increase(${C}{service_name="shop"}[10800s] offset 10800s)))${AND}${POOL}`,
  pP99: `(histogram_quantile(0.99, sum by (le, http_request_method, http_route) (increase(${B}{service_name="shop"}[10800s] offset 10800s))))${AND}${POOL}`,
};

const byId = (qs: ReturnType<typeof listQueries>) => Object.fromEntries(qs.map(q => [q.id, q.expr]));

describe('listQueries — bayt bayt pinler (v0.10.1359)', () => {
  it('compare kapalı: 7 sorgu, hepsi anlık', () => {
    const qs = listQueries({ app: 'shop' }, 10800, 300, false);
    expect(qs.map(q => q.id)).toEqual(['total', 'err5', 'avg', 'p50', 'p95', 'p99', 'cls']);
    expect(qs.every(q => q.instant)).toBe(true);
    const got = byId(qs);
    for (const id of qs.map(q => q.id)) expect(got[id]).toBe(PINS[id]);
  });
  it('compare açık: + pTotal/pErr5/pAvg/pP99 (offset W, havuz BU pencerenin topk\'sı)', () => {
    const qs = listQueries({ app: 'shop' }, 10800, 300, true);
    expect(qs.map(q => q.id)).toEqual(['total', 'err5', 'avg', 'p50', 'p95', 'p99', 'cls', 'pTotal', 'pErr5', 'pAvg', 'pP99']);
    const got = byId(qs);
    for (const id of qs.map(q => q.id)) expect(got[id]).toBe(PINS[id]);
  });
  it('N = min(limit, 300); W en az 60 s', () => {
    expect(byId(listQueries({ app: 'shop' }, 10800, 100, false)).total).toContain('topk(100, ');
    expect(byId(listQueries({ app: 'shop' }, 10800, 10000, false)).total).toContain(`topk(${ENDPOINT_LIST_POOL_MAX}, `);
    expect(byId(listQueries({ app: 'shop' }, 5, 100, false)).total).toContain('[60s]');
  });
  it('yol araması route regex\'i (büyük/küçük harf duyarsız alt-dize, metakarakter kaçışlı)', () => {
    expect(endpointListScope('shop', '  ')).toEqual({ app: 'shop' });
    expect(endpointListScope('shop', '/v1.0')).toEqual({ app: 'shop', routeRe: '(?i).*/v1\\.0.*' });
    expect(routeSubstringRegex('a(b)')).toBe('(?i).*a\\(b\\).*');
    expect(byId(listQueries(endpointListScope('shop', '/v1.0'), 10800, 300, false)).total)
      .toBe(`topk(300, sum by (http_request_method, http_route) (increase(${C}{service_name="shop", http_route=~"(?i).*/v1\\\\.0.*"}[10800s])))`);
  });
  it('/api/metrics/promql 8192 karakter tavanı: uzun servis + uzun arama', () => {
    const qs = listQueries(endpointListScope('s'.repeat(253), '/x'.repeat(100)), 30 * 86400, 300, true);
    for (const q of qs) expect(q.expr.length).toBeLessThan(8192);
  });
});

describe('ölçü = sekme üreticisi + havuz sarmalı (v0.10.1359)', () => {
  const BY = 'http_request_method, http_route';
  const tw = windowQueries({ app: 'shop' }, 10800, BY);
  const tab = httpQueries({ app: 'shop' }, 10800);
  const got = byId(listQueries({ app: 'shop' }, 10800, 300, true));
  const wrap = (m: string) => `(${m})${AND}topk(300, ${tw.total})`;
  it('sekmenin "Endpoint Detayları" tablosunun aynı ifadeleri', () => {
    expect(got.total).toBe(`topk(300, ${tab.tTotal})`);
    expect(got.avg).toBe(wrap(tab.tAvg));
    expect(got.p95).toBe(wrap(tab.tP95));
    expect(got.p99).toBe(wrap(tab.tP99));
    expect(got.err5).toBe(wrap(tw.err5Count));
    expect(got.p50).toBe(wrap(tw.p50));
    const pw = windowQueries({ app: 'shop' }, 10800, BY, 10800);
    expect(got.pTotal).toBe(wrap(pw.total));
    expect(got.pErr5).toBe(wrap(pw.err5Count));
    expect(got.pAvg).toBe(wrap(pw.avg));
    expect(got.pP99).toBe(wrap(pw.p99));
  });
});

// ── indirgeyici ─────────────────────────────────────────────────────────
const ser = (labels: Record<string, string>, v: number): SpanMetricSeries => ({
  groupKey: Object.entries(labels).map(([k, x]) => `${k}="${x}"`),
  points: [{ time: 1e9, value: v }],
});
const MR = (method: string, route: string, extra: Record<string, string> = {}) =>
  ({ http_request_method: method, http_route: route, ...extra });

describe('endpointListRows (v0.10.1359)', () => {
  it('birleşim (method, route); s → ms; oran; req/dk; durum sınıfları; p90 yok', () => {
    const rows = endpointListRows({
      total: [ser(MR('GET', '/a'), 1200), ser(MR('POST', '/a'), 600)],
      err5: [ser(MR('GET', '/a'), 12), ser(MR('POST', '/a'), 0)],
      avg: [ser(MR('GET', '/a'), 0.05)],
      p50: [ser(MR('GET', '/a'), 0.007)],
      p95: [ser(MR('GET', '/a'), 0.2)],
      p99: [ser(MR('GET', '/a'), 0.4), ser(MR('POST', '/a'), NaN)],
      cls: [ser(MR('GET', '/a', { cls: '2xx' }), 1100), ser(MR('GET', '/a', { cls: '4xx' }), 88), ser(MR('GET', '/a', { cls: '5xx' }), 12)],
    }, 10800, 'shop');
    expect(rows).toEqual([
      {
        service: 'shop', path: '/a', method: 'GET', calls: 1200, errors: 12, errorRate: 1,
        reqPerMin: 1200 / 180, avgMs: 50, p50Ms: 7, p95Ms: 200, p99Ms: 400,
        http2xx: 1100, http3xx: 0, http4xx: 88, http5xx: 12,
      },
      {
        service: 'shop', path: '/a', method: 'POST', calls: 600, errors: 0, errorRate: 0,
        reqPerMin: 600 / 180, avgMs: 0, p50Ms: undefined, p95Ms: undefined, p99Ms: 0,
        http2xx: 0, http3xx: 0, http4xx: 0, http5xx: 0,
      },
    ]);
    expect(rows[0]).not.toHaveProperty('p90Ms');
    expect(rows[0]).not.toHaveProperty('priorCalls');
  });
  it.each([
    ['satır kümesi = total havuzu (diğer ölçüdeki fazla seri satır yaratmaz)',
      { total: [ser(MR('GET', '/a'), 5)], avg: [ser(MR('GET', '/b'), 1)] }, 1],
    ['total boş → satır yok', { total: [] }, 0],
    ['total null (Go nil) → satır yok', { total: null }, 0],
    ['NaN toplam satırı atlanır', { total: [ser(MR('GET', '/a'), NaN)] }, 0],
  ])('%s', (_n, d, want) => {
    expect(endpointListRows(d as Parameters<typeof endpointListRows>[0], 10800, 'shop')).toHaveLength(want);
  });
  it('0 çağrı: oran 0 (bölme yok); route etiketsiz → path ""; boş method → undefined', () => {
    const [r] = endpointListRows({ total: [ser({ http_request_method: '' }, 0)] }, 10800, 'shop');
    expect(r).toMatchObject({ path: '', calls: 0, errorRate: 0, reqPerMin: 0 });
    expect(r.method).toBeUndefined();
  });
  it('compare: prior alanları; önceki pencerede olmayan satır 0 (UI "listede yeni")', () => {
    const rows = endpointListRows({
      total: [ser(MR('GET', '/a'), 10), ser(MR('GET', '/new'), 3)],
      pTotal: [ser(MR('GET', '/a'), 8)], pErr5: [ser(MR('GET', '/a'), 1)],
      pAvg: [ser(MR('GET', '/a'), 0.02)], pP99: [ser(MR('GET', '/a'), 0.3)],
    }, 10800, 'shop');
    expect(rows[0]).toMatchObject({ priorCalls: 8, priorErrors: 1, priorAvgMs: 20, priorP99Ms: 300 });
    expect(rows[1]).toMatchObject({ priorCalls: 0, priorErrors: 0, priorAvgMs: 0, priorP99Ms: 0 });
  });
});

// ── tutarlılık: liste satırı == sekme satırı == detay karoları ────────────
describe('tek fikstür: liste == sekme == detay (method kapsamlı) (v0.10.1359)', () => {
  // VM'in (method, route) grubu başına döndüreceği gerçek pencere değerleri.
  const FIX = [
    { m: 'GET', r: '/orders', total: 1200, err5: 12, sumS: 60, p50: 0.007, p95: 0.2, p99: 0.4 },
    { m: 'POST', r: '/orders', total: 300, err5: 30, sumS: 90, p50: 0.1, p95: 0.5, p99: 0.9 },
    { m: 'GET', r: '/health', total: 5000, err5: 0, sumS: 5, p50: 0.001, p95: 0.002, p99: 0.003 },
  ];
  const grouped = (f: (x: typeof FIX[number]) => number) => FIX.map(x => ser(MR(x.m, x.r), f(x)));
  const one = (v: number) => [ser({}, v)];
  const R = '/orders', M = 'POST';
  const x = FIX.find(f => f.r === R && f.m === M)!;

  it('PromQL: detay karosu = liste ölçüsünün (method, route) diliminin toplamı', () => {
    const list = byId(listQueries({ app: 'shop' }, 10800, 300, true));
    const inner = (e: string) => (e.startsWith('topk(') ? e.slice('topk(300, '.length, -1) : e.slice(1, e.indexOf(AND) - 1));
    const narrow = (e: string) => e
      .replaceAll('sum by (le, http_request_method, http_route) (', 'sum by (le) (')
      .replaceAll('sum by (http_request_method, http_route) (', 'sum(')
      .replaceAll('{service_name="shop"', `{service_name="shop", http_route="${R}", http_request_method="${M}"`);
    const det = Object.fromEntries(endpointHttpQueries({ service: 'shop', route: R, method: M }, 10800, { latQ: 'p50', compare: true })
      .map(q => [q.id, q.expr]));
    const pairs: Array<[EndpointListId, string]> = [
      ['total', 'wTotal'], ['err5', 'wErr5'], ['avg', 'wAvg'], ['p50', 'wP50'], ['p95', 'wP95'], ['p99', 'wP99'],
      ['pTotal', 'pTotal'], ['pErr5', 'pErr5'], ['pAvg', 'pAvg'], ['pP99', 'pP99'],
    ];
    for (const [l, d] of pairs) expect(narrow(inner(list[l]))).toBe(det[d]);
  });

  it('değerler: liste satırı == sekme mergeTable satırı == endpointRed karoları', () => {
    const rows = endpointListRows({
      total: grouped(f => f.total), err5: grouped(f => f.err5), avg: grouped(f => f.sumS / f.total),
      p50: grouped(f => f.p50), p95: grouped(f => f.p95), p99: grouped(f => f.p99),
    }, 10800, 'shop');
    const lr = rows.find(r => r.path === R && r.method === M)!;

    const tabRow = mergeTable({
      tTotal: grouped(f => f.total), tAvg: grouped(f => f.sumS / f.total),
      tP95: grouped(f => f.p95), tP99: grouped(f => f.p99), tErr5: grouped(f => f.err5 / f.total),
    }, ['http_request_method', 'http_route']).find(t => t.http_route === R && t.http_request_method === M)!;

    // Detay: method kapsamlı tek seri = o grubun değeri.
    const red = endpointRed({
      total: one(x.total), err5: one(x.err5), avg: one(x.sumS / x.total),
      p50: one(x.p50), p95: one(x.p95), p99: one(x.p99),
    }, 10800);

    expect(lr.calls).toBe(tabRow.tTotal);
    expect(lr.calls).toBe(red.calls);
    expect(lr.errors).toBe(red.errors);
    expect(lr.errorRate / 100).toBeCloseTo(tabRow.tErr5 as number, 12);
    expect(lr.errorRate / 100).toBeCloseTo(red.errRate!, 12);
    expect(lr.reqPerMin).toBe(red.reqPerMin);
    expect(lr.avgMs / 1000).toBeCloseTo(tabRow.tAvg as number, 12);
    expect(lr.avgMs / 1000).toBeCloseTo(red.avgS!, 12);
    expect(lr.p95Ms! / 1000).toBeCloseTo(tabRow.tP95 as number, 12);
    expect(lr.p95Ms! / 1000).toBeCloseTo(red.p95S!, 12);
    expect(lr.p99Ms / 1000).toBeCloseTo(tabRow.tP99 as number, 12);
    expect(lr.p99Ms / 1000).toBeCloseTo(red.p99S!, 12);
    expect(lr.p50Ms! / 1000).toBeCloseTo(red.p50S!, 12);
  });
});
