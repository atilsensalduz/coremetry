// v0.10.1533 (Problem standardı R5) — bildirim politikası ekranının saf çekirdeği.
import { describe, it, expect } from 'vitest';
import { holdLabel, enforceReadiness, parseDigestTimes, reductionPct } from './notifyPolicyForm';
import type { NotifyReplayReport } from './types';

const rep = (checked: number, held: number) => ({ recall: { checked, held, pass: held === 0, cases: [], outOfFunnel: 0, notFound: 0 } }) as unknown as NotifyReplayReport;

describe('enforceReadiness (D8)', () => {
  it('replay yok / işaret yok / tutulan var → kapalı; %100 → açık', () => {
    expect(enforceReadiness(null).ok).toBe(false);
    expect(enforceReadiness(rep(0, 0)).ok).toBe(false);
    const r = enforceReadiness(rep(10, 1));
    expect(r.ok).toBe(false);
    expect(r.text).toContain('%90');
    expect(enforceReadiness(rep(10, 0))).toEqual({ ok: true, text: 'Recall %100 — 10 gerçek olayın hepsi yeni barla da bildirilirdi.' });
  });
});

describe('holdLabel', () => {
  it.each([['p3:digest', 'P3 → özet'], ['gate:G1', 'düşük güven (G1)'], ['impact:error', 'etki tabanı (hata)'], ['x', 'x']])('%s', (c, w) => {
    expect(holdLabel(c)).toBe(w);
  });
});

describe('parseDigestTimes', () => {
  it('ayrıştırır, sıralar, tekrarı atar', () => {
    expect(parseDigestTimes('16:00, 09:00 09:00')).toEqual({ times: ['09:00', '16:00'] });
  });
  it('bozuk saat', () => { expect(parseDigestTimes('9:00').error).toContain('SS:DD'); });
  it('en çok 6', () => { expect(parseDigestTimes('01:00 02:00 03:00 04:00 05:00 06:00 07:00').error).toBeTruthy(); });
});

it('reductionPct', () => {
  expect(reductionPct(100, 30)).toBe(70);
  expect(reductionPct(0, 0)).toBeNull();
});
