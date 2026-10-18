// resultSubject — v0.10.1335: arama sonucu türü → CoSRE öznesi tablosu ve
// çekmecenin birincil "Open in …" hedefi.
import { describe, it, expect } from 'vitest';
import { resultOpenTarget, resultSubject } from './resultSubject';
import type { SearchResult, TimeRange } from '@/lib/types';

const WIN = { fromNs: 1_000, toNs: 2_000 };
const base = { title: 't', subtitle: '', service: 'shop-api', href: '' };

const ROWS: Record<SearchResult['type'], SearchResult> = {
  service: { ...base, type: 'service', id: 'shop-api', href: '/service?name=shop-api' },
  endpoint: { ...base, type: 'endpoint', id: 'shop-api\x1fGET /cart', title: 'GET /cart', payload: { service: 'shop-api', name: 'GET /cart' } },
  problem: {
    ...base, type: 'problem', id: 'p1', href: '/problems?problem=p1',
    payload: { id: 'p1', displayId: 'P-1', ruleName: 'r', severity: 'critical', status: 'open', service: 'shop-api', description: '', startedAt: 1 },
  },
  exception: {
    ...base, type: 'exception', id: 'fp1', href: '/problems?exc=fp1',
    payload: { fingerprint: 'fp1', type: 'E', message: 'm', service: 'shop-api', state: 'open', firstSeen: 1, lastSeen: 2, occurrences: 3 },
  },
  trace: {
    ...base, type: 'trace', id: 'a'.repeat(32), href: '/trace?id=' + 'a'.repeat(32),
    payload: { traceId: 'a'.repeat(32), rootName: 'GET /', serviceName: 'shop-api', startTime: 1, durationMs: 2, spanCount: 3, hasError: false },
  },
  log: {
    ...base, type: 'log', id: '9', ts: 1_500,
    payload: { timestamp: 1_500, severity: 17, severityText: 'ERROR', body: 'boom', serviceName: 'shop-api', traceId: 'B'.repeat(32) },
  },
};

describe('resultSubject — tür → AISubject (v0.10.1335)', () => {
  it('trace / problem / exception kendi kimliğiyle', () => {
    expect(resultSubject(ROWS.trace, WIN)).toEqual({ kind: 'trace', id: 'a'.repeat(32) });
    expect(resultSubject(ROWS.problem, WIN)).toEqual({ kind: 'problem', id: 'p1' });
    expect(resultSubject(ROWS.exception, WIN)).toEqual({ kind: 'exception', id: 'fp1' });
  });

  it('service → service-health, pencereyle', () => {
    expect(resultSubject(ROWS.service, WIN)).toEqual({ kind: 'service-health', id: 'shop-api', fromNs: 1_000, toNs: 2_000 });
  });

  it('endpoint → ÜST servisin service-health’i', () => {
    expect(resultSubject(ROWS.endpoint, WIN)).toEqual({ kind: 'service-health', id: 'shop-api', fromNs: 1_000, toNs: 2_000 });
  });

  it('log → log öznesi (logLineSubject): servis + ts + küçük harf trace id', () => {
    const s = resultSubject(ROWS.log, WIN);
    expect(s).toMatchObject({ kind: 'log', id: 'shop-api', tsNs: '1500', traceId: 'b'.repeat(32) });
  });

  it('log servis/zaman taşımıyorsa null (boş vaat yok)', () => {
    const r = { ...ROWS.log, payload: { ...(ROWS.log as Extract<SearchResult, { type: 'log' }>).payload, serviceName: '' } } as SearchResult;
    expect(resultSubject(r, WIN)).toBeNull();
  });
});

describe('resultOpenTarget — tek birincil hedef', () => {
  const ctx = { range: { preset: '1h' } as TimeRange, env: '', q: 'cart' };
  it('her tür güvenli bir uygulama yoluna iner', () => {
    expect(resultOpenTarget(ROWS.trace, ctx)?.href).toMatch(/^\/trace\?/);
    expect(resultOpenTarget(ROWS.log, ctx)?.href).toMatch(/^\/logs\?/);
    expect(resultOpenTarget(ROWS.problem, ctx)?.href).toBe('/problems?problem=p1');
    expect(resultOpenTarget(ROWS.exception, ctx)?.href).toBe('/problems?exc=fp1');
    expect(resultOpenTarget(ROWS.service, ctx)?.href).toMatch(/^\/service\?name=shop-api/);
    expect(resultOpenTarget(ROWS.endpoint, ctx)?.href).toMatch(/^\/traces\?/);
  });

  it('güvensiz sunucu href’i (problem) → null', () => {
    expect(resultOpenTarget({ ...ROWS.problem, href: '//evil.com' } as SearchResult, ctx)).toBeNull();
  });
});
