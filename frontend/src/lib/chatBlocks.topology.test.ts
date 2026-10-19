// v0.10.1388 — topologyBlocks şekil kapısı: güvensiz deepLink yükü düşürür,
// güvensiz traceHref silinir (kart kalır), kenarsız / düğümsüz / >60 düğümlü
// yük düşer, ucu düğüm kümesinde olmayan kenar atılır.
import { describe, it, expect } from 'vitest';
import { topologyBlocks, TOPOLOGY_BLOCK_MAX_NODES } from './chatBlocks';
import type { ChatTopologyPayload, ChatTypedBlock, GraphNode } from './types';

const node = (id: string, kind: GraphNode['kind'] = 'service'): GraphNode => ({ id, name: id, kind, calls: 10, errors: 0, errorRate: 0, rate: 1 });
const payload = (over: Partial<ChatTopologyPayload> = {}): ChatTopologyPayload => ({
  focus: 'payment-service', scope: 'neighborhood', hops: 1, window: { fromNs: 1, toNs: 2 }, rangeS: 3600,
  nodes: [node('checkout-service'), node('payment-service'), node('ledger-service')],
  edges: [
    { source: 'checkout-service', target: 'payment-service', calls: 10, errors: 0, errorRate: 0, rate: 1, avgMs: 5, p99Ms: 20 },
    { source: 'payment-service', target: 'ledger-service', calls: 8, errors: 1, errorRate: 12.5, rate: 1, avgMs: 9, p99Ms: 80 },
  ],
  deepLink: '/service?name=payment-service&tab=topology&hops=1',
  truncated: { nodes: 0, edges: 0 },
  ...over,
});
const blk = (p: unknown, type: ChatTypedBlock['type'] = 'topology'): ChatTypedBlock => ({ id: 'b1', type, seq: 1, final: true, payload: p });

describe('topologyBlocks', () => {
  it('geçerli yük geçer', () => {
    const out = topologyBlocks([blk(payload())]);
    expect(out).toHaveLength(1);
    expect(out[0].edges).toHaveLength(2);
  });
  it('güvensiz deepLink yükü düşürür; güvensiz traceHref silinir', () => {
    expect(topologyBlocks([blk(payload({ deepLink: '/\\evil.example' }))])).toHaveLength(0);
    expect(topologyBlocks([blk(payload({ deepLink: '//evil.example' }))])).toHaveLength(0);
    const out = topologyBlocks([blk(payload({ scope: 'trace', traceHref: '//evil.example' }))]);
    expect(out).toHaveLength(1);
    expect(out[0].traceHref).toBeUndefined();
    expect(topologyBlocks([blk(payload({ traceHref: '/trace?id=abc' }))])[0].traceHref).toBe('/trace?id=abc');
  });
  it('bozuk şekiller düşer', () => {
    expect(topologyBlocks([blk(null)])).toHaveLength(0);
    expect(topologyBlocks([blk(payload({ edges: [] }))])).toHaveLength(0);
    expect(topologyBlocks([blk(payload({ nodes: [] }))])).toHaveLength(0);
    expect(topologyBlocks([blk({ ...payload(), scope: 'global' })])).toHaveLength(0);
    const many = Array.from({ length: TOPOLOGY_BLOCK_MAX_NODES + 1 }, (_, i) => node(`svc-${i}`));
    expect(topologyBlocks([blk(payload({ nodes: many }))])).toHaveLength(0);
    expect(topologyBlocks([blk(payload(), 'chart')])).toHaveLength(0);
  });
  it('ucu bilinmeyen kenar atılır', () => {
    const p = payload();
    p.edges.push({ source: 'payment-service', target: 'ghost', calls: 1, errors: 0, errorRate: 0, rate: 1, avgMs: 1, p99Ms: 1 });
    expect(topologyBlocks([blk(p)])[0].edges).toHaveLength(2);
  });
});
