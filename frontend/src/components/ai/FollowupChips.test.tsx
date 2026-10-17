// @vitest-environment jsdom
//
// v0.10.1291 (CoSRE follow-up D) — kural-türevli takip çipleri + satır ✨.
// Pinler: (1) followups tıkı metni + yapısal kapsamı gönderir, followup_click
// sinyali yollanır; suggestions/statik geri düşüş korunur; aksiyon çipi mesaj
// göndermez, verify çağırır. (2) Sohbet log tablosunda satır ✨ YALNIZ satır
// öznesi varken ve AI çekmecesinin açıklama kipinde DEĞİLKEN; trace listesi
// satırı ✨ trace. (3) CopilotChat / AIDrawerBody çipi kapsamla gönderir (kaynak pini).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ChatFollowup, ChatQueryEvidence, ChatTurn } from '@/lib/types';

const aiSignal = vi.fn();
const enqueue = vi.fn(async () => ({ created: true }));
vi.mock('@/lib/api', () => ({ api: { aiSignal: (...a: unknown[]) => aiSignal(...a), enqueueProblemInvestigation: (...a: unknown[]) => enqueue(...(a as [])) } }));
vi.mock('./useCopilotEnabled', () => ({ useCopilotEnabled: () => true, useCopilotConfig: () => null }));
vi.mock('@/components/CosreChart', () => ({ CosreChart: () => null }));

import { FollowupChips } from './FollowupChips';
import { followupSendOpts } from './followupSend';
import { ChatBubble } from './ChatBubble';
import { ChatTraceList } from './ChatTraceList';
import { InAiDrawerContext } from './aiDrawerContext';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  aiSignal.mockClear();
  enqueue.mockClear();
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const mount = async (node: React.ReactNode) => {
  const qc = new QueryClient();
  await act(async () => {
    root.render(<QueryClientProvider client={qc}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider>);
  });
};
const chips = () => Array.from(host.querySelectorAll<HTMLButtonElement>('button'));

const FU: ChatFollowup[] = [
  { text: 'payments logları pod:"payments-a"', kind: 'pod', scope: { services: ['payments'], env: 'prod' } },
  { text: '0af7651916cd43dd8448eb211c80319c trace\'ini açıkla', kind: 'trace', scope: { trace: '0af7651916cd43dd8448eb211c80319c' } },
  { text: 'Kapasite doygunluğu hipotezini doğrula', kind: 'hypothesis',
    action: { kind: 'sre_hypothesis', problemId: 'p-1', hypothesis: 'saturation', label: 'Kapasite doygunluğu hipotezini doğrula' } },
];

describe('FollowupChips — takip çipi tıkı kapsamı taşır', () => {
  it('followups: metin + {parsed:{scope}} gönderilir, followup_click sinyali', async () => {
    const onAsk = vi.fn();
    await mount(<FollowupChips followups={FU} suggestions={['eski öneri']} fallback={['statik']} exchangeId="x1" onAsk={onAsk} />);
    expect(chips().map(b => b.textContent)).toEqual([
      '↳ payments logları pod:"payments-a"', '↳ 0af7651916cd43dd8448eb211c80319c trace\'ini açıkla', '🔎 Kapasite doygunluğu hipotezini doğrula']);
    await act(async () => { chips()[0].click(); });
    expect(onAsk).toHaveBeenCalledWith('payments logları pod:"payments-a"', { parsed: { scope: { services: ['payments'], env: 'prod' } } });
    expect(aiSignal).toHaveBeenCalledWith('x1', 'followup_click', 'pod');
  });
  it('aksiyon çipi mesaj göndermez, verify kuyruğa alır', async () => {
    const onAsk = vi.fn();
    await mount(<FollowupChips followups={FU} fallback={[]} exchangeId="x1" onAsk={onAsk} />);
    await act(async () => { chips()[2].click(); });
    expect(onAsk).not.toHaveBeenCalled();
    expect(enqueue).toHaveBeenCalledWith('p-1', { kind: 'verify', hypothesis: 'saturation' });
    expect(aiSignal).toHaveBeenCalledWith('x1', 'followup_click', 'hypothesis');
  });
  it('bozuk aksiyon yükü çizilmez', async () => {
    const bad: ChatFollowup[] = [{ text: 'x', kind: 'hypothesis', action: { kind: 'sre_hypothesis', problemId: 'p', hypothesis: 'bogus', label: 'x' } }];
    await mount(<FollowupChips followups={bad} fallback={['statik']} onAsk={vi.fn()} />);
    expect(chips().length).toBe(0);
  });
  it('followups yoksa suggestions (sinyal guided), o da yoksa statik (sinyalsiz)', async () => {
    const onAsk = vi.fn();
    await mount(<FollowupChips suggestions={['payments hata logları?']} fallback={['statik']} exchangeId="x1" onAsk={onAsk} />);
    await act(async () => { chips()[0].click(); });
    expect(onAsk).toHaveBeenCalledWith('payments hata logları?');
    expect(aiSignal).toHaveBeenCalledWith('x1', 'followup_click', 'guided');
    aiSignal.mockClear();
    await mount(<FollowupChips fallback={['statik']} exchangeId="x1" onAsk={onAsk} />);
    await act(async () => { chips()[0].click(); });
    expect(onAsk).toHaveBeenLastCalledWith('statik');
    expect(aiSignal).not.toHaveBeenCalled();
  });
  it('followupSendOpts: kapsamsız çip istek gövdesini değiştirmez', () => {
    expect(followupSendOpts({ text: 'a', kind: 'slow' })).toBeUndefined();
    expect(followupSendOpts({ text: 'a', kind: 'x', command: 'logs' })).toEqual({ parsed: { command: 'logs' } });
  });
});

describe('satır ✨ — yalnız satır öznesiyle ve çekmece dışında', () => {
  const ev: ChatQueryEvidence = { tool: 'query_logs', source: 'Elasticsearch', query: 'q', queryKind: 'summary',
    window: { fromMs: 1, toMs: 2 }, rows: 2, durationMs: 1, href: '/logs?service=payments', ref: 'ev1' };
  const turn = (rowSubjects?: string[]): ChatTurn => ({
    role: 'assistant', text: 'cevap', evidence: [ev],
    blocks: [{ id: 'b1', type: 'table', seq: 1, final: true, payload: {
      title: 'payments · loglar', evidenceRef: 'ev1',
      columns: [{ key: 'time', label: 'Zaman', kind: 'time' }, { key: 'body', label: 'Mesaj', kind: 'text' }],
      rows: [[1, 'boom'], [2, 'ok']], ...(rowSubjects ? { rowSubjects } : {}),
    } }],
  });
  const html = (t: ChatTurn, inDrawer = false) => renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}><MemoryRouter>
      <InAiDrawerContext.Provider value={inDrawer}><ChatBubble turn={t} /></InAiDrawerContext.Provider>
    </MemoryRouter></QueryClientProvider>);
  const subj = 'log:1700000000123000000:payments:payments-a::ca29aa39';

  it('özne varken satır ✨ (yalnız öznesi olan satırda düğme)', () => {
    const h = html(turn([subj, '']));
    expect((h.match(/data-row-ai/g) ?? []).length).toBe(2); // sütun her satırda, düğme yalnız öznelide
    expect((h.match(/aria-expanded/g) ?? []).length).toBeGreaterThanOrEqual(1);
    expect(h).toContain('Bu log satırını AI ile açıkla');
  });
  it('rowSubjects yoksa sütun yok (eski sunucu)', () => {
    expect(html(turn())).not.toContain('data-row-ai');
  });
  it('AI çekmecesinin açıklama kipinde gizli', () => {
    expect(html(turn([subj]), true)).not.toContain('data-row-ai');
  });
  it('trace listesi satırı ✨ trace; çekmecede gizli', () => {
    const tl = { query: 'q', window: { fromNs: 1, toNs: 2 }, deepLink: '/traces', truncated: false,
      traces: [{ traceId: 'a'.repeat(32), startTime: 1e6, serviceName: 'checkout', rootName: 'POST /pay', durationMs: 12, spanCount: 3, hasError: false }] };
    const out = renderToStaticMarkup(<MemoryRouter><ChatTraceList tl={tl} /></MemoryRouter>);
    expect(out).toContain('data-row-ai');
    expect(out).toContain('Bu trace&#x27;i AI ile açıkla');
    const inDrawer = renderToStaticMarkup(<MemoryRouter><InAiDrawerContext.Provider value={true}><ChatTraceList tl={tl} /></InAiDrawerContext.Provider></MemoryRouter>);
    expect(inDrawer).not.toContain('data-row-ai');
  });
});

describe('kaynak pinleri — çip tıkı kapsamla gönderilir', () => {
  const src = (f: string) => readFileSync(new URL(f, import.meta.url), 'utf8');
  it('CopilotChat: FollowupChips → askChip → send(text, so)', () => {
    const c = src('../CopilotChat.tsx');
    expect(c).toContain('<FollowupChips followups={last.followups} suggestions={last.suggestions} fallback={FOLLOWUPS}');
    expect(c).toMatch(/const askChip = \(text: string, so\?: ChatSendOpts\) => \{[\s\S]{0,80}void send\(text, so\);/);
    expect(c).toContain('<InAiDrawerContext.Provider value={true}>');
  });
  it('AIDrawerBody: aynı bileşen, aynı kapsam yolu', () => {
    const d = src('./AIDrawerBody.tsx');
    expect(d).toContain('<FollowupChips followups=');
    expect(d).toMatch(/const askChip = \(text: string, so\?: ChatSendOpts\) => \{[\s\S]{0,60}void send\(text, so\);/);
  });
  it('useChatThread followups alanını cevaba kopyalar', () => {
    expect(src('./useChatThread.ts')).toContain('followups: e.followups');
  });
  it('Logs satırı ✨ çekmecede gizli', () => {
    const l = src('../LogTable.tsx');
    expect(l).toContain('if (inDrawer || !subject) return null;');
    expect(l).toContain('{!l.origin && <LogLineExplain l={l} />}');
  });
});
