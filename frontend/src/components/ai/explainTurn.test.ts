// explainTurn.test.ts — v0.10.1400 (Tek CoSRE). Operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor."
//
// Saf çekirdek: özne → görünen tur + yapısal niyet; son açıklama turu → takip
// bağlamı; arşiv gidiş-dönüşü künyeyi korur; çip ve bağlam çipi niyet taşır.
import { describe, it, expect } from 'vitest';
import type { ChatFollowup, ChatTurn } from '@/lib/types';
import {
  activeExplainFrom, explainAskFor, explainAskSubject, explainContextLabel, explainEvidence, explainFollowupContext,
  explainSubject, explainTurnSendOpts, explainUserText, isExplainTurnKind, shortExplainId,
} from './explainTurn';
import { endpointAskQuestion } from './drawerChat';
import { starterAsk } from './capabilityHints';
import { hasCompletedExchange, persistMessages, restoreTurns } from './chatPersist';
import { followupSendOpts } from './followupSend';
import { contextStarter } from '@/lib/chatContext';

const TRACE = '5b8efff798038103d269b633813fc60c';

describe('explainAskFor', () => {
  it('trace / exception → görünen metin + niyet', () => {
    expect(explainAskFor({ kind: 'trace', id: TRACE }, 'button')).toEqual({
      userText: "Bu trace'i açıkla · 5b8efff7…",
      explainAsk: { kind: 'trace', id: TRACE, src: 'button' },
    });
    expect(explainAskFor({ kind: 'exception', id: 'ora:00000000000000b2' })).toEqual({
      userText: 'Exception grubunu açıkla · ora:00000000…',
      explainAsk: { kind: 'exception', id: 'ora:00000000000000b2' },
    });
    expect(explainAskFor({ kind: 'trace', id: '  ' })).toBeNull();
    expect(explainAskFor(null)).toBeNull();
  });
  it('kısa kimlik + grup etiketi', () => {
    expect(shortExplainId('abcd')).toBe('abcd');
    expect(shortExplainId('0000000000000c3')).toBe('00000000…');
    expect(explainUserText('exception', '0000000000000c3', 'ORD-200')).toBe('ORD-200 grubunu açıkla · 00000000…');
    expect(isExplainTurnKind('trace')).toBe(true);
    expect(isExplainTurnKind('incident')).toBe(true); // v0.10.1405
    expect(isExplainTurnKind('charts')).toBe(false);  // 1407'ye dek çekmece özne kipi
    expect(isExplainTurnKind('log-spike')).toBe(false);
  });
});

// v0.10.1405 (Tek CoSRE adım 2; operatör: "Trace ve exception üzerinden çalışan
// Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
// uymuyor.") — kalan ✨ türleri de sohbet turu: tür başına görünen metin + niyet;
// kimliği tek alana sığmayan türler kanonik kodeği (subject) taşır, endpoint niyet
// değil sabit pencere taşır.
describe('explainAskFor — tür başına (v0.10.1405)', () => {
  const SPAN = 'aaaaaaaaaaaaaaa1';
  const LOG = { kind: 'log' as const, id: 'orders-api', tsNs: '1790000000123000000', pod: 'orders-api-0', traceId: TRACE, hash: 'ca29aa39' };
  const cases: Array<[string, Parameters<typeof explainAskFor>[0], ReturnType<typeof explainAskFor>]> = [
    ['span', { kind: 'span', id: TRACE, spanId: SPAN }, {
      userText: "Bu span'i açıkla · aaaaaaaa…",
      explainAsk: { kind: 'span', id: TRACE, subject: `span:${TRACE}:${SPAN}`, src: 'button' } }],
    ['problem', { kind: 'problem', id: '0000000000000p01' }, {
      userText: 'Bu problemi açıkla · 00000000…', explainAsk: { kind: 'problem', id: '0000000000000p01', src: 'button' } }],
    ['incident', { kind: 'incident', id: 'inc-7' }, {
      userText: 'Bu olayı açıkla · inc-7', explainAsk: { kind: 'incident', id: 'inc-7', src: 'button' } }],
    ['anomaly', { kind: 'anomaly', id: 'an-9' }, {
      userText: 'Bu anomaliyi açıkla · an-9', explainAsk: { kind: 'anomaly', id: 'an-9', src: 'button' } }],
    ['service-health', { kind: 'service-health', id: 'orders-api', fromNs: 1000, toNs: 2000 }, {
      userText: 'orders-api sağlığını açıkla',
      explainAsk: { kind: 'service-health', id: 'orders-api', subject: 'service-health:orders-api:1000:2000', src: 'button' } }],
    ['runbook', { kind: 'runbook', id: 'p-42' }, {
      userText: 'Bu problem için runbook çıkar · p-42', explainAsk: { kind: 'runbook', id: 'p-42', src: 'button' } }],
    ['log', LOG, {
      userText: 'Bu log satırını açıkla · orders-api · orders-api-0',
      explainAsk: { kind: 'log', id: 'orders-api', subject: `log:1790000000123000000:orders-api:orders-api-0:${TRACE}:ca29aa39`, src: 'button' } }],
  ];
  for (const [name, subj, want] of cases) {
    it(name, () => expect(explainAskFor(subj, 'button')).toEqual(want));
  }
  it('endpoint: görünür soru + düğmenin penceresi, niyet YOK', () => {
    const ep = { kind: 'endpoint' as const, id: 'orders-api', route: '/orders', method: 'GET', fromNs: 1_790_000_000_000 * 1e6, toNs: (1_790_000_000_000 + 1_800_000) * 1e6 };
    const x = explainAskFor(ep, 'button');
    expect(x).toEqual({ userText: endpointAskQuestion(ep), window: { rangeS: 1800, toMs: 1_790_000_000_000 + 1_800_000 } });
    expect(explainTurnSendOpts(x!)).toEqual({ window: x!.window });
  });
  it('charts / log-spike hâlâ çekmece özne kipi (null)', () => {
    expect(explainAskFor({ kind: 'charts', id: 'orders-api', fromNs: 1, toNs: 2, scope: 'all' })).toBeNull();
    expect(explainAskFor({ kind: 'log-spike', id: '', fromNs: 1, toNs: 2, mode: 'auto', env: '', q: '', level: 'all' })).toBeNull();
  });
  it('niyet → özne: kodekten ya da {kind, id}; kodeksiz span çözülmez', () => {
    expect(explainAskSubject({ kind: 'span', id: TRACE, subject: `span:${TRACE}:${SPAN}` })).toEqual({ kind: 'span', id: TRACE, spanId: SPAN });
    expect(explainAskSubject({ kind: 'problem', id: 'p-1' })).toEqual({ kind: 'problem', id: 'p-1' });
    expect(explainAskSubject({ kind: 'span', id: TRACE })).toBeNull();
    expect(explainAskSubject({ kind: 'service-health', id: 'x', subject: 'charts:x:1:2:all' })).toBeNull(); // tür uyuşmaz
  });
  it('başlangıç öğesi (exception) yolu değişmedi', () => {
    expect(starterAsk({ chip: 'ORD-200 · orders-api', question: 'q', kind: 'exception', explainAsk: { kind: 'exception', id: 'ora:00000000000000b2', src: 'starter' } } as never))
      .toEqual({ text: 'ORD-200 · orders-api grubunu açıkla · ora:00000000…', so: { explainAsk: { kind: 'exception', id: 'ora:00000000000000b2', src: 'starter' } } });
  });
});

describe('göç eden türlerin takip bağlamı (v0.10.1405)', () => {
  const SH = 'service-health:orders-api:1790000000000000000:1790003600000000000';
  const turns: ChatTurn[] = [
    { role: 'user', text: 'orders-api sağlığını açıkla', explainAsk: { kind: 'service-health', id: 'orders-api', subject: SH } },
    { role: 'assistant', text: '**Karar** orders-api sağlıklı.', exchangeId: 'x1',
      explain: { kind: 'service-health', id: 'orders-api', subject: SH, surface: 'explain-service:chat', renderer: 'body', label: 'orders-api' } },
  ];
  it('service-health: kodek + servis + açıklanan pencere', () => {
    const a = activeExplainFrom(turns);
    if (!a) throw new Error('etkin açıklama yok');
    const fc = explainFollowupContext(a);
    expect(fc.subject).toBe(SH);
    expect(fc.service).toBe('orders-api');
    expect(fc.rangeS).toBe(3600);
    expect(fc.toMs).toBe(1790003600000);
    expect(fc.explain).toContain('orders-api sağlıklı');
    expect(explainContextLabel(a.meta)).toBe('✨ orders-api sağlığı');
  });
  it('kodeği çözülemeyen künye etkin açıklama sayılmaz (bozuk arşiv satırı)', () => {
    const bad: ChatTurn[] = [{ ...turns[1], explain: { kind: 'span', id: TRACE } }];
    expect(activeExplainFrom(bad)).toBeNull();
    expect(explainSubject({ kind: 'span', id: TRACE })).toBeNull();
  });
  it('bağlam etiketleri', () => {
    expect(explainContextLabel({ kind: 'runbook', id: '0000000000000p01', label: 'RUNBOOK' })).toBe('✨ runbook 00000000…');
    expect(explainContextLabel({ kind: 'problem', id: 'p-1', label: 'orders-api p99 latency' })).toBe('✨ problem orders-api p99 latency');
    expect(explainContextLabel({ kind: 'incident', id: 'inc-7' })).toBe('✨ olay inc-7');
    expect(explainContextLabel({ kind: 'log', id: 'orders-api' })).toBe('✨ log orders-api');
  });
  it('künye (subject / renderer / note) arşiv gidiş-dönüşünde korunur', () => {
    const rb: ChatTurn = { role: 'assistant', text: '1. adım', explain: { kind: 'runbook', id: 'p-42', label: 'RUNBOOK', renderer: 'body', note: 'Based on 2 past resolved instances of this rule on this service.' } };
    expect(restoreTurns(persistMessages([...turns, rb]))[2].explain).toEqual(rb.explain);
    expect(restoreTurns(persistMessages(turns))[1].explain?.subject).toBe(SH);
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
