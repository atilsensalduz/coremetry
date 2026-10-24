import { describe, it, expect } from 'vitest';
import { pickTraceOpCard, entrySelfFraction, GATEWAY_NAME_RE, type OpCardPickSpan, type OpCardPick } from './traceOpCardPick';

// v0.10.1503 — Trace operasyon kartı çipi: gateway kökü → ilk iç servis.
const ms = 1e6;
let n = 0;
function sp(p: Partial<OpCardPickSpan> & { serviceName: string; kind: string; start: number; end: number }): OpCardPickSpan {
  const { start, end, ...rest } = p;
  return { spanId: rest.spanId ?? `s${++n}`, parentSpanId: '', name: '', startTime: start * ms, endTime: end * ms, ...rest };
}

// gateway: giriş 0-100, istemci 2-98 → orders-api girişi 3-97 (öz süre %4)
const gw = (gwSvc = 'edge-gw', extra: OpCardPickSpan[] = []) => [
  sp({ spanId: 'r', serviceName: gwSvc, kind: 'server', name: 'GET /api/orders/*', httpRoute: '/api/orders/*', start: 0, end: 100 }),
  sp({ spanId: 'c', parentSpanId: 'r', serviceName: gwSvc, kind: 'client', name: 'GET', start: 2, end: 98 }),
  sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', name: 'GET /orders/42', httpRoute: '/orders/{id}', start: 3, end: 97 }),
  sp({ spanId: 'db', parentSpanId: 'i', serviceName: 'orders-api', kind: 'client', name: 'SELECT', start: 10, end: 60 }),
  ...extra,
];

describe('pickTraceOpCard', () => {
  const cases: Array<{ name: string; spans: OpCardPickSpan[]; opts?: { gatewayServices?: string[] }; want: OpCardPick | null }> = [
    {
      name: 'gateway kökü (yapısal, ad nötr) → iç servis + gateway bağlantısı',
      spans: gw('edge-svc'),
      want: { ref: { service: 'orders-api', path: '/orders/{id}' }, service: 'orders-api', via: 'structure',
        gateway: { service: 'edge-svc', path: '/api/orders/*' } },
    },
    {
      name: 'BFF kökü (iki alt servis, kendi mantığı) → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'web-bff', kind: 'server', httpRoute: '/home', start: 0, end: 100 }),
        sp({ spanId: 'c1', parentSpanId: 'r', serviceName: 'web-bff', kind: 'client', start: 1, end: 40 }),
        sp({ spanId: 'a', parentSpanId: 'c1', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 2, end: 39 }),
        sp({ spanId: 'c2', parentSpanId: 'r', serviceName: 'web-bff', kind: 'client', start: 41, end: 95 }),
        sp({ spanId: 'b', parentSpanId: 'c2', serviceName: 'users-api', kind: 'server', httpRoute: '/users', start: 42, end: 94 }),
      ],
      want: { ref: { service: 'web-bff', path: '/home' }, service: 'web-bff', via: 'root' },
    },
    {
      name: 'tek alt servisli BFF ama yüksek öz süre + iç span → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'web-bff', kind: 'server', httpRoute: '/home', start: 0, end: 100 }),
        sp({ spanId: 'x', parentSpanId: 'r', serviceName: 'web-bff', kind: 'internal', name: 'render', start: 1, end: 50 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'web-bff', kind: 'client', start: 60, end: 80 }),
        sp({ spanId: 'a', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 61, end: 79 }),
      ],
      want: { ref: { service: 'web-bff', path: '/home' }, service: 'web-bff', via: 'root' },
    },
    {
      name: 'iç giriş yok (gateway adı olsa da yalnız istemci span) → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'api-gateway', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', name: 'GET legacy', start: 1, end: 99 }),
      ],
      want: { ref: { service: 'api-gateway', path: '/x' }, service: 'api-gateway', via: 'root' },
    },
    {
      name: 'tüketici girişi: proxy üretici → consumer (span adı, entry=rpc)',
      spans: [
        sp({ spanId: 'r', serviceName: 'ingest-svc', kind: 'server', httpRoute: '/events', start: 0, end: 10 }),
        sp({ spanId: 'p', parentSpanId: 'r', serviceName: 'ingest-svc', kind: 'producer', name: 'order-events publish', start: 1, end: 9 }),
        sp({ spanId: 'k', parentSpanId: 'p', serviceName: 'billing-worker', kind: 'consumer', name: 'order-events process', start: 5, end: 50 }),
      ],
      want: { ref: { service: 'billing-worker', path: 'order-events process', entry: 'rpc' }, service: 'billing-worker', via: 'structure',
        gateway: { service: 'ingest-svc', path: '/events' } },
    },
    {
      name: 'ad sezgisi yalnız bağ bozucu: ara katman iç span + %40 öz süre, ad eşleşir → iç servis',
      spans: [
        sp({ spanId: 'r', serviceName: 'kong-proxy', kind: 'server', httpRoute: '/pay', start: 0, end: 100 }),
        sp({ spanId: 'pl', parentSpanId: 'r', serviceName: 'kong-proxy', kind: 'internal', name: 'kong.access.plugin.auth', start: 0, end: 5 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'kong-proxy', kind: 'client', start: 40, end: 100 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'payments-api', kind: 'server', name: 'payments.Charge', start: 41, end: 99 }),
      ],
      want: { ref: { service: 'payments-api', path: 'payments.Charge', entry: 'rpc' }, service: 'payments-api', via: 'name',
        gateway: { service: 'kong-proxy', path: '/pay' } },
    },
    {
      name: 'aynı şekil, ad eşleşmez → kök kalır (ad tek başına değil, yapı tek başına da değil)',
      spans: [
        sp({ spanId: 'r', serviceName: 'pay-front', kind: 'server', httpRoute: '/pay', start: 0, end: 100 }),
        sp({ spanId: 'pl', parentSpanId: 'r', serviceName: 'pay-front', kind: 'internal', name: 'auth', start: 0, end: 5 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'pay-front', kind: 'client', start: 40, end: 100 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'payments-api', kind: 'server', name: 'payments.Charge', start: 41, end: 99 }),
      ],
      want: { ref: { service: 'pay-front', path: '/pay' }, service: 'pay-front', via: 'root' },
    },
    {
      name: 'yapılandırılmış liste yapıyı geçersiz kılar (fan-out olsa da)',
      opts: { gatewayServices: ['web-edge'] },
      spans: [
        sp({ spanId: 'r', serviceName: 'web-edge', kind: 'server', httpRoute: '/home', start: 0, end: 100 }),
        sp({ spanId: 'c1', parentSpanId: 'r', serviceName: 'web-edge', kind: 'client', start: 5, end: 40 }),
        sp({ spanId: 'a', parentSpanId: 'c1', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 6, end: 39 }),
        sp({ spanId: 'c2', parentSpanId: 'r', serviceName: 'web-edge', kind: 'client', start: 1, end: 95 }),
        sp({ spanId: 'b', parentSpanId: 'c2', serviceName: 'users-api', kind: 'server', httpRoute: '/users', start: 2, end: 94 }),
      ],
      want: { ref: { service: 'users-api', path: '/users' }, service: 'users-api', via: 'config',
        gateway: { service: 'web-edge', path: '/home' } },
    },
    {
      name: 'zincir: ingress → api-gateway → servis (en dış gateway bağlantıda)',
      spans: [
        sp({ spanId: 'r', serviceName: 'nginx-ingress', kind: 'server', name: 'GET', start: 0, end: 100 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'nginx-ingress', kind: 'client', start: 1, end: 99 }),
        sp({ spanId: 'g', parentSpanId: 'c', serviceName: 'api-gateway', kind: 'server', httpRoute: '/api/*', start: 2, end: 98 }),
        sp({ spanId: 'gc', parentSpanId: 'g', serviceName: 'api-gateway', kind: 'client', start: 3, end: 97 }),
        sp({ spanId: 'i', parentSpanId: 'gc', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 4, end: 96 }),
      ],
      want: { ref: { service: 'orders-api', path: '/orders' }, service: 'orders-api', via: 'structure',
        gateway: { service: 'nginx-ingress', path: 'GET', entry: 'rpc' } },
    },
    {
      name: 'istemci kökü → null (bugünkü davranış)',
      spans: [sp({ spanId: 'r', serviceName: 'cron', kind: 'client', name: 'GET', start: 0, end: 1 })],
      want: null,
    },
    { name: 'boş liste → null', spans: [], want: null },
  ];
  for (const c of cases) {
    it(c.name, () => expect(pickTraceOpCard(c.spans, c.opts)).toEqual(c.want));
  }

  it('en erken iç giriş seçilir (aynı alt servise iki çağrı)', () => {
    const got = pickTraceOpCard(gw('edge-svc', [
      sp({ spanId: 'c0', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', start: 1, end: 2 }),
      sp({ spanId: 'i0', parentSpanId: 'c0', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders/auth', start: 1, end: 2 }),
    ]));
    expect(got?.ref).toEqual({ service: 'orders-api', path: '/orders/auth' });
  });
  it('öz süre: örtüşen çocuklar birleşik sayılır, kök dışı kırpılır', () => {
    const root = sp({ serviceName: 'a', kind: 'server', start: 0, end: 100 });
    expect(entrySelfFraction(root, [
      sp({ serviceName: 'a', kind: 'client', start: 10, end: 50 }),
      sp({ serviceName: 'a', kind: 'client', start: 30, end: 70 }),
      sp({ serviceName: 'a', kind: 'client', start: 90, end: 200 }),
    ])).toBeCloseTo(0.3);
    expect(entrySelfFraction(sp({ serviceName: 'a', kind: 'server', start: 5, end: 5 }), [])).toBe(0);
  });
  it('ad deseni listesi (zayıf sinyal)', () => {
    for (const s of ['api-gateway', 'apigw', 'nginx-ingress', 'envoy', 'kong', 'apisix', 'edge-proxy']) expect(GATEWAY_NAME_RE.test(s), s).toBe(true);
    expect(GATEWAY_NAME_RE.test('orders-api')).toBe(false);
  });
});
