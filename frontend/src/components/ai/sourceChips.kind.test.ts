// sourceChips.kind.test.ts — v0.10.1170: kaynak türü rozeti çipe taşınır.
import { describe, it, expect } from 'vitest';
import { sourceChips, SOURCE_KIND_LABEL } from './sourceChips';

describe('sourceChips kind', () => {
  it('bilinen tür taşınır, bilinmeyen / eksik yok', () => {
    const chips = sourceChips([
      { doc: 'Wiki · Runbook', ref: 'https://w/x', chunk: 1, score: 0.9, kind: 'wiki' },
      { doc: 'kasa.pdf', chunk: 1, score: 0.8, kind: 'upload' },
      { doc: 'eski', chunk: 1, score: 0.7 },
      { doc: 'garip', chunk: 1, score: 0.6, kind: 'x' as never },
    ]);
    expect(chips.map(c => c.kind)).toEqual(['wiki', 'upload', undefined, undefined]);
    expect(SOURCE_KIND_LABEL.upload).toBe('yükleme');
  });
});
