import { describe, it, expect } from 'vitest';
import { serviceReportBlocks } from './chatBlocks';
import type { ChatServiceReportPayload, ChatTypedBlock } from './types';

// v0.10.1337 — servis raporu bloğu: şekil doğrulanır, HER href isSafeAppPath'ten
// geçer (güvensiz link çipi düşer, güvensiz exception linki satırdan silinir).
const mk = (id: string, seq: number, type: ChatTypedBlock['type'], payload: unknown): ChatTypedBlock => ({ id, seq, type, final: true, payload });

const reportFixture = (): ChatServiceReportPayload => ({
  service: 'payment-service',
  rangeS: 3600,
  verdict: 'Sorunlu',
  reasons: ['hata oranı %7.20 (baseline %0.40)'],
  red: { spans: 5000, errorRate: 7.2, p99Ms: 1850, baseErrorRate: 0.4, baseP99Ms: 210 },
  ops: {
    slow: [{ name: 'POST /api/pay', calls: 4120, errors: 890, errorRate: 21.6, p99Ms: 2400, priorP99Ms: 300, ratio: 8, hasPrior: true }],
    erroring: [{ name: 'POST /api/pay', calls: 4120, errors: 890, errorRate: 21.6, p99Ms: 2400, hasPrior: true }],
  },
  exceptions: [{
    fingerprint: 'ora:1', type: 'COR-30518', message: 'PAYMENT_AUTH_REST', state: 'new', priority: 'P1', oracle: true, code: 'COR-30518',
    operation: 'PAYMENT_AUTH_REST', hourKnown: true, lastHour: 900, prevHour: 200, occurrences: 12345, firstSeen: 1, lastSeen: 2,
    href: '/problems?exc=ora%3A1', explainHref: '/problems?exc=ora%3A1&ai=exception',
  }],
  drills: [{
    traceId: '0af7651916cd43dd8448eb211c80319c', label: 'en yavaş', ok: true, text: "50.02 s'nin 50.0 s'i (%100) CLIENT çağrısında: POST auth-gateway",
    logsState: 'ok', logs: [{ ts: 1_790_000_000_000_000_000, severity: 'ERROR', service: 'payment-service', body: 'auth call failed: Read timed out' }],
  }],
  logBackend: 'elasticsearch',
  links: [{ label: 'payment-service · Overview', href: '/service?name=payment-service' }],
});

describe('serviceReportBlocks', () => {
  it('geçerli yük kalır, bozuk yük ve başka tür düşer', () => {
    const bs = [mk('b1', 1, 'service_report', reportFixture()), mk('b2', 2, 'service_report', { nope: 1 }), mk('b3', 3, 'evidence', reportFixture())];
    const got = serviceReportBlocks(bs);
    expect(got).toHaveLength(1);
    expect(got[0].service).toBe('payment-service');
    expect(got[0].links).toEqual([{ label: 'payment-service · Overview', href: '/service?name=payment-service' }]);
    expect(serviceReportBlocks(undefined)).toEqual([]);
  });
  it('güvensiz href düşer: link çipi, exception linki', () => {
    const p = reportFixture();
    p.links = [
      { label: 'ok', href: '/problems?service=x' },
      { label: 'kötü', href: '/\\evil.com' },
      { label: 'dış', href: 'https://evil.example' },
      { label: 'protokol', href: '//evil.com' },
    ];
    p.exceptions = [{ ...p.exceptions![0], href: '/\\evil.com', explainHref: 'javascript:alert(1)' }];
    const [got] = serviceReportBlocks([mk('b1', 1, 'service_report', p)]);
    expect(got.links!.map(l => l.label)).toEqual(['ok']);
    expect(got.exceptions![0].href).toBe('');
    expect(got.exceptions![0].explainHref).toBe('');
    expect(got.exceptions![0].code).toBe('COR-30518'); // satır kalır, link olmaz
  });
  it('null listeler boş diziye iner; trace id\'siz kırılım düşer', () => {
    const p = reportFixture();
    p.links = null;
    p.exceptions = null;
    p.drills = [{ ...p.drills![0], traceId: '' }];
    const [got] = serviceReportBlocks([mk('b1', 1, 'service_report', p)]);
    expect(got.links).toEqual([]);
    expect(got.exceptions).toEqual([]);
    expect(got.drills).toEqual([]);
  });
});
