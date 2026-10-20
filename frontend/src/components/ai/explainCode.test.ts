// explainCode.test.ts — v0.10.1406 (Tek CoSRE adım 3: sohbette "Kodu da incele"
// ve taze "Yeniden üret"). Operatör: "Trace ve exception üzerinden çalışan Ask
// CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE bütünselliğine
// uymuyor."
//
// Saf çekirdek: ?aicode=1 kod niyeti yalnız trace / span exception'da; fresh
// yalnız istekte yaşar (kullanıcı turunda saklanmaz); kod çipi niyetini aynen
// gönderir.
import { describe, it, expect } from 'vitest';
import type { ChatFollowup } from '@/lib/types';
import { explainAskFor, explainCodeCapable, withoutFresh } from './explainTurn';
import { followupSendOpts } from './followupSend';

const TRACE = '5b8efff798038103d269b633813fc60c';

describe('explainAskFor — kod turu (?aicode=1)', () => {
  it('trace / span exception: code:true + kod metni', () => {
    expect(explainAskFor({ kind: 'trace', id: TRACE }, 'ai-param', undefined, true)).toEqual({
      userText: "Bu trace'i kodla birlikte açıkla · 5b8efff7…",
      explainAsk: { kind: 'trace', id: TRACE, src: 'ai-param', code: true },
    });
    expect(explainAskFor({ kind: 'exception', id: 'abc123ff00112233' }, undefined, 'ORDERS_GET_X', true)).toEqual({
      userText: 'ORDERS_GET_X grubunu kodla birlikte açıkla · abc123ff…',
      explainAsk: { kind: 'exception', id: 'abc123ff00112233', code: true },
    });
  });
  it('Oracle grubu ve diğer türler: kodsuz (dürüst düşüş)', () => {
    expect(explainAskFor({ kind: 'exception', id: 'ora:00000000000000b2' }, undefined, undefined, true)).toEqual({
      userText: 'Exception grubunu açıkla · ora:00000000…',
      explainAsk: { kind: 'exception', id: 'ora:00000000000000b2' },
    });
    expect(explainAskFor({ kind: 'problem', id: 'p-1' }, undefined, undefined, true)?.explainAsk).toEqual({ kind: 'problem', id: 'p-1' });
    expect(explainCodeCapable('trace', TRACE)).toBe(true);
    expect(explainCodeCapable('exception', 'abc123ff')).toBe(true);
    expect(explainCodeCapable('exception', 'ora:00000000000000b2')).toBe(false);
    expect(explainCodeCapable('span', TRACE)).toBe(false);
  });
  it('kod bayrağı yokken davranış v0.10.1405 ile aynı', () => {
    expect(explainAskFor({ kind: 'trace', id: TRACE }, 'button')).toEqual({
      userText: "Bu trace'i açıkla · 5b8efff7…", explainAsk: { kind: 'trace', id: TRACE, src: 'button' },
    });
  });
});

describe('withoutFresh — fresh yalnız istekte', () => {
  it('fresh düşer, gerisi aynen; fresh yoksa aynı nesne', () => {
    const a = { kind: 'trace' as const, id: TRACE, code: true, fresh: true };
    expect(withoutFresh(a)).toEqual({ kind: 'trace', id: TRACE, code: true });
    const b = { kind: 'trace' as const, id: TRACE };
    expect(withoutFresh(b)).toBe(b);
  });
});

describe('kod çipi', () => {
  it('sunucunun "Kodu da incele" çipi niyetini (code:true, src:chip) aynen gönderir', () => {
    const chip: ChatFollowup = { text: 'Kodu da incele', label: 'Kodu da incele', kind: 'code',
      explainAsk: { kind: 'trace', id: TRACE, src: 'chip', code: true } };
    expect(followupSendOpts(chip)?.explainAsk).toEqual({ kind: 'trace', id: TRACE, src: 'chip', code: true });
  });
});
