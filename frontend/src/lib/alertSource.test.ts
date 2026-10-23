// v0.10.1489 — kural önizlemesinin SAF yardımcıları: birim, ateşleme sayımı
// (boşluk sayılmaz), kaynak rozeti metni.
import { describe, it, expect } from 'vitest';
import { alertSourceLabel, previewUnit, wouldFireCount } from './alertSource';

describe('alertSource', () => {
  it('birim değerlendiricinin birimi', () => {
    expect(previewUnit('p99_ms')).toBe('ms');
    expect(previewUnit('avg_ms')).toBe('ms');
    expect(previewUnit('request_rate')).toBe('rps');
    expect(previewUnit('error_rate')).toBe('%');
    expect(previewUnit('error_count')).toBe('count');
  });
  it('boşluk ateşleme sayılmaz', () => {
    expect(wouldFireCount([1, null, 5, 7], '>', 4)).toBe(2);
    expect(wouldFireCount([1, null, 5], '<=', 1)).toBe(1);
    expect(wouldFireCount([1, 2], '?', 0)).toBe(0);
  });
  it('rozet metni kanıt satırıyla aynı anlam', () => {
    expect(alertSourceLabel({ vm: true, family: 'HTTP', evidence: '' })).toBe('kaynak: VictoriaMetrics (örneklenmemiş) · HTTP');
    expect(alertSourceLabel({ vm: false, reason: 'x', evidence: '' })).toBe('kaynak: span (örneklenmiş)');
  });
});
