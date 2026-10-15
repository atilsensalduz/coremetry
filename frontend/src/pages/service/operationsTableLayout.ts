// operationsTableLayout.ts — v0.10.1179 (operatör, prod: Operations
// sekmesinde ad kolonundan sonra büyük boşluk; trend tıklaması kapanmıyor).
import type { OperationRow } from '@/lib/types';
import { opRowKey } from './operationRoutes';

/** Trend grafiği genişliği: kolonun iç genişliği (dolgu payı düşülür), 160–640 px. */
export function trendSparkWidth(colPx: number): number {
  return Math.max(160, Math.min(640, Math.floor(colPx - 16)));
}

/** Trend tıklaması: aynı satır açıksa kapatır, değilse o satırı açar. */
export function nextOpDetail(cur: OperationRow | null, op: OperationRow): OperationRow | null {
  return cur && opRowKey(cur) === opRowKey(op) ? null : op;
}
