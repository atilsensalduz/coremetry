// v0.10.1529 (Problem standardı R2) — satır + başlık metninin saf çekirdeği.
import { describe, it, expect } from 'vitest';
import {
  fmtStdDuration, fmtStdClock, sinceSuffix, stdSinceLine, stdDurationLine, stdImpactSecondary,
  stdGateSummary, stdSourceChip, splitStdHeadline, stdConfidenceRank, stdNextHref, STD_CONFIDENCE_LABEL,
} from './problemStandardView';
import type { ProblemStandard } from './types';

function std(over: Partial<ProblemStandard> = {}): ProblemStandard {
  return {
    family: 'error', kind: 'anomaly.error_rate', subject: 'mobile-bff', headline: 'mobile-bff: teknik hata arttı',
    impact: { primary: { text: 'hata oranı %0,7 (normal %0,05)', value: 0.7, normal: 0.05 }, known: true },
    since: { startedAt: 0, durationSec: 0, ongoing: true },
    evidence: { sentence: 'x', source: 'vm', sampled: false },
    confidence: { level: 'high', gates: [] },
    nextStep: { text: 'Servisin hata kırılımına bak' },
    ...over,
  };
}

describe('fmtStdDuration', () => {
  it.each([
    [0, '0 sn'], [45, '45 sn'], [60, '1 dk'], [35 * 60, '35 dk'], [3600, '1 sa'],
    [5 * 3600 + 40 * 60, '5 sa 40 dk'], [86400, '1 g'], [2 * 86400 + 3 * 3600, '2 g 3 sa'], [-5, '0 sn'],
  ])('%i sn → %s', (s, want) => { expect(fmtStdDuration(s)).toBe(want); });
});

describe('sinceSuffix — okunuşun son kelimesi', () => {
  it.each([
    ['06:05', "'ten"], ['09:48', "'den"], ['11:20', "'den"], ['10:12', "'den"], ['13:00', "'ten"],
    ['06:00', "'dan"], ['06:30', "'dan"], ['06:40', "'tan"], ['06:50', "'den"], ['06:10', "'dan"],
    ['06:09', "'dan"], ['06:03', "'ten"], ['00:00', "'dan"], ['24.10 06:05', "'ten"],
  ])('%s → %s', (c, want) => { expect(sinceSuffix(c)).toBe(want); });
});

describe('fmtStdClock', () => {
  const now = new Date(2026, 9, 25, 12, 0, 0).getTime();
  it('bugün → saat', () => {
    expect(fmtStdClock(new Date(2026, 9, 25, 6, 5).getTime() * 1e6, now)).toBe('06:05');
  });
  it('dün → gün.ay saat', () => {
    expect(fmtStdClock(new Date(2026, 9, 24, 6, 5).getTime() * 1e6, now)).toBe('24.10 06:05');
  });
  it('0 → tire', () => { expect(fmtStdClock(0, now)).toBe('—'); });
});

describe('stdSinceLine / stdDurationLine', () => {
  const now = new Date(2026, 9, 25, 12, 0, 0).getTime();
  it('başlangıç + ek', () => {
    const s = std({ since: { startedAt: new Date(2026, 9, 25, 6, 5).getTime() * 1e6, durationSec: 0, ongoing: true } });
    expect(stdSinceLine(s, now)).toBe("06:05'ten beri");
  });
  it('süre · sürüyor · tekrar', () => {
    expect(stdDurationLine(std({ since: { startedAt: 1, durationSec: 5 * 3600 + 40 * 60, ongoing: true, episode: 3 } })))
      .toBe('5 sa 40 dk · sürüyor · 3. kez');
    expect(stdDurationLine(std({ since: { startedAt: 1, durationSec: 35 * 60, ongoing: false, episode: 1 } })))
      .toBe('35 dk · bitti');
  });
});

describe('stdImpactSecondary — ölçülmeyeni iddia etmez', () => {
  it('ölçülmüş hata + doğrulanmış çağıran', () => {
    expect(stdImpactSecondary(std({ impact: { primary: { text: '', value: 0, normal: 0 }, known: true, failedPerMin: 420, callers: ['a', 'b', 'c'] } })))
      .toBe('~420 hatalı istek/dk · 3 çağıran (doğrulanmış)');
  });
  it('hiçbiri yok → boş', () => {
    expect(stdImpactSecondary(std())).toBe('');
  });
  it('etki bilinmiyor', () => {
    expect(stdImpactSecondary(std({ impact: { primary: { text: '', value: 0, normal: 0 }, known: false } }))).toBe('etki bilinmiyor');
  });
});

describe('stdGateSummary', () => {
  it('na/context yazılmaz; işaretler', () => {
    expect(stdGateSummary([
      { id: 'G1', name: 'trafik', status: 'pass' },
      { id: 'G2', name: 'sürdürme', status: 'unknown' },
      { id: 'G3', name: 'VM karar', status: 'na' },
      { id: 'G5', name: 'deploy', status: 'context' },
      { id: 'G6', name: 'takvim', status: 'flag' },
      { id: 'G4', name: 'topoloji', status: 'fail' },
    ])).toBe('trafik ✓ sürdürme ? takvim ! topoloji ✗');
  });
});

describe('kaynak çipi, manşet, güven', () => {
  it('kaynak çipi', () => {
    expect(stdSourceChip(std())).toBe('VM · örneklenmemiş');
    expect(stdSourceChip(std({ evidence: { sentence: '', source: 'span', sampled: true } }))).toBe('span · örneklenmiş');
    expect(stdSourceChip(std({ evidence: { sentence: '', source: 'exception', sampled: true } }))).toBe('exception grubu');
  });
  it('manşet özne önekini ayırır', () => {
    expect(splitStdHeadline(std())).toEqual({ subject: 'mobile-bff', rest: 'teknik hata arttı' });
    expect(splitStdHeadline(std({ subject: '', headline: 'Olay: x' }))).toEqual({ subject: '', rest: 'Olay: x' });
  });
  it('güven sırası + etiket', () => {
    expect(stdConfidenceRank(undefined)).toBe(2);
    expect(stdConfidenceRank(std({ confidence: { level: 'low', gates: [] } }))).toBe(1);
    expect(STD_CONFIDENCE_LABEL.high).toBe('YÜKSEK');
  });
});

describe('stdNextHref — kendine dönen ve dış adres link olmaz', () => {
  it.each([
    ['/service?name=a', '/service?name=a'],
    ['/exceptions', undefined],
    ['/inbox?anomaly=x', undefined],
    ['/problems?problem=p', undefined],
    ['/databases', '/databases'],
    ['https://x', undefined],
    ['//evil', undefined],
    [undefined, undefined],
  ])('%s → %s', (href, want) => {
    expect(stdNextHref(std({ nextStep: { text: 't', href: href as string | undefined } }))).toBe(want);
  });
});
