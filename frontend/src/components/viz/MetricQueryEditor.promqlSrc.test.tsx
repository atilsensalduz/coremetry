// @vitest-environment jsdom
//
// v0.10.1309 — PromQL sekmesinde ?src=vm|ch kurulum varsayılanından farklıyken
// metrik-adı autocomplete'i, `{…}` anahtar/değer önerileri ve etiket gezgini
// (MetricNamePicker dahil) VARSAYILAN depoya soruyordu (v0.10.1306'nın bilinen
// sınırı): sorgu CH'de koşarken adlar VM'den önerilir, seçilen ad boş seri
// döner. Artık her metadata isteği aynı src'yi taşır ve öneri mini-cache'inin
// anahtarı src'yi içerir; src yokken istekler v0.10.1306'daki gibi (src
// argümanı undefined → sayfanın ?metricsrc= denemesi / varsayılan);
// src=thanos'ta öneri ve gezgin gizli kalır.
// Kalıp: createRoot + act (repo @testing-library kullanmıyor).
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { act, useMemo } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useSearchParams, type SetURLSearchParams } from 'react-router-dom';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const m = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return {
    names: [] as { service: string; q?: string; src?: string }[],
    keys: [] as { metric: string; src?: string }[],
    vals: [] as { metric: string; key: string; src?: string }[],
    promql: [] as { query: string; src?: string }[],
  };
});

vi.mock('@/lib/api', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return {
    ...mod,
    api: {
      ...(mod.api as Record<string, unknown>),
      clusterSources: async () => ({ clusters: ['c1'] }),
      promqlQueryRange: async () => ({
        status: 'success', data: { resultType: 'matrix', result: [] },
        meta: { clusterId: 'c1', effectiveQuery: 'up', stepRaised: false, series: 0, totalSeries: 0, truncated: false, durationMs: 1 },
      }),
      metricPromql: async (p: { query: string; src?: string }) => { m.promql.push(p); return []; },
      serviceDeploys: async () => [],
      metricNamesSearch: async (service: string, q?: string, _limit?: number, _offset?: number, src?: string) => {
        m.names.push({ service, q, src });
        return { names: [{ name: `${q ?? ''}_total`, unit: '', type: 'sum' }], total: 1, hasMore: false };
      },
      metricAttrKeys: async (metric: string, _service?: string, _since?: string, src?: string) => {
        m.keys.push({ metric, src });
        return ['pod', 'namespace'];
      },
      metricLabels: async (metric: string, key: string, _since?: string, _q?: string, _limit?: number, src?: string) => {
        m.vals.push({ metric, key, src });
        return ['a', 'b'];
      },
    },
  };
});
vi.mock('@/components/AuthProvider', async (importOriginal) => {
  const mod = await importOriginal<Record<string, unknown>>();
  return { ...mod, useAuth: () => ({ user: { id: 'u1', role: 'editor' } }) };
});

import { MetricQueryEditor } from './MetricQueryEditor';

let host: HTMLDivElement | null = null;
let root: Root | null = null;
let setSp: SetURLSearchParams | null = null;

function Harness() {
  const [sp, set] = useSearchParams();
  setSp = set;
  const r = sp.get('range') ?? '1h';
  const range = useMemo(() => ({ preset: r }), [r]);
  return <MetricQueryEditor range={range} />;
}

// Bu ortamda global localStorage güvenilir değil (Node'un deneysel Storage'ı
// jsdom'unkini gölgeleyebiliyor) — gezginin açık/kapalı durumu için bellek içi.
const store = new Map<string, string>();
beforeEach(() => {
  store.clear();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => store.clear(),
  });
});
afterEach(() => {
  if (root) act(() => root!.unmount());
  host?.remove(); host = null; root = null; setSp = null;
  m.names.length = 0; m.keys.length = 0; m.vals.length = 0; m.promql.length = 0;
  vi.unstubAllGlobals();
});

const flush = async (n = 10) => {
  for (let i = 0; i < n; i++) await act(async () => { await new Promise(r => setTimeout(r, 0)); });
};
const wait = async (ms: number) => {
  await act(async () => { await new Promise(r => setTimeout(r, ms)); });
  await flush();
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
async function type(text: string) {
  await act(async () => {
    const ta = textarea();
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(ta, text);
    ta.setSelectionRange(text.length, text.length);
    ta.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
const openLabelBrowser = () => { store.set('cm.mqe.labelBrowser', '1'); };

// Öneri mini-cache'i modül düzeyinde (60s TTL) ve testler arasında yaşar —
// her test kendi metrik adını kullanır ki bir öncekinin cache'i sonucu bozmasın.
describe('MetricQueryEditor — PromQL metadata istekleri src taşır (v0.10.1309)', () => {
  it('src=ch: metrik-adı autocomplete isteği src=ch taşır', async () => {
    await mount('/metrics?editor=1&tab=promql&src=ch&range=1h');
    await type('chname_a');
    await wait(300);
    expect(m.names.length).toBeGreaterThan(0);
    expect(m.names.every(c => c.src === 'ch')).toBe(true);
    expect(m.names.at(-1)).toMatchObject({ q: 'chname_a', src: 'ch' });
    // Sorgunun kendisi de aynı depoda — ad ve seri aynı yerden.
    expect(m.promql.at(-1)).toMatchObject({ query: 'chname_a', src: 'ch' });
  });

  it('src=ch: `{` anahtar ve `="` değer önerileri src=ch taşır', async () => {
    await mount('/metrics?editor=1&tab=promql&src=ch&range=1h');
    await type('chlbl_m{');
    await wait(50);
    expect(m.keys).toEqual([{ metric: 'chlbl_m', src: 'ch' }]);
    await type('chlbl_m{pod="');
    await wait(50);
    expect(m.vals).toEqual([{ metric: 'chlbl_m', key: 'pod', src: 'ch' }]);
  });

  it('src=ch: etiket gezgini (picker + anahtar + değer) src=ch taşır', async () => {
    openLabelBrowser();
    await mount('/metrics?editor=1&tab=promql&src=ch&range=1h');
    // İmleç metrikte değil → gezgin MetricNamePicker'ı gösterir; arama src'li.
    await wait(250);
    expect(m.names.some(c => c.service === '' && c.src === 'ch')).toBe(true);
    expect(m.names.some(c => c.src !== 'ch')).toBe(false);
    // İmleç metriğe gelince gezgin o metriğin anahtarlarını çeker.
    await type('chlb_metric');
    await wait(450);
    expect(m.keys).toContainEqual({ metric: 'chlb_metric', src: 'ch' });
    const podChip = [...host!.querySelectorAll<HTMLElement>('.mqe-lb-chips button, .mqe-lb-chips [role="button"]')]
      .find(el => el.textContent === 'pod');
    expect(podChip).toBeTruthy();
    await act(async () => { podChip!.click(); });
    await flush();
    expect(m.vals).toContainEqual({ metric: 'chlb_metric', key: 'pod', src: 'ch' });
    expect([...m.keys, ...m.vals].every(c => c.src === 'ch')).toBe(true);
  });

  it('öneri cache anahtarı src içerir: aynı metrik başka kaynakta yeniden çekilir', async () => {
    await mount('/metrics?editor=1&tab=promql&src=ch&range=1h');
    await type('cachekey_m{');
    await wait(50);
    expect(m.keys).toEqual([{ metric: 'cachekey_m', src: 'ch' }]);
    // Kaynak dışarıdan (kanıt çipi / geri) vm'e döner — CH'nin listesi
    // cache'ten önerilmemeli; aynı metin VM'e sorulur.
    await act(async () => {
      setSp!(prev => { const n = new URLSearchParams(prev); n.set('src', 'vm'); return n; }, { replace: true });
    });
    await flush();
    await type('cachekey_m{ ');
    await wait(50);
    expect(m.keys).toEqual([{ metric: 'cachekey_m', src: 'ch' }, { metric: 'cachekey_m', src: 'vm' }]);
    // Varsayılana dönüş de ayrı anahtar.
    await act(async () => {
      setSp!(prev => { const n = new URLSearchParams(prev); n.delete('src'); return n; }, { replace: true });
    });
    await flush();
    await type('cachekey_m{');
    await wait(50);
    expect(m.keys).toHaveLength(3);
    expect(m.keys[2]).toEqual({ metric: 'cachekey_m', src: undefined });
  });

  it('varsayılan (src yok): istekler src argümanı olmadan — v0.10.1306 davranışı', async () => {
    openLabelBrowser();
    await mount('/metrics?editor=1&tab=promql&range=1h');
    await type('defname_x');
    await wait(500);
    expect(m.names.length).toBeGreaterThan(0);
    expect(m.names.every(c => c.src === undefined)).toBe(true);
    expect(m.keys).toContainEqual({ metric: 'defname_x', src: undefined });
    await type('defname_x{pod="');
    await wait(50);
    expect(m.vals).toContainEqual({ metric: 'defname_x', key: 'pod', src: undefined });
    expect(m.promql.every(c => c.src === undefined)).toBe(true);
  });

  it('src=thanos: autocomplete ve etiket gezgini gizli, metadata isteği yok', async () => {
    openLabelBrowser();
    await mount('/metrics?editor=1&tab=promql&src=thanos&cluster=c1&range=1h');
    expect(host!.querySelector('.mqe-lb')).toBeNull();
    await type('thanos_m');
    await wait(300);
    await type('thanos_m{pod="');
    await wait(400);
    expect(m.names).toHaveLength(0);
    expect(m.keys).toHaveLength(0);
    expect(m.vals).toHaveLength(0);
    expect(host!.querySelector('.mqe-lb')).toBeNull();
  });
});
