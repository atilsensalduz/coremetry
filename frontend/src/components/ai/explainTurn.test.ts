// explainTurn.test.ts — v0.10.1400 (Tek CoSRE). Operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor."
//
// Saf çekirdek: özne → görünen tur + yapısal niyet; son açıklama turu → takip
// bağlamı; arşiv gidiş-dönüşü künyeyi korur; çip ve bağlam çipi niyet taşır.
import { describe, it, expect } from 'vitest';
import type { ChatFollowup, ChatTurn } from '@/lib/types';
import {
  activeExplainFrom, explainAskFor, explainContextLabel, explainEvidence, explainFollowupContext,
  explainUserText, isExplainTurnKind, shortExplainId,
} from './explainTurn';
import { hasCompletedExchange, persistMessages, restoreTurns } from './chatPersist';
import { followupSendOpts } from './followupSend';
import { contextStarter } from '@/lib/chatContext';

const TRACE = '5b8efff798038103d269b633813fc60c';

describe('explainAskFor', () => {
  it('trace / exception → görünen metin + niyet; diğer türler null (çekmece özne kipi)', () => {
    expect(explainAskFor({ kind: 'trace', id: TRACE }, 'button')).toEqual({
      userText: "Bu trace'i açıkla · 5b8efff7…",
      explainAsk: { kind: 'trace', id: TRACE, src: 'button' },
    });
    expect(explainAskFor({ kind: 'exception', id: 'ora:00000000000000b2' })).toEqual({
      userText: 'Exception grubunu açıkla · ora:00000000…',
      explainAsk: { kind: 'exception', id: 'ora:00000000000000b2' },
    });
    expect(explainAskFor({ kind: 'problem', id: 'p-1' })).toBeNull();
    expect(explainAskFor({ kind: 'span', id: TRACE, spanId: 'aaaaaaaaaaaaaaa1' })).toBeNull();
    expect(explainAskFor({ kind: 'trace', id: '  ' })).toBeNull();
    expect(explainAskFor(null)).toBeNull();
  });
  it('kısa kimlik + grup etiketi', () => {
    expect(shortExplainId('abcd')).toBe('abcd');
    expect(shortExplainId('0000000000000c3')).toBe('00000000…');
    expect(explainUserText('exception', '0000000000000c3', 'ORD-200')).toBe('ORD-200 grubunu açıkla · 00000000…');
    expect(isExplainTurnKind('trace')).toBe(true);
    expect(isExplainTurnKind('incident')).toBe(false);
  });
});

const EXPLAIN_TURNS: ChatTurn[] = [
  { role: 'user', text: "Bu trace'i açıkla · 5b8efff7…", explainAsk: { kind: 'trace', id: TRACE } },
  {
    role: 'assistant', text: '**Kök Neden**\n- payments kartı reddediyor. Kontrol: sağlayıcı.', exchangeId: 'x1',
    explain: { kind: 'trace', id: TRACE, surface: 'explain-trace:chat', evidenceSpanIds: ['aaaaaaaaaaaaaaa1'], oracleRows: 2 },
  },
];

describe('activeExplainFrom + takip bağlamı', () => {
  it('son tamamlanmış açıklama turu; araya giren düz turlar onu düşürmez', () => {
    const a = activeExplainFrom([...EXPLAIN_TURNS, { role: 'user', text: 'peki loglar?' }, { role: 'assistant', text: 'log cevabı' }]);
    expect(a?.meta.id).toBe(TRACE);
    expect(a?.key).toBe(`1:trace:${TRACE}`);
  });
  it('bekleyen / hatalı / boş açıklama turu etkin değil', () => {
    expect(activeExplainFrom([EXPLAIN_TURNS[0], { ...EXPLAIN_TURNS[1], pending: true }])).toBeNull();
    expect(activeExplainFrom([EXPLAIN_TURNS[0], { ...EXPLAIN_TURNS[1], error: 'x' }])).toBeNull();
    expect(activeExplainFrom([EXPLAIN_TURNS[0], { ...EXPLAIN_TURNS[1], text: ' ' }])).toBeNull();
    expect(activeExplainFrom([])).toBeNull();
  });
  it('çekmece özne kipindeki AYNI iki alan: explain (künye + metin + kanıt) ve subject kodeği', () => {
    const a = activeExplainFrom(EXPLAIN_TURNS);
    if (!a) throw new Error('etkin açıklama yok');
    const fc = explainFollowupContext(a);
    expect(fc.subject).toBe(`trace:${TRACE}`);
    expect(fc.explain).toContain(`trace ${TRACE}`);
    expect(fc.explain).toContain('payments kartı reddediyor');
    expect(fc.explain).toContain('aaaaaaaaaaaaaaa1');
    expect(explainContextLabel(a.meta)).toBe('✨ trace 5b8efff7…');
    expect(explainContextLabel({ kind: 'exception', id: 'ora:00000000000000b2', label: 'ORD-200' })).toBe('✨ ORD-200 ora:00000000…');
    expect(explainEvidence(a.meta)).toEqual({ spans: 1, traces: 0, oracle: 2 });
    expect(explainEvidence(undefined)).toBeUndefined();
  });
});

describe('kalıcılık — künye arşive girer, geri yüklenince yapısal kalır', () => {
  it('persistMessages / restoreTurns gidiş-dönüşü', () => {
    const msgs = persistMessages(EXPLAIN_TURNS);
    expect(msgs[0]).toEqual({ role: 'user', text: EXPLAIN_TURNS[0].text }); // kullanıcı turu niyet taşımaz
    expect(msgs[1].explain?.id).toBe(TRACE);
    const back = restoreTurns(msgs);
    expect(back[1].explain).toEqual(EXPLAIN_TURNS[1].explain);
    expect(back[1].exchangeId).toBeUndefined(); // canlı değil → kanıt yeniden duyurulmaz
    expect(hasCompletedExchange(EXPLAIN_TURNS)).toBe(true);
    // Künyesiz mesaj bayt bayt eski şekil.
    expect(persistMessages([{ role: 'assistant', text: 'x' }])).toEqual([{ role: 'assistant', text: 'x' }]);
  });
});

describe('niyet taşıyan çipler', () => {
  it('grup çipi (sunucu followup.explainAsk) → send ikinci argümanı', () => {
    const f: ChatFollowup = { text: 'ORD-100 grubunu açıkla', kind: 'exception_group', explainAsk: { kind: 'exception', id: 'ora:00000000000000a1', src: 'chip' } };
    expect(followupSendOpts(f)).toEqual({ explainAsk: f.explainAsk });
  });
  it('bağlam çipi (trace / exception) niyet taşır; problem çipi düz soru', () => {
    expect(contextStarter('/trace', `?id=${TRACE.toUpperCase()}`)?.explainAsk).toEqual({ kind: 'trace', id: TRACE, src: 'starter' });
    expect(contextStarter('/inbox', '?exception=abc123ff')?.explainAsk).toEqual({ kind: 'exception', id: 'abc123ff', src: 'starter' });
    expect(contextStarter('/inbox', '?problem=pr-1')?.explainAsk).toBeUndefined();
  });
});
