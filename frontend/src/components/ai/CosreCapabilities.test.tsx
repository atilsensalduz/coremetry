// @vitest-environment jsdom
//
// v0.10.1128 (operatör) — boş CoSRE sohbetinin "neler yapabilirim" ipucu:
//   (1) saf: wiki satırı yalnız `wiki` bayrağıyla ve en başta; `|` imleç işareti;
//   (2) EN/TR kataloglarında her anahtar var, metinler dile göre (EN gelecekteki
//       sohbet-geneli i18n için katalogda durur);
//   (3) çekmecede: ipucu boş sohbette görünür; wiki kapalıyken wiki satırı ve
//       wiki çipi YOK, açıkken VAR; satıra tıklamak composer'ı doldurur ama
//       GÖNDERMEZ (copilotChat çağrılmaz); wiki çipi "@wiki " doldurur (v0.10.1378);
//   (4) prod hatası (v0.10.1130): sohbet yüzeyi Türkçe-öncelikli — UI dili EN
//       iken de ipuçları, title'ı, wiki çipi ve doldurulan örnekler TÜRKÇE.
//   /cosre sayfası aynı bileşeni çizer — pages/CoSRE.test.tsx.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

import { api } from '@/lib/api';
import { setUserLang, t } from '@/lib/i18n';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import {
  capabilityItems, splitCaret, tCosre, COSRE_LANG,
  groupForCapability, localServiceChips, starterSendOpts, serviceHealthQuestion,
} from './capabilityHints';
import type { CopilotStarterGroup } from '@/lib/types';

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  __resetCopilotEnabledCache();
  // Dil seçimi localStorage'da; Node'un yerleşik localStorage'ı jsdom'unkini
  // gölgeliyor (traceAskCosre.contract.test.tsx emsali). Marka isteği ağa çıkmaz.
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.test', role: 'viewer', firstName: 'Ada' } as never);
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 } as never);
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false } as never);
  vi.spyOn(api, 'aiConversations').mockResolvedValue([] as never);
  vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  setUserLang(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('capabilityHints — saf', () => {
  it('wiki satırı yalnız bayrakla ve en başta', () => {
    expect(capabilityItems(false).map(i => i.kind)).toEqual(['service', 'operation', 'navigate']);
    expect(capabilityItems(true).map(i => i.kind)).toEqual(['wiki', 'service', 'operation', 'navigate']);
  });

  it('splitCaret: | imleç yeri, işaretsiz metinde imleç sonda', () => {
    expect(splitCaret('@| svc')).toEqual({ text: '@ svc', caret: 1 });
    expect(splitCaret('| op')).toEqual({ text: ' op', caret: 0 });
    expect(splitCaret('wikide ')).toEqual({ text: 'wikide ', caret: 7 });
  });

  it('EN/TR: her anahtar iki katalogda da var ve dile göre farklı', () => {
    const keys = capabilityItems(true).flatMap(i => [i.labelKey, i.promptKey])
      .concat(['cosre.cap.aria', 'cosre.cap.tryHint', 'cosre.chip.wiki']);
    for (const k of keys) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(t('cosre.cap.wiki', 'tr')).toContain('wiki');
    expect(t('cosre.cap.service', 'tr')).toContain('servis adını yaz');
    expect(t('cosre.cap.service', 'en')).toContain('service name');
    // v0.10.1378 — çipin metni kapsam belirteci: iki dilde aynı `@wiki `.
    expect(t('cosre.chip.wiki.prompt', 'tr')).toBe('@wiki ');
    expect(t('cosre.chip.wiki.prompt', 'en')).toBe('@wiki ');
  });

  it('tCosre: UI dilinden bağımsız, karşılamanın dili (TR)', () => {
    expect(COSRE_LANG).toBe('tr');
    setUserLang('en');
    expect(tCosre('cosre.cap.wiki')).toBe(t('cosre.cap.wiki', 'tr'));
    expect(tCosre('cosre.cap.tryHint')).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(tCosre('cosre.chip.wiki.prompt')).toBe('@wiki ');
  });
});

// v0.10.1382 — gruplu başlangıç seçenekleri (sentetik adlar).
const GROUPS: CopilotStarterGroup[] = [
  { kind: 'services', fleet: true, items: [
    { chip: 'checkout-service', question: 'checkout-service son 1 saatte neden hata veriyor?', kind: 'service_errors' },
    { chip: '/api/checkout', question: "/api/checkout hatalı trace'lerini getir", kind: 'endpoint_errors' },
  ] },
  { kind: 'operations', items: [
    { chip: 'INSURANCE_DOC_PDF', question: 'INSURANCE_DOC_PDF operasyonu neden hata veriyor?', kind: 'operation' },
  ] },
  { kind: 'p1', items: [
    { chip: 'orders-api · error rate', question: 'orders-api kök nedeni ne?', kind: 'p1', scope: { problem: 'p-1' } },
  ] },
  { kind: 'deploys', items: [] },
];

describe('capabilityHints — v0.10.1382 gruplar (saf)', () => {
  it('p1/deploy satırları yalnız öğeli grupla; sıra operasyon → p1 → deploy → navigate', () => {
    expect(capabilityItems(false, GROUPS).map(i => i.kind)).toEqual(['service', 'operation', 'p1', 'navigate']);
    const withDeploy: CopilotStarterGroup[] = [...GROUPS.slice(0, 3),
      { kind: 'deploys', items: [{ chip: 'orders-api → 2.0', question: 'orders-api son deploy etkisi?', kind: 'deploy' }] }];
    expect(capabilityItems(true, withDeploy).map(i => i.kind)).toEqual(['wiki', 'service', 'operation', 'p1', 'deploy', 'navigate']);
    expect(groupForCapability('deploy', GROUPS)).toBeUndefined();
    expect(groupForCapability('navigate', GROUPS)).toBeUndefined();
    expect(groupForCapability('service', GROUPS)?.fleet).toBe(true);
  });

  it('yerel servis çipleri: sabitlenmiş önce, sunucunun andığı ve uygunsuz ad atlanır, tavan 2', () => {
    const got = localServiceChips(GROUPS[0].items, ['checkout-service', 'payment-service'], ['payment-service', 'oracle:eu-a', 'two words', 'inventory', 'api-gateway']);
    expect(got.map(c => c.question)).toEqual(['payment-service sağlığı nasıl?', 'inventory sağlığı nasıl?']);
    expect(got.every(c => c.kind === 'service_health')).toBe(true);
    expect(serviceHealthQuestion('orders-api')).toBe('orders-api sağlığı nasıl?');
  });

  it('kapsamlı çip → parsed.scope; kapsamsız → undefined (istek gövdesi eski)', () => {
    expect(starterSendOpts(GROUPS[2].items[0])).toEqual({ parsed: { scope: { problem: 'p-1' } } });
    expect(starterSendOpts(GROUPS[0].items[0])).toBeUndefined();
  });

  it('istemci URL\'si sorgu dizesini ? ile ayırır (v0.10.702 hatası: "/startersrange_s=…")', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ groups: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    vi.mocked(api.copilotStarters).mockRestore(); // beforeEach casusu: gerçek istemci
    await api.copilotStarters(3600, 'prod');
    const url = String(f.mock.calls[0][0]);
    expect(url).toContain('/api/copilot/starters?');
    expect(url).toContain('range_s=3600');
    expect(url).toContain('env=prod');
  });

  it('yeni anahtarlar iki katalogda da var', () => {
    for (const k of ['cosre.cap.p1', 'cosre.cap.p1.prompt', 'cosre.cap.deploy', 'cosre.cap.deploy.prompt',
      'cosre.cap.choices', 'cosre.cap.fleet', 'cosre.cap.fleet.title', 'cosre.cap.pickService', 'cosre.cap.pickService.placeholder']) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(tCosre('cosre.cap.fleet')).toBe('filo geneli');
    expect(tCosre('cosre.cap.pickService')).toBe('başka bir servis…');
  });
});

function shell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <AuthProvider>
          <ConfirmProvider>
            <CopilotChat />
          </ConfirmProvider>
        </AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

async function openDrawer() {
  await act(async () => { root.render(shell()); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await act(async () => { await new Promise(r => setTimeout(r, 10)); });
}

const rows = () => Array.from(document.body.querySelectorAll<HTMLButtonElement>('button[data-cap]'));
const textarea = () => document.body.querySelector<HTMLTextAreaElement>('textarea');

describe('CopilotChat çekmecesi — yetenek ipucu', () => {
  it('wiki kapalı: üç satır, wiki satırı/çipi yok', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['service', 'operation', 'navigate']);
    expect(document.body.querySelector('[data-chip="wiki"]')).toBeNull();
    expect(document.body.textContent).toContain('Hata veren teknik operasyonları inceleyebilirim');
  });

  it('wiki açık: wiki satırı + çip; tıklama doldurur, GÖNDERMEZ', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['wiki', 'service', 'operation', 'navigate']);
    expect(document.body.textContent).toContain('Kurum wiki’sinde arayabilirim');

    const wikiRow = rows().find(b => b.dataset.cap === 'wiki')!;
    await act(async () => { wikiRow.click(); });
    expect(textarea()?.value).toBe('@wiki BSA cache refresh en sık karşılaşılan hatalar nelerdir?');

    const svcRow = rows().find(b => b.dataset.cap === 'service')!;
    await act(async () => { svcRow.click(); });
    expect(textarea()?.value.startsWith('@ servisinin')).toBe(true);

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('@wiki ');
    expect(send).not.toHaveBeenCalled();
    // Hâlâ boş sohbet: ipucu yerinde (gönderilmedi).
    expect(rows().length).toBe(4);
  });

  it('UI dili EN: ipuçları, title, wiki çipi ve örnekler yine TÜRKÇE (karşılamayla aynı dil)', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('en');
    await openDrawer();
    const text = document.body.textContent ?? '';
    expect(text).toContain('Sana nasıl yardımcı olabilirim?');
    expect(text).toContain('Kurum wiki’sinde arayabilirim');
    expect(text).toContain('Hata veren teknik operasyonları inceleyebilirim');
    expect(text).not.toContain('I can search the company wiki');
    expect(text).not.toContain('type the operation name');
    for (const b of rows()) expect(b.title).toBe('Kutuya örnek bir soru yazar — düzenleyip gönder');
    expect(document.body.querySelector('[role="list"].cosre-cap')?.getAttribute('aria-label')).toBe('CoSRE neler yapabilir');

    await act(async () => { rows().find(b => b.dataset.cap === 'wiki')!.click(); });
    expect(textarea()?.value).toBe('@wiki BSA cache refresh en sık karşılaşılan hatalar nelerdir?');
    await act(async () => { rows().find(b => b.dataset.cap === 'operation')!.click(); });
    expect(textarea()?.value).toBe(' operasyonu neden hata veriyor?');
    await act(async () => { rows().find(b => b.dataset.cap === 'navigate')!.click(); });
    expect(textarea()?.value).toBe(' sorununu incelemek için nereye bakmalıyım?');

    const chip = document.body.querySelector<HTMLButtonElement>('[data-chip="wiki"]');
    expect(chip?.textContent).toContain('Wikide ara');
    expect(chip?.textContent).not.toContain('Search the wiki');
    await act(async () => { chip!.click(); });
    expect(textarea()?.value).toBe('@wiki ');
    expect(send).not.toHaveBeenCalled();
  });
});

describe('CopilotChat çekmecesi — v0.10.1382 seçilebilir başlangıçlar', () => {
  const choices = (kind: string) => Array.from(document.body.querySelectorAll<HTMLButtonElement>(`[data-cap-choices="${kind}"] button`));

  it('gruplar satırların altında; somut çip HEMEN gönderir, P1 kapsamıyla', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: GROUPS } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('en');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['service', 'operation', 'p1', 'navigate']);
    const grp = document.body.querySelector('[data-cap-choices="service"]');
    expect(grp?.getAttribute('role')).toBe('group');
    expect(grp?.textContent).toContain('filo geneli');
    expect(choices('service').map(b => b.textContent)).toEqual(['checkout-service', '/api/checkout', 'başka bir servis…']);
    expect(choices('operation').map(b => b.textContent)).toEqual(['INSURANCE_DOC_PDF']);
    expect(document.body.querySelector('[data-cap-choices="navigate"]')).toBeNull();

    await act(async () => { choices('p1')[0].click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(send).toHaveBeenCalledTimes(1);
    const args = send.mock.calls[0] as unknown[];
    const msgs = args[0] as { role: string; text: string }[];
    expect(msgs[msgs.length - 1].text).toBe('orders-api kök nedeni ne?');
    expect(args.some(a => !!a && typeof a === 'object' && (a as { problem?: string }).problem === 'p-1')).toBe(true);
  });

  it('başlık hâlâ şablonu doldurur; "başka bir servis…" seçici açar ve seçim cümleyi DOLDURUR (göndermez)', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: GROUPS } as never);
    vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['orders-api'], total: 1 } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    await act(async () => { rows().find(b => b.dataset.cap === 'p1')!.click(); });
    expect(textarea()?.value).toBe(' kök nedeni ne?');

    const pickChip = document.body.querySelector<HTMLButtonElement>('[data-chip="pick-service"]')!;
    expect(pickChip.getAttribute('aria-expanded')).toBe('false');
    await act(async () => { pickChip.click(); });
    expect(pickChip.getAttribute('aria-expanded')).toBe('true');
    const input = document.body.querySelector<HTMLInputElement>('.cosre-cap__picker input');
    expect(input).not.toBeNull();
    const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
    await act(async () => {
      setVal.call(input, 'orders-api');
      input!.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await act(async () => {
      input!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(textarea()?.value).toBe('orders-api sağlığı nasıl?');
    expect(document.body.querySelector('.cosre-cap__picker')).toBeNull();
    expect(send).not.toHaveBeenCalled();
  });

  it('istek env + pencere taşır; gruplar boşken p1/deploy satırı yok', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const st = vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
    await openDrawer();
    expect(st).toHaveBeenCalled();
    expect(st.mock.calls[0][0]).toBe(3600);
    expect(typeof st.mock.calls[0][1]).toBe('string');
    expect(rows().map(b => b.dataset.cap)).toEqual(['service', 'operation', 'navigate']);
    // Servis satırında veri yokken de seçici çipi var; diğer satırlarda grup yok.
    expect(choices('service').map(b => b.textContent)).toEqual(['başka bir servis…']);
    expect(document.body.querySelector('[data-cap-choices="operation"]')).toBeNull();
  });
});
