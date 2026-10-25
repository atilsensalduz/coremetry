// v0.10.1536 — Service → Messaging sekmesi: operatörün Grafana panosu
// "OTel Java - Kafka Client Metrics" ile birebir sorgular.
import { describe, it, expect } from 'vitest';
import { kafkaPanels, kafkaSel, kafkaOptionQueries, kafkaOptionValues, KAFKA_SECTIONS } from './kafkaMetrics';

describe('kafkaSel', () => {
  it('service_name kesin, host/client yalnız seçiliyse', () => {
    expect(kafkaSel({ service: 'svc' }, 'consumer')).toBe('{service_name="svc"}');
    expect(kafkaSel({ service: 'svc', host: 'pod-1', consumer: 'c1', producer: 'p1' }, 'producer'))
      .toBe('{service_name="svc", host_name="pod-1", client_id="p1"}');
  });
  it('kaçış: tırnak ve ters bölü', () => {
    expect(kafkaSel({ service: 'a"b\\c' }, 'consumer')).toBe('{service_name="a\\"b\\\\c"}');
  });
});

describe('kafkaPanels — pano paritesi', () => {
  const ps = kafkaPanels({ service: 'orders' });
  const byId = Object.fromEntries(ps.map(p => [p.id, p]));
  it('pano sorguları birebir (örnekler)', () => {
    expect(byId.consumer_conns.queries[0].expr).toBe('sum(kafka_consumer_connection_count{service_name="orders"})');
    expect(byId.max_lag.queries[0].expr).toBe('max(kafka_consumer_records_lag_max{service_name="orders"})');
    expect(byId.active_pods.queries[0].expr).toBe('count(count by (host_name) (kafka_consumer_connection_count{service_name="orders"}) or count by (host_name) (kafka_producer_connection_count{service_name="orders"}))');
    expect(byId.partition_lag.queries[0].expr).toBe('topk(20, kafka_consumer_records_lag{service_name="orders"})');
    expect(byId.send_error_rate.queries.map(q => q.expr)).toEqual([
      'sum by (host_name, client_id) (kafka_producer_record_send_rate{service_name="orders"})',
      'sum by (host_name, client_id) (kafka_producer_record_error_rate{service_name="orders"})',
    ]);
  });
  it('panodaki her metrik adı kapsanır', () => {
    const all = ps.flatMap(p => p.queries.map(q => q.expr)).join(' ');
    for (const m of [
      'kafka_consumer_connection_creation_rate', 'kafka_consumer_connection_close_rate', 'kafka_consumer_fetch_rate',
      'kafka_consumer_fetch_size_avg', 'kafka_consumer_records_lag_avg', 'kafka_consumer_fetch_latency_max',
      'kafka_consumer_rebalance_rate_per_hour', 'kafka_consumer_rebalance_latency_avg', 'kafka_consumer_failed_rebalance_total',
      'kafka_consumer_assigned_partitions', 'kafka_consumer_last_rebalance_seconds_ago', 'kafka_consumer_fetch_throttle_time_max',
      'kafka_consumer_partition_assigned_latency_max', 'kafka_producer_request_latency_max', 'kafka_producer_record_queue_time_max',
      'kafka_producer_buffer_available_bytes', 'kafka_producer_buffer_total_bytes', 'kafka_producer_bufferpool_wait_time_total',
      'kafka_producer_batch_size_avg', 'kafka_producer_records_per_request_avg', 'kafka_consumer_incoming_byte_rate',
      'kafka_producer_outgoing_byte_rate', 'kafka_consumer_io_ratio', 'kafka_producer_io_ratio', 'kafka_consumer_request_rate',
      'kafka_producer_request_rate', 'kafka_consumer_failed_authentication_total', 'kafka_producer_failed_authentication_total',
    ]) expect(all, m).toContain(m);
  });
  it('kimlikler tekil, her panel bir bölümde', () => {
    expect(new Set(ps.map(p => p.id)).size).toBe(ps.length);
    const secs = new Set(KAFKA_SECTIONS.map(s => s.id));
    for (const p of ps) expect(secs.has(p.section)).toBe(true);
  });
  it('eşik tonları panonunki', () => {
    expect(byId.max_lag.tone!(500)).toBeUndefined();
    expect(byId.max_lag.tone!(1000)).toBe('warn');
    expect(byId.max_lag.tone!(10000)).toBe('err');
    expect(byId.producer_latency.tone!(600)).toBe('err');
    expect(byId.producer_errors.tone!(0)).toBeUndefined();
    expect(byId.producer_errors.tone!(0.5)).toBe('warn');
    expect(byId.consumer_conns.tone!(0)).toBe('err');
    expect(byId.consumer_conns.tone!(3)).toBeUndefined();
    expect(byId.consumer_conns.tone!(null)).toBeUndefined();
  });
});

describe('seçenekler', () => {
  it('sorgular süzgeçsiz, değerler tekil + sıralı', () => {
    expect(kafkaOptionQueries('svc').consumers).toBe('count by (host_name, client_id) (kafka_consumer_connection_count{service_name="svc"})');
    expect(kafkaOptionValues([{ host_name: 'b' }, { host_name: 'a' }, { host_name: 'b' }, {}], 'host_name')).toEqual(['a', 'b']);
  });
});
