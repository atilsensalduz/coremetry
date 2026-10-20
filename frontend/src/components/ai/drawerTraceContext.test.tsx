// @vitest-environment jsdom
//
// v0.10.944 (CoSRE Faz A) — trace çekmecesinin BAĞLAM devri, çalışma
// zamanında. Kaynak pini burada yetmez: iddiaların hepsi çalışma-zamanı
// dalı (hangi alanın hangi turda tele gittiği, kaydetmenin neyi taşıdığı,
// geçmişten açılan konuşmanın hangi kipe döndüğü).
//
//   (1) Çekmece sohbeti HER TURDA trace/env/page + trace penceresi ±5 dk
//       (rangeS/toMs) gönderir; page Go aynasının alanlarıyla;
//   (2) konuşma `subject` + ≤2 KB `context` anlık görüntüsüyle saklanır;
//   (3) geçmişten açılan ÖZNELİ konuşma özne kipini yeniden açar: ?ai=
//       adrese yazılır, "Bağlam" şeridi kayıtlı görüntüyü gösterir, sohbet
//       turları devralınır ve devam yazımı AYNI kimliğe gider;
//   (4) canlı bağlam (sayfanın yayını) varsa şerit onu gösterir; seçim
//       değişince sonraki tur yeni span'i taşır;
//   (5) devralınan konuşma açıklamadan BAĞIMSIZ görünür: açıklama hata/boş
//       dönse de, "Yeniden sor" onAnswer('') bassa da turlar kalır;
//   (6) özneden gezinip çıkınca (?ai= düştü) devralma da düşer — aynı özne
//       sonra açılınca eski konuşma/bağlam (env, span, pencere) sızmaz.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act, useEffect } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({
    matches: false, media: q, onchange: null,
    addListener() {}, removeListener() {},
    addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
});

// Açıklama gövdesi kapsam dışı: anında bir açıklama "üretir" ki çekmece
// sohbeti (explain metni varken çizilir) açılsın. v0.10.944 — kontrol
// nesnesi: `answer: null` = açıklama hata/boş döndü (onAnswer hiç çağrılmaz);
// `onAnswer` dışarı verilir ki test "Yeniden sor"un onAnswer('')'ını bassın.
const EXPLAIN_TEXT = 'Kök neden: payments yavaş.';
const explainCtl = vi.hoisted(() => ({
  answer: 'Kök neden: payments yavaş.' as string | null,
  onAnswer: null as ((t: string) => void) | null,
}));
vi.mock('@/components/CopilotExplain', () => ({
  CopilotExplain: ({ onAnswer }: { onAnswer?: (t: string) => void }) => {
    useEffect(() => {
      explainCtl.onAnswer = onAnswer ?? null;
      if (explainCtl.answer !== null) onAnswer?.(explainCtl.answer);
    }, [onAnswer]);
    return null;
  },
}));

import { api } from '@/lib/api';
import type { AiConversation, AiConversationSummary, ChatStreamEvent, PageContext } from '@/lib/types';
import { AIDrawerBody } from './AIDrawerBody';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';
import { AuthProvider } from '@/components/AuthProvider';
import { ConfirmProvider } from '@/components/ui/ConfirmDialog';
import { CopilotChat } from '@/components/CopilotChat';
import {
  TRACE_CHAT_PAD_MS, publishTraceAiContext, traceContextToPage, type TraceAiContext,
} from '@/lib/traceAiContext';

const TRACE = '0af7651916cd43dd8448eb211c80319c';
const FROM_MS = 1_790_000_000_000;
const CTX: TraceAiContext = {
  traceId: TRACE, spanId: 'b7ad6b7169203331', spanName: 'charge', service: 'payments',
  env: 'prod', cluster: 'cluster-a', namespace: 'billing', pod: 'payments-0',
  fromNs: FROM_MS * 1e6, toNs: (FROM_MS + 1_200) * 1e6,
};

let host: HTMLDivElement;
let root: Root;
let search = '';
function Probe() { search = useLocation().search; return null; }

const bodyText = () => document.body.textContent ?? '';
const button = (label: string) =>
  Array.from(document.body.querySelectorAll('button')).find(b => b.textContent?.includes(label));

function stubChat() {
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      onEvent({ kind: 'answer', text: 'payments p95 arttı', exchangeId: 'x1' });
      onEvent({ kind: 'done', ok: true });
    });
}

// copilotChat konumsal: (messages, onEvent, signal, service, operation,
// explain, subject, rangeS, trace, env, toMs, profile, conversation, page, pinnedPage)
type ChatArgs = Parameters<typeof api.copilotChat>;
const argsOf = (c: ChatArgs) => ({
  messages: c[0], service: c[3], subject: c[6], rangeS: c[7], trace: c[8], env: c[9],
  toMs: c[10], conversation: c[12], page: c[13] as PageContext | undefined,
});

beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  search = '';
  __resetCopilotEnabledCache();
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  publishTraceAiContext(null);
  explainCtl.answer = EXPLAIN_TEXT;
  explainCtl.onAnswer = null;
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('AIDrawerChat — trace bağlamı her turda (1) + kalıcı görüntü (2)', () => {
  it('trace/env/page + pencere ±5 dk gider; kaydetme subject + context taşır', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const chat = stubChat();
    const save = vi.spyOn(api, 'saveAiConversation').mockResolvedValue({
      id: 'drw-1', title: 't', updatedAt: 1, messages: [],
    });
    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={[`/trace?id=${TRACE}`]}>
          <AIDrawerBody subject={{ kind: 'trace', id: TRACE }} onClose={() => {}} traceCtx={CTX} />
        </MemoryRouter>,
      );
    });
    await act(async () => { button('Bu neden oluyor?')?.click(); });

    expect(chat).toHaveBeenCalledTimes(1);
    const a = argsOf(chat.mock.calls[0]);
    expect(a.subject).toBe(`trace:${TRACE}`);
    expect(a.trace).toBe(TRACE);
    expect(a.env).toBe('prod');
    expect(a.service).toBeUndefined(); // guided'ın servis varsayılanı DEĞİŞMEDİ
    expect(a.page).toEqual(traceContextToPage(CTX));
    expect(a.page?.timeRange).toEqual({ preset: 'custom', fromMs: FROM_MS, toMs: FROM_MS + 1_200 });
    // Trace 2026'da bitti, test saati gerçek → bitiş şimdiden geride, kırpılmaz.
    expect(a.toMs).toBe(FROM_MS + 1_200 + TRACE_CHAT_PAD_MS);
    expect(a.rangeS).toBe(Math.ceil((1_200 + 2 * TRACE_CHAT_PAD_MS) / 1000));

    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0][0].subject).toBe(`trace:${TRACE}`);
    expect(save.mock.calls[0][0].context).toEqual(traceContextToPage(CTX));

    // İkinci tur da AYNI alanları taşır (her tur, yalnız ilki değil).
    await act(async () => { button('Nasıl düzeltirim?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(2);
    expect(argsOf(chat.mock.calls[1]).page?.spanId).toBe(CTX.spanId);
    expect(argsOf(chat.mock.calls[1]).conversation).toBe('drw-1');
  });

  it('bağlam yoksa yalnız trace kimliği gider — env/pencere TAHMİN edilmez', async () => {
    const chat = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'drw-2', title: 't', updatedAt: 1, messages: [] });
    await act(async () => {
      root.render(
        <MemoryRouter>
          <AIDrawerBody subject={{ kind: 'trace', id: TRACE }} onClose={() => {}} traceCtx={null} />
        </MemoryRouter>,
      );
    });
    await act(async () => { button('Bu neden oluyor?')?.click(); });
    const a = argsOf(chat.mock.calls[0]);
    expect(a.trace).toBe(TRACE);
    expect(a.env).toBeUndefined();
    expect(a.rangeS).toBeUndefined();
    expect(a.toMs).toBeUndefined();
    expect(a.page).toEqual({ page: 'trace', path: '/trace', traceId: TRACE });
  });

  it('trace DIŞI özne (exception) eski davranışta: trace/page/pencere gönderilmez', async () => {
    const chat = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'drw-3', title: 't', updatedAt: 1, messages: [] });
    await act(async () => {
      root.render(
        <MemoryRouter>
          <AIDrawerBody subject={{ kind: 'exception', id: 'fp-checkout-npe' }} onClose={() => {}} traceCtx={CTX} />
        </MemoryRouter>,
      );
    });
    await act(async () => { button('Bu neden oluyor?')?.click(); });
    const a = argsOf(chat.mock.calls[0]);
    expect(a.trace).toBeUndefined();
    expect(a.page).toBeUndefined();
    expect(a.rangeS).toBeUndefined();
  });
});

// ── (3)/(4) CopilotChat kabuğu: geçmişten özneli konuşma + şerit ──
const SAVED_CTX: PageContext = {
  page: 'trace', path: '/trace', traceId: TRACE, spanId: 'b7ad6b7169203331', service: 'payments',
  env: 'uat', cluster: 'cluster-a', namespace: 'billing',
  timeRange: { preset: 'custom', fromMs: FROM_MS, toMs: FROM_MS + 1_200 },
};
const THREADS: AiConversationSummary[] = [
  { id: 'd1', title: 'Explain trace · 0af76519…', updatedAt: Date.now() * 1e6, messages: 2, subject: `trace:${TRACE}` },
];
const CONV: AiConversation = {
  id: 'd1', title: 'Explain trace · 0af76519…', updatedAt: 5, subject: `trace:${TRACE}`, context: SAVED_CTX,
  messages: [{ role: 'user', text: 'dünkü soru' }, { role: 'assistant', text: 'dünkü cevap' }],
};

function shell(path = '/') {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <AuthProvider><ConfirmProvider><CopilotChat /><Probe /></ConfirmProvider></AuthProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
}

/** FAB → Geçmiş → kayıtlı `trace:<id>` satırı. */
async function openSavedTrace() {
  await act(async () => { root.render(shell('/')); });
  const fab = document.querySelector<HTMLButtonElement>('.cm-ai-fab');
  if (!fab) throw new Error('FAB çizilmedi');
  await act(async () => { fab.click(); });
  await act(async () => { button('Geçmiş')?.click(); });
  await act(async () => { button('Explain trace · 0af76519…')?.click(); });
}

// v0.10.1400 (Tek CoSRE; operatör: "Trace ve exception üzerinden çalışan Ask
// CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
// uymuyor.") — trace / exception artık çekmecenin ÖZNE KİPİNE dönmez:
// v0.10.944'ün (3)/(5)/(6) pinleri (geçmişten özne kipini yeniden açma,
// açıklamadan bağımsız devralma, gezinmede devralmanın düşmesi) trace için
// GEÇERSİZ — açıklama bir sohbet turu (CopilotChat.explainTurn.test.tsx).
// Kalan sözleşme: eski trace-özneli konuşma Geçmiş'ten NORMAL yüklenir; canlı
// şerit artık açıklama turunun bağlamında (yeni dosyada pinli).
describe('CopilotChat — trace özneli eski konuşma (v0.10.1400)', () => {
  beforeEach(() => {
    vi.spyOn(api, 'me').mockResolvedValue({ id: 'u1', email: 'op@example.com', role: 'admin', firstName: 'Deniz' });
    vi.spyOn(api, 'problemsCount').mockResolvedValue({ count: 0 });
    vi.spyOn(api, 'problems').mockResolvedValue({ items: [], total: 0, truncated: false });
  });

  it('Geçmiş satırı özne kipini AÇMAZ; turlar sohbete yüklenir, devam AYNI konuşmaya', async () => {
    vi.spyOn(api, 'aiConversations').mockResolvedValue(THREADS);
    const get = vi.spyOn(api, 'aiConversation').mockResolvedValue(CONV);
    const chat = stubChat();
    vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ ...CONV, messages: [] });

    await openSavedTrace();
    expect(get).toHaveBeenCalledWith('d1');
    expect(new URLSearchParams(search).get('ai')).toBeNull();
    expect(bodyText()).toContain('dünkü cevap');
    expect(document.body.querySelector('.ai-ctx')).toBeNull();
    // Tek composer (CoSRE'nin kendisi) — çekmece sohbetinin ikincisi yok.
    expect(document.body.querySelectorAll('textarea').length).toBe(1);

    await act(async () => { button('Açık problemlerin kök nedeni?')?.click(); });
    expect(chat).toHaveBeenCalledTimes(1);
    const a = argsOf(chat.mock.calls[0]);
    expect(a.conversation).toBe('d1');
    expect(a.messages.map(m => m.text)).toContain('dünkü soru');
  });

  it('trace DIŞI öznede şerit yok', async () => {
    vi.spyOn(api, 'aiConversations').mockResolvedValue([]);
    stubChat(); // v0.10.1400 — ?ai=exception sohbete açıklama turu gönderir
    await act(async () => { root.render(shell('/problems?exc=fp-1&ai=exception')); });
    await act(async () => { await Promise.resolve(); });
    expect(document.body.querySelector('[role="dialog"]')).toBeTruthy();
    expect(document.body.querySelector('.ai-ctx')).toBeNull();
  });
});
