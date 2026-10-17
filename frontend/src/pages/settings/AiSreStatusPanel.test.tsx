// @vitest-environment jsdom
// AiSreStatusPanel.test.tsx — v0.10.1286 (AI SRE ayar ekranı): GET
// /api/sre/status salt-okunur panelinin hâlleri — yükleniyor, okuma hatası
// (boş panel değil, sunucu metni + tekrar dene), rol yok bandı, süreç içi
// drain (bant yok), deployment + pod satırları, kapalı ajan.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { SREStatus } from '@/lib/types';

const m = vi.hoisted(() => ({
  status: (() => new Promise(() => {})) as () => Promise<unknown>,
}));
vi.mock('@/lib/api', () => ({
  api: { sreStatus: vi.fn(() => m.status()) },
}));

import { AiSreStatusPanel } from './AiSreStatusPanel';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let host: HTMLDivElement | null = null;
let root: Root | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); host = null; root = null; });

async function render(): Promise<HTMLElement> {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => { root!.render(<QueryClientProvider client={qc}><AiSreStatusPanel /></QueryClientProvider>); });
  await act(async () => { await new Promise(r => setTimeout(r, 0)); });
  return host;
}

const NOW_NS = Date.now() * 1e6;
const base: SREStatus = { enabled: true, launcher: 'none', roleSeen: false, pods: [], queueDepth: 0, running: 0 };
const kv = (el: HTMLElement, k: string) =>
  Array.from(el.querySelectorAll('.keyval__row')).find(r => r.querySelector('dt')?.textContent === k)?.querySelector('dd')?.textContent;

describe('AiSreStatusPanel', () => {
  it('yükleniyor: spinner, boş panel değil', async () => {
    m.status = () => new Promise(() => {});
    const el = await render();
    expect(el.querySelector('[role="status"][aria-busy="true"]')?.getAttribute('aria-label')).toBe('Durum okunuyor…');
    expect(el.querySelector('dl')).toBeNull();
  });

  it('okuma düştü: hata başlığı + sunucu metni + Tekrar dene', async () => {
    m.status = () => Promise.reject(new Error('HTTP 500: {"error":"redis down"}'));
    const el = await render();
    expect(el.textContent).toContain('AI SRE durumu okunamadı');
    expect(el.textContent).toContain('redis down');
    expect(Array.from(el.querySelectorAll('button')).some(b => /tekrar|retry/i.test(b.textContent ?? ''))).toBe(true);
    expect(el.querySelector('dl')).toBeNull();
  });

  it('rol yok + kuyrukta iş: uyarı bandı, alanlar dolu', async () => {
    m.status = () => Promise.resolve({ ...base, queueDepth: 4 });
    const el = await render();
    expect(el.querySelector('[role="alert"]')?.textContent).toContain('SRE rolü yok — kuyrukta 4 iş bekliyor');
    expect(kv(el, 'Ajan')).toBe('açık');
    expect(kv(el, 'Drain')).toBe('yok');
    expect(kv(el, 'Rol görüldü')).toBe('hayır');
    expect(kv(el, 'Kuyruk')).toBe('4');
    expect(kv(el, 'Koşan')).toBe('0');
    expect(el.textContent).toContain("Taze kalp atışı olan SRE pod'u yok.");
  });

  it('süreç içi drain (all): kuyruk dolu olsa da bant yok', async () => {
    m.status = () => Promise.resolve({ ...base, launcher: 'inprocess', queueDepth: 2 });
    const el = await render();
    expect(el.querySelector('[role="alert"]')).toBeNull();
    expect(kv(el, 'Drain')).toBe('bu süreçte (all modu)');
  });

  it('deployment + pod satırları', async () => {
    m.status = () => Promise.resolve({
      ...base, launcher: 'deployment', roleSeen: true, running: 1, queueDepth: 0,
      pods: [{ pod: 'coremetry-sre-0', version: 'v0.10.1286', mode: 'sre', startedAt: NOW_NS - 3600e9, at: NOW_NS - 5e9, inFlight: 1, drained: 7, idleExitSec: 0 }],
    });
    const el = await render();
    expect(kv(el, 'Drain')).toBe('ayrı sre deployment’ı');
    expect(kv(el, 'Rol görüldü')).toBe('evet (son 60 sn)');
    const pods = el.querySelector('dl[aria-label="SRE pod\'ları"]');
    expect(pods?.textContent).toContain('coremetry-sre-0');
    expect(pods?.textContent).toContain('sre · v0.10.1286 · 1 koşan · 7 tamamlanan');
    expect(el.querySelector('[role="alert"]')).toBeNull();
  });

  it('ajan kapalı: nötr bilgi bandı, rozet "kapalı"', async () => {
    m.status = () => Promise.resolve({ ...base, enabled: false });
    const el = await render();
    expect(el.textContent).toContain('AI SRE kapalı — yeni araştırma açılmaz');
    expect(kv(el, 'Ajan')).toBe('kapalı');
  });
});
