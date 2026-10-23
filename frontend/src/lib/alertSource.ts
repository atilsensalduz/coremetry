import type { MetricUnit } from '@/lib/metricQuery';
import type { AlertSourceInfo } from '@/lib/types';

// alertSource.ts — v0.10.1489: kural önizlemesi / taban önerisinin SAF
// yardımcıları. Kaynak rozeti metni değerlendiricinin kanıt satırıyla aynı
// anlamı taşır (Problem açıklaması: "kaynak: VictoriaMetrics (örneklenmemiş)"
// ya da "kaynak: span (örneklenmiş) — VM kullanılmadı: <neden>").

// previewUnit — kural metriğinin grafik birimi (değerlendiricinin birimiyle aynı).
export function previewUnit(metric: string): MetricUnit {
  if (metric.endsWith('_ms')) return 'ms';
  if (metric === 'request_rate') return 'rps';
  if (metric.endsWith('_rate')) return '%';
  return 'count';
}

// wouldFireCount — eşiği aşan nokta sayısı (boşluk sayılmaz).
export function wouldFireCount(values: (number | null)[], comparator: string, threshold: number): number {
  return values.filter(v => v != null && (
    comparator === '>' ? v > threshold
    : comparator === '<' ? v < threshold
    : comparator === '>=' ? v >= threshold
    : comparator === '<=' ? v <= threshold
    : false)).length;
}

// alertSourceLabel — rozetin kısa metni.
export function alertSourceLabel(s: AlertSourceInfo): string {
  if (s.vm) return `kaynak: VictoriaMetrics (örneklenmemiş)${s.family ? ` · ${s.family}` : ''}`;
  return 'kaynak: span (örneklenmiş)';
}
