// @vitest-environment jsdom
//
// v0.10.1306 — kanıt çipi deep link'i: /metrics?editor=1&tab=promql&promql=…
// &src=thanos&cluster=… editörü PromQL sekmesinde, sorgu metni ve Thanos
// kaynağı seçili açar; Thanos konsolu (/api/promql/query_range) cluster ile
// çağrılır. İçe aktarma imza-korumalı: range değişimi yazılan sorguyu silmez
// (v0.8.253 sınıfı). Viewer rolü: istek atılmaz, kilit durumu + salt-okunur sorgu.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act, useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useSearchParams, type SetURLSearchParams } from 'react-router-dom';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => {
  // uPlot modül yüklenirken matchMedia okur (jsdom'da yok).
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    role: 'editor',
    thanosCalls: [] as { query: string; cluster: string; start: number; end: number }[],
    vmCalls: [] as { query: string; src?: string }[],
  };
});

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      clusterSources: async () => ({ clusters: ['c1', 'c2'] }),
      promqlQueryRange: async (p: { query: string; cluster: string; start: number; end: number }) => {
        m.thanosCalls.push(p);
        return {
          status: 'success', data: { resultType: 'matrix', result: [] },
          meta: { clusterId: 'c1', effectiveQuery: `${p.query}{cluster="c1"}`, stepRaised: false, series: 0, totalSeries: 0, truncated: false, durationMs: 3 },
        };
      },
      metricPromql: async (p: { query: string; src?: string }) => { m.vmCalls.push(p); return []; },
      serviceDeploys: async () => [],
    },
  };
});
vi.mock('@/components/AuthProvider', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useAuth: () => ({ user: { id: 'u1', role: m.role } }) };
});

import { MetricQueryEditor } from './MetricQueryEditor';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let setSp: SetURLSearchParams | null = null;
let search = '';

function Harness() {
  const [sp, set] = useSearchParams();
  setSp = set;
  search = sp.toString();
  const r = sp.get('range') ?? '1h';
  const range = useMemo(() => ({ preset: r }), [r]);
  return <MetricQueryEditor range={range} />;
}

afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null; setSp = null; search = '';
  m.thanosCalls.length = 0; m.vmCalls.length = 0; m.role = 'editor';
});

const flush = async (n = 10) => {
  for (let i = 0; i < n; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
};

async function mount(url: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[url]}><Harness /></MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await flush();
}

const textarea = () => host!.querySelector<HTMLTextAreaElement>('textarea[aria-label="PromQL sorgusu"]')!;
const radio = (label: string) =>
  [...host!.querySelectorAll<HTMLElement>('[role="radio"]')].find(el => el.textContent === label);

const DEEP = '/metrics?editor=1&tab=promql&promql=up&src=thanos&cluster=c1&range=1h';

describe('MetricQueryEditor — PromQL URL (v0.10.1306)', () => {
  it('deep link: sorgu metni, Thanos kaynağı ve cluster seçili; konsol cluster ile çağrılır', async () => {
    await mount(DEEP);
    expect(textarea().value).toBe('up');
    expect(radio('PromQL')?.getAttribute('aria-checked')).toBe('true');
    expect(radio('Thanos')?.getAttribute('aria-checked')).toBe('true');
    expect(host!.querySelector<HTMLSelectElement>('select[aria-label="Thanos cluster"]')!.value).toBe('c1');
    expect(m.thanosCalls.length).toBeGreaterThan(0);
    expect(m.thanosCalls[0]).toMatchObject({ query: 'up', cluster: 'c1' });
    expect(m.vmCalls).toHaveLength(0);
    // Koşan sorgu (meta.effectiveQuery) görünür.
    expect(host!.textContent).toContain('up{cluster="c1"}');
  });

  it('range değişimi sorguyu ve kaynağı korur (imza-korumalı içe aktarma)', async () => {
    await mount(DEEP);
    // Yazılan ama henüz URL'e işlenmemiş (debounce) metin, ilgisiz bir range
    // yazımıyla SİLİNMEMELİ — imzasız içe aktarma onu URL'deki 'up'a döndürürdü.
    await act(async () => {
      const ta = textarea();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(ta, 'up + 1');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { setSp!(prev => { const n = new URLSearchParams(prev); n.set('range', '6h'); return n; }, { replace: true }); });
    await flush();
    expect(textarea().value).toBe('up + 1');
    await act(async () => { await new Promise(r => setTimeout(r, 400)); });
    await flush();
    expect(textarea().value).toBe('up + 1');
    const sp = new URLSearchParams(search);
    expect(sp.get('range')).toBe('6h');
    expect(sp.get('promql')).toBe('up + 1');
    expect(sp.get('src')).toBe('thanos');
    expect(sp.get('cluster')).toBe('c1');
  });

  it('dışarıdan gelen yeni sorgu (başka kanıt çipi) durumu ezer', async () => {
    await mount(DEEP);
    await act(async () => {
      setSp!(prev => { const n = new URLSearchParams(prev); n.set('promql', 'rate(x[5m])'); n.set('src', 'vm'); n.delete('cluster'); return n; }, { replace: true });
    });
    await flush();
    expect(textarea().value).toBe('rate(x[5m])');
    expect(radio('VictoriaMetrics')?.getAttribute('aria-checked')).toBe('true');
    expect(m.vmCalls.some(c => c.query === 'rate(x[5m])' && c.src === 'vm')).toBe(true);
  });

  it('yazılan sorgu debounce sonrası URL’e replace ile yazılır, yabancı paramlar kalır', async () => {
    await mount(DEEP);
    const ta = textarea();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!;
      setter.call(ta, 'sum(up)');
      ta.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => { await new Promise(r => setTimeout(r, 400)); });
    await flush();
    const sp = new URLSearchParams(search);
    expect(sp.get('promql')).toBe('sum(up)');
    expect(sp.get('range')).toBe('1h');
    expect(sp.get('editor')).toBe('1');
  });

  it('viewer: konsol çağrılmaz, kilit durumu + salt-okunur sorgu', async () => {
    m.role = 'viewer';
    await mount(DEEP);
    expect(m.thanosCalls).toHaveLength(0);
    expect(textarea().value).toBe('up');
    expect(textarea().readOnly).toBe(true);
    expect(host!.textContent).toContain('PromQL konsolu editör+ rolü ister');
  });
});
