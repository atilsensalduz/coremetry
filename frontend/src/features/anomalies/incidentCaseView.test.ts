import { describe, it, expect } from 'vitest';
import { caseHref, caseSignalLines, categoryLabel, correctnessLabel, defaultCaseCategory, independenceNote, sameCause } from './incidentCaseView';
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

describe('incidentCaseView — v0.10.1475 ortak onay formu', () => {
  it('sameCause Go aynası (boşluk/harf)', () => {
    expect(sameCause('DB  havuzu Doldu', 'db havuzu doldu')).toBe(true);
    expect(sameCause('', '')).toBe(false);
    expect(sameCause('a', 'b')).toBe(false);
  });
  it('bağımsızlık notu: aynen kabul manşete sayılmaz, düzeltme sayılır', () => {
    expect(independenceNote('deploy 2.3', 'Deploy  2.3').independent).toBe(false);
    expect(independenceNote('deploy 2.3', 'orders-api havuz sızıntısı').independent).toBe(true);
    expect(independenceNote('', 'x').independent).toBe(true);
  });
  it('varsayılan kategori ve kaynak bağlantısı', () => {
    expect(defaultCaseCategory({ deployInWindow: true })).toBe('deploy');
    expect(defaultCaseCategory({ deployInWindow: false })).toBe('other');
    expect(caseHref({ source: 'problem', sourceId: 'p 1' })).toBe('/problems?problem=p%201');
    expect(caseHref({ source: 'exception', sourceId: 'ff01' })).toBe('/problems?exception=ff01');
  });
});
