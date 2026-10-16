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
