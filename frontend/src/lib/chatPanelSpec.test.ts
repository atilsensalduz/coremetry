// chatPanelSpec.test.ts — v0.10.1443 (sohbetten dashboard, R1): sohbet
// grafiği → dashboard paneli eşleme tablosu. Çevrilebilen: VM PromQL kanıt
// grafiği, servis raporu HTTP grafikleri, span spec-chart; çevrilemeyen her
// şey null / boş (düğme gizli).
import { describe, it, expect } from 'vitest';
import {
  chartBlockToPanel, chartFenceSpecs, chatPanelDescription, cosreSpecToPanel, panelDedupeKey,
  rangeOverrideFor, rangeSToPreset, serviceReportChartsToPanels, turnToPanels,
} from './chatPanelSpec';
import type { ChatEvidenceChartPayload, ChatQueryEvidence, ChatTypedBlock, PromqlPanelConfig, SpanMetricPanelConfig } from './types';

const W = { fromNs: 1_000_000_000_000, toNs: 1_000_000_000_000 + 3600 * 1e9 };
const chart = (ref: string, title = 'orders-api p99'): ChatEvidenceChartPayload => ({
  title, evidenceRef: ref, window: W,
  series: [{ name: 'p99', unit: 'ms', points: [[1, 2]] }],
});
const ev = (ref: string, over: Partial<ChatQueryEvidence> = {}): ChatQueryEvidence => ({
  tool: 'metric_chart', source: 'VictoriaMetrics', queryKind: 'promql', ref, durationMs: 3,
  query: 'histogram_quantiles("le", 0.99, sum(rate(http_server_duration_bucket{service="orders-api"}[5m])) by (le))',
  window: { fromMs: 0, toMs: 1 }, ...over,
});
const block = (id: string, type: string, payload: unknown, seq = 0): ChatTypedBlock =>
  ({ id, type: type as ChatTypedBlock['type'], seq, final: true, payload });

describe('rangeSToPreset / rangeOverrideFor', () => {
  it('≥ rangeS en yakın preset; tavan 30d; varsayılan 3h override yazmaz', () => {
    expect(rangeSToPreset(1800)).toBe('30m');
    expect(rangeSToPreset(2000)).toBe('1h');
    expect(rangeSToPreset(10800)).toBe('3h');
    expect(rangeSToPreset(99_999_999)).toBe('30d');
    expect(rangeSToPreset(0)).toBeUndefined();
    expect(rangeOverrideFor(10800)).toBeUndefined();
    expect(rangeOverrideFor(3600)).toEqual({ preset: '1h' });
  });
});

describe('chartBlockToPanel', () => {
  it('VM PromQL kanıtı → promql paneli, kaynak pini vm, birim ilk seriden', () => {
    const p = chartBlockToPanel(chart('r1'), ev('r1'), { question: 'orders-api p99 neden arttı?', conversationId: 'c-1' })!;
    expect(p.type).toBe('promql');
    const c = p.config as PromqlPanelConfig;
    expect(c.source).toBe('vm');
    expect(c.unit).toBe('ms');
    expect(c.viz).toBe('line');
    expect(c.query).toContain('histogram_quantiles');
    expect(p.rangeOverride).toEqual({ preset: '1h' });
    expect(p.width).toBe(2);
    expect(p.description).toBe('CoSRE · orders-api p99 neden arttı? · /cosre?chat=c-1');
  });
  it('çevrilemeyen kanıt: SQL, Thanos, sorgusuz, kanıtsız', () => {
    expect(chartBlockToPanel(chart('r'), ev('r', { queryKind: 'sql' }))).toBeNull();
    expect(chartBlockToPanel(chart('r'), ev('r', { source: 'Thanos' }))).toBeNull();
    expect(chartBlockToPanel(chart('r'), ev('r', { query: '  ' }))).toBeNull();
    expect(chartBlockToPanel(chart('r'), undefined)).toBeNull();
  });
});

describe('serviceReportChartsToPanels', () => {
  it('her sorgulu HTTP grafiği bir promql paneli; servis başlığa girer', () => {
    const mk = (kind: string, query: string) => ({ ...chart('x', kind === 'rate' ? 'HTTP throughput' : 'Yanıt süresi'), kind, unit: kind === 'rate' ? 'req/s' : 'ms', query });
    const ps = serviceReportChartsToPanels({ service: 'orders-api', httpCharts: [mk('rate', 'sum(rate(a_total[5m]))'), mk('latency', 'x'), mk('5xx', '')] });
    expect(ps).toHaveLength(2);
    expect(ps[0].title).toBe('orders-api · HTTP throughput');
    expect((ps[0].config as PromqlPanelConfig).unit).toBe('req/s');
    expect((ps[1].config as PromqlPanelConfig).source).toBe('vm');
  });
});

describe('cosreSpecToPanel', () => {
  it('span spec → spanmetric (service.name + name süzgeci, agg birimi)', () => {
    const p = cosreSpecToPanel({ service: 'orders-api', operation: 'GET /orders', agg: 'p95', rangeS: 21600 })!;
    expect(p.type).toBe('spanmetric');
    const c = p.config as SpanMetricPanelConfig;
    expect(JSON.parse(c.filters!)).toEqual([
      { k: 'service.name', op: '=', v: ['orders-api'] },
      { k: 'name', op: '=', v: ['GET /orders'] },
    ]);
    expect(c.agg).toBe('p95');
    expect(c.unit).toBe('ms');
    expect(p.title).toBe('orders-api · GET /orders · p95');
    expect(p.rangeOverride).toEqual({ preset: '6h' });
  });
  it('metrik kaynaklı (VM /metric-red) ve bozuk spec çevrilmez', () => {
    expect(cosreSpecToPanel({ service: 'a', agg: 'rate', source: 'metric' })).toBeNull();
    expect(cosreSpecToPanel({ service: '', agg: 'rate' })).toBeNull();
  });
  it('mutlak pencere göreliye döner', () => {
    const p = cosreSpecToPanel({ service: 'a', agg: 'rate', fromNs: 0 + 1, toNs: 1 + 900 * 1e9 })!;
    expect(p.rangeOverride).toEqual({ preset: '15m' });
  });
});

describe('chartFenceSpecs', () => {
  it('kapalı ```chart çitleri; bozuk JSON atlanır', () => {
    const t = 'a\n```chart\n{"service":"s","agg":"rate"}\n```\nb\n```chart\n{bozuk\n```';
    expect(chartFenceSpecs(t)).toEqual([{ service: 's', agg: 'rate' }]);
    expect(chartFenceSpecs(undefined)).toEqual([]);
  });
});

describe('turnToPanels', () => {
  it('kanıt grafikleri + servis raporu + span grafikleri; tekrar düşer; çevrilemeyen bloklar sayılmaz', () => {
    const turn = {
      text: '',
      evidence: [ev('r1'), ev('r2'), ev('r3', { queryKind: 'sql' })],
      blocks: [
        block('b1', 'chart', chart('r1')),
        block('b2', 'chart', chart('r2', 'aynı sorgu')), // aynı sorgu/birim/kaynak → tekrar
        block('b3', 'chart', chart('r3')),
        block('b4', 'chart', { service: 'orders-api', agg: 'rate' }),
        block('b5', 'charts_signals', { service: 'x', signals: {}, fromNs: 1, toNs: 2 }),
        block('b6', 'topology', { focus: 'x' }),
      ],
    };
    const ps = turnToPanels(turn);
    expect(ps.map(p => p.type)).toEqual(['promql', 'spanmetric']);
  });
  it('kanıt yoksa adımların kanıtı (evidenceList önce)', () => {
    const turn = {
      blocks: [block('b1', 'chart', chart('r9'))],
      stepDetails: [{ i: 0, tool: 'metric_chart', evidenceList: [ev('r9')] }],
    };
    expect(turnToPanels(turn)).toHaveLength(1);
  });
  it('tipli chart bloğu yoksa metindeki çit', () => {
    expect(turnToPanels({ text: '```chart\n{"service":"s","agg":"p99"}\n```' })).toHaveLength(1);
    expect(turnToPanels({ text: 'grafik yok' })).toHaveLength(0);
  });
});

describe('panelDedupeKey / chatPanelDescription', () => {
  it('boşluk farkı aynı anahtar; kaynak farkı ayrı anahtar', () => {
    const a = chartBlockToPanel(chart('r'), ev('r', { query: 'sum(rate(a_total[5m]))' }))!;
    const b = chartBlockToPanel(chart('r'), ev('r', { query: 'sum(rate(a_total[5m]))   ' }))!;
    expect(panelDedupeKey(a)).toBe(panelDedupeKey(b));
    const c = { ...a, config: { ...(a.config as PromqlPanelConfig), source: 'ch' as const } };
    expect(panelDedupeKey(c)).not.toBe(panelDedupeKey(a));
  });
  it('soru kırpılır, konuşma yoksa link yok', () => {
    expect(chatPanelDescription()).toBe('CoSRE');
    const d = chatPanelDescription({ question: 'x'.repeat(400) });
    expect(Array.from(d).length).toBeLessThan(180);
  });
});
