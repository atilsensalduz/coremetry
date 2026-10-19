// @vitest-environment jsdom
//
// v0.10.1388 — ChatTopologyCard render sözleşmesi + chatTopologyView (sütun
// yerleşimi, renk eşikleri): başlık, pil düğümleri, "Topology'de aç" / "Trace'i
// aç" linkleri, bütçe notu; odak sütunu ortada, trace kipinde kök solda.
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { ChatTopologyCard } from './ChatTopologyCard';
import { topologyColumns, topologyPillEdges, topologyPillNodes, topoLevel } from './chatTopologyView';
import type { ChatTopologyPayload, GraphNode } from '@/lib/types';

class NoopResizeObserver { observe() {} unobserve() {} disconnect() {} }

const node = (id: string, errorRate = 0, kind: GraphNode['kind'] = 'service'): GraphNode => ({ id, name: id.replace(/^ext:/, ''), kind, calls: 1200, errors: 0, errorRate, rate: 1 });
const fixture = (over: Partial<ChatTopologyPayload> = {}): ChatTopologyPayload => ({
  focus: 'payment-service', scope: 'neighborhood', hops: 1, window: { fromNs: 1, toNs: 2 }, rangeS: 3600,
  nodes: [node('checkout-service'), node('payment-service', 0.4), node('ledger-service', 6.1), node('ext:api.stripe.example', 0, 'external')],
  edges: [
    { source: 'checkout-service', target: 'payment-service', calls: 1200, errors: 5, errorRate: 0.4, rate: 20, avgMs: 40, p99Ms: 320 },
    { source: 'payment-service', target: 'ledger-service', calls: 900, errors: 55, errorRate: 6.1, rate: 15, avgMs: 90, p99Ms: 1200 },
    { source: 'payment-service', target: 'ext:api.stripe.example', calls: 300, errors: 4, errorRate: 1.3, rate: 5, avgMs: 200, p99Ms: 800 },
  ],
  deepLink: '/service?name=payment-service&tab=topology&hops=1',
  truncated: { nodes: 2, edges: 3 },
  ...over,
});

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  if (!('ResizeObserver' in globalThis)) (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = NoopResizeObserver;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});
const render = (p: ChatTopologyPayload) => act(() => { root.render(<MemoryRouter><ChatTopologyCard topo={p} /></MemoryRouter>); });
const hrefs = () => Array.from(host.querySelectorAll('a')).map(a => a.getAttribute('href'));

describe('ChatTopologyCard', () => {
  it('başlık, düğümler, linkler, bütçe notu', () => {
    render(fixture());
    expect(host.querySelector('.ev-head')?.textContent).toContain('Topoloji · payment-service · 1 hop · son 1 sa');
    expect(host.querySelectorAll('.topo-node')).toHaveLength(4);
    expect(host.querySelector('.topo-node.focus')?.textContent).toContain('payment-service');
    expect(host.querySelector('.topo-dot.red')).not.toBeNull();
    expect(hrefs()).toEqual(['/service?name=payment-service&tab=topology&hops=1']);
    expect(host.textContent).toContain('+2 düğüm, +3 kenar gösterilmedi');
    expect(host.querySelector('button')).toBeNull(); // ham düğme yok (buttonUnity)
  });
  it('trace kipi: operasyon başlıkta, trace linki', () => {
    render(fixture({ scope: 'trace', hops: 0, operation: 'PAYMENT_CREATE', traceId: '0af7651916cd43dd8448eb211c80319c', traceHref: '/trace?id=0af7651916cd43dd8448eb211c80319c', truncated: { nodes: 0, edges: 0 } }));
    expect(host.querySelector('.ev-head')?.textContent).toContain('örnek trace (PAYMENT_CREATE)');
    expect(hrefs()).toContain('/trace?id=0af7651916cd43dd8448eb211c80319c');
    expect(host.textContent).not.toContain('gösterilmedi');
  });
});

describe('chatTopologyView', () => {
  it('odak sütunu: çağıran solda, bağımlılıklar sağda', () => {
    expect(topologyColumns(fixture())).toEqual([['checkout-service'], ['payment-service'], ['ledger-service', 'ext:api.stripe.example']]);
  });
  it('trace kipi: kökten BFS derinliği; ulaşılmayan düğüm en sağda', () => {
    const p = fixture({ scope: 'trace', nodes: [...fixture().nodes, node('orphan-service')] });
    expect(topologyColumns(p)).toEqual([['checkout-service', 'orphan-service'], ['payment-service'], ['ledger-service', 'ext:api.stripe.example']]);
  });
  it('renk eşikleri + alt satır', () => {
    expect([topoLevel(0.5), topoLevel(1), topoLevel(5)]).toEqual(['green', 'amber', 'red']);
    expect(topologyPillEdges(fixture()).map(e => e.level)).toEqual([undefined, 'err', 'warn']);
    const ledger = topologyPillNodes(fixture()).find(n => n.id === 'ledger-service')!;
    expect(ledger.sub).toContain('p99 1.20 s');
    const stripe = topologyPillNodes(fixture()).find(n => n.id === 'ext:api.stripe.example')!;
    expect(stripe.title).toContain('(dış)');
  });
});
