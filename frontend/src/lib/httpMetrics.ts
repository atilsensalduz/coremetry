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

export interface HttpScope {
  app: string; route?: string; method?: string;
  /** v0.10.1352 — route REGEX (`=~`, VM tam-çapalı); şekil kipi (`/users/:id`). route'u ezer. */
  routeRe?: string;
}

// v0.10.1350 — VM etiket adları TEK yerde. Backend aynı OTel anahtarlarını
// promLabel ile buraya sanitize eder (internal/api/http_labels.go
// httpStatusKeyCandidates[0] → status); Go pin testi
// (TestHTTPLabelsMatchFrontend) iki dilin ayrışmasını kırar.
export const HTTP_LABELS = {
  service: 'service_name',
  route: 'http_route',
  method: 'http_request_method',
  status: 'http_response_status_code',
} as const;
const L = HTTP_LABELS;
const ST5 = `${L.status}=~"5.."`;
const ST4 = `${L.status}=~"4.."`;
const ST5C = `${L.status}=~"5..|"`; // downstream: bağlantı hatası (boş kod) da hata

/** PromQL dize kaçışı (" ve \). */
export function promStr(v: string): string {
  return '"' + v.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"';
}

/** Inbound seçici: service_name + isteğe bağlı route/method. */
export function serverSel(s: HttpScope, extra = ''): string {
  const parts = [`${L.service}=${promStr(s.app)}`];
  if (s.routeRe !== undefined) parts.push(`${L.route}=~${promStr(s.routeRe)}`);
  else if (s.route) parts.push(`${L.route}=${promStr(s.route)}`);
  if (s.method) parts.push(`${L.method}=${promStr(s.method)}`);
  if (extra) parts.push(extra);
  return `{${parts.join(', ')}}`;
}

/** Downstream seçici (panoda route/method süzgeci yok). */
export function clientSel(s: HttpScope, extra = ''): string {
  return `{${L.service}=${promStr(s.app)}${extra ? ', ' + extra : ''}}`;
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

/** Pencere uzunluğu (`$__range`): en az 60 s. */
export function windowRange(windowSec: number): string {
  return `${Math.max(60, Math.round(windowSec))}s`;
}

/** `increase(m{sel}[W])`; offsetSec > 0 → bir önceki pencere (`offset Ns`). */
function winInc(s: HttpScope, windowSec: number, m: string, extra = '', offsetSec = 0): string {
  const off = offsetSec > 0 ? ` offset ${Math.round(offsetSec)}s` : '';
  return `increase(${m}${serverSel(s, extra)}[${windowRange(windowSec)}]${off})`;
}

/**
 * v0.10.1352 — route bazında (ya da bütün) yüzdelik zaman serisi:
 * `histogram_quantile(p, sum by (le[, by]) (rate(bucket[R])))`. Sekmenin
 * "Latency Yüzdelikleri" (by yok) ve "P95 gecikme (route)" (by = method,
 * route) panelleri ile endpoint sayfasının gecikme panelleri AYNI üretici.
 */
export function quantileQuery(s: HttpScope, windowSec: number, p: number, by = ''): string {
  const R = rateWindow(windowSec);
  return `histogram_quantile(${p}, sum by (le${by ? ', ' + by : ''}) (rate(${SB}${serverSel(s)}[${R}])))`;
}

export interface HttpWindowQueries {
  total: string; err5Count: string; avg: string;
  p50: string; p95: string; p99: string; err5: string; err4: string;
}

/**
 * v0.10.1352 — GERÇEK PENCERE değerleri (anlık sorgu, `increase[W]`):
 * sekmenin "Endpoint Detayları" tablosu (by = method, route) ve endpoint
 * sayfasının RED karoları (by yok → tek seri) aynı üreticiden. Pencere
 * yüzdeliği = penceredeki kova artışlarının yüzdeliği (adım ortalaması
 * DEĞİL). offsetSec > 0 → aynı sorgular bir önceki pencere için (compare).
 */
export function windowQueries(s: HttpScope, windowSec: number, by = '', offsetSec = 0): HttpWindowQueries {
  const inc = (m: string, x = '') => winInc(s, windowSec, m, x, offsetSec);
  const agg = (x: string) => (by ? `sum by (${by}) (${x})` : `sum(${x})`);
  const qt = (p: number) => `histogram_quantile(${p}, sum by (le${by ? ', ' + by : ''}) (${inc(SB)}))`;
  const ratio = (st: string) => `(${agg(inc(SC, st))} or (0 * ${agg(inc(SC))})) / ${agg(inc(SC))}`;
  return {
    total: agg(inc(SC)),
    err5Count: `${agg(inc(SC, ST5))} or (0 * ${agg(inc(SC))})`,
    avg: `${agg(inc(SS))} / ${agg(inc(SC))}`,
    p50: qt(0.5), p95: qt(0.95), p99: qt(0.99),
    err5: ratio(ST5), err4: ratio(ST4),
  };
}

/** Panodaki sorguların tamamı (id → PromQL). W = pencere, R = rate penceresi. */
export function httpQueries(s: HttpScope, windowSec: number): Record<string, string> {
  const R = rateWindow(windowSec);
  const W = windowRange(windowSec);
  const sel = (x = '') => serverSel(s, x);
  const csel = (x = '') => clientSel(s, x);
  const rate = (m: string, x = '') => `rate(${m}${sel(x)}[${R}])`;
  const crate = (m: string, x = '') => `rate(${m}${csel(x)}[${R}])`;
  const cinc = (m: string, x = '') => `increase(${m}${csel(x)}[${W}])`;
  const q = (p: number) => quantileQuery(s, windowSec, p);
  const cq = (p: number, by = 'le') => `histogram_quantile(${p}, sum by (${by}) (${crate(CB)}))`;
  const byMR = `${L.method}, ${L.route}`;
  const byDM = `server_address, ${L.method}`;
  const tw = windowQueries(s, windowSec, byMR);
  return {
    // Genel bakış
    total: `sum(${winInc(s, windowSec, SC)})`,
    rps: `sum(${rate(SC)})`,
    err5: `(sum(${rate(SC, ST5)}) or vector(0)) / sum(${rate(SC)})`,
    err4: `(sum(${rate(SC, ST4)}) or vector(0)) / sum(${rate(SC)})`,
    p95: q(0.95),
    sli: `sum(${rate(SB, 'le="0.5"')}) / sum(${rate(SC)})`,
    // Trafik
    rateByRoute: `sum by (${byMR}) (${rate(SC)})`,
    topRoutes: `topk(10, sum by (${L.route}) (${winInc(s, windowSec, SC)}))`,
    statusClass: `sum by (cls) (label_replace(${rate(SC)}, "cls", "\${1}xx", "${L.status}", "([0-9]).*"))`,
    statusCodes: `sum by (${L.status}) (${winInc(s, windowSec, SC)})`,
    // Gecikme
    p50: q(0.5), p90: q(0.9), p99: q(0.99),
    p95ByRoute: quantileQuery(s, windowSec, 0.95, byMR),
    // Hatalar
    err5ByRoute: `sum by (${L.route}, ${L.method}, ${L.status}) (${rate(SC, ST5)}) > 0`,
    err4ByRoute: `sum by (${L.route}, ${L.method}, ${L.status}) (${rate(SC, ST4)}) > 0`,
    // Endpoint özeti
    tRps: `sum by (${byMR}) (rate(${SC}${sel()}[5m]))`, // panodaki gibi [5m]
    tTotal: tw.total, tAvg: tw.avg, tP95: tw.p95, tP99: tw.p99, tErr5: tw.err5, tErr4: tw.err4,
    // Downstream
    dRps: `sum(${crate(CC)})`,
    dErr: `(sum(${crate(CC, ST5C)}) or vector(0)) / sum(${crate(CC)})`,
    dP95: cq(0.95),
    dConnErr: `sum(${cinc(CC, `${L.status}=""`)}) or vector(0)`,
    dRateByAddr: `sum by (server_address) (${crate(CC)})`,
    dP95ByAddr: cq(0.95, 'le, server_address'),
    dErrByAddr: `sum by (server_address) (${crate(CC, ST5C)}) / sum by (server_address) (${crate(CC)})`,
    dErrTypes: `sum by (server_address, error_type) (${crate(CC, 'error_type!=""')}) > 0`,
    dtTotal: `sum by (${byDM}) (${cinc(CC)})`,
    dtRps: `sum by (${byDM}) (rate(${CC}${csel()}[5m]))`,
    dBars: `sort_desc(sum by (server_address) (rate(${CC}${csel()}[5m])))`, // bargauge (instant)
    dtAvg: `sum by (${byDM}) (${cinc(CS)}) / sum by (${byDM}) (${cinc(CC)})`,
    dtP95: `histogram_quantile(0.95, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtP99: `histogram_quantile(0.99, sum by (le, ${byDM}) (${cinc(CB)}))`,
    dtErr5: `(sum by (${byDM}) (${cinc(CC, ST5C)}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
    dtErr4: `(sum by (${byDM}) (${cinc(CC, ST4)}) or (0 * sum by (${byDM}) (${cinc(CC)}))) / sum by (${byDM}) (${cinc(CC)})`,
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

/** Lejant: şablon dizesi ya da etiket → ad fonksiyonu (methodRouteLegend). */
export type LegendTpl = string | ((labels: Record<string, string>) => string);

/** Serileri lejant şablonuyla yeniden adlandırır (grafik bileşeni groupKey'i basar). */
export function relabel(series: SpanMetricSeries[] | null | undefined, tpl: LegendTpl, scale = 1): SpanMetricSeries[] {
  // v0.10.1245 — Go nil dilimi `points: null` gelebilir; boş say.
  return (series ?? []).map(s => {
    const pts = s.points ?? [];
    const labels = seriesLabels(s);
    return {
      groupKey: [(typeof tpl === 'function' ? tpl(labels) : legendOf(tpl, labels)) || '—'],
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
    const r = l[L.route];
    const m = l[L.method];
    if (r && r !== 'none') routes.add(r);
    if (m) methods.add(m);
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

// v0.10.1325 — operatör (prod, Status Code Dağılımı): "%0.01 olarak da
// dağılım gösterebilir, burada 0 yazıyor çoğu." Lejant payı tam sayıya
// yuvarlanıyordu (toFixed(0)); 40,5 Mn × 200 yanında 134,3 B × 400 "%0"
// okunuyordu. Uyarlanır hassasiyet, tr-TR ondalık virgül:
//   ≥ 99 ve < 100 → 2 ondalık, asla "%100" okunmaz (99,999 → %99,99)
//   ≥ 10          → tam sayı (%42)
//   ≥ 1           → 1 ondalık (%2,4)
//   ≥ 0,01        → 2 ondalık (%0,33)
//   (0, 0,01)     → "<%0,01"   · tam 0 → "%0"
const SHARE_FMT = [0, 1, 2].map(d => new Intl.NumberFormat('tr-TR', { maximumFractionDigits: d, useGrouping: false }));
/** Yüzde payı (0–100) → "%0,33" biçimi; null/NaN → "—". */
export function fmtShare(pct: MaybeNum): string {
  if (!finite(pct)) return '—';
  if (pct <= 0) return '%0';
  if (pct >= 100) return '%100';
  if (pct >= 99) return `%${SHARE_FMT[2].format(Math.min(Math.round(pct * 100) / 100, 99.99))}`;
  if (pct >= 10) return `%${SHARE_FMT[0].format(pct)}`;
  if (pct >= 1) return `%${SHARE_FMT[1].format(pct)}`;
  if (pct >= 0.01) return `%${SHARE_FMT[2].format(pct)}`;
  return '<%0,01';
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
  return `sum by (${L.status}) (rate(${SC}${serverSel({ app })}[${rateWindow(windowSec)}]))`;
}

/** Kod serileri: ad = kod, sıra 2xx→5xx (kod artan); renk tokenı sınıftan. */
export function statusCodeSeries(series: SpanMetricSeries[] | null | undefined): { series: SpanMetricSeries[]; tokens: Map<string, string> } {
  const out = relabel(series, `{{${L.status}}}`)
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

// ── v0.10.1352 — endpoint sayfası metrik kipi = HTTP Metrics sekmesinin
// AYNI panelleri, route'a süzülü (operatör: "HTTP Metrics tabındaki aynı
// paneli/grafiği ilgili endpoint'e göre filtreleyerek koyabiliriz direkt").
// Eski kip /api/endpoints/metric'ten (throughput-metric seam'i, 180 s adım
// başına yüzdelik + çağrı-ağırlıklı karo ortalaması) okuyordu; sekmeyle
// tutmuyordu.

/** Lejant: "GET /a"; route etiketi yoksa "GET (route yok)" (çıplak "GET" değil). */
export function methodRouteLegend(labels: Record<string, string>, withStatus = false): string {
  const route = labels[L.route] ? labels[L.route] : '(route yok)';
  return [withStatus ? labels[L.status] : '', labels[L.method] ?? '', route]
    .filter(Boolean).join(' ');
}

/** Kod → sınıf rengi (`var(--ok)` …); bilinmeyen sınıf → undefined. */
export function statusCodeColorVar(code: string): string | undefined {
  const tok = HTTP_SERIES_TOKENS[`${code.charAt(0)}xx`];
  return tok ? `var(${tok})` : undefined;
}

/** Grafana "short": 19 Mn, 2 B (tam sayı title'da). */
const COMPACT = new Intl.NumberFormat('tr-TR', { notation: 'compact', maximumFractionDigits: 1 });
export function fmtCompact(v: MaybeNum): string {
  return finite(v) ? COMPACT.format(v) : '—';
}

/**
 * Şekil (`/users/:id`, endpointPathSignature çıktısı) → route regex'i: regex
 * metakarakterleri kaçışlı, her `:id` = bir ya da daha çok segment-içi
 * karakter (`[^/]+`). VM `=~` tam çapalar; ham şablon `/users/:id` da eşleşir.
 */
export function shapeRouteRegex(shape: string): string {
  return shape.split(':id').map(p => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('[^/]+');
}

export interface EndpointIdentity {
  service: string;
  /** http_route değeri (ya da sig=true'da şekil). */
  route: string;
  method?: string;
  /** Şekil kipi (Endpoints "group by shape"): route regex ile eşlenir. */
  sig?: boolean;
}

/** Endpoint kimliği → sekme üreticilerinin kapsamı. Route HER ZAMAN süzer (boşsa `http_route=~""` = route'suz seriler; servis geneline genişlemez). */
export function endpointScope(ep: EndpointIdentity): HttpScope {
  const method = ep.method?.trim() || undefined;
  if (ep.sig) return { app: ep.service, routeRe: shapeRouteRegex(ep.route), method };
  if (!ep.route) return { app: ep.service, routeRe: '', method };
  return { app: ep.service, route: ep.route, method };
}

/** Endpoint'in inbound etiket seçicisi (`{service_name="…", http_route="…"[, http_request_method="…"]}`). */
export function endpointFilter(ep: EndpointIdentity): string {
  return serverSel(endpointScope(ep));
}

export type LatQ = 'p50' | 'p95' | 'p99';
const LAT_P: Record<LatQ, number> = { p50: 0.5, p95: 0.95, p99: 0.99 };

export interface EndpointHttpQuery { id: string; expr: string; instant: boolean }

/**
 * Endpoint metrik kipinin TÜM sorguları. Grafikler sekmenin range
 * sorguları (aynı adım: ceil(W/300), aynı rate penceresi); karolar sekmenin
 * "Endpoint Detayları" tablosunun pencere sorguları (by yok → tek seri,
 * anlık); compare → aynı pencere sorguları `offset W` ile.
 */
export function endpointHttpQueries(ep: EndpointIdentity, windowSec: number, opts: { latQ: LatQ; compare: boolean }): EndpointHttpQuery[] {
  const sc = endpointScope(ep);
  const tab = httpQueries(sc, windowSec);
  const w = windowQueries(sc, windowSec);
  const byMR = `${L.method}, ${L.route}`;
  const out: EndpointHttpQuery[] = [
    { id: 'rateByRoute', expr: tab.rateByRoute, instant: false },
    { id: 'err5ByRoute', expr: tab.err5ByRoute, instant: false },
    { id: 'err4ByRoute', expr: tab.err4ByRoute, instant: false },
    { id: 'p50', expr: tab.p50, instant: false },
    { id: 'p90', expr: tab.p90, instant: false },
    { id: 'p95', expr: tab.p95, instant: false },
    { id: 'p99', expr: tab.p99, instant: false },
    { id: 'latByRoute', expr: quantileQuery(sc, windowSec, LAT_P[opts.latQ], byMR), instant: false },
    { id: 'statusCodes', expr: tab.statusCodes, instant: true },
    { id: 'wTotal', expr: w.total, instant: true },
    { id: 'wErr5', expr: w.err5Count, instant: true },
    { id: 'wAvg', expr: w.avg, instant: true },
    { id: 'wP50', expr: w.p50, instant: true },
    { id: 'wP95', expr: w.p95, instant: true },
    { id: 'wP99', expr: w.p99, instant: true },
  ];
  if (opts.compare) {
    const pw = windowQueries(sc, windowSec, '', Math.max(60, Math.round(windowSec)));
    out.push(
      { id: 'pTotal', expr: pw.total, instant: true },
      { id: 'pErr5', expr: pw.err5Count, instant: true },
      { id: 'pAvg', expr: pw.avg, instant: true },
      { id: 'pP99', expr: pw.p99, instant: true },
    );
  }
  return out;
}

export interface EndpointRed {
  calls: number | null; errors: number | null; errRate: number | null; reqPerMin: number | null;
  avgS: number | null; p50S: number | null; p95S: number | null; p99S: number | null;
}

/** Pencere sorgularının anlık değerleri → karolar (errRate 0–1, reqPerMin = çağrı / pencere dk). */
export function endpointRed(
  d: Partial<Record<'total' | 'err5' | 'avg' | 'p50' | 'p95' | 'p99', SpanMetricSeries[] | null | undefined>>, windowSec: number,
): EndpointRed {
  const calls = scalarOf(d.total);
  const errors = scalarOf(d.err5);
  const nn = (v: number | null) => (v === null || !Number.isFinite(v) ? null : v);
  const W = Math.max(60, Math.round(windowSec));
  return {
    calls, errors,
    errRate: calls !== null && calls > 0 && errors !== null ? errors / calls : null,
    reqPerMin: calls !== null ? calls / (W / 60) : null,
    avgS: nn(scalarOf(d.avg)), p50S: nn(scalarOf(d.p50)), p95S: nn(scalarOf(d.p95)), p99S: nn(scalarOf(d.p99)),
  };
}
