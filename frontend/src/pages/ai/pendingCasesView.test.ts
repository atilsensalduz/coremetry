// pendingCasesView.test.ts — v0.10.1475: /ai onay bekleyen vakalar yardımcıları.
import { describe, it, expect } from 'vitest';
import { evalReadinessHint, isoToNs, nextCaseId, pendingCounterText, suggestionSourceLabel } from './pendingCasesView';

const counts = { draftProblem: 41, draftException: 9, confirmedProblem: 7, confirmedException: 2, independentProblem: 4 };

describe('pendingCasesView', () => {
  it('sayaç yalnız problem vakaları (eval kümesi)', () => {
    expect(pendingCounterText(counts)).toBe('onaylı: 7 / taslak: 41');
  });
  it('eşik ipucu: eksik sayı / yeterli', () => {
    expect(evalReadinessHint(counts, 20)).toEqual({ ready: false, text: expect.stringContaining('13 vaka daha') });
    expect(evalReadinessHint({ ...counts, confirmedProblem: 20 }, 20).ready).toBe(true);
  });
  it('sıradaki satır atlananları geçer, sonda null', () => {
    const rows = [{ caseId: 'a' }, { caseId: 'b' }, { caseId: 'c' }];
    expect(nextCaseId(rows, 'a', new Set())).toBe('b');
    expect(nextCaseId(rows, 'a', new Set(['b']))).toBe('c');
    expect(nextCaseId(rows, 'c', new Set())).toBeNull();
  });
  it('zaman ve öneri kaynağı', () => {
    expect(isoToNs('2026-10-20T10:00:00Z')).toBe(Date.parse('2026-10-20T10:00:00Z') * 1e6);
    expect(isoToNs('1970-01-01T00:00:00Z')).toBe(0);
    expect(isoToNs('x')).toBe(0);
    expect(suggestionSourceLabel('rca_verdict')).toBe('RCA hükmü');
    expect(suggestionSourceLabel(undefined)).toBe('—');
  });
});
