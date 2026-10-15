import { describe, it, expect } from 'vitest';
import { caseSignalLines, categoryLabel, correctnessLabel } from './incidentCaseView';
import type { IncidentCase } from '@/lib/types';

const base = {
  errorCodes: ['MCA-00029'], functionCodes: null, channelCodes: [],
  errRateBefore: 0.003, errRateDuring: 0.041, p99BeforeMs: 0, p99DuringMs: 0,
  deployInWindow: true, deployVersion: '2.3',
} as unknown as IncidentCase;

describe('incidentCaseView (CoSRE öğrenme Faz 3)', () => {
  it('ölçülen sinyaller düz cümle, ölçülmeyen yazılmaz', () => {
    expect(caseSignalLines(base)).toEqual([
      'Hata kodları: MCA-00029',
      'Hata oranı %0.3 → %4.1',
      'Pencerede deploy: 2.3',
    ]);
  });
  it('etiketler', () => {
    expect(categoryLabel('code_bug')).toBe('Kod hatası');
    expect(categoryLabel('x')).toBe('x');
    expect(correctnessLabel(1)).toBe('doğruydu');
    expect(correctnessLabel(-1)).toBe('değerlendirilemedi');
  });
});
