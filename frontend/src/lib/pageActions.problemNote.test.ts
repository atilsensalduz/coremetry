// pageActions.problemNote.test.ts — v0.10.1169: "probleme not" aksiyon ayrıştırıcısı.
import { describe, it, expect } from 'vitest';
import { parseProblemNoteAction, parseAction } from './pageActions';

describe('parseProblemNoteAction', () => {
  const ok = { kind: 'problem_note', problemId: 'p-1', text: 'CoSRE bulgusu (x): y', token: 'a.b', tool: 'x', label: 'L' };
  it('geçerli yük', () => {
    expect(parseProblemNoteAction(ok)).toEqual(ok);
  });
  it('eksik jeton / metin / problem ya da başka tür reddedilir', () => {
    expect(parseProblemNoteAction({ ...ok, token: '' })).toBeNull();
    expect(parseProblemNoteAction({ ...ok, text: '' })).toBeNull();
    expect(parseProblemNoteAction({ ...ok, problemId: '' })).toBeNull();
    expect(parseProblemNoteAction({ ...ok, kind: 'apply' })).toBeNull();
    expect(parseProblemNoteAction(null)).toBeNull();
  });
  it('apply ayrıştırıcısı not aksiyonunu almaz', () => {
    expect(parseAction(ok)).toBeNull();
  });
});
