// @vitest-environment jsdom
//
// CopilotChat.explainTurn.test.tsx — v0.10.1400 (Tek CoSRE). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//   "Explain trace'te çıkan loading ikonu güzel ama chat'ten 'bu trace'i
//    açıkla' deyince aynı yerde devam etmiyor, sanki yeni drawer açılıyor."
//
// `?ai=trace|exception` tek seferlik bir NİYET: CoSRE açılır, açıklama SOHBET
// TURU olarak gider (aynı thread, aynı composer), adres temizlenir; StrictMode
// çift effect'i ikinci tur göndermez; çekmecenin açıklama kipi (CopilotExplain
// + ikinci composer) hiç mount olmaz. Takip soruları açıklamanın bağlamını
// (explain + subject + trace sayfa bağlamı) taşır; ✕ bağlamı düşürür. Grup
// çipi niyet taşır; /cosre sayfası da aynı yolu kullanır.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, StrictMode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: q.includes('prefers-reduced-motion: reduce'), media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

// Çekmecenin açıklama gövdesi trace / exception için ARTIK mount olmamalı.
const explainMounts = vi.hoisted(() => ({ n: 0 }));
vi.mock('@/components/CopilotExplain', () => ({
  CopilotExplain: () => { explainMounts.n++; return null; },
}));

import { api } from '@/lib/api';
import type { ChatExplainMeta, ChatFollowup, ChatStreamEvent } from '@/lib/types';
import { __resetCopilotEnabledCache } from './ai/useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import { publishTraceAiContext, traceContextToPage, type TraceAiContext } from '@/lib/traceAiContext';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const CTX: TraceAiContext = {
  traceId: TRACE, spanId: 'b7ad6b7169203331', spanName: 'charge', service: 'payments',
  env: 'prod', cluster: 'cluster-a', namespace: 'billing', pod: 'payments-0',
  fromNs: 1_790_000_000_000 * 1e6, toNs: (1_790_000_000_000 + 1_200) * 1e6,
};
const TEXT = '**Kök Neden ve Sonraki Adım**\n- Ödeme sağlayıcısı kartı reddediyor. Kontrol: sağlayıcı yanıt kodu.';

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }
const bodyText = () => document.body.textContent ?? '';
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));
const byAria = (label: string) => document.body.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);

type ChatArgs = Parameters<typeof api.copilotChat>;
const argsOf = (c: ChatArgs) => ({
  messages: c[0], explain: c[5], subject: c[6], rangeS: c[7], trace: c[8], env: c[9], page: c[13], explainAsk: c[20],
});

function stubChat(meta?: (n: number) => ChatExplainMeta | undefined, followups?: ChatFollowup[]) {
  let n = 0;
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void, ...rest) => {
      n++;
      const ask = rest[18];
      onEvent({ kind: 'delta', text: TEXT });
      onEvent({ kind: 'answer', text: ask ? TEXT : `takip cevabı ${n}`, exchangeId: `x${n}`, explain: ask ? meta?.(n) : undefined, followups });
      onEvent({ kind: 'done', ok: true });
    });
}

function shell(path: string, variant: 'drawer' | 'page' = 'drawer') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <StrictMode>
      <MemoryRouter initialEntries={[path]}>
        <QueryClientProvider client={qc}>
          <AuthProvider><ConfirmProvider><CopilotChat variant={variant} /><Probe /></ConfirmProvider></AuthProvider>
        </QueryClientProvider>
      </MemoryRouter>
    </StrictMode>
  );
}

async function mount(path: string, variant: 'drawer' | 'page' = 'drawer') {
  await act(async () => { root.render(shell(path, variant)); });
  for (let i = 0; i < 3; i++) await act(async () => { await Promise.resolve(); });
}

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  (Element.prototype as unknown as { scrollIntoView: () => void }).scrollIntoView = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
  explainMounts.n = 0;
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
  vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
  vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'c1', title: 't', updatedAt: 1, messages: [] });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  publishTraceAiContext(null);
  vi.restoreAllMocks();
});

const traceMeta = (): ChatExplainMeta => ({ kind: 'trace', id: TRACE, surface: 'explain-trace:chat', evidenceSpanIds: ['b7ad6b7169203331'] });

describe('?ai=trace → sohbet içi açıklama turu', () => {
  it('tek tur (StrictMode), görünen kullanıcı turu + niyet, adres temizlenir, çekmece açıklama kipi yok', async () => {
    const chat = stubChat(traceMeta);
    await mount(`/trace?id=${TRACE}&ai=trace`);

    expect(chat).toHaveBeenCalledTimes(1);
    const a = argsOf(chat.mock.calls[0]);
    expect(a.explainAsk).toEqual({ kind: 'trace', id: TRACE, src: 'ai-param' });
    expect(a.messages[a.messages.length - 1].text).toBe("Bu trace'i açıkla · 0af76519…");
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(new URLSearchParams(search).get('id')).toBe(TRACE);

    // Aynı CoSRE: çekmece açık, tek composer, ✨ Explain anatomisi balonda.
    expect(document.body.querySelector('[role="dialog"]')).toBeTruthy();
    expect(document.body.querySelectorAll('textarea').length).toBe(1);
    expect(explainMounts.n).toBe(0);
    expect(bodyText()).toContain("Bu trace'i açıkla · 0af76519…");
    expect(document.body.querySelector('.cx-verdict')?.textContent).toContain('Ödeme sağlayıcısı kartı reddediyor.');
  });

  it('takip sorusu açıklamanın bağlamını taşır; ✕ bağlamı bu konuşmadan düşürür', async () => {
    publishTraceAiContext(CTX);
    const chat = stubChat(traceMeta);
    await mount(`/trace?id=${TRACE}&ai=trace`);
    expect(chat).toHaveBeenCalledTimes(1);

    // Canlı trace bağlamı şeridi + bağlam şeridinde açıklama öğesi.
    expect(document.body.querySelector('.ai-ctx')?.textContent).toContain('charge');
    expect(bodyText()).toContain('✨ trace 0af76519…');

    await act(async () => { button('Bu neden oluyor?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(2);
    const f = argsOf(chat.mock.calls[1]);
    expect(f.explainAsk).toBeUndefined();
    expect(f.subject).toBe(`trace:${TRACE}`);
    expect(f.explain).toContain(`trace ${TRACE}`);
    expect(f.explain).toContain('Ödeme sağlayıcısı kartı reddediyor');
    expect(f.trace).toBe(TRACE);
    expect(f.env).toBe('prod');
    expect(f.page).toEqual(traceContextToPage(CTX));

    await act(async () => { byAria('✨ trace 0af76519… bağlamını bu konuşmadan çıkar')?.click(); });
    expect(bodyText()).not.toContain('✨ trace 0af76519…');
    await act(async () => { button('Açık problemlerin kök nedeni?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(3);
    const g = argsOf(chat.mock.calls[2]);
    expect(g.subject).toBeUndefined();
    expect(g.explain).toBeUndefined();
  });

  it('grup çipi (explainAsk) sohbet içi açıklama turu gönderir — link açmaz', async () => {
    const chip: ChatFollowup = { text: 'ORD-100 grubunu açıkla', kind: 'exception_group', explainAsk: { kind: 'exception', id: 'ora:00000000000000a1', src: 'chip' } };
    const chat = stubChat(n => (n === 1 ? traceMeta() : { kind: 'exception', id: 'ora:00000000000000a1', label: 'ORD-100' }), [chip]);
    await mount(`/trace?id=${TRACE}&ai=trace`);
    const before = search;
    await act(async () => { button('ORD-100 grubunu açıkla')?.click(); });
    expect(chat).toHaveBeenCalledTimes(2);
    const a = argsOf(chat.mock.calls[1]);
    expect(a.explainAsk).toEqual(chip.explainAsk);
    expect(a.messages[a.messages.length - 1].text).toBe('ORD-100 grubunu açıkla');
    expect(search).toBe(before); // gezinme yok
  });
});

describe('/cosre sayfası', () => {
  it('?ai=exception:<fp> aynı yoldan: açıklama turu, adres temizlenir', async () => {
    const chat = stubChat(() => ({ kind: 'exception', id: 'ora:00000000000000b2', label: 'ORD-200' }));
    // Kanonik kodek (formatAiParam) kimliği encodeURIComponent'ler; adres onu bir kez daha kaçırır.
    await mount('/cosre?' + new URLSearchParams({ ai: 'exception:' + encodeURIComponent('ora:00000000000000b2') }).toString(), 'page');
    expect(chat).toHaveBeenCalledTimes(1);
    expect(argsOf(chat.mock.calls[0]).explainAsk).toEqual({ kind: 'exception', id: 'ora:00000000000000b2', src: 'ai-param' });
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(explainMounts.n).toBe(0);
    expect(bodyText()).toContain('Exception grubunu açıkla · ora:00000000…');
  });
});
