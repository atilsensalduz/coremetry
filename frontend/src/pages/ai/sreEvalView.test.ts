// sreEvalView.test.ts — v0.10.1301 — AI SRE Faz 5: vaka isabeti Go
// scoreEval kuralıyla aynı (puanlı = biten tekrar + veri + hipotez), biçimler,
// form sınırları sunucuyla aynı.
import { describe, it, expect } from 'vitest';
import type { SREEvalCase, SREHypothesisKind } from '@/lib/types';
import { caseHit, caseScored, caseUnscoredReason, coverageText, dateInputMs, durationTR, evalPct, coveragePct, validateEvalForm, defaultEvalWindow, HYP_KIND_LABEL, CATEGORY_LABEL } from './sreEvalView';

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
});
