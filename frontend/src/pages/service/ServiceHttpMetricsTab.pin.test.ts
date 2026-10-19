import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1244 — HTTP Metrics sekmesi: Service'te kayıtlı, kaynak VM'e sabit.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('HTTP Metrics sekmesi', () => {
  it('Service sekme şeridinde ve render ediliyor', () => {
    const svc = read('../Service.tsx');
    expect(svc).toContain("{ key: 'http',       label: 'HTTP Metrics' }");
    // v0.10.1377 — env + Topbar cluster kapsamı sekmeye iner (VM ortam süzgeci).
    expect(svc).toContain("{tab === 'http' && <ServiceHttpMetricsTab service={svc} range={range} env={env} cluster={scopeCluster} />}");
  });
  it('sorgular VictoriaMetrics\'e sabit', () => {
    expect(read('../../lib/api.ts')).toContain("withMetricSource(`/api/metrics/promql?${qs(params)}`, 'vm')");
    // v0.10.1352 — okuma paylaşılan kancada (endpoint sayfası da kullanır).
    // v0.10.1377 — ortam çözülene dek sorgu atılmaz (süzgeçsiz an yok).
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('useHttpPromql(all, win, httpEnvScopeReady(envScope))');
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('useHttpEnvScope(env, cluster, win.to)');
    expect(read('../../lib/queries/httpMetrics.ts')).toContain('api.httpMetricsPromql(');
    expect(read('../../lib/queries/httpMetrics.ts')).toContain('staleTime: 30_000');
  });
});

// v0.10.1247 — Grafana birebir: panel tipleri ve grafik davranışı.
describe('HTTP Metrics Grafana paritesi', () => {
  const tab = read('ServiceHttpMetricsTab.tsx');
  it('stat / gauge / donut / tablo hücreleri; heatmap yok (v0.10.1258)', () => {
    expect(tab).toContain('<HttpStat label="P95 Latency"');
    expect(tab).toContain('<HttpGauge label="SLI: < 500ms"');
    expect(tab).toContain("donut('İstek Dağılımı (Route, Top 10)'");
    expect(tab).toContain("donut('Status Code Dağılımı'");
    expect(tab).not.toContain('LatencyHeatmap');
    expect(tab).toContain('<GaugeCell');
    expect(tab).toContain('<ToneCell');
  });
  it('grafikler tüm seriler + açık lejant + sabit renkler', () => {
    // v0.10.1352 — panel gövdesi paylaşılan HttpChartPanel'de.
    const viz = read('httpMetricsViz.tsx');
    expect(tab).toContain('<HttpChartPanel ');
    expect(viz).toMatch(/maxSeries=\{60\} legendDefaultOpen/);
    expect(viz).toContain('seriesColors={httpSeriesColors(');
  });
});

// v0.10.1352 (operatör bildirimi) — route gecikme paneli yüzdeliği açıkça
// söyler; route etiketsiz seri "GET (route yok)" (çıplak "GET" değil).
describe('HTTP Metrics etiketleri (v0.10.1352)', () => {
  const tab = read('ServiceHttpMetricsTab.tsx');
  it('route gecikme başlığı', () => {
    expect(tab).toContain("chart('P95 gecikme (route)', 'p95ByRoute', MR, 's')");
    expect(tab).not.toContain('P95 Latency (Route bazında)');
  });
  it('route lejantları methodRouteLegend', () => {
    expect(tab).toContain('const MR: LegendTpl = l => methodRouteLegend(l);');
    expect(tab).toContain("chart('Request Rate (Route bazında)', 'rateByRoute', MR, 'req/s')");
    expect(tab).toContain("chart('5xx Hataları (Route bazında)', 'err5ByRoute', SMR, 'req/s')");
    expect(tab).not.toContain("'{{http_request_method}} {{http_route}}'");
  });
});
