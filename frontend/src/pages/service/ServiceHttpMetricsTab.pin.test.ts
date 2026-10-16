import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1244 — HTTP Metrics sekmesi: Service'te kayıtlı, kaynak VM'e sabit.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('HTTP Metrics sekmesi', () => {
  it('Service sekme şeridinde ve render ediliyor', () => {
    const svc = read('../Service.tsx');
    expect(svc).toContain("{ key: 'http',       label: 'HTTP Metrics' }");
    expect(svc).toContain("{tab === 'http' && <ServiceHttpMetricsTab service={svc} range={range} />}");
  });
  it('sorgular VictoriaMetrics\'e sabit', () => {
    expect(read('../../lib/api.ts')).toContain("withMetricSource(`/api/metrics/promql?${qs(params)}`, 'vm')");
    expect(read('ServiceHttpMetricsTab.tsx')).toContain('api.httpMetricsPromql(');
  });
});

// v0.10.1247 — Grafana birebir: panel tipleri ve grafik davranışı.
describe('HTTP Metrics Grafana paritesi', () => {
  const tab = read('ServiceHttpMetricsTab.tsx');
  it('stat / gauge / donut / heatmap / tablo hücreleri', () => {
    expect(tab).toContain('<HttpStat label="P95 Latency"');
    expect(tab).toContain('<HttpGauge label="SLI: < 500ms"');
    expect(tab).toContain("donut('İstek Dağılımı (Route, Top 10)'");
    expect(tab).toContain("donut('Status Code Dağılımı'");
    expect(tab).toContain('<LatencyHeatmap data={heat}');
    expect(tab).toContain('<GaugeCell');
    expect(tab).toContain('<ToneCell');
  });
  it('grafikler tüm seriler + açık lejant + sabit renkler', () => {
    expect(tab).toMatch(/maxSeries=\{60\} legendDefaultOpen/);
    expect(tab).toContain('seriesColors={httpSeriesColors(');
  });
});
