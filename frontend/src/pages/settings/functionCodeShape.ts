// functionCodeShape.ts — v0.10.1226, SAF: biçim ölçümünün özeti.
import type { FunctionCodeShapeRow } from '@/lib/types';

export interface ShapeSummary {
  okDistinct: number; okSpans: number; offDistinct: number; offSpans: number;
  /** Biçim dışı uzunluk kovaları, farklı değer sayısına göre azalan (en çok 10). */
  topOff: FunctionCodeShapeRow[];
}

export function summarizeShape(rows: FunctionCodeShapeRow[]): ShapeSummary {
  const s: ShapeSummary = { okDistinct: 0, okSpans: 0, offDistinct: 0, offSpans: 0, topOff: [] };
  for (const r of rows) {
    if (r.conforms) { s.okDistinct += r.distinct; s.okSpans += r.spans; }
    else { s.offDistinct += r.distinct; s.offSpans += r.spans; }
  }
  s.topOff = rows.filter(r => !r.conforms).sort((a, b) => b.distinct - a.distinct).slice(0, 10);
  return s;
}
