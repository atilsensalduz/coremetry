// sreEvalView.test.ts — v0.10.1301 — AI SRE Faz 5: vaka isabeti Go
// scoreEval kuralıyla aynı (puanlı = biten tekrar + veri + hipotez), biçimler,
// form sınırları sunucuyla aynı.
import { describe, it, expect } from 'vitest';
import type { SREEvalCase, SREHypothesisKind } from '@/lib/types';
import { DET_OUTCOME_LABEL, detOutcomeText, evalDelta, independentTop1, ratioText, runMode, caseHit, caseScored, caseUnscoredReason, coverageText, dateInputMs, durationTR, evalPct, coveragePct, validateEvalForm, defaultEvalWindow, HYP_KIND_LABEL, CATEGORY_LABEL } from './sreEvalView';

const base: SREEvalCase = { caseId: 'p:1', problemId: '1', service: 'pay', category: 'dependency', finalCause: 'db', startedAt: 0, state: 'done',
  coverage: { data: 2, noData: 1, failed: 0 } };
const h = (kind: SREHypothesisKind, category: string) => ({ kind, category, score: 0.5, band: 'orta' as const });

describe('sreEvalView', () => {
  it('isabet: ilk sıra / ilk 3 / kaçtı; 4. sıra sayılmaz', () => {
    expect(caseHit({ ...base, top: [h('dependency', 'dependency')] })).toBe('top1');
    expect(caseHit({ ...base, top: [h('deploy', 'deploy'), h('db_slow', 'dependency')] })).toBe('top3');
    expect(caseHit({ ...base, top: [h('deploy', 'deploy'), h('config', 'config'), h('saturation', 'capacity'), h('dependency', 'dependency')] })).toBe('miss');
  });
  it('puanlanmayan vaka: veri yok / sürüyor / eşlenmiyor', () => {
    const noData = { ...base, coverage: { data: 0, noData: 3, failed: 0 }, top: [h('deploy', 'deploy')] };
    expect(caseScored(noData)).toBe(false);
    expect(caseHit(noData)).toBeNull();
    expect(caseUnscoredReason(noData)).toContain('veri yok');
    expect(caseUnscoredReason({ ...base, state: 'queued' })).toBe('kuyrukta');
    expect(caseUnscoredReason({ ...base, state: 'unmapped', reason: 'kategori eşlenmiyor' })).toBe('kategori eşlenmiyor');
  });
  it('biçimler', () => {
    expect(evalPct(null)).toBe('—');
    expect(evalPct(0.6667)).toBe('%67');
    expect(coveragePct(45.5)).toBe('%46');
    expect(durationTR(42_000)).toBe('42 sn');
    expect(durationTR(210_000)).toBe('3,5 dk');
    expect(durationTR(null)).toBe('—');
    expect(coverageText(base)).toBe('2/3');
    expect(coverageText({ ...base, coverage: undefined })).toBeNull();
  });
  it('form: sunucu sınırları', () => {
    expect(validateEvalForm('2026-10-01', '2026-09-01', 10)).toBe('başlangıç bitişten önce olmalı');
    expect(validateEvalForm('2025-01-01', '2026-10-01', 10)).toBe('pencere en çok 366 gün');
    expect(validateEvalForm('2026-09-01', '2026-10-01', 51)).toBe('vaka sayısı 1–50');
    expect(validateEvalForm('x', '2026-10-01', 5)).toBe('tarih yyyy-aa-gg olmalı');
    const ok = validateEvalForm('2026-09-01', '2026-10-01', 20);
    expect(ok).toEqual({ from: dateInputMs('2026-09-01'), to: dateInputMs('2026-10-01', true), limit: 20 });
    expect(defaultEvalWindow(Date.parse('2026-10-17T12:00:00Z'))).toEqual({ from: '2026-07-19', to: '2026-10-17' });
  });
  it('Go aynaları: 8 hipotez türü, 10 vaka kategorisi', () => {
    expect(Object.keys(HYP_KIND_LABEL)).toHaveLength(8);
    expect(Object.keys(CATEGORY_LABEL).sort()).toEqual(['capacity', 'code_bug', 'config', 'data', 'dependency', 'deploy', 'external', 'infra', 'noise', 'other']);
  });
  it('v0.10.1450 karşılaştırma biçimleri', () => {
    expect(evalDelta(null)).toBe('—');
    expect(evalDelta(0)).toBe('0');
    expect(evalDelta(0.004)).toBe('0');
    expect(evalDelta(0.0525)).toBe('+5 puan');
    expect(evalDelta(-0.1667)).toBe('−17 puan');
    expect(evalDelta(-12.5, 1)).toBe('−13 puan');
    expect(ratioText({ n: 2, d: 3, v: 0.6667 })).toBe('%67 (2/3)');
    expect(ratioText({ n: 0, d: 0, v: null })).toBe('— (0/0)');
    expect(detOutcomeText(undefined)).toBe('—');
    expect(detOutcomeText({ value: 'inconclusive', reason: 'low_coverage', confidence: 0.8, coverage: 0.3 })).toBe('sonuçsuz (düşük kapsam)');
    expect(detOutcomeText({ value: 'likely', confidence: 0.6, coverage: 1 })).toBe(DET_OUTCOME_LABEL.likely);
    expect(runMode({})).toBe('deterministik');
    expect(runMode({ loop: true })).toBe('döngü');
  });
  it('manşet bağımsız top-1: eski koşuda yok', () => {
    const score = { cases: 3, pending: 0, unmapped: 0, skipped: 0, errored: 0, noData: 0, scored: 3, top1Hits: 2, top3Hits: 3, top1Acc: 0.6667,
      top3Acc: 1, entityScored: 0, entityHits: 0, entityAcc: null, coveragePct: 100, live: { runs: 0, p50Ms: null, p90Ms: null },
      acceptedSuggestion: 1, independentScored: 2, independentTop1Hits: 1, independentTop3Hits: 2, independentTop1Acc: 0.5, independentTop3Acc: 1 };
    expect(independentTop1({ score })).toBeNull();
    const v = independentTop1({ score, suggestionKnown: true });
    expect(v?.pct).toBe('%50');
    expect(v?.hint).toContain('1/2 vaka');
    expect(v?.hint).toContain('1 öneri aynen kabul');
  });
});
