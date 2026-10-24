import { describe, it, expect } from 'vitest';
import { pickTraceOpCard, entrySelfFraction, isOwnWorkSpan, isCacheDbSpan, GATEWAY_NAME_RE, type OpCardPickSpan, type OpCardPick } from './traceOpCardPick';

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
      // v0.10.1526 — üretici span'i servisin KENDİ işi (MQ'ya yazmak iş mantığı):
      // eskiden (v0.10.1503) proxy sayılıp tüketiciye gidiyordu.
      name: 'üretici span\'i kendi iş: HTTP giriş → producer → consumer → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'ingest-svc', kind: 'server', httpRoute: '/events', start: 0, end: 10 }),
        sp({ spanId: 'p', parentSpanId: 'r', serviceName: 'ingest-svc', kind: 'producer', name: 'order-events publish', start: 1, end: 9 }),
        sp({ spanId: 'k', parentSpanId: 'p', serviceName: 'billing-worker', kind: 'consumer', name: 'order-events process', start: 5, end: 50 }),
      ],
      want: { ref: { service: 'ingest-svc', path: '/events' }, service: 'ingest-svc', via: 'root' },
    },
    {
      name: 'gRPC yönlendirici → tek alt servis (span adı, entry=rpc)',
      spans: [
        sp({ spanId: 'r', serviceName: 'edge-svc', kind: 'server', httpRoute: '/pay', start: 0, end: 100 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', name: 'payments.Charge', attributes: { 'rpc.system': 'grpc' }, start: 1, end: 99 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'payments-api', kind: 'server', name: 'payments.Charge', start: 2, end: 98 }),
      ],
      want: { ref: { service: 'payments-api', path: 'payments.Charge', entry: 'rpc' }, service: 'payments-api', via: 'structure',
        gateway: { service: 'edge-svc', path: '/pay' } },
    },
    {
      // v0.10.1526 — prod şekli (operatör 2026-10-24): kök kendi tablolarına
      // SELECT/INSERT/UPDATE + MQ'ya üretici, ARTI tek HTTP istemci çağrısı
      // (OTP servisine). Öz süre düşük ve tek alt servis — ama kendi işi var.
      name: 'kök: DB (SELECT/INSERT/UPDATE) + MQ üretici + tek HTTP istemci → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'atm-core', kind: 'server', name: 'POST /atm/core/inquiry', httpRoute: '/atm/core/inquiry', start: 0, end: 100 }),
        sp({ spanId: 'd1', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'SELECT accounts', dbSystem: 'oracle', start: 1, end: 20 }),
        sp({ spanId: 'd2', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'INSERT audit', dbSystem: 'oracle', start: 20, end: 30 }),
        sp({ spanId: 'd3', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'UPDATE limits', attributes: { 'db.system': 'oracle' }, start: 30, end: 40 }),
        sp({ spanId: 'm1', parentSpanId: 'r', serviceName: 'atm-core', kind: 'producer', name: 'ATM.EVENTS send', attributes: { 'messaging.system': 'ibmmq' }, start: 40, end: 45 }),
        sp({ spanId: 'h', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'POST', start: 45, end: 99 }),
        sp({ spanId: 'o', parentSpanId: 'h', serviceName: 'otp-api', kind: 'server', httpRoute: '/otp/send', start: 46, end: 98 }),
      ],
      want: { ref: { service: 'atm-core', path: '/atm/core/inquiry' }, service: 'atm-core', via: 'root' },
    },
    {
      name: 'kök: yalnız DB span\'i (db.system.name, alt span altında) + tek HTTP istemci → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'edge-svc', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', start: 1, end: 99 }),
        sp({ spanId: 'd', parentSpanId: 'c', serviceName: 'edge-svc', kind: 'client', name: 'SELECT', attributes: { 'db.system.name': 'postgresql' }, start: 2, end: 3 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 4, end: 98 }),
      ],
      want: { ref: { service: 'edge-svc', path: '/x' }, service: 'edge-svc', via: 'root' },
    },
    {
      name: 'kök: messaging.system istemci span\'i (Kafka send kind=client) + tek HTTP istemci → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'edge-svc', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'k', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', name: 'orders send', attributes: { 'messaging.system': 'kafka' }, start: 1, end: 2 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', start: 2, end: 99 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 3, end: 98 }),
      ],
      want: { ref: { service: 'edge-svc', path: '/x' }, service: 'edge-svc', via: 'root' },
    },
    {
      // Ad eşleşse de (gevşek yol) kendi DB işi olan servis gateway değil.
      // v0.10.1527 — DB redis'ten postgresql'e: önbellek/KV artık kendi iş değil (aşağıdaki vakalar).
      name: 'gateway adlı kök ama kendi DB span\'i var → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'api-gateway', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'd', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', name: 'SELECT tokens', dbSystem: 'postgresql', start: 0, end: 3 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', start: 3, end: 99 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 4, end: 98 }),
      ],
      want: { ref: { service: 'api-gateway', path: '/x' }, service: 'api-gateway', via: 'root' },
    },
    {
      // v0.10.1527 — prod şekli (operatör 2026-10-24): gateway kökü oran sınırlama /
      // önbellek için iki redis span'i (GET, PTTL) atıp tek BFF'e yönlendiriyor.
      // Önbellek/KV DB'si kendi iş değil → gateway; kart BFF'i gösterir.
      name: 'gateway: yalnız redis (GET, PTTL) + tek HTTP yönlendirme → alt servis (BFF)',
      spans: [
        sp({ spanId: 'r', serviceName: 'apigateway-mobile-prod', kind: 'server', name: 'POST /mobile/transfer', httpRoute: '/mobile/transfer', start: 0, end: 100 }),
        sp({ spanId: 'g', parentSpanId: 'r', serviceName: 'apigateway-mobile-prod', kind: 'client', name: 'GET', dbSystem: 'redis', start: 1, end: 2 }),
        sp({ spanId: 't', parentSpanId: 'r', serviceName: 'apigateway-mobile-prod', kind: 'client', name: 'PTTL', attributes: { 'db.system': 'redis' }, start: 2, end: 3 }),
        sp({ spanId: 'h', parentSpanId: 'r', serviceName: 'apigateway-mobile-prod', kind: 'client', name: 'POST', start: 3, end: 99 }),
        sp({ spanId: 'b', parentSpanId: 'h', serviceName: 'mobile-bff', kind: 'server', httpRoute: '/transfer', start: 4, end: 98 }),
        sp({ spanId: 'bd', parentSpanId: 'b', serviceName: 'mobile-bff', kind: 'client', name: 'SELECT', dbSystem: 'oracle', start: 10, end: 20 }),
      ],
      want: { ref: { service: 'mobile-bff', path: '/transfer' }, service: 'mobile-bff', via: 'structure',
        gateway: { service: 'apigateway-mobile-prod', path: '/mobile/transfer' } },
    },
    {
      // Ad nötr + başka önbellek sistemleri (db.system.name memcached; kind=internal valkey) → yine gateway.
      name: 'yönlendirici: memcached + valkey (internal) + tek HTTP → alt servis',
      spans: [
        sp({ spanId: 'r', serviceName: 'edge-svc', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'm', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', name: 'get', attributes: { 'db.system.name': 'memcached' }, start: 1, end: 2 }),
        sp({ spanId: 'v', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'internal', name: 'GET', attributes: { 'db.system': 'Valkey' }, start: 2, end: 3 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'edge-svc', kind: 'client', start: 3, end: 99 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 4, end: 98 }),
      ],
      want: { ref: { service: 'orders-api', path: '/orders' }, service: 'orders-api', via: 'structure',
        gateway: { service: 'edge-svc', path: '/x' } },
    },
    {
      // Önbellek + ilişkisel DB birlikte: ilişkisel DB kendi iş → kök kalır.
      name: 'redis + SQL (mssql) + tek HTTP → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'atm-core', kind: 'server', httpRoute: '/atm/core/inquiry', start: 0, end: 100 }),
        sp({ spanId: 'g', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'GET', dbSystem: 'redis', start: 1, end: 2 }),
        sp({ spanId: 'q', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'SELECT cards', dbSystem: 'mssql', start: 2, end: 5 }),
        sp({ spanId: 'h', parentSpanId: 'r', serviceName: 'atm-core', kind: 'client', name: 'POST', start: 5, end: 99 }),
        sp({ spanId: 'o', parentSpanId: 'h', serviceName: 'otp-api', kind: 'server', httpRoute: '/otp/send', start: 6, end: 98 }),
      ],
      want: { ref: { service: 'atm-core', path: '/atm/core/inquiry' }, service: 'atm-core', via: 'root' },
    },
    {
      name: 'gateway adlı kök: redis + mongodb (belge DB) + tek HTTP → kök kalır',
      spans: [
        sp({ spanId: 'r', serviceName: 'api-gateway', kind: 'server', httpRoute: '/x', start: 0, end: 100 }),
        sp({ spanId: 'g', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', name: 'GET', dbSystem: 'redis', start: 1, end: 2 }),
        sp({ spanId: 'd', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', name: 'find sessions', attributes: { 'db.system': 'mongodb' }, start: 2, end: 4 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'api-gateway', kind: 'client', start: 4, end: 99 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'orders-api', kind: 'server', httpRoute: '/orders', start: 5, end: 98 }),
      ],
      want: { ref: { service: 'api-gateway', path: '/x' }, service: 'api-gateway', via: 'root' },
    },
    {
      // Saf BFF: yalnız tek HTTP istemci, DB/MQ yok, öz süre düşük → yönlendirici
      // (operatör 2026-10-24: DB/MQ işi olmayan BFF yönlendirici sayılabilir).
      name: 'BFF: yalnız tek HTTP istemci (DB/MQ yok) → alt servis (yönlendirici)',
      spans: [
        sp({ spanId: 'r', serviceName: 'mobile-bff', kind: 'server', httpRoute: '/qr/pay', start: 0, end: 100 }),
        sp({ spanId: 'c', parentSpanId: 'r', serviceName: 'mobile-bff', kind: 'client', name: 'POST', start: 3, end: 97 }),
        sp({ spanId: 'i', parentSpanId: 'c', serviceName: 'cards-switch', kind: 'server', httpRoute: '/switch/pay', start: 4, end: 96 }),
        sp({ spanId: 'db', parentSpanId: 'i', serviceName: 'cards-switch', kind: 'client', name: 'SELECT', dbSystem: 'oracle', start: 10, end: 50 }),
      ],
      want: { ref: { service: 'cards-switch', path: '/switch/pay' }, service: 'cards-switch', via: 'structure',
        gateway: { service: 'mobile-bff', path: '/qr/pay' } },
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
  it('kendi iş sınıflandırıcısı: DB (kolon / db.system / db.system.name) ve mesajlaşma (producer / messaging.system)', () => {
    const b = { serviceName: 'a', start: 0, end: 1 };
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', dbSystem: 'oracle' }))).toBe(true);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', attributes: { 'db.system': 'mysql' } }))).toBe(true);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', attributes: { 'db.system.name': 'postgresql' } }))).toBe(true);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'PRODUCER' }))).toBe(true);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', attributes: { 'messaging.system': 'kafka' } }))).toBe(true);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', attributes: { 'http.request.method': 'POST' } }))).toBe(false);
    expect(isOwnWorkSpan(sp({ ...b, kind: 'client', dbSystem: '  ' }))).toBe(false);
  });
  it('v0.10.1527 — önbellek/KV DB kendi iş DEĞİL; ilişkisel / belge / arama DB kendi iş', () => {
    const b = { serviceName: 'a', start: 0, end: 1, kind: 'client' };
    for (const sys of ['redis', 'REDIS', 'memcached', 'valkey', 'hazelcast', 'aerospike', 'etcd']) {
      expect(isCacheDbSpan(sp({ ...b, dbSystem: sys })), sys).toBe(true);
      expect(isOwnWorkSpan(sp({ ...b, dbSystem: sys })), sys).toBe(false);
      expect(isOwnWorkSpan(sp({ ...b, attributes: { 'db.system.name': sys } })), sys).toBe(false);
    }
    for (const sys of ['oracle', 'mssql', 'postgresql', 'mysql', 'db2', 'mongodb', 'cassandra', 'elasticsearch']) {
      expect(isCacheDbSpan(sp({ ...b, dbSystem: sys })), sys).toBe(false);
      expect(isOwnWorkSpan(sp({ ...b, dbSystem: sys })), sys).toBe(true);
      expect(isOwnWorkSpan(sp({ ...b, attributes: { 'db.system': sys } })), sys).toBe(true);
    }
    expect(isOwnWorkSpan(sp({ ...b, kind: 'producer', dbSystem: 'redis' }))).toBe(true); // üretici her zaman kendi iş
  });
  it('ad deseni listesi (zayıf sinyal)', () => {
    for (const s of ['api-gateway', 'apigw', 'nginx-ingress', 'envoy', 'kong', 'apisix', 'edge-proxy']) expect(GATEWAY_NAME_RE.test(s), s).toBe(true);
    expect(GATEWAY_NAME_RE.test('orders-api')).toBe(false);
  });
});
