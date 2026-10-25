// v0.10.1539 — topic sayfası: yalnız topic etiketli Kafka metrikleri (operatör:
// "Kafka istemcileri tabında topicle ilgisi olmayan metrikler geliyor").
import { describe, it, expect } from 'vitest';
import { kafkaTopicPanels, kafkaTopicSel, kafkaTopicClientQueries, kafkaTopicClientRows } from './kafkaTopicMetrics';

describe('kafkaTopicSel', () => {
  it('topic kesin, servis isteğe bağlı, kaçış', () => {
    expect(kafkaTopicSel('orders')).toBe('{topic="orders"}');
    expect(kafkaTopicSel('a"b', 'svc')).toBe('{topic="a\\"b", service_name="svc"}');
  });
});

describe('kafkaTopicPanels', () => {
  const ps = kafkaTopicPanels('orders');
  const byId = Object.fromEntries(ps.map(p => [p.id, p]));
  it('HER sorgu topic süzgeçli (istemci düzeyi metrik sızmaz)', () => {
    for (const p of ps) for (const q of p.queries) expect(q.expr, p.id).toContain('{topic="orders"}');
  });
  it('istemci düzeyi (topic etiketsiz) metrikler yok', () => {
    const all = ps.flatMap(p => p.queries.map(q => q.expr)).join(' ');
    for (const m of ['connection_count', 'request_latency', 'rebalance', 'fetch_latency', 'io_ratio']) expect(all).not.toContain(m);
  });
  it('sade: 5 kart, 3 grafik', () => {
    expect(ps.filter(p => p.kind === 'stat').map(p => p.id)).toEqual(['send_rate', 'send_errors', 'consume_rate', 'max_lag', 'min_lead']);
    expect(ps.filter(p => p.kind === 'chart').map(p => p.id)).toEqual(['flow', 'lag_by_client', 'errors_by_client']);
    expect(byId.flow.queries.map(q => q.legend)).toEqual(['produce', 'consume']);
    expect(byId.lag_by_client.queries[0].expr).toBe('max by (service_name, client_id) (kafka_consumer_records_lag_max{topic="orders"})');
  });
  it('eşikler', () => {
    expect(byId.max_lag.tone!(999)).toBeUndefined();
    expect(byId.max_lag.tone!(1000)).toBe('warn');
    expect(byId.max_lag.tone!(10000)).toBe('err');
    expect(byId.send_errors.tone!(0)).toBeUndefined();
    expect(byId.send_errors.tone!(0.1)).toBe('err');
    expect(byId.min_lead.tone!(5000)).toBeUndefined();
    expect(byId.min_lead.tone!(500)).toBe('warn');
    expect(byId.min_lead.tone!(50)).toBe('err');
    expect(byId.min_lead.tone!(null)).toBeUndefined();
  });
});

describe('istemci satırları', () => {
  it('sorgu pod düzeyinde', () => {
    expect(kafkaTopicClientQueries('t').producers).toBe('sum by (service_name, client_id, host_name) (kafka_producer_record_send_rate{topic="t"})');
  });
  it('pod sayısı tekil host, hız toplanır, hıza göre sıralı', () => {
    const rows = kafkaTopicClientRows('consumer', [
      { labels: { service_name: 'a', client_id: 'c1', host_name: 'p1' }, value: 2 },
      { labels: { service_name: 'a', client_id: 'c1', host_name: 'p2' }, value: 3 },
      { labels: { service_name: 'b', client_id: 'c2', host_name: 'p9' }, value: 10 },
      { labels: { service_name: 'c', client_id: 'c3' }, value: null },
    ]);
    expect(rows.map(r => [r.service, r.pods, r.rate])).toEqual([['b', 1, 10], ['a', 2, 5], ['c', 0, null]]);
  });
});
