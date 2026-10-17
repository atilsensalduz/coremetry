// httpMetrics.ts — v0.10.1244 (operatör: "OpenTelemetry HTTP Metrics"
// Grafana panosunun aynısı servis sayfasında, VictoriaMetrics'ten). SAF:
// PromQL üreticileri, etiket ayrıştırma, lejant ve tablo birleştirme.
//
// Sorgular panodakiyle aynı (http_server_* inbound, http_client_* downstream;
// service_name / http_route / http_request_method / http_response_status_code /
// server_address / error_type etiketleri). Grafana değişkenlerinin karşılığı:
//   $app            → servis adı
//   $route/$method  → seçiliyse tam eşitlik, değilse süzgeç yok
//   $__rate_interval→ rateWindow(step)
//   $__range        → pencere saniyesi (range sorgusunun SON noktası okunur)
import type { SpanMetricSeries } from './types';

export interface HttpScope { app: string; route?: string; method?: string }

/** PromQL dize kaçışı (" ve \). */
export function promStr(v: string): string {
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

/** Inbound seçici: service_name + isteğe bağlı route/method. */
export function serverSel(s: HttpScope, extra = ''): string {
  const parts = [`service_name=${promStr(s.app)}`];
  if (s.route) parts.push(`http_route=${promStr(s.route)}`);
  if (s.method) parts.push(`http_request_method=${promStr(s.method)}`);
  if (extra) parts.push(extra);
  return `{${parts.join(', ')}}`;
}

/** Downstream seçici (panoda route/method süzgeci yok). */
export function clientSel(s: HttpScope, extra = ''): string {
  return `{service_name=${promStr(s.app)}${extra ? ', ' + extra : ''}}`;
}

/** Grafik nokta bütçesi; adım = ceil(pencere / bu). */
export const HTTP_METRICS_POINTS = 300;

export function stepSeconds(windowSec: number): number {
  return Math.max(1, Math.ceil(windowSec / HTTP_METRICS_POINTS));
}

/** $__rate_interval karşılığı: en az 1 dk, en az 4 adım. */
export function rateWindow(windowSec: number): string {
  return `${Math.max(60, 4 * stepSeconds(windowSec))}s`;
}

const SC = 'http_server_request_duration_seconds_count';
const SB = 'http_server_request_duration_seconds_bucket';
const SS = 'http_server_request_duration_seconds_sum';
const CC = 'http_client_request_duration_seconds_count';
const CB = 'http_client_request_duration_seconds_bucket';
const CS = 'http_client_request_duration_seconds_sum';

export interface HttpQuery { id: string; expr: string }

/** Panodaki sorguların tamamı (id → PromQL). W = pencere, R = rate penceresi. */
export function httpQueries(s: HttpScope, windowSec: number): Record<string, string> {
  const R = rateWindow(windowSec);
  const W = `${Math.max(60, Math.round(windowSec))}s`;
  const sel = (x = '') => serverSel(s, x);
  const csel = (x = '') => clientSel(s, x);
  const rate = (m: string, x = '') => `rate(${m}${sel(x)}[${R}])`;
  const crate = (m: string, x = '') => `rate(${m}${csel(x)}[${R}])`;
  const inc = (m: string, x = '') => `increase(${m}${sel(x)}[${W}])`;
  const cinc = (m: string, x = '') => `increase(${m}${csel(x)}[${W}])`;
  const q = (p: number, by = 'le') => `histogram_quantile(${p}, sum by (${by}) (${rate(SB)}))`;
  const cq = (p: number, by = 'le') => `histogram_quantile(${p}, sum by (${by}) (${crate(CB)}))`;
  const byMR = 'http_request_method, http_route';
  const byDM = 'server_address, http_request_method';
  return {
    // Genel bakış
    total: `sum(${inc(SC)})`,
    rps: `sum(${rate(SC)})`,
    err5: `(sum(${rate(SC, 'http_response_status_code=~"5.."')}) or vector(0)) / sum(${rate(SC)})`,
    err4: `(sum(${rate(SC, 'http_response_status_code=~"4.."')}) or vector(0)) / sum(${rate(SC)})`,
    p95: q(0.95),
    sli: `sum(${rate(SB, 'le="0.5"')}) / sum(${rate(SC)})`,
    // Trafik
    rateByRoute: `sum by (${byMR}) (${rate(SC)})`,
    topRoutes: `topk(10, sum by (http_route) (${inc(SC)}))`,
    statusClass: `sum by (cls) (label_replace(${rate(SC)}, "cls", "\${1}xx", "http_response_status_code", "([0-9]).*"))`,
    statusCodes: `sum by (http_response_status_code) (${inc(SC)})`,
    // Gecikme
    p50: q(0.5), p90: q(0.9), p99: q(0.99),
    p95ByRoute: q(0.95, `le, ${byMR}`),
    // Hatalar
    err5ByRoute: `sum by (http_route, http_request_method, http_response_status_code) (${rate(SC, 'http_response_status_code=~"5.."')}) > 0`,
    err4ByRoute: `sum by (http_route, http_request_method, http_response_status_code) (${rate(SC, 'http_response_status_code=~"4.."')}) > 0`,
    // Endpoint özeti
    tRps: `sum by (${byMR}) (rate(${SC}${sel()}[5m]))`, // panodaki gibi [5m]
    tTotal: `sum by (${byMR}) (${inc(SC)})`,
    tAvg: `sum by (${byMR}) (${inc(SS)}) / sum by (${byMR}) (${inc(SC)})`,
    tP95: `histogram_quantile(0.95, sum by (le, ${byMR}) (${inc(SB)}))`,
    tP99: `histogram_quantile(0.99, sum by (le, ${byMR}) (${inc(SB)}))`,
    tErr5: `(sum by (${byMR}) (${inc(SC, 'http_response_status_code=~"5.."')}) or (0 * sum by (${byMR}) (${inc(SC)}))) / sum by (${byMR}) (${inc(SC)})`,
    tErr4: `(sum by (${byMR}) (${inc(SC, 'http_response_status_code=~"4.."')}) or (0 * sum by (${byMR}) (${inc(SC)}))) / sum by (${byMR}) (${inc(SC)})`,
    // Downstream
    dRps: `sum(${crate(CC)})`,
    dErr: `(sum(${crate(CC, 'http_response_status_code=~"5..|"')}) or vector(0)) / sum(${crate(CC)})`,
    dP95: cq(0.95),
    dConnErr: `sum(${cinc(CC, 'http_response_status_code=""')}) or vector(0)`,
    dRateByAddr: `sum by (server_address) (${crate(CC)})`,
    dP95ByAddr: cq(0.95, 'le, server_address'),
    dErrByAddr: `sum by (server_address) (${crate(CC, 'http_response_status_code=~"5..|"')}) / sum by (server_address) (${crate(CC)})`,
    dErrTypes: `sum by (server_address, error_type) (${crate(CC, 'error_type!=""')}) > 0`,
    dtTotal: `sum by (${byDM}) (${cinc(CC)})`,
    dtRps: `sum by (${byDM}) (rate(${CC}${csel()}[5m]))`,
    dBars: `sort_desc(sum by (server_address) (rate(${CC}${csel()}[5m])))`, // bargauge (instant)
    dtAvg: `sum by (${byDM}) (${cinc(CS)}) / sum by (${byDM}) (${cinc(CC)})`,
    dtP95: `histogram_quantile(0.95, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtP99: `histogram_quantile(0.99, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtErr5: `(sum by (${byDM}) (${cinc(CC, 'http_response_status_code=~"5..|"')}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
    dtErr4: `(sum by (${byDM}) (${cinc(CC, 'http_response_status_code=~"4.."')}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
  };
}

/** groupKey (`k="v"` dizileri) → etiket haritası. */
export function seriesLabels(s: Pick<SpanMetricSeries, 'groupKey'>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const kv of s.groupKey ?? []) {
    const i = kv.indexOf('=');
    if (i <= 0) continue;
    let v = kv.slice(i + 1);
    if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
      v = v.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    }
    out[kv.slice(0, i)] = v;
  }
  return out;
}

/** Lejant: şablondaki {{label}} yerine değer (Grafana legendFormat). */
export function legendOf(tpl: string, labels: Record<string, string>): string {
  return tpl.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (_, k: string) => labels[k] ?? '').trim();
}

/** Serileri lejant şablonuyla yeniden adlandırır (grafik bileşeni groupKey'i basar). */
export function relabel(series: SpanMetricSeries[] | null | undefined, tpl: string, scale = 1): SpanMetricSeries[] {
  // v0.10.1245 — Go nil dilimi `points: null` gelebilir; boş say.
  return (series ?? []).map(s => {
    const pts = s.points ?? [];
    return {
      groupKey: [legendOf(tpl, seriesLabels(s)) || '—'],
      points: scale === 1 ? pts : pts.map(p => ({ time: p.time, value: p.value * scale })),
    };
  });
}

/** Serinin son geçerli değeri (null = veri yok). */
export function lastValue(s: SpanMetricSeries | undefined): number | null {
  const pts = s?.points ?? [];
  for (let i = pts.length - 1; i >= 0; i--) {
    const v = pts[i].value;
    if (Number.isFinite(v)) return v;
  }
  return null;
}

/** Tek değerli sorgu (stat): ilk serinin son değeri. */
export function scalarOf(series: SpanMetricSeries[] | null | undefined): number | null {
  return lastValue(series?.[0]);
}

export interface BarRow { label: string; value: number }

/** Çok serili sorgunun son değerleri → azalan çubuk satırları (pasta yerine). */
export function barsOf(series: SpanMetricSeries[] | null | undefined, labelKey: string, max = 10): BarRow[] {
  const rows: BarRow[] = [];
  for (const s of series ?? []) {
    const v = lastValue(s);
    if (v === null || v <= 0) continue;
    rows.push({ label: seriesLabels(s)[labelKey] || '(boş)', value: v });
  }
  return rows.sort((a, b) => b.value - a.value).slice(0, max);
}

/** Tablo birleştirme: anahtar etiketlere göre her sorgunun son değeri bir sütun. */
export function mergeTable(
  cols: Record<string, SpanMetricSeries[] | null | undefined>, keys: readonly string[],
): Array<Record<string, string | number | null>> {
  const rows = new Map<string, Record<string, string | number | null>>();
  for (const [col, series] of Object.entries(cols)) {
    for (const s of series ?? []) {
      const l = seriesLabels(s);
      const id = keys.map(k => l[k] ?? '').join('\u0000');
      let row = rows.get(id);
      if (!row) {
        row = {};
        for (const k of keys) row[k] = l[k] ?? '';
        rows.set(id, row);
      }
      row[col] = lastValue(s);
    }
  }
  return [...rows.values()];
}

/** Route/method seçenekleri route-bazlı rate serisinden (ek sorgu yok). */
export function filterOptions(series: SpanMetricSeries[] | null | undefined): { routes: string[]; methods: string[] } {
  const routes = new Set<string>();
  const methods = new Set<string>();
  for (const s of series ?? []) {
    const l = seriesLabels(s);
    if (l.http_route && l.http_route !== 'none') routes.add(l.http_route);
    if (l.http_request_method) methods.add(l.http_request_method);
  }
  return { routes: [...routes].sort(), methods: [...methods].sort() };
}

// v0.10.1245 — operatör (prod): sekme "Cannot read properties of undefined
// (reading 'toLocaleString')" ile düştü. Tablo satırında bir sütunun serisi
// yoksa değer undefined geliyordu; biçimlendiriciler yalnız null'a bakıyordu.
// Saf ve toleranslı: null / undefined / NaN / Infinity → "—".
type MaybeNum = number | string | null | undefined;
const finite = (v: MaybeNum): v is number => typeof v === 'number' && Number.isFinite(v);

export function fmtCount(v: MaybeNum, digits = 0): string {
  return finite(v) ? v.toLocaleString('tr-TR', { maximumFractionDigits: digits }) : '—';
}
export function fmtRatio(v: MaybeNum): string {
  return finite(v) ? `%${(v * 100).toFixed(2)}` : '—';
}
export function fmtSeconds(v: MaybeNum, fmt: (n: number) => string): string {
  return finite(v) ? fmt(v) : '—';
}

// v0.10.1247 — Grafana paritesi: panodaki eşikler ve sabit seri renkleri.
// Renk yalnız sapan değerde (CLAUDE.md tablo standardı): yeşil = renksiz.
export type HttpTone = 'err' | 'warn' | undefined;
const fin = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);
/** Gecikme (s): panoda P95/P99 eşikleri 0,5 sn sarı, 1 sn kırmızı. */
export function latencyTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 1 ? 'err' : v >= 0.5 ? 'warn' : undefined;
}
/** Hata oranı (0–1): %1 sarı, %5 kırmızı (5xx ve 4xx sütunları). */
export function errorRatioTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 0.05 ? 'err' : v >= 0.01 ? 'warn' : undefined;
}
/** 4xx stat kartı: %5 sarı, %15 kırmızı (panodaki "4xx Oranı" eşiği). */
export function clientErrorTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v >= 0.15 ? 'err' : v >= 0.05 ? 'warn' : undefined;
}
/** SLI < 500ms (0–1): %99 altı sarı, %95 altı kırmızı. */
export function sliTone(v: unknown): HttpTone {
  return !fin(v) ? undefined : v < 0.95 ? 'err' : v < 0.99 ? 'warn' : undefined;
}

/** Sabit seri renkleri (CSS token adı): status sınıfları ve yüzdelikler. */
export const HTTP_SERIES_TOKENS: Readonly<Record<string, string>> = {
  '2xx': '--ok', '3xx': '--info', '4xx': '--orange', '5xx': '--err',
  p50: '--ok', p90: '--warn', p95: '--orange', p99: '--err',
};

/** Token → çözülmüş renk haritası (resolve: CSS değişkeni okuyucu). */
export function httpSeriesColors(names: readonly string[], resolve: (token: string) => string): Map<string, string> {
  const out = new Map<string, string>();
  for (const n of names) {
    const tok = HTTP_SERIES_TOKENS[n];
    if (!tok) continue;
    const c = resolve(tok).trim();
    if (c) out.set(n, c);
  }
  return out;
}

// v0.10.1247 — Grafana birebir: donut / stat sparkline yardımcıları (heatmap v0.10.1258'de kaldırıldı).

export interface DonutSlice { label: string; value: number; pct: number }

/** Çubuk satırları → donut dilimleri (yüzde toplamı 100). */
export function donutSlices(rows: BarRow[]): DonutSlice[] {
  const total = rows.reduce((a, r) => a + r.value, 0);
  return rows.map(r => ({ label: r.label, value: r.value, pct: total > 0 ? (r.value / total) * 100 : 0 }));
}

/** Stat kartı trend çizgisi: ilk serinin değerleri (yoksa boş). */
export function sparkValues(series: SpanMetricSeries[] | null | undefined): (number | null)[] {
  return (series?.[0]?.points ?? []).map(p => (Number.isFinite(p.value) ? p.value : null));
}

/**
 * v0.10.1248 — Grafana'nın ANLIK (instant) sorguyla okuduğu paneller: stat
 * toplamı, pasta, tablo, bağlantı hataları, downstream bargauge. Bunlar
 * /api/v1/query ile `to` anında değerlendirilir (range sorgusunun son
 * noktası bir adım geride kalıyordu).
 */
export const HTTP_INSTANT_IDS: ReadonlySet<string> = new Set([
  'total', 'topRoutes', 'statusCodes', 'dConnErr', 'dBars',
  'tTotal', 'tRps', 'tAvg', 'tP95', 'tP99', 'tErr5', 'tErr4',
  'dtTotal', 'dtRps', 'dtAvg', 'dtP95', 'dtP99', 'dtErr5', 'dtErr4',
]);

// v0.10.1253 (operatör: Overview'da "Failure rate · metrik" yerine HTTP
// status; yalnız sınıflar değil kodlar) — status KODU başına istek hızı.
export function statusCodeRateQuery(app: string, windowSec: number): string {
  return `sum by (http_response_status_code) (rate(${SC}${serverSel({ app })}[${rateWindow(windowSec)}]))`;
}

/** Kod serileri: ad = kod, sıra 2xx→5xx (kod artan); renk tokenı sınıftan. */
export function statusCodeSeries(series: SpanMetricSeries[] | null | undefined): { series: SpanMetricSeries[]; tokens: Map<string, string> } {
  const out = relabel(series, '{{http_response_status_code}}')
    .filter(s => s.groupKey[0] && s.groupKey[0] !== '—')
    .sort((a, b) => a.groupKey[0].localeCompare(b.groupKey[0]));
  const tokens = new Map<string, string>();
  for (const s of out) {
    const tok = HTTP_SERIES_TOKENS[`${s.groupKey[0].charAt(0)}xx`];
    if (tok) tokens.set(s.groupKey[0], tok);
  }
  return { series: out, tokens };
}

// v0.10.1259 — Overview "Toplam istek" karosu: penceredeki toplam (anlık sorgu).
export function totalRequestsQuery(app: string, windowSec: number): string {
  return `sum(increase(${SC}${serverSel({ app })}[${Math.max(60, Math.round(windowSec))}s]))`;
}
