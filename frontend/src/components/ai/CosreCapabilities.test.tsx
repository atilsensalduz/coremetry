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
//   (5) v0.10.1390 (operatör: "filo geneli ve böyle birçok servis yazmasın,
//       search kısmı çıksa yeter") — servis/operasyon satırlarında çip ve
//       "filo geneli" yok; her zaman görünen arama kutusu, seçim HEMEN gönderir.
//   (6) v0.10.1402 — Takımım kartı her zaman EN BAŞTA ('team'), deploy satırı
//       yok (ayrıntı: CosreTeamCard.test.tsx).
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
  groupForCapability, starterSendOpts, serviceHealthQuestion, operationErrorQuestion,
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
    expect(capabilityItems(false).map(i => i.kind)).toEqual(['team', 'service', 'operation', 'navigate']);
    expect(capabilityItems(true).map(i => i.kind)).toEqual(['team', 'wiki', 'service', 'operation', 'navigate']);
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
// v0.10.1390 — sunucu yalnız p1 + deploys grubu döner. v0.10.1402 — yalnız p1.
const GROUPS: CopilotStarterGroup[] = [
  { kind: 'p1', fleet: true, items: [
    { chip: 'orders-api · error rate', question: 'orders-api kök nedeni ne?', kind: 'p1', scope: { problem: 'p-1' } },
  ] },
];

describe('capabilityHints — v0.10.1382 gruplar (saf)', () => {
  it('p1 satırı yalnız öğeli grupla; sıra team → operasyon → p1 → navigate (v0.10.1402: deploy yok)', () => {
    expect(capabilityItems(false, GROUPS).map(i => i.kind)).toEqual(['team', 'service', 'operation', 'p1', 'navigate']);
    expect(capabilityItems(true, GROUPS).map(i => i.kind)).toEqual(['team', 'wiki', 'service', 'operation', 'p1', 'navigate']);
    expect(capabilityItems(false, [{ kind: 'p1', items: [] }]).map(i => i.kind)).not.toContain('p1');
    expect(groupForCapability('navigate', GROUPS)).toBeUndefined();
    expect(groupForCapability('team', GROUPS)).toBeUndefined();
    expect(groupForCapability('p1', GROUPS)?.fleet).toBe(true);
  });

  it('v0.10.1390 — servis/operasyon satırının sunucu grubu yok (yalnız arama kutusu)', () => {
    expect(groupForCapability('service', GROUPS)).toBeUndefined();
    expect(groupForCapability('operation', GROUPS)).toBeUndefined();
  });

  it('v0.10.1390 — arama kutularının cümleleri (LLM\'siz yönlenir: TestStarterQuestionsRoute)', () => {
    expect(serviceHealthQuestion('orders-api')).toBe('orders-api sağlığı nasıl?');
    expect(operationErrorQuestion('INSURANCE_DOC_PDF')).toBe('INSURANCE_DOC_PDF operasyonu neden hata veriyor?');
    // Şablon satırının doldurduğu kalıpla aynı.
    expect(splitCaret(tCosre('cosre.cap.operation.prompt')).text).toBe(operationErrorQuestion(''));
  });

  it('kapsamlı çip → parsed.scope; kapsamsız → undefined (istek gövdesi eski)', () => {
    expect(starterSendOpts(GROUPS[0].items[0])).toEqual({ parsed: { scope: { problem: 'p-1' } } });
    expect(starterSendOpts({ chip: 'a', question: 'a kök nedeni ne?', kind: 'p1' })).toBeUndefined();
  });

  it('istemci URL\'si sorgu dizesini ? ile ayırır; v0.10.1390 — range_s yok', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ groups: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    vi.mocked(api.copilotStarters).mockRestore(); // beforeEach casusu: gerçek istemci
    await api.copilotStarters('prod');
    const url = String(f.mock.calls[0][0]);
    expect(url).toContain('/api/copilot/starters?');
    expect(url).toContain('env=prod');
    expect(url).not.toContain('range_s');
  });

  it('v0.10.1390 — operasyon araması istemcisi', async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ names: [], total: 0 }), { status: 200, headers: { 'Content-Type': 'application/json' } }));
    vi.stubGlobal('fetch', f);
    await api.copilotOperationSearch('INSU');
    const url = String(f.mock.calls[0][0]);
    expect(url).toContain('/api/copilot/operation-search?');
    expect(url).toContain('q=INSU');
    expect(url).toContain('limit=20');
  });

  it('anahtarlar iki katalogda da var; kaldırılan anahtarlar yok', () => {
    for (const k of ['cosre.cap.p1', 'cosre.cap.p1.prompt',
      'cosre.cap.choices', 'cosre.cap.fleet', 'cosre.cap.fleet.title', 'cosre.cap.search.service', 'cosre.cap.search.operation']) {
      expect(t(k, 'en'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'tr'), k).not.toBe(t(k, 'en'));
    }
    expect(tCosre('cosre.cap.search.service')).toBe('Servis ara…');
    expect(tCosre('cosre.cap.search.operation')).toBe('Operasyon ara…');
    for (const gone of ['cosre.cap.pickService', 'cosre.cap.pickService.placeholder', 'cosre.cap.deploy', 'cosre.cap.deploy.prompt']) {
      expect(t(gone, 'tr'), gone).toBe(gone);
    }
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
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'navigate']);
    expect(document.body.querySelector('[data-chip="wiki"]')).toBeNull();
    expect(document.body.textContent).toContain('Hata veren teknik operasyonları inceleyebilirim');
  });

  it('wiki açık: wiki satırı + çip; tıklama doldurur, GÖNDERMEZ', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm', wiki: true });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('tr');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'wiki', 'service', 'operation', 'navigate']);
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
    expect(rows().length).toBe(5);
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

describe('CopilotChat çekmecesi — v0.10.1382 / v0.10.1390 başlangıçlar', () => {
  const choices = (kind: string) => Array.from(document.body.querySelectorAll<HTMLButtonElement>(`[data-cap-choices="${kind}"] button`));
  const search = (kind: string) => document.body.querySelector<HTMLInputElement>(`[data-cap-search="${kind}"] input`);
  const setVal = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!;
  const type = async (input: HTMLInputElement, v: string) => {
    await act(async () => {
      setVal.call(input, v);
      input.dispatchEvent(new Event('input', { bubbles: true }));
    });
  };
  const key = async (input: HTMLInputElement, k: string) => {
    await act(async () => { input.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true })); });
  };
  // Debounce (180 ms) + cevap + setTimeout(onEnter) için bekleme.
  const settle = async (ms = 260) => { await act(async () => { await new Promise(r => setTimeout(r, ms)); }); };
  const lastSent = (send: ReturnType<typeof vi.spyOn>) => {
    const msgs = (send.mock.calls[send.mock.calls.length - 1] as unknown[])[0] as { role: string; text: string }[];
    return msgs[msgs.length - 1].text;
  };

  it('servis/operasyon satırında çip ve "filo geneli" YOK, arama kutusu her zaman görünür; P1 çipi kapsamıyla gönderir', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: GROUPS } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    setUserLang('en');
    await openDrawer();
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'p1', 'navigate']);
    expect(document.body.querySelector('[data-cap-choices="service"]')).toBeNull();
    expect(document.body.querySelector('[data-cap-choices="operation"]')).toBeNull();
    expect(document.body.querySelector('[data-chip="pick-service"]')).toBeNull();
    expect(document.body.textContent).not.toContain('başka bir servis');
    expect(document.body.textContent).not.toContain('teknik hataları');
    // UI dili EN iken de sohbetin dili (TR).
    expect(search('service')?.placeholder).toBe('Servis ara…');
    expect(search('operation')?.placeholder).toBe('Operasyon ara…');
    // "filo geneli" yalnız P1 grubunda (v0.10.1402: deploy grubu kalktı).
    const svcItem = search('service')!.closest('.cosre-cap__item')!;
    expect(svcItem.textContent).not.toContain('filo geneli');
    expect(document.body.querySelector('[data-cap-choices="p1"]')?.textContent).toContain('filo geneli');

    await act(async () => { choices('p1')[0].click(); });
    await act(async () => { await new Promise(r => setTimeout(r, 10)); });
    expect(send).toHaveBeenCalledTimes(1);
    const args = send.mock.calls[0] as unknown[];
    expect(lastSent(send)).toBe('orders-api kök nedeni ne?');
    expect(args.some(a => !!a && typeof a === 'object' && (a as { problem?: string }).problem === 'p-1')).toBe(true);
  });

  it('servis kutusu: listeden seçim HEMEN gönderir "<svc> sağlığı nasıl?"', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const names = vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['orders-api'], total: 1, hasMore: false } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = search('service')!;
    await type(input, 'ord');
    await settle();
    expect(names).toHaveBeenCalledWith('ord', 200);
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('orders-api sağlığı nasıl?');
  });

  it('servis kutusu: Enter — birebir ad sunucuda doğrulanınca gönderir, değilse cümleyi DOLDURUR', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    vi.spyOn(api, 'serviceNames').mockResolvedValue({ names: ['orders-api', 'orders-worker'], total: 2, hasMore: false } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    let input = search('service')!;
    await type(input, 'orders');
    await key(input, 'Enter');
    await settle(20);
    expect(send).not.toHaveBeenCalled();
    expect(textarea()?.value).toBe('orders sağlığı nasıl?');
    expect(input.value).toBe('');

    input = search('service')!;
    await type(input, 'orders-api');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('orders-api sağlığı nasıl?');
  });

  it('operasyon kutusu: <2 karakter istek atmaz; seçim HEMEN gönderir "<OP> operasyonu neden hata veriyor?"', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const opSearch = vi.spyOn(api, 'copilotOperationSearch').mockResolvedValue({ names: ['INSURANCE_DOC_PDF'], total: 1 });
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    const input = search('operation')!;
    await settle();
    expect(opSearch).not.toHaveBeenCalled(); // boş alan: liste yok, istek yok
    await type(input, 'I');
    await settle();
    expect(opSearch).not.toHaveBeenCalled();
    await type(input, 'INSU');
    await settle();
    expect(opSearch).toHaveBeenCalledWith('INSU');
    await key(input, 'ArrowDown');
    await key(input, 'Enter');
    await settle(20);
    expect(send).toHaveBeenCalledTimes(1);
    expect(lastSent(send)).toBe('INSURANCE_DOC_PDF operasyonu neden hata veriyor?');
  });

  it('başlık hâlâ şablonu doldurur (göndermez); istek yalnız env taşır', async () => {
    vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'm' });
    const st = vi.spyOn(api, 'copilotStarters').mockResolvedValue({ groups: [] } as never);
    const send = vi.spyOn(api, 'copilotChat').mockResolvedValue(undefined as never);
    await openDrawer();
    expect(st).toHaveBeenCalled();
    expect(typeof st.mock.calls[0][0]).toBe('string');
    expect(rows().map(b => b.dataset.cap)).toEqual(['team', 'service', 'operation', 'navigate']);
    expect(search('service')).not.toBeNull();
    expect(search('operation')).not.toBeNull();
    await act(async () => { rows().find(b => b.dataset.cap === 'operation')!.click(); });
    expect(textarea()?.value).toBe(' operasyonu neden hata veriyor?');
    expect(send).not.toHaveBeenCalled();
  });
});
