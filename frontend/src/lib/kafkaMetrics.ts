// kafkaMetrics — v0.10.1536 (operatör: "Service sayfasında Messaging diye bir
// tab olsun, bu dashboardtaki gibi metrikleri o servis için VictoriaMetrics'ten
// çeksin"). Kaynak: operatörün Grafana panosu "OTel Java - Kafka Client
// Metrics" — sorgular panodakiyle BİREBİR (metrik adları, toplama, lejant
// şablonu), yalnız değişkenler Coremetry'den gelir:
//
//   $service_name        → servis sayfasının servisi (kesin eşleşme)
//   $host_name           → ?khost (boş = hepsi; panonun "All" = .* karşılığı)
//   $consumer_client_id  → ?kcons
//   $producer_client_id  → ?kprod
//
// Neden mevcut istemci paneli (vmetrics.KafkaServiceQuestions) değil: o panel
// metrikleri OTel nokta adlarıyla (kafka.producer.record_send_rate) ve çeviri
// katmanıyla soruyor; operatör "metrikler gözükmüyor ya da hatalı" diyor.
// Pano VM'deki GERÇEK adlarla (kafka_producer_record_send_rate, service_name,
// host_name, client_id) çalışıyor — aynı sorgular aynı sonucu verir.
//
// SAF: sorgu kurucu + lejant + eşik tonları; tablo testli. Sorgular VM'e
// /api/metrics/promql üzerinden gider (useHttpPromql, 30 sn sunucu önbelleği).
import { promStr } from './httpMetrics';

export interface KafkaScope {
  service: string;
  host?: string;
  consumer?: string;
  producer?: string;
}

export type KafkaSection = 'overview' | 'connections' | 'consumer' | 'producer' | 'network';
export type KafkaTone = 'err' | 'warn' | undefined;

export interface KafkaQuery { expr: string; legend: string }

export interface KafkaPanel {
  id: string;
  section: KafkaSection;
  kind: 'stat' | 'chart';
  title: string;
  /** Panonun açıklaması (Türkçe; kartın title'ı). */
  hint: string;
  /** Grafik birimi (lib/chartFmt fmtSmart): '', 'ms', 's', 'bytes', 'reqps'… */
  unit: string;
  queries: KafkaQuery[];
  /** Yalnız stat: panonun eşik basamakları. */
  tone?: (v: number | null) => KafkaTone;
}

export const KAFKA_SECTIONS: { id: KafkaSection; title: string }[] = [
  { id: 'overview', title: 'Genel bakış' },
  { id: 'connections', title: 'Bağlantılar' },
  { id: 'consumer', title: 'Tüketici (consumer)' },
  { id: 'producer', title: 'Üretici (producer)' },
  { id: 'network', title: 'Ağ ve I/O' },
];

/** Seçici: service_name kesin; host/client yalnız seçiliyse (panonun "All"u). */
export function kafkaSel(s: KafkaScope, side: 'consumer' | 'producer'): string {
  const parts = [`service_name=${promStr(s.service)}`];
  if (s.host) parts.push(`host_name=${promStr(s.host)}`);
  const client = side === 'consumer' ? s.consumer : s.producer;
  if (client) parts.push(`client_id=${promStr(client)}`);
  return `{${parts.join(', ')}}`;
}

const HC = '{{host_name}}/{{client_id}}';

/** Panonun eşik basamakları (Grafana thresholds). */
const ge = (warn: number, err: number) => (v: number | null): KafkaTone =>
  v == null ? undefined : v >= err ? 'err' : v >= warn ? 'warn' : undefined;
const zeroIsBad = (v: number | null): KafkaTone => (v != null && v < 1 ? 'err' : undefined);

export function kafkaPanels(s: KafkaScope): KafkaPanel[] {
  const c = kafkaSel(s, 'consumer');
  const p = kafkaSel(s, 'producer');
  const byC = (agg: string, m: string, legend: string): KafkaQuery => ({ expr: `${agg} by (host_name, client_id) (${m}${c})`, legend });
  const byP = (agg: string, m: string, legend: string): KafkaQuery => ({ expr: `${agg} by (host_name, client_id) (${m}${p})`, legend });
  return [
    // ── Genel bakış (stat) ──
    { id: 'consumer_conns', section: 'overview', kind: 'stat', title: 'Tüketici bağlantıları', unit: '',
      hint: 'Seçili pod / consumer client’lardaki toplam consumer connection sayısı (0 = bağlı değil).',
      queries: [{ expr: `sum(kafka_consumer_connection_count${c})`, legend: 'consumer conns' }], tone: zeroIsBad },
    { id: 'producer_conns', section: 'overview', kind: 'stat', title: 'Üretici bağlantıları', unit: '',
      hint: 'Seçili pod / producer client’lardaki toplam producer connection sayısı (0 = bağlı değil).',
      queries: [{ expr: `sum(kafka_producer_connection_count${p})`, legend: 'producer conns' }], tone: zeroIsBad },
    { id: 'active_pods', section: 'overview', kind: 'stat', title: 'Aktif pod', unit: '',
      hint: 'Metrik gönderen (consumer ya da producer client’ı olan) pod sayısı.',
      queries: [{ expr: `count(count by (host_name) (kafka_consumer_connection_count${c}) or count by (host_name) (kafka_producer_connection_count${p}))`, legend: 'pods' }] },
    { id: 'producer_errors', section: 'overview', kind: 'stat', title: 'Üretici kayıt hatası/sn', unit: '',
      hint: 'Producer record error rate toplamı. > 0 ise mesaj kaybı riski var.',
      queries: [{ expr: `sum(kafka_producer_record_error_rate${p})`, legend: 'errors/s' }], tone: ge(0.001, 1) },
    { id: 'max_lag', section: 'overview', kind: 'stat', title: 'En yüksek tüketici lag', unit: '',
      hint: 'kafka_consumer_records_lag_max — en yüksek partition lag değeri.',
      queries: [{ expr: `max(kafka_consumer_records_lag_max${c})`, legend: 'max lag' }], tone: ge(1000, 10000) },
    { id: 'producer_latency', section: 'overview', kind: 'stat', title: 'Üretici istek gecikmesi (ort.)', unit: 'ms',
      hint: 'Producer ortalama request latency (ms).',
      queries: [{ expr: `avg(kafka_producer_request_latency_avg${p})`, legend: 'avg latency' }], tone: ge(100, 500) },

    // ── Bağlantılar ──
    { id: 'consumer_conn_series', section: 'connections', kind: 'chart', title: 'Tüketici bağlantı sayısı', unit: '',
      hint: 'Pod / consumer client_id bazında kafka_consumer_connection_count.',
      queries: [byC('sum', 'kafka_consumer_connection_count', HC)] },
    { id: 'producer_conn_series', section: 'connections', kind: 'chart', title: 'Üretici bağlantı sayısı', unit: '',
      hint: 'Pod / producer client_id bazında kafka_producer_connection_count.',
      queries: [byP('sum', 'kafka_producer_connection_count', HC)] },
    { id: 'consumer_churn', section: 'connections', kind: 'chart', title: 'Tüketici bağlantı açma / kapama /sn', unit: '',
      hint: 'Yeni kurulan / kapanan bağlantı oranı. Yüksek değer connection churn (reconnect fırtınası) işaretidir.',
      queries: [byC('sum', 'kafka_consumer_connection_creation_rate', `açılan - ${HC}`), byC('sum', 'kafka_consumer_connection_close_rate', `kapanan - ${HC}`)] },
    { id: 'producer_churn', section: 'connections', kind: 'chart', title: 'Üretici bağlantı açma / kapama /sn', unit: '',
      hint: 'Producer tarafında connection churn.',
      queries: [byP('sum', 'kafka_producer_connection_creation_rate', `açılan - ${HC}`), byP('sum', 'kafka_producer_connection_close_rate', `kapanan - ${HC}`)] },

    // ── Tüketici ──
    { id: 'fetch_rate', section: 'consumer', kind: 'chart', title: 'Fetch /sn', unit: 'reqps',
      hint: 'kafka_consumer_fetch_rate: saniyedeki fetch request sayısı.',
      queries: [byC('sum', 'kafka_consumer_fetch_rate', `fetch/s - ${HC}`)] },
    { id: 'fetch_size', section: 'consumer', kind: 'chart', title: 'Fetch başına ortalama boyut', unit: 'bytes',
      hint: 'kafka_consumer_fetch_size_avg — fetch başına ortalama byte.',
      queries: [byC('avg', 'kafka_consumer_fetch_size_avg', `fetch size avg - ${HC}`)] },
    { id: 'lag', section: 'consumer', kind: 'chart', title: 'Tüketici kayıt lag (maks / ort.)', unit: '',
      hint: 'kafka_consumer_records_lag_max / _avg. Tüketim geride kalıyorsa buradan görünür.',
      queries: [byC('max', 'kafka_consumer_records_lag_max', `max - ${HC}`), byC('avg', 'kafka_consumer_records_lag_avg', `avg - ${HC}`)] },
    { id: 'fetch_latency', section: 'consumer', kind: 'chart', title: 'Fetch gecikmesi', unit: 'ms',
      hint: 'kafka_consumer_fetch_latency_avg / _max (ms).',
      queries: [byC('avg', 'kafka_consumer_fetch_latency_avg', `avg - ${HC}`), byC('max', 'kafka_consumer_fetch_latency_max', `max - ${HC}`)] },
    { id: 'rebalance_rate', section: 'consumer', kind: 'chart', title: 'Rebalance /saat', unit: '',
      hint: 'kafka_consumer_rebalance_rate_per_hour. Sık rebalance = tüketim kesintisi.',
      queries: [byC('sum', 'kafka_consumer_rebalance_rate_per_hour', `rebalance/saat - ${HC}`)] },
    { id: 'rebalance_latency', section: 'consumer', kind: 'chart', title: 'Rebalance süresi (ort.)', unit: 'ms',
      hint: 'kafka_consumer_rebalance_latency_avg (ms).',
      queries: [byC('avg', 'kafka_consumer_rebalance_latency_avg', `rebalance latency - ${HC}`)] },
    { id: 'failed_rebalance', section: 'consumer', kind: 'chart', title: 'Başarısız rebalance', unit: '',
      hint: 'kafka_consumer_failed_rebalance_rate_per_hour / _total. > 0 ise group join sorunları var demektir.',
      queries: [byC('sum', 'kafka_consumer_failed_rebalance_rate_per_hour', `başarısız/saat - ${HC}`), byC('sum', 'kafka_consumer_failed_rebalance_total', `başarısız toplam - ${HC}`)] },
    { id: 'assigned_partitions', section: 'consumer', kind: 'chart', title: 'Atanmış partition', unit: '',
      hint: 'kafka_consumer_assigned_partitions.',
      queries: [byC('sum', 'kafka_consumer_assigned_partitions', `partitions - ${HC}`)] },
    { id: 'last_rebalance', section: 'consumer', kind: 'chart', title: 'Son rebalance’tan beri', unit: 's',
      hint: 'kafka_consumer_last_rebalance_seconds_ago.',
      queries: [byC('max', 'kafka_consumer_last_rebalance_seconds_ago', `son rebalance - ${HC}`)] },
    { id: 'fetch_throttle', section: 'consumer', kind: 'chart', title: 'Fetch kota kısıtı (throttle)', unit: 'ms',
      hint: 'kafka_consumer_fetch_throttle_time_avg / _max. > 0 ise broker quota throttling uyguluyor.',
      queries: [byC('avg', 'kafka_consumer_fetch_throttle_time_avg', `avg - ${HC}`), byC('max', 'kafka_consumer_fetch_throttle_time_max', `max - ${HC}`)] },
    { id: 'partition_lag', section: 'consumer', kind: 'chart', title: 'Partition başına lag (en yüksek 20)', unit: '',
      hint: 'kafka_consumer_records_lag — partition bazında anlık lag. En yüksek 20 seri.',
      queries: [{ expr: `topk(20, kafka_consumer_records_lag${c})`, legend: '{{topic}}-{{partition}} ({{host_name}})' }] },
    { id: 'partition_assigned_latency', section: 'consumer', kind: 'chart', title: 'Partition atama süresi', unit: 'ms',
      hint: 'kafka_consumer_partition_assigned_latency_avg / _max. Rebalance sonrası partition atama süresi.',
      queries: [byC('avg', 'kafka_consumer_partition_assigned_latency_avg', `avg - ${HC}`), byC('max', 'kafka_consumer_partition_assigned_latency_max', `max - ${HC}`)] },

    // ── Üretici ──
    { id: 'send_error_rate', section: 'producer', kind: 'chart', title: 'Gönderilen kayıt ve hata /sn', unit: '',
      hint: 'Gönderilen kayıt oranı ve hata oranı.',
      queries: [byP('sum', 'kafka_producer_record_send_rate', `gönderim/sn - ${HC}`), byP('sum', 'kafka_producer_record_error_rate', `hata/sn - ${HC}`)] },
    { id: 'request_latency', section: 'producer', kind: 'chart', title: 'Üretici istek gecikmesi', unit: 'ms',
      hint: 'kafka_producer_request_latency_avg / _max (ms).',
      queries: [byP('avg', 'kafka_producer_request_latency_avg', `avg - ${HC}`), byP('max', 'kafka_producer_request_latency_max', `max - ${HC}`)] },
    { id: 'queue_time', section: 'producer', kind: 'chart', title: 'Kayıt kuyruk süresi', unit: 'ms',
      hint: 'kafka_producer_record_queue_time_avg / _max (ms) — gönderilmeden önce buffer’da bekleme.',
      queries: [byP('avg', 'kafka_producer_record_queue_time_avg', `avg - ${HC}`), byP('max', 'kafka_producer_record_queue_time_max', `max - ${HC}`)] },
    { id: 'buffer_bytes', section: 'producer', kind: 'chart', title: 'Üretici buffer (boş / toplam)', unit: 'bytes',
      hint: 'kafka_producer_buffer_available_bytes / _total_bytes. Boş alan sıfıra yaklaşırsa gönderim bloklanır.',
      queries: [byP('sum', 'kafka_producer_buffer_available_bytes', `boş - ${HC}`), byP('sum', 'kafka_producer_buffer_total_bytes', `toplam - ${HC}`)] },
    { id: 'bufferpool_wait', section: 'producer', kind: 'chart', title: 'Buffer pool bekleme süresi (toplam)', unit: '',
      hint: 'kafka_producer_bufferpool_wait_time_total — buffer boşalmasını bekleyen toplam süre (artıyorsa gönderim bekliyor).',
      queries: [byP('sum', 'kafka_producer_bufferpool_wait_time_total', HC)] },
    { id: 'batch_size', section: 'producer', kind: 'chart', title: 'Ortalama batch boyutu', unit: 'bytes',
      hint: 'kafka_producer_batch_size_avg.',
      queries: [byP('avg', 'kafka_producer_batch_size_avg', `batch size - ${HC}`)] },
    { id: 'records_per_request', section: 'producer', kind: 'chart', title: 'İstek başına kayıt (ort.)', unit: '',
      hint: 'kafka_producer_records_per_request_avg.',
      queries: [byP('avg', 'kafka_producer_records_per_request_avg', `records/request - ${HC}`)] },

    // ── Ağ ve I/O ──
    { id: 'byte_rate', section: 'network', kind: 'chart', title: 'Ağ trafiği (tüketici gelen / üretici giden)', unit: 'bytes',
      hint: 'kafka_consumer_incoming_byte_rate / kafka_producer_outgoing_byte_rate (byte/sn).',
      queries: [byC('sum', 'kafka_consumer_incoming_byte_rate', `gelen - ${HC}`), byP('sum', 'kafka_producer_outgoing_byte_rate', `giden - ${HC}`)] },
    { id: 'io_ratio', section: 'network', kind: 'chart', title: 'İstemci I/O oranı', unit: '',
      hint: 'kafka_*_io_ratio — I/O thread’inin I/O’da geçirdiği zaman oranı (0–1).',
      queries: [byC('avg', 'kafka_consumer_io_ratio', `consumer - ${HC}`), byP('avg', 'kafka_producer_io_ratio', `producer - ${HC}`)] },
    { id: 'request_rate', section: 'network', kind: 'chart', title: 'Broker istek /sn', unit: 'reqps',
      hint: 'kafka_*_request_rate — broker’a giden istek oranı.',
      queries: [byC('sum', 'kafka_consumer_request_rate', `consumer - ${HC}`), byP('sum', 'kafka_producer_request_rate', `producer - ${HC}`)] },
    { id: 'failed_auth', section: 'network', kind: 'chart', title: 'Başarısız kimlik doğrulama (toplam)', unit: '',
      hint: 'kafka_*_failed_authentication_total. Artıyorsa SASL/SSL kimlik sorunu var.',
      queries: [byC('sum', 'kafka_consumer_failed_authentication_total', `consumer - ${HC}`), byP('sum', 'kafka_producer_failed_authentication_total', `producer - ${HC}`)] },
  ];
}

/** Süzgeç seçenekleri: host + consumer/producer client_id (anlık, süzgeçsiz). */
export function kafkaOptionQueries(service: string): { consumers: string; producers: string } {
  const sel = `{service_name=${promStr(service)}}`;
  return {
    consumers: `count by (host_name, client_id) (kafka_consumer_connection_count${sel})`,
    producers: `count by (host_name, client_id) (kafka_producer_connection_count${sel})`,
  };
}

/** Seçenek listeleri: seri etiketlerinden tekil + sıralı değerler. */
export function kafkaOptionValues(rows: Record<string, string>[], key: string): string[] {
  return [...new Set(rows.map(r => r[key]).filter((v): v is string => !!v))].sort();
}
