import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { opCallTreeCols, targetLabel, kindGlyph, errBadgeClass, indentPx, windowEdgesLine } from './opCallTreeCols';
import { operationCardHref, traceRootOpCardRef, operationRowOpCardRef, OPCARD_ANCHOR } from './links';

// v0.10.1491 — operasyon kartı (R3) saf yardımcıları + pivot kimlikleri.
describe('opCallTreeCols', () => {
  it('her kolonda minWidth + priority, tek flex kolon (fit sözleşmesi)', () => {
    const cols = opCallTreeCols(k => k);
    expect(cols.filter(c => c.flex).length).toBe(1);
    for (const c of cols) {
      expect(c.minWidth, c.id).toBeGreaterThan(0);
      expect(c.priority, c.id).toBeGreaterThan(0);
    }
    expect(cols.map(c => c.id)).toEqual(['target', 'op', 'protocol', 'calls', 'errPct', 'p95Ms', 'samples']);
  });
  it('hedef etiketi öneki atar', () => {
    expect(targetLabel('db:oracle')).toBe('oracle');
    expect(targetLabel('queue:kafka:order-events')).toBe('kafka · order-events');
    expect(targetLabel('queue:rabbitmq')).toBe('rabbitmq');
    expect(targetLabel('ext:bank-gw.example.test')).toBe('bank-gw.example.test');
    expect(targetLabel('payments-api')).toBe('payments-api');
  });
  it('glif / rozet / girinti / dipnot', () => {
    expect(kindGlyph('db')).not.toBe(kindGlyph('service'));
    expect(errBadgeClass(0.5)).toBe('b-gray');
    expect(errBadgeClass(1)).toBe('b-warn');
    expect(errBadgeClass(5)).toBe('b-err');
    expect([1, 2, 3, 9].map(indentPx)).toEqual([0, 16, 32, 32]);
    const edges = Array.from({ length: 7 }, (_, i) => ({ childService: `s${i}`, childOp: i ? 'op' : '', calls: i }));
    const line = windowEdgesLine(edges, n => String(n));
    expect(line.split(' — ').length).toBe(5);
    expect(line.startsWith('s0: 0 — s1 · op: 1')).toBe(true);
  });
});

describe('operasyon kartı pivotları', () => {
  it('operationCardHref: /endpoint + çapa, boş alan yazılmaz', () => {
    expect(operationCardHref({ service: 'orders-api', path: '/orders/{id}' }))
      .toBe(`/endpoint?service=orders-api&path=%2Forders%2F%7Bid%7D#${OPCARD_ANCHOR}`);
    const h = operationCardHref({ service: 's', path: 'orders.Approve', entry: 'rpc', sig: true }, { range: '1h', env: 'prod' });
    expect(h).toBe('/endpoint?service=s&path=orders.Approve&sig=1&range=1h&env=prod&entry=rpc#opcard');
  });
  it('traceRootOpCardRef: rota > span adı (rpc); istemci kökte yok', () => {
    expect(traceRootOpCardRef({ serviceName: 'a', name: 'POST /x/42', kind: 'server', httpRoute: '/x/{id}' }))
      .toEqual({ service: 'a', path: '/x/{id}' });
    expect(traceRootOpCardRef({ serviceName: 'a', name: 'GET', kind: 'server', attributes: { 'http.route': '/y' } }))
      .toEqual({ service: 'a', path: '/y' });
    expect(traceRootOpCardRef({ serviceName: 'a', name: 'orders.Approve', kind: 'server' }))
      .toEqual({ service: 'a', path: 'orders.Approve', entry: 'rpc' });
    expect(traceRootOpCardRef({ serviceName: 'a', name: 'GET', kind: 'client' })).toBeNull();
    expect(traceRootOpCardRef(null)).toBeNull();
  });
  it('operationRowOpCardRef: Normalized / rotasız artık satırda yok', () => {
    expect(operationRowOpCardRef('s', { name: 'GET', route: '/m' }, false)).toEqual({ service: 's', path: '/m' });
    expect(operationRowOpCardRef('s', { name: 'orders.Approve' }, false)).toEqual({ service: 's', path: 'orders.Approve', entry: 'rpc' });
    expect(operationRowOpCardRef('s', { name: 'GET /u/:id' }, true)).toBeNull();
    expect(operationRowOpCardRef('s', { name: 'GET', route: '', splitResidual: true }, false)).toBeNull();
  });
  it('Trace çipi ve Operations eylemi bağlı (kaynak pini; liste ön-getirmesi yok)', () => {
    const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');
    const trace = read('../Trace.tsx');
    expect(trace).toMatch(/data-testid="trace-opcard-chip"/);
    expect(trace).toMatch(/operationCardHref\(opCardRef/);
    const ops = read('../service/OperationsTable.tsx');
    expect(ops).toMatch(/data-testid="op-opcard-link"/);
    // Satır eylemi yalnız bağlantı: Operations listesi kart ucunu ÇAĞIRMAZ.
    expect(ops).not.toMatch(/useOperationCard|api\.operationCard/);
    expect(trace).not.toMatch(/useOperationCard|api\.operationCard/);
  });
});
