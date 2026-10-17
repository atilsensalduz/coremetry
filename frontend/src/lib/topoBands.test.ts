// v0.10.1268 — operatör 2026-10-17: tek namespace'in bağımlılıkları o namespace şeridinde.
//
// Değişmezler:
//   • tüm çağıranları TEK adlı şeritte olan db/queue/ext → o şeritte, katman sütununda
//   • karışık şeritli çağıranlar → grupsuz
//   • grupsuz (ns'siz) çağıran → grupsuz
//   • tek çağıran şeridi katlı (çağıran süper-düğüm) → grupsuz
//   • şerit başlığı servis sayısı bağımlılıklarla şişmez
//   • şeritler çakışmaz, her üye (bağımlılık dahil) kendi şeridinde
//   (v0.10.1294: odak pin testi kaldırıldı — odaklı görünüm lib/topoFocusLayout'ta)
import { describe, it, expect } from 'vitest';
import { serviceBands, dependencyBands, bandServiceCounts } from './topoBands';
import { bfsBarycenterLayout } from './topoBfsLayout';
import { foldTopology } from './topoFold';
import type { ServiceMap, ServiceMapEdge, ServiceMapNode } from './types';

const PITCH = 46;
const node = (service: string, kind?: string): ServiceMapNode =>
  ({ service, spanCount: 100, errorRate: 0, ...(kind ? { kind } : {}) });
const edge = (caller: string, callee: string): ServiceMapEdge =>
  ({ caller, callee, traceCount: 10, spanCount: 100, errorCount: 0 });
const map = (nodes: ServiceMapNode[], edges: ServiceMapEdge[]): ServiceMap =>
  ({ nodes, edges } as ServiceMap);

const NS: Record<string, string | undefined> = {
  'crm-api': 'crm', 'crm-worker': 'crm', 'crm-sync': 'crm',
  'pay-api': 'pay', 'pay-ledger': 'pay',
  loner: undefined,
};
const nsOf = (s: string) => NS[s];

const graph = map(
  [
    node('crm-api'), node('crm-worker'), node('crm-sync'), node('pay-api'), node('pay-ledger'), node('loner'),
    node('db:crm', 'db'),        // yalnız crm çağırır
    node('queue:crm', 'queue'),  // crm-api + crm-worker (aynı şerit)
    node('db:shared', 'db'),     // crm + pay
    node('ext:stripe', 'external'), // loner (grupsuz) + pay
    node('ext:geo', 'external'), // yalnız loner
    node('db:orphan', 'db'),     // çağıranı yok
  ],
  [
    edge('crm-api', 'crm-worker'), edge('crm-worker', 'crm-sync'), edge('crm-api', 'pay-api'),
    edge('pay-api', 'pay-ledger'),
    edge('crm-sync', 'db:crm'),
    edge('crm-api', 'queue:crm'), edge('crm-worker', 'queue:crm'),
    edge('crm-worker', 'db:shared'), edge('pay-ledger', 'db:shared'),
    edge('pay-api', 'ext:stripe'), edge('loner', 'ext:stripe'),
    edge('loner', 'ext:geo'), edge('loner', 'crm-api'),
  ],
);

function bandsFor(g: ServiceMap) {
  const svc = serviceBands(g.nodes, nsOf);
  const all = new Map(svc);
  for (const [d, ns] of dependencyBands(g.nodes, g.edges, svc)) all.set(d, ns);
  return { svc, all };
}

describe('dependencyBands', () => {
  const { svc, all } = bandsFor(graph);

  it('tek şeritli çağıranlar → bağımlılık o şeritte', () => {
    expect(all.get('db:crm')).toBe('crm');
    expect(all.get('queue:crm')).toBe('crm');
  });
  it('karışık şeritli çağıranlar → grupsuz', () => {
    expect(all.has('db:shared')).toBe(false);
  });
  it('grupsuz çağıran → grupsuz (diğer çağıran adlı şeritte olsa bile)', () => {
    expect(all.has('ext:stripe')).toBe(false);
    expect(all.has('ext:geo')).toBe(false);
  });
  it('çağıranı olmayan bağımlılık grupsuz', () => {
    expect(all.has('db:orphan')).toBe(false);
  });
  it('servis olmayan düğüme şerit atanmaz; servis eşlemesi değişmez', () => {
    expect([...svc.keys()].sort()).toEqual(['crm-api', 'crm-sync', 'crm-worker', 'pay-api', 'pay-ledger']);
  });
  it('katlı çağıran (süper-düğüm grupsuz şeritte) → bağımlılık grupsuz', () => {
    const { data } = foldTopology(graph, nsOf, new Set(['crm']));
    const b = bandsFor(data);
    expect(b.all.has('db:crm')).toBe(false);
    expect(b.all.has('queue:crm')).toBe(false);
    // pay açık kalır; db:shared'in çağıranları artık süper-düğüm + pay → grupsuz
    expect(b.all.has('db:shared')).toBe(false);
    expect(b.all.get('pay-api')).toBe('pay');
  });
});

describe('şerit yerleşimi + başlık sayısı', () => {
  const { svc, all } = bandsFor(graph);
  const r = bfsBarycenterLayout(graph.nodes, graph.edges, 800, 600, PITCH, { bandOf: s => all.get(s) });

  it('bağımlılık kendi şeridinin içinde, katman sütununda; şeritler çakışmaz', () => {
    for (let i = 1; i < r.bands.length; i++) expect(r.bands[i].y0).toBeGreaterThanOrEqual(r.bands[i - 1].y1);
    for (const n of graph.nodes) {
      const b = r.bands.find(x => x.key === (all.get(n.service) ?? null))!;
      const p = r.pos.get(n.service)!;
      expect(p.y - PITCH / 2, n.service).toBeGreaterThanOrEqual(b.y0);
      expect(p.y + PITCH / 2, n.service).toBeLessThanOrEqual(b.y1);
    }
    // ASAP lavabo: en sağdaki çağıranının hemen sağı
    expect(r.layer.get('db:crm')).toBe(r.layer.get('crm-sync')! + 1);
    expect(r.layer.get('queue:crm')).toBe(r.layer.get('crm-worker')! + 1);
    const ung = r.bands.find(b => b.key === null)!;
    expect(r.bands[r.bands.length - 1]).toBe(ung);
  });

  it('başlık servis sayısı bağımlılıkları saymaz (crm = 3 svc, şerit üyesi 5)', () => {
    const c = bandServiceCounts(svc);
    expect(c.get('crm')).toBe(3);
    expect(c.get('pay')).toBe(2);
    expect(r.bands.find(b => b.key === 'crm')!.members).toBe(5);
  });
});
