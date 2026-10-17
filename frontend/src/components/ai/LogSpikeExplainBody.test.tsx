// @vitest-environment jsdom
//
// LogSpikeExplainBody — v0.10.1284 (AI SRE Faz 1a: explain_log_spike, log
// artışını ne açıklıyor).
//
// NE ÇİVİLİYOR:
//   • istek gövdesi öznenin TÜM girdilerini taşır (servis/env/arama/seviye/kip/pencere);
//   • grafik taban + artış BÖLGELERİYLE çizilir (TimeChart `regions`);
//   • katkı tablosu sunucu sırasıyla, ≤5 satır, gerekçe sütunuyla (kanıt tablosu);
//   • okunamayan boyut "okunamadı" der (yokluk ≠ okunamadı);
//   • anlatım düşse de (error) kanıt görünür, 👍/👎 yalnız exchangeId varken.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { LogSpikeExplain, LogSpikeExplainRequest } from '@/lib/types';
import type { ChartTimeRegion } from '@/lib/chart/overlays';

const m = vi.hoisted(() => ({ data: null as unknown, body: null as unknown, regions: null as unknown }));
vi.mock('@/lib/api', () => ({
  api: { copilotExplainLogSpike: async (b: unknown) => { m.body = b; return m.data; } },
  isCanceled: () => false,
}));
vi.mock('@/components/charts/TimeChart', () => ({
  TimeChart: (p: { regions?: ChartTimeRegion[]; times: number[] }) => {
    m.regions = p.regions;
    return <div data-testid="timechart">{p.times.length} kova</div>;
  },
}));

import { LogSpikeExplainBody } from './LogSpikeExplainBody';
import { logSpikeChart, logSpikeContributorTable, trPct } from './logSpikeView';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const FROM = 1_700_000_000e9;
const TO = 1_700_003_600e9;
const S = 1_700_003_000_000; // ms

const FULL: LogSpikeExplain = {
  explanation: 'Artış: checkout logları ×5,9.\nKaynak:\n- pod checkout-7f9-abc',
  exchangeId: 'x1',
  spike: {
    tool: 'explain_log_spike', ok: true, partial: true, request: { service: 'checkout' },
    spike: { fromMs: S, toMs: S + 420_000, source: 'detected', factor: 5.9, ratePerMinSpike: 1840, ratePerMinBase: 310,
      baseFromMs: S - 1_260_000, baseToMs: S, spikeCount: 12880, baseCount: 6510, bucketS: 60 },
    contributors: [
      { rank: 1, dim: 'pod', value: 'checkout-7f9-abc', spikeCount: 9000, baseCount: 800, sharePct: 62, spikePct: 71, basePct: 12, liftPts: 59,
        reason: "artışın %62'si pod checkout-7f9-abc kaynaklı (artışta payı %71, normalde %12)" },
      { rank: 2, dim: 'pattern', value: 'Connection reset <x>', spikeCount: 4000, baseCount: 0, sharePct: 35, spikePct: 31, basePct: 0, liftPts: 31, new: true,
        reason: "yeni desen «Connection reset <x>» — artışın %35'i (öncesinde görülmedi)" },
    ],
    dims: [{ dim: 'pod', state: 'ok' }, { dim: 'cluster', state: 'unmapped', note: 'cluster alanı log eşlemesinde yok — okunmadı' }, { dim: 'logger', state: 'timeout' }],
    warnings: ['env süzgeci uygulanamadı'],
    data: {
      histogram: Array.from({ length: 60 }, (_, i) => [S - 3_000_000 + i * 60_000, i > 49 && i < 57 ? 1800 : 300, 10] as [number, number, number]),
      samples: [{ tsMs: S + 5000, severity: 'ERROR', pod: 'checkout-7f9-abc', body: 'Connection reset by peer' }],
    },
  },
};

let host: HTMLDivElement | null = null;
let root: Root | null = null;

async function mount(data: LogSpikeExplain): Promise<HTMLElement> {
  m.data = data;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => {
    root!.render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <LogSpikeExplainBody service="checkout" fromNs={FROM} toNs={TO} mode="auto" env="prod" q="timeout" level="all" />
        </MemoryRouter>
      </QueryClientProvider>,
    );
  });
  await act(async () => { await new Promise(r => setTimeout(r, 20)); });
  return host;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  host = null; root = null;
});

describe('LogSpikeExplainBody', () => {
  it('istek tüm girdileri taşır; grafik bölgeli, tablo sunucu sırasıyla', async () => {
    const el = await mount(FULL);
    const want: LogSpikeExplainRequest = { service: 'checkout', env: 'prod', query: 'timeout', level: 'all', mode: 'auto', fromNs: FROM, toNs: TO };
    expect(m.body).toEqual(want);
    expect(el.querySelector('[data-testid="timechart"]')?.textContent).toBe('60 kova');
    const regions = m.regions as ChartTimeRegion[];
    expect(regions.map(r => r.label)).toEqual(['taban', 'artış']);
    expect(regions[1].fromSec).toBe(S / 1000);
    const rows = [...el.querySelectorAll('tbody tr')].map(tr => tr.textContent ?? '');
    expect(rows).toHaveLength(2);
    expect(rows[0]).toContain('checkout-7f9-abc');
    expect(rows[1]).toContain('yeni');
    expect(el.textContent).toContain('logger: okunamadı (timeout)');
    expect(el.textContent).toContain('cluster: eşlenmedi');
    expect(el.textContent).toContain('×5,9');
    expect(el.querySelector('[aria-label*="Yararlı"], [aria-label*="yararlı"], button[aria-pressed]')).not.toBeNull();
  });

  it('anlatım düşse de kanıt görünür, oy düğmesi yok', async () => {
    const el = await mount({ ...FULL, explanation: '', exchangeId: undefined, error: 'kota' });
    expect(el.textContent).toContain('Anlatım üretilemedi (kota)');
    expect(el.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(el.querySelector('button[aria-pressed]')).toBeNull();
  });
});

describe('logSpikeView (saf)', () => {
  it('tepe yoksa bölge yok, tablo yok', () => {
    const r = { ...FULL.spike, spike: undefined, contributors: [] };
    expect(logSpikeChart(r).regions).toEqual([]);
    expect(logSpikeContributorTable(r)).toBeNull();
  });
  it('yüzde biçimi', () => {
    expect(trPct(5.9)).toBe('5,9');
    expect(trPct(62)).toBe('62');
    expect(trPct(2.94)).toBe('2,9');
  });
});
