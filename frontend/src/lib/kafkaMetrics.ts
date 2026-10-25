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

// v0.10.1540 — operatör: "panoyu baz alarak daha anlamlı, sade grafikler;
// sayı aynı olmak zorunda değil, bazıları çok detay". 30 grafik → 9: açık
// "Sağlık" (akış, lag, gecikme, hata sinyalleri) + kapalı "Ayrıntı".
export type KafkaSection = 'overview' | 'health' | 'detail';
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
  { id: 'health', title: 'Sağlık' },
  { id: 'detail', title: 'Ayrıntı' },
];

/** Seçici: service_name kesin; host/client yalnız seçiliyse (panonun "All"u). */
export function kafkaSel(s: KafkaScope, side: 'consumer' | 'producer'): string {
  const parts = [`service_name=${promStr(s.service)}`];
  if (s.host) parts.push(`host_name=${promStr(s.host)}`);
  const client = side === 'consumer' ? s.consumer : s.producer;
  if (client) parts.push(`client_id=${promStr(client)}`);
  return `{${parts.join(', ')}}`;
}

const CL = '{{client_id}}';

/** Panonun eşik basamakları (Grafana thresholds). */
const ge = (warn: number, err: number) => (v: number | null): KafkaTone =>
  v == null ? undefined : v >= err ? 'err' : v >= warn ? 'warn' : undefined;
const zeroIsBad = (v: number | null): KafkaTone => (v != null && v < 1 ? 'err' : undefined);

export function kafkaPanels(s: KafkaScope): KafkaPanel[] {
  const c = kafkaSel(s, 'consumer');
  const p = kafkaSel(s, 'producer');
  // İstemci (client_id) kırılımı — pod'lar toplanır; pod için üstteki süzgeç.
  // `extra`: Kafka aynı metriği istemci düzeyinde VE topic/node düzeyinde
  // yayınlar; istemci düzeyi `topic=""` / `node_id=""` ile seçilir, yoksa
  // toplam iki kez sayılır (panodaki sum bunu yapıyordu).
  // v0.10.1544 (operatör ekranı: producer-1 seçiliyken hata/gecikme kartı
  // "—") — istemci düzeyi seri her kurulumda yok; varsa o, yoksa `or` ile
  // tüm seriler (o istemci için tek düzey vardır, çift sayım yine olmaz).
  const withM = (sel: string, extra?: string) => (extra ? sel.replace(/}$/, `, ${extra}}`) : sel);
  const pref = (aggBy: string, m: string, sel: string, extra?: string) =>
    (extra ? `${aggBy} (${m}${withM(sel, extra)}) or ${aggBy} (${m}${sel})` : `${aggBy} (${m}${sel})`);
  const byC = (agg: string, m: string, legend: string, extra?: string): KafkaQuery => ({ expr: pref(`${agg} by (client_id)`, m, c, extra), legend });
  const byP = (agg: string, m: string, legend: string, extra?: string): KafkaQuery => ({ expr: pref(`${agg} by (client_id)`, m, p, extra), legend });
  return [
    // ── Genel bakış (stat) ──
    { id: 'consumer_conns', section: 'overview', kind: 'stat', title: 'Consumer bağlantıları', unit: '',
      hint: 'Seçili pod / consumer client’lardaki toplam consumer connection sayısı (0 = bağlı değil).',
      queries: [{ expr: `sum(kafka_consumer_connection_count${c})`, legend: 'consumer conns' }], tone: zeroIsBad },
    { id: 'producer_conns', section: 'overview', kind: 'stat', title: 'Producer bağlantıları', unit: '',
      hint: 'Seçili pod / producer client’lardaki toplam producer connection sayısı (0 = bağlı değil).',
      queries: [{ expr: `sum(kafka_producer_connection_count${p})`, legend: 'producer conns' }], tone: zeroIsBad },
    { id: 'active_pods', section: 'overview', kind: 'stat', title: 'Aktif pod', unit: '',
      hint: 'Metrik gönderen (consumer ya da producer client’ı olan) pod sayısı.',
      queries: [{ expr: `count(count by (host_name) (kafka_consumer_connection_count${c}) or count by (host_name) (kafka_producer_connection_count${p}))`, legend: 'pods' }] },
    { id: 'producer_errors', section: 'overview', kind: 'stat', title: 'Producer kayıt hatası/sn', unit: '',
      hint: 'Producer record error rate toplamı. > 0 ise mesaj kaybı riski var.',
      queries: [{ expr: pref('sum', 'kafka_producer_record_error_rate', p, 'topic=""'), legend: 'errors/s' }], tone: ge(0.001, 1) },
    { id: 'max_lag', section: 'overview', kind: 'stat', title: 'En yüksek consumer lag', unit: '',
      hint: 'kafka_consumer_records_lag_max — en yüksek partition lag değeri.',
      queries: [{ expr: `max(kafka_consumer_records_lag_max${c})`, legend: 'max lag' }], tone: ge(1000, 10000) },
    { id: 'producer_latency', section: 'overview', kind: 'stat', title: 'Producer istek gecikmesi (ort.)', unit: 'ms',
      hint: 'Producer ortalama request latency (ms).',
      queries: [{ expr: pref('avg', 'kafka_producer_request_latency_avg', p, 'node_id=""'), legend: 'avg latency' }], tone: ge(100, 500) },

    // ── Sağlık (açık): dört soru — akıyor mu, geride mi, yavaş mı, hata var mı ──
    { id: 'flow', section: 'health', kind: 'chart', title: 'Produce / Consume — kayıt /sn', unit: 'reqps',
      hint: 'İstemci düzeyi kafka_producer_record_send_rate ve kafka_consumer_records_consumed_rate (topic toplamı; çift sayım yok).',
      queries: [byP('sum', 'kafka_producer_record_send_rate', `produce - ${CL}`, 'topic=""'),
        byC('sum', 'kafka_consumer_records_consumed_rate', `consume - ${CL}`, 'topic=""')] },
    { id: 'lag', section: 'health', kind: 'chart', title: 'Consumer lag (en kötü partition)', unit: '',
      hint: 'kafka_consumer_records_lag_max, istemci başına. 1.000 üstü sarı, 10.000 üstü kırmızı (Genel bakış eşiği).',
      queries: [byC('max', 'kafka_consumer_records_lag_max', CL)] },
    { id: 'latency', section: 'health', kind: 'chart', title: 'Gecikme — producer istek / consumer fetch (maks.)', unit: 'ms',
      hint: 'kafka_producer_request_latency_max ve kafka_consumer_fetch_latency_max (ms). Broker ya da ağ yavaşlığı burada görünür.',
      queries: [byP('max', 'kafka_producer_request_latency_max', `producer - ${CL}`, 'node_id=""'),
        byC('max', 'kafka_consumer_fetch_latency_max', `fetch - ${CL}`)] },
    { id: 'errors', section: 'health', kind: 'chart', title: 'Hata sinyalleri /sn', unit: '',
      hint: 'Gönderim hatası (record_error_rate), başarısız rebalance ve başarısız kimlik doğrulama (sayaçların 5 dk hızı). Hepsi 0 olmalı.',
      queries: [byP('sum', 'kafka_producer_record_error_rate', `gönderim hatası - ${CL}`, 'topic=""'),
        { expr: `sum by (client_id) (rate(kafka_consumer_failed_rebalance_total${c}[5m]))`, legend: `başarısız rebalance - ${CL}` },
        { expr: `sum by (client_id) (rate(kafka_consumer_failed_authentication_total${c}[5m]))`, legend: `auth hatası (consumer) - ${CL}` },
        { expr: `sum by (client_id) (rate(kafka_producer_failed_authentication_total${p}[5m]))`, legend: `auth hatası (producer) - ${CL}` }] },

    // ── Ayrıntı (kapalı, açılınca sorgu) ──
    { id: 'connections', section: 'detail', kind: 'chart', title: 'Açık bağlantı', unit: '',
      hint: 'kafka_consumer_connection_count / kafka_producer_connection_count. Sıfıra düşüş = broker\'a bağlı değil.',
      queries: [byC('sum', 'kafka_consumer_connection_count', `consumer - ${CL}`), byP('sum', 'kafka_producer_connection_count', `producer - ${CL}`)] },
    { id: 'rebalance', section: 'detail', kind: 'chart', title: 'Rebalance /saat', unit: '',
      hint: 'kafka_consumer_rebalance_rate_per_hour. Sürekli rebalance = tüketici gruptan düşüp geri giriyor (poll aralığı, pod yeniden başlatma).',
      queries: [byC('max', 'kafka_consumer_rebalance_rate_per_hour', CL)] },
    { id: 'buffer', section: 'detail', kind: 'chart', title: 'Producer buffer doluluğu %', unit: '%',
      hint: '1 − buffer_available_bytes / buffer_total_bytes. %100\'e yaklaşınca send() bloklanır.',
      queries: [{ expr: `100 * (1 - sum by (client_id) (kafka_producer_buffer_available_bytes${p}) / sum by (client_id) (kafka_producer_buffer_total_bytes${p}))`, legend: CL }] },
    { id: 'bytes', section: 'detail', kind: 'chart', title: 'Ağ trafiği (gelen / giden)', unit: 'bytes',
      hint: 'İstemci düzeyi kafka_consumer_incoming_byte_rate / kafka_producer_outgoing_byte_rate (bayt/sn; broker-node serileri hariç).',
      queries: [byC('sum', 'kafka_consumer_incoming_byte_rate', `gelen - ${CL}`, 'node_id=""'),
        byP('sum', 'kafka_producer_outgoing_byte_rate', `giden - ${CL}`, 'node_id=""')] },
    { id: 'partition_lag', section: 'detail', kind: 'chart', title: 'Partition başına lag (en yüksek 10)', unit: '',
      hint: 'kafka_consumer_records_lag, topic-partition başına (pod\'lar arasında maks.).',
      queries: [{ expr: `topk(10, max by (topic, partition) (kafka_consumer_records_lag${c}))`, legend: '{{topic}}-{{partition}}' }] },
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
