// ServiceHttpMetricsTab — v0.10.1244 (operatör): "OpenTelemetry HTTP Metrics"
// Grafana panosunun servis sekmesi hâli. Kaynak VictoriaMetrics'e SABİT
// (api.httpMetricsPromql, ?metricsrc=vm); sorgular panodakiyle aynı
// (lib/httpMetrics.ts). Pano → Coremetry eşlemesi: stat → StatTile, zaman
// serisi → MultiLineChart (uPlot), pasta → çubuk listesi, heatmap → kova
// dağılımı (yalnız uPlot kuralı), tablo → useDataTable. Route / method
// süzgeçleri URL'de (?hroute, ?hmethod), seçenekler süzgeçsiz route serisinden.
// Yalnız sekme açıkken sorgu atılır (30 sn sunucu önbelleği).
import { useMemo } from 'react';
import { useQueries } from '@tanstack/react-query';
import { Card, PanelTitle, SectionHead, StatTile, SelectField } from '@/components/ui';
import { Spinner, Empty } from '@/components/Spinner';
import { MultiLineChart } from '@/components/MultiLineChart';
import {
  useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef,
} from '@/components/ui/DataTable';
import { api } from '@/lib/api';
import { timeRangeToNs } from '@/lib/utils';
import { fmtSmart } from '@/lib/chartFmt';
import { useUrlParams, strParam } from '@/lib/urlParams';
import {
  httpQueries, relabel, scalarOf, barsOf, bucketDistribution, mergeTable, filterOptions,
  HTTP_METRICS_POINTS, type BarRow,
} from '@/lib/httpMetrics';
import type { TimeRange, SpanMetricSeries } from '@/lib/types';

const FILTER_SPEC = { hroute: strParam(), hmethod: strParam() };

type Row = Record<string, string | number | null>;

const pct = (v: number | null) => (v === null ? '—' : `%${(v * 100).toFixed(2)}`);
const sec = (v: number | null) => (v === null ? '—' : fmtSmart(v, 's'));
const num = (v: number | null, d = 0) => (v === null ? '—' : v.toLocaleString('tr-TR', { maximumFractionDigits: d }));

function cols(keyCols: Array<{ id: string; label: string }>, prefix: string): ColumnDef<Row>[] {
  const n = (r: Row, k: string) => (typeof r[k] === 'number' ? (r[k] as number) : -1);
  return [
    ...keyCols.map((c, i) => ({
      id: c.id, label: c.label, sortValue: (r: Row) => String(r[c.id] ?? ''), naturalDir: 'asc' as const,
      mono: true, minWidth: i === 0 ? 70 : 180, priority: 1, ...(i === keyCols.length - 1 ? { flex: true } : { width: 160 }),
    })),
    { id: `${prefix}Total`, label: 'Toplam İstek', numeric: true, width: 120, minWidth: 90, priority: 2, sortValue: r => n(r, `${prefix}Total`) },
    { id: `${prefix}Rps`, label: 'RPS', numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}Rps`) },
    { id: `${prefix}Avg`, label: 'Ortalama', numeric: true, width: 100, minWidth: 80, priority: 2, sortValue: r => n(r, `${prefix}Avg`) },
    { id: `${prefix}P95`, label: 'P95', numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}P95`) },
    { id: `${prefix}P99`, label: 'P99', numeric: true, width: 90, minWidth: 70, priority: 3, sortValue: r => n(r, `${prefix}P99`) },
    { id: `${prefix}Err5`, label: '5xx %', numeric: true, width: 90, minWidth: 70, priority: 2, sortValue: r => n(r, `${prefix}Err5`) },
    { id: `${prefix}Err4`, label: '4xx %', numeric: true, width: 90, minWidth: 70, priority: 3, sortValue: r => n(r, `${prefix}Err4`) },
  ];
}
const IN_COLS = cols([{ id: 'http_request_method', label: 'Method' }, { id: 'http_route', label: 'Route' }], 't');
const OUT_COLS = cols([{ id: 'http_request_method', label: 'Method' }, { id: 'server_address', label: 'Downstream' }], 'dt');

export function ServiceHttpMetricsTab({ service, range }: { service: string; range: TimeRange }) {
  const [f, setF] = useUrlParams(FILTER_SPEC);
  const win = useMemo(() => timeRangeToNs(range), [range]);
  const windowSec = Math.max(60, (win.to - win.from) / 1e9);
  const scope = { app: service, route: f.hroute || undefined, method: f.hmethod || undefined };
  const queries = useMemo(() => httpQueries(scope, windowSec), [service, f.hroute, f.hmethod, windowSec]); // eslint-disable-line react-hooks/exhaustive-deps
  const optsExpr = useMemo(() => httpQueries({ app: service }, windowSec).rateByRoute, [service, windowSec]);
  const ids = Object.keys(queries);
  const all = [...ids.map(id => ({ id, expr: queries[id] })), { id: 'opts', expr: optsExpr }];
  const results = useQueries({
    queries: all.map(q => ({
      queryKey: ['http-metrics', q.expr, win.from, win.to],
      queryFn: () => api.httpMetricsPromql({ query: q.expr, from: win.from, to: win.to, maxDataPoints: HTTP_METRICS_POINTS }),
      staleTime: 30_000,
      retry: false,
    })),
  });
  const data: Record<string, SpanMetricSeries[] | null | undefined> = {};
  all.forEach((q, i) => { data[q.id] = results[i].data; });
  const optsQ = results[results.length - 1];
  const opts = filterOptions(data.opts);

  const inRows = useMemo(() => mergeTable({
    tTotal: data.tTotal, tRps: data.tRps, tAvg: data.tAvg, tP95: data.tP95, tP99: data.tP99, tErr5: data.tErr5, tErr4: data.tErr4,
  }, ['http_request_method', 'http_route']), [data.tTotal, data.tRps, data.tAvg, data.tP95, data.tP99, data.tErr5, data.tErr4]);
  const outRows = useMemo(() => mergeTable({
    dtTotal: data.dtTotal, dtRps: data.dtRps, dtAvg: data.dtAvg, dtP95: data.dtP95, dtP99: data.dtP99, dtErr5: data.dtErr5, dtErr4: data.dtErr4,
  }, ['server_address', 'http_request_method']), [data.dtTotal, data.dtRps, data.dtAvg, data.dtP95, data.dtP99, data.dtErr5, data.dtErr4]);
  const inDt = useDataTable<Row>({ storageKey: 'svc-http-endpoints', columns: IN_COLS, rows: inRows, initialSort: { id: 'tTotal', dir: 'desc' } });
  const outDt = useDataTable<Row>({ storageKey: 'svc-http-downstream', columns: OUT_COLS, rows: outRows, initialSort: { id: 'dtTotal', dir: 'desc' } });

  if (optsQ.isPending) return <Spinner />;
  if (optsQ.isError) {
    return (
      <Empty icon="!" title="HTTP metrikleri okunamadı">
        {(optsQ.error as Error).message} — bu sekme VictoriaMetrics'ten okur
        (http_server_request_duration_seconds_* / http_client_request_duration_seconds_*).
      </Empty>
    );
  }
  if (!data.opts?.length && !f.hroute && !f.hmethod) {
    return (
      <Empty icon="∿" title="Bu servis için HTTP metriği yok">
        VictoriaMetrics'te service_name="{service}" etiketli http_server_request_duration_seconds metriği bu pencerede bulunamadı.
      </Empty>
    );
  }

  const pending = (id: string) => results[ids.indexOf(id)]?.isPending;
  const chart = (title: string, id: string, tpl: string, unit: string, scale = 1, extra?: { id: string; tpl: string }[]) => {
    const series = [
      ...relabel(data[id], tpl, scale),
      ...(extra ?? []).flatMap(e => relabel(data[e.id], e.tpl, scale)),
    ];
    return (
      <Card header={<PanelTitle>{title}</PanelTitle>}>
        {pending(id) ? <Spinner /> : series.length === 0
          ? <div className="field-hint">Bu pencerede veri yok.</div>
          : <MultiLineChart series={series} unit={unit} height={220} syncKey="svc-http" legendStorageKey={`svc-http:${id}`} />}
      </Card>
    );
  };
  const bars = (title: string, rows: BarRow[], fmt: (v: number) => string, loading?: boolean) => {
    const max = rows[0]?.value ?? 0;
    const top = Math.max(...rows.map(r => r.value), 0);
    return (
      <Card header={<PanelTitle>{title}</PanelTitle>}>
        {loading ? <Spinner /> : rows.length === 0 ? <div className="field-hint">Bu pencerede veri yok.</div> : (
          <div style={{ display: 'grid', gap: 4 }}>
            {rows.map(r => (
              <div key={r.label} style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) auto', gap: 8, fontSize: 12 }}>
                <div style={{ minWidth: 0 }}>
                  <div className="mono dt-trunc" title={r.label}>{r.label}</div>
                  <div style={{ height: 4, background: 'var(--bg2)', borderRadius: 2 }}>
                    <div style={{ width: `${(top || max) > 0 ? (r.value / (top || max)) * 100 : 0}%`, height: 4, background: 'var(--accent2)', borderRadius: 2 }} />
                  </div>
                </div>
                <span style={{ fontVariantNumeric: 'tabular-nums' }}>{fmt(r.value)}</span>
              </div>
            ))}
          </div>
        )}
      </Card>
    );
  };
  const table = (dt: typeof inDt, prefix: string, keys: string[], loadingId: string) => (
    <div className="table-wrap">
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {dt.sortedRows.length === 0
            ? <DataTableState dt={dt} {...(pending(loadingId) ? { kind: 'loading' as const } : { kind: 'empty' as const, message: 'Bu pencerede veri yok.' })} />
            : dt.sortedRows.map(r => (
              <tr key={keys.map(k => r[k]).join('|')}>
                {keys.map(k => <DataTableCell key={k} dt={dt} col={k} row={r} value={String(r[k] ?? '')}><span className="dt-trunc">{String(r[k] ?? '') || '—'}</span></DataTableCell>)}
                <DataTableCell dt={dt} col={`${prefix}Total`} row={r}>{num(r[`${prefix}Total`] as number | null)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Rps`} row={r}>{num(r[`${prefix}Rps`] as number | null, 2)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Avg`} row={r}>{sec(r[`${prefix}Avg`] as number | null)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}P95`} row={r}>{sec(r[`${prefix}P95`] as number | null)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}P99`} row={r}>{sec(r[`${prefix}P99`] as number | null)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Err5`} row={r}>{pct(r[`${prefix}Err5`] as number | null)}</DataTableCell>
                <DataTableCell dt={dt} col={`${prefix}Err4`} row={r}>{pct(r[`${prefix}Err4`] as number | null)}</DataTableCell>
              </tr>
            ))}
        </tbody>
      </table>
    </div>
  );
  const stat = (label: string, v: string) => <StatTile label={label}>{v}</StatTile>;
  const grid = (min: number) => ({ display: 'grid', gap: 12, gridTemplateColumns: `repeat(auto-fit, minmax(${min}px, 1fr))`, marginBottom: 14 });

  return (
    <div>
      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', alignItems: 'flex-end', marginBottom: 12 }}>
        <SelectField label="Route" value={f.hroute} onChange={e => setF({ hroute: e.target.value })}>
          <option value="">Tümü</option>
          {opts.routes.map(r => <option key={r} value={r}>{r}</option>)}
        </SelectField>
        <SelectField label="Method" value={f.hmethod} onChange={e => setF({ hmethod: e.target.value })}>
          <option value="">Tümü</option>
          {opts.methods.map(m => <option key={m} value={m}>{m}</option>)}
        </SelectField>
        <span className="field-hint">Kaynak: VictoriaMetrics · OpenTelemetry HTTP semconv metrikleri</span>
      </div>

      <SectionHead title="Genel Bakış" />
      <div style={grid(150)}>
        {stat('Toplam İstek', num(scalarOf(data.total)))}
        {stat('Throughput', `${num(scalarOf(data.rps), 2)} req/s`)}
        {stat('5xx Hata Oranı', pct(scalarOf(data.err5)))}
        {stat('4xx Oranı', pct(scalarOf(data.err4)))}
        {stat('P95 Latency', sec(scalarOf(data.p95)))}
        {stat('SLI: < 500ms', pct(scalarOf(data.sli)))}
      </div>

      <SectionHead title="Trafik" />
      <div style={grid(420)}>
        {chart('Request Rate (Route bazında)', 'rateByRoute', '{{http_request_method}} {{http_route}}', 'req/s')}
        {bars('İstek Dağılımı (Route, Top 10)', barsOf(data.topRoutes, 'http_route'), v => num(v), pending('topRoutes'))}
        {chart('HTTP Status Sınıfları', 'statusClass', '{{cls}}', 'req/s')}
        {bars('Status Code Dağılımı', barsOf(data.statusCodes, 'http_response_status_code', 20), v => num(v), pending('statusCodes'))}
      </div>

      <SectionHead title="Gecikme (Latency)" />
      <div style={grid(420)}>
        {chart('Latency Yüzdelikleri (p50 / p90 / p95 / p99)', 'p50', 'p50', 's', 1,
          [{ id: 'p90', tpl: 'p90' }, { id: 'p95', tpl: 'p95' }, { id: 'p99', tpl: 'p99' }])}
        {chart('P95 Latency (Route bazında)', 'p95ByRoute', '{{http_request_method}} {{http_route}}', 's')}
        {bars('Latency Dağılımı (kova başına istek)', bucketDistribution(data.buckets), v => num(v), pending('buckets'))}
      </div>

      <SectionHead title="Hatalar" />
      <div style={grid(420)}>
        {chart('5xx Hataları (Route bazında)', 'err5ByRoute', '{{http_response_status_code}} {{http_request_method}} {{http_route}}', 'req/s')}
        {chart('4xx Hataları (Route bazında)', 'err4ByRoute', '{{http_response_status_code}} {{http_request_method}} {{http_route}}', 'req/s')}
      </div>

      <SectionHead title="Endpoint Detayları" meta="seçili zaman aralığı" />
      <div style={{ marginBottom: 14 }}>{table(inDt, 't', ['http_request_method', 'http_route'], 'tTotal')}</div>

      <SectionHead title={`Downstream Bağımlılıkları (${service} →)`} />
      <div style={grid(150)}>
        {stat('Downstream RPS', `${num(scalarOf(data.dRps), 2)} req/s`)}
        {stat('Downstream Hata Oranı', pct(scalarOf(data.dErr)))}
        {stat('Downstream P95', sec(scalarOf(data.dP95)))}
        {stat('Bağlantı Hataları', num(scalarOf(data.dConnErr)))}
      </div>
      <div style={grid(420)}>
        {bars('Downstream Servisler (RPS)', barsOf(data.dRateByAddr, 'server_address', 15), v => `${num(v, 2)} req/s`, pending('dRateByAddr'))}
        {chart('Request Rate (Downstream bazında)', 'dRateByAddr', '→ {{server_address}}', 'req/s')}
        {chart('P95 Latency (Downstream bazında)', 'dP95ByAddr', '→ {{server_address}}', 's')}
        {chart('Inbound P95 vs Downstream P95', 'p95', `Inbound (${service})`, 's', 1, [{ id: 'dP95ByAddr', tpl: '→ {{server_address}}' }])}
        {chart('Hata Oranı (Downstream bazında)', 'dErrByAddr', '→ {{server_address}}', '%', 100)}
        {chart('Hata Tipleri (error_type)', 'dErrTypes', '{{server_address}} · {{error_type}}', 'req/s')}
      </div>
      <SectionHead title="Downstream Özeti" meta="seçili zaman aralığı" />
      {table(outDt, 'dt', ['http_request_method', 'server_address'], 'dtTotal')}
    </div>
  );
}
