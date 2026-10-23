// @vitest-environment jsdom
//
// ConditionPreview.render — v0.10.1489: önizleme değerlendiricinin KAYNAĞINDAN
// (/api/alert-rules/preview-series) ve taslağın penceresiyle; kaynak rozeti
// görünür (VM örneklenmemiş / span + gerekçe); servis yoksa istek yok.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { AlertRuleSeries } from '@/lib/types';

const m = vi.hoisted(() => ({
  calls: [] as { service: string; metric: string; windowSec: number }[],
  vm: true,
}));

vi.mock('@/lib/api', () => ({
  api: {
    alertPreviewSeries: async (p: { service: string; metric: string; windowSec: number; fromNs: number; toNs: number }): Promise<AlertRuleSeries> => {
      m.calls.push({ service: p.service, metric: p.metric, windowSec: p.windowSec });
      return {
        metric: p.metric, service: p.service, windowSec: p.windowSec, stepSec: 300, from: p.fromNs, to: p.toNs,
        points: [{ t: p.fromNs + 300e9, v: 2 }, { t: p.fromNs + 600e9, v: 9 }, { t: p.fromNs + 900e9, v: null }],
        source: m.vm
          ? { vm: true, family: 'HTTP', evidence: 'kaynak: VictoriaMetrics (örneklenmemiş) · HTTP sunucu histogramı' }
          : { vm: false, reason: 'durum kodu etiketi yok', evidence: 'kaynak: span (örneklenmiş) — VM kullanılmadı: durum kodu etiketi yok' },
      };
    },
  },
  isCanceled: () => false,
}));

vi.mock('@/components/MultiLineChart', () => ({
  MultiLineChart: (p: { unit?: string }) => <div data-testid="chart">{p.unit}</div>,
}));

import { ConditionPreview } from './ConditionPreview';

let root: Root | null = null;
let el: HTMLDivElement;

async function render(draft: Record<string, unknown>) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(<QueryClientProvider client={qc}><ConditionPreview draft={draft} /></QueryClientProvider>);
  });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  m.calls = [];
  m.vm = true;
  el = document.createElement('div');
  document.body.appendChild(el);
  root = createRoot(el);
});
afterEach(() => {
  act(() => root?.unmount());
  el.remove();
});

describe('ConditionPreview', () => {
  it('taslağın servisi + metriği + penceresiyle ister, VM kaynağını gösterir', async () => {
    await render({ service: 'orders-api', metric: 'error_rate', windowSec: 600, threshold: 5, comparator: '>' });
    expect(m.calls).toEqual([{ service: 'orders-api', metric: 'error_rate', windowSec: 600 }]);
    expect(el.textContent).toContain('kaynak: VictoriaMetrics (örneklenmemiş) · HTTP');
    expect(el.textContent).toContain('would have fired 1×');
    expect(el.querySelector('[data-testid="chart"]')?.textContent).toBe('%');
  });
  it('span yedeğinde gerekçe görünür', async () => {
    m.vm = false;
    await render({ service: 'orders-api', metric: 'error_rate', windowSec: 300 });
    expect(el.textContent).toContain('kaynak: span (örneklenmiş)');
    expect(el.textContent).toContain('VM kullanılmadı: durum kodu etiketi yok');
  });
  it('servis yoksa istek yok', async () => {
    await render({ metric: 'p99_ms' });
    expect(m.calls).toHaveLength(0);
    expect(el.textContent).toContain('servis seçin');
  });
});
