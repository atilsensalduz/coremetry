// @vitest-environment jsdom
//
// v0.10.1367 (operatör onaylı, v0.10.1343 R2) — `endpoint` öznesinin çekmecesi
// ayrı bir explain yüzeyi çağırmaz; görünür bir ilk soruyu sohbete sorar ve
// sunucunun operasyon/endpoint ön-getirmesi onu deterministik çözer. Çalışma
// zamanında pinlenen sözleşme:
//   (1) soru mount'ta TAM BİR KEZ gider (yeniden render tekrar sordurmaz), ilk
//       görünür tur sorunun kendisidir (çizilmeyen seed yok);
//   (2) context: subject = uzun kodek, service = özne servisi, rangeS/toMs =
//       düğmeye basılan pencere (her zaman sabitli);
//   (3) takip sorusu ilk tamamlanmış cevabı açıklama bağlamı (explain) taşır;
//   (4) Geçmiş'ten açılan konuşmada (resume) soru YENİDEN sorulmaz.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { MemoryRouter } from 'react-router-dom';
import { api } from '@/lib/api';
import { formatAiParam, type AISubject } from '@/lib/aiSubject';
import type { AiConversation, ChatStreamEvent } from '@/lib/types';
import { AIDrawerBody } from './AIDrawerBody';
import { ENDPOINT_ASK_SUFFIX, endpointAskQuestion } from './drawerChat';
import { __resetCopilotEnabledCache } from './useCopilotEnabled';

const FROM_MS = 1_790_000_000_000;
const SUBJECT: Extract<AISubject, { kind: 'endpoint' }> = {
  kind: 'endpoint', id: 'bsa-web', route: '/BSAWEB/application/{id}', method: 'POST',
  fromNs: FROM_MS * 1e6, toNs: (FROM_MS + 3_600_000) * 1e6,
};
const ANSWER = 'POST /BSAWEB/application/{id} p99 2.4 sn; süre DB çağrısında.';

// copilotChat konumsal: (messages, onEvent, signal, service, operation,
// explain, subject, rangeS, trace, env, toMs, …)
type ChatArgs = Parameters<typeof api.copilotChat>;
const argsOf = (c: ChatArgs) => ({
  messages: c[0], service: c[3], explain: c[5], subject: c[6], rangeS: c[7], toMs: c[10],
});

let host: HTMLDivElement;
let root: Root;

function stubChat() {
  return vi.spyOn(api, 'copilotChat').mockImplementation(
    async (_m, onEvent: (e: ChatStreamEvent) => void) => {
      onEvent({ kind: 'answer', text: ANSWER, exchangeId: 'x1' });
      onEvent({ kind: 'done', ok: true });
    });
}

beforeEach(() => {
  window.history.replaceState({}, '', '/');
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = () => {};
  __resetCopilotEnabledCache();
  const mem = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => mem.get(k) ?? null,
    setItem: (k: string, v: string) => { mem.set(k, String(v)); },
    removeItem: (k: string) => { mem.delete(k); },
  });
  vi.spyOn(api, 'copilotConfig').mockResolvedValue({ enabled: true, model: 'gemma4' });
  vi.spyOn(api, 'saveAiConversation').mockResolvedValue({ id: 'drw-1', title: 't', updatedAt: 1, messages: [] });
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function flush() {
  for (let i = 0; i < 4; i++) await act(async () => { await Promise.resolve(); });
}

async function mount(props: { resume?: AiConversation | null } = {}) {
  await act(async () => {
    root.render(<MemoryRouter><AIDrawerBody subject={SUBJECT} onClose={() => {}} resume={props.resume} /></MemoryRouter>);
  });
  await flush();
}

describe('AIDrawerBody — endpoint öznesi autoAsk (v0.10.1367)', () => {
  it('soru bir kez gider; bağlam uzun kodek + servis + sabitli pencere', async () => {
    const chat = stubChat();
    await mount();
    expect(chat).toHaveBeenCalledTimes(1);
    const a = argsOf(chat.mock.calls[0]);
    const q = endpointAskQuestion(SUBJECT);
    expect(q.endsWith(ENDPOINT_ASK_SUFFIX)).toBe(true);
    // Görünür ilk tur sorunun kendisi — çizilmeyen seed turu yok.
    expect(a.messages).toEqual([{ role: 'user', text: q }]);
    expect(a.subject).toBe(formatAiParam(SUBJECT));
    expect(a.subject).toBe(`endpoint:bsa-web:${encodeURIComponent(SUBJECT.route)}:${SUBJECT.fromNs}:${SUBJECT.toNs}:POST`);
    expect(a.service).toBe('bsa-web');
    expect(a.rangeS).toBe(3600);
    expect(a.toMs).toBe(FROM_MS + 3_600_000);
    expect(a.explain).toBeUndefined();
    expect(host.textContent).toContain(q);
    expect(host.textContent).toContain(ANSWER);
    expect(host.textContent).toContain('CoSRE bu endpoint\'i inceliyor');

    // Yeniden render (aynı özne) soruyu tekrar sordurmaz.
    await mount();
    expect(chat).toHaveBeenCalledTimes(1);
  });

  it('takip sorusu ilk cevabı açıklama bağlamı olarak taşır', async () => {
    const chat = stubChat();
    await mount();
    const chip = Array.from(host.querySelectorAll('button')).find(b => b.textContent?.includes('Hangi kanıta dayanıyorsun?'));
    expect(chip).toBeDefined();
    await act(async () => { chip!.click(); });
    await flush();
    expect(chat).toHaveBeenCalledTimes(2);
    const f = argsOf(chat.mock.calls[1]);
    expect(f.explain).toContain('KONU: Endpoint analizi — endpoint POST /BSAWEB/application/{id} · servis bsa-web');
    expect(f.explain).toContain(ANSWER);
    expect(f.subject).toBe(formatAiParam(SUBJECT));
    expect(f.rangeS).toBe(3600);
    expect(f.toMs).toBe(FROM_MS + 3_600_000);
  });

  it('Geçmiş’ten açılan konuşmada (resume) yeniden sorulmaz', async () => {
    const chat = stubChat();
    const conv: AiConversation = {
      id: 'conv-1', title: 'Ask CoSRE · POST /x · bsa-web', updatedAt: 1, subject: formatAiParam(SUBJECT),
      messages: [
        { role: 'user', text: endpointAskQuestion(SUBJECT) },
        { role: 'assistant', text: ANSWER },
      ],
    };
    await mount({ resume: conv });
    expect(chat).not.toHaveBeenCalled();
    expect(host.textContent).toContain(ANSWER);
  });
});
