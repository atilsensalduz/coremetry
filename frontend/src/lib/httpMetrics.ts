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
    buckets: `sum by (le) (${inc(SB)})`,
    // Hatalar
    err5ByRoute: `sum by (http_route, http_request_method, http_response_status_code) (${rate(SC, 'http_response_status_code=~"5.."')}) > 0`,
    err4ByRoute: `sum by (http_route, http_request_method, http_response_status_code) (${rate(SC, 'http_response_status_code=~"4.."')}) > 0`,
    // Endpoint özeti
    tRps: `sum by (${byMR}) (${rate(SC)})`,
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
    dtRps: `sum by (${byDM}) (${crate(CC)})`,
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
  return (series ?? []).map(s => ({
    groupKey: [legendOf(tpl, seriesLabels(s)) || '—'],
    points: scale === 1 ? s.points : s.points.map(p => ({ time: p.time, value: p.value * scale })),
  }));
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

/** Birikimli histogram kovaları (le) → kova başına sayı (heatmap yerine dağılım). */
export function bucketDistribution(series: SpanMetricSeries[] | null | undefined): BarRow[] {
  const cum: { le: number; label: string; v: number }[] = [];
  for (const s of series ?? []) {
    const raw = seriesLabels(s).le;
    const v = lastValue(s);
    if (raw === undefined || v === null) continue;
    const le = raw === '+Inf' ? Infinity : Number(raw);
    if (Number.isNaN(le)) continue;
    cum.push({ le, label: raw, v });
  }
  cum.sort((a, b) => a.le - b.le);
  const out: BarRow[] = [];
  let prev = 0;
  let prevLabel = '0';
  for (const b of cum) {
    const n = Math.max(0, b.v - prev);
    out.push({ label: b.le === Infinity ? `> ${prevLabel}s` : `≤ ${b.label}s`, value: n });
    prev = Math.max(prev, b.v);
    prevLabel = b.label;
  }
  return out;
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
