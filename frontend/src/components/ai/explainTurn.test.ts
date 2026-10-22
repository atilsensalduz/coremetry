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
  explainRenderer, explainSubject, explainTurnSendOpts, explainUserText, isExplainTurnKind, shortExplainId,
  EXPLAIN_TURN_KINDS,
} from './explainTurn';
import { AI_KINDS, formatAiParam } from '@/lib/aiSubject'; // v0.10.1435 — formatAiParam
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
    expect(isExplainTurnKind('charts')).toBe(true);    // v0.10.1407 — özne kipi silindi
    expect(isExplainTurnKind('log-spike')).toBe(true);
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
  // v0.10.1407 — charts / log-spike da sohbet turu (kodekli niyet); log-spike'ta
  // boş kimlik = filo geneli (null DEĞİL).
  it('charts / log-spike → kodekli niyet (v0.10.1407)', () => {
    expect(explainAskFor({ kind: 'charts', id: 'orders-api', fromNs: 1, toNs: 2, scope: 'err' }, 'button')).toEqual({
      userText: 'orders-api grafiklerini açıkla · Error rate by operation',
      explainAsk: { kind: 'charts', id: 'orders-api', subject: 'charts:orders-api:1:2:err', src: 'button' },
    });
    expect(explainAskFor({ kind: 'log-spike', id: '', fromNs: 1, toNs: 2, mode: 'auto', env: '', q: '', level: 'all' })).toEqual({
      userText: 'Tüm servislerin log artışını açıkla',
      explainAsk: { kind: 'log-spike', id: '', subject: 'log-spike:*:1:2:auto:::all' },
    });
    expect(explainAskFor({ kind: 'charts', id: ' ', fromNs: 1, toNs: 2, scope: 'all' })).toBeNull();
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

// v0.10.1407 (Tek CoSRE adım 4; operatör: "Trace ve exception üzerinden çalışan
// Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
// uymuyor.") — çekmecenin özne kipi silindi: AI_KINDS'in HER türü ya bir
// açıklama turu niyetine ya da (endpoint) görünür soruya iner.
describe('her ?ai= türü bir sohbet turu (v0.10.1407)', () => {
  it('EXPLAIN_TURN_KINDS = AI_KINDS (eksik/fazla yok)', () => {
    expect([...EXPLAIN_TURN_KINDS].sort()).toEqual([...AI_KINDS].sort());
  });
  it('her tür bir niyete ya da endpoint sorusuna çözülür', () => {
    const fromNs = 1_790_000_000_000_000_000, toNs = fromNs + 3_600_000_000_000;
    const subjects = {
      trace: { kind: 'trace', id: TRACE }, span: { kind: 'span', id: TRACE, spanId: 'aaaaaaaaaaaaaaa1' },
      problem: { kind: 'problem', id: 'p-1' }, incident: { kind: 'incident', id: 'i-1' }, anomaly: { kind: 'anomaly', id: 'a-1' },
      'service-health': { kind: 'service-health', id: 'orders-api', fromNs, toNs }, runbook: { kind: 'runbook', id: 'p-1' },
      exception: { kind: 'exception', id: 'fp-1' }, charts: { kind: 'charts', id: 'orders-api', fromNs, toNs, scope: 'all' },
      'log-spike': { kind: 'log-spike', id: 'checkout', fromNs, toNs, mode: 'sel', env: 'prod', q: '', level: 'error' },
      log: { kind: 'log', id: 'orders-api', tsNs: '1790000000123000000', pod: '', traceId: '', hash: 'ca29aa39' },
      endpoint: { kind: 'endpoint', id: 'orders-api', route: '/orders', method: 'GET', fromNs, toNs },
      metric_panel: { kind: 'metric_panel', id: 'x.A', fromNs, toNs, brushFromNs: 0, brushToNs: 0, series: [] }, // v0.10.1435
    } as const;
    for (const k of AI_KINDS) {
      const x = explainAskFor(subjects[k] as Parameters<typeof explainAskFor>[0]);
      expect(x, k).not.toBeNull();
      if (k === 'endpoint') {
        expect(x!.explainAsk, k).toBeUndefined();
        expect(x!.window, k).toEqual({ rangeS: 3600, toMs: Math.floor(toNs / 1e6) });
      } else {
        expect(x!.explainAsk?.kind, k).toBe(k);
      }
    }
  });
  it('çizim kipi: künyenin renderer\'ı, yoksa türden (canlı turun geçici künyesi)', () => {
    expect(explainRenderer({ kind: 'charts' })).toBe('charts');
    expect(explainRenderer({ kind: 'log-spike' })).toBe('log-spike');
    expect(explainRenderer({ kind: 'trace' })).toBe('body');
    expect(explainRenderer({ kind: 'charts', renderer: 'body' })).toBe('body');
  });
  it('charts takip bağlamı: kodek + servis + açıklanan pencere; bağlam etiketi', () => {
    const CH = 'charts:orders-api:1790000000000000000:1790003600000000000:err';
    const a = activeExplainFrom([
      { role: 'user', text: 'orders-api grafiklerini açıkla', explainAsk: { kind: 'charts', id: 'orders-api', subject: CH } },
      { role: 'assistant', text: 'deploy sonrası hata oranı arttı', exchangeId: 'x1',
        explain: { kind: 'charts', id: 'orders-api', subject: CH, renderer: 'charts', label: 'orders-api' } },
    ]);
    if (!a) throw new Error('etkin açıklama yok');
    const fc = explainFollowupContext(a);
    expect(fc.subject).toBe(CH);
    expect(fc.service).toBe('orders-api');
    expect(fc.rangeS).toBe(3600);
    expect(fc.toMs).toBe(1790003600000);
    expect(explainContextLabel({ kind: 'charts', id: 'orders-api' })).toBe('✨ orders-api grafikleri');
    expect(explainContextLabel({ kind: 'log-spike', id: '' })).toBe('✨ log artışı tüm servisler');
  });
});

// v0.10.1435 — panelden "Bu grafiği açıkla": niyet kodek (konum + pencere +
// brush + seri) + yapısal yük; görünen metin başlık + seçili aralık + seri;
// takip bağlamı açıklanan pencere; balon metric-panel kartı.
describe('metric_panel açıklama turu (v0.10.1435)', () => {
  const fromNs = Date.UTC(2026, 9, 22, 14, 0) * 1e6, toNs = Date.UTC(2026, 9, 22, 15, 0) * 1e6;
  const subj = {
    kind: 'metric_panel' as const, id: 'd.dash01.p1', fromNs, toNs,
    brushFromNs: Date.UTC(2026, 9, 22, 14, 5) * 1e6, brushToNs: Date.UTC(2026, 9, 22, 14, 20) * 1e6, series: ['pod-a'],
  };
  const panel = { src: 'promql' as const, promql: 'sum(rate(x_total[5m]))', vars: { svc: 'checkout' }, title: 'Checkout RPS' };
  it('niyet: kodek + yük; görünen metin başlık · seçili aralık · seri', () => {
    const x = explainAskFor(subj, 'button', undefined, false, panel);
    expect(x?.explainAsk).toEqual({ kind: 'metric_panel', id: 'd.dash01.p1', subject: formatAiParam(subj), src: 'button', panel });
    expect(x?.userText).toMatch(/^Bu grafiği açıkla · Checkout RPS · \d\d:05–\d\d:20 seçili · seri: pod-a$/);
  });
  it('yük yalnız metric_panel türünde; başlıksız konum adı', () => {
    expect(explainAskFor({ kind: 'trace', id: TRACE }, 'x', undefined, false, panel)?.explainAsk?.panel).toBeUndefined();
    const bare = explainAskFor({ ...subj, id: 'x.B', brushFromNs: 0, brushToNs: 0, series: [], cmp: true });
    expect(bare?.userText).toBe('Bu grafiği açıkla · Explore · B · önceki pencereyle');
    expect(bare?.explainAsk?.panel).toBeUndefined();
  });
  it('takip bağlamı: açıklanan pencere; bağlam etiketi; çizim kipi', () => {
    const S = formatAiParam(subj);
    const a = activeExplainFrom([
      { role: 'user', text: 'Bu grafiği açıkla', explainAsk: { kind: 'metric_panel', id: 'd.dash01.p1', subject: S } },
      { role: 'assistant', text: 'pod-a 14:07 sıçradı', exchangeId: 'x1',
        explain: { kind: 'metric_panel', id: 'd.dash01.p1', subject: S, renderer: 'metric-panel', label: 'Checkout RPS' } },
    ]);
    if (!a) throw new Error('etkin açıklama yok');
    const fc = explainFollowupContext(a);
    expect(fc.subject).toBe(S);
    expect(fc.rangeS).toBe(3600);
    expect(fc.toMs).toBe(Math.floor(toNs / 1e6));
    expect(explainContextLabel(a.meta)).toBe('✨ grafik Checkout RPS');
    expect(explainRenderer({ kind: 'metric_panel' })).toBe('metric-panel');
  });
});
