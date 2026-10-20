// transforms.ts — pure quantile helpers (v0.8.6 Phase 0).
//
// v0.10.1426 — the transform Web Worker pipeline (transform.worker.ts /
// workerClient.ts / useTransformWorker.ts), the `runTransform` dispatcher and
// the worker-only transforms (aggregateBuckets, buildFlameTree) were deleted:
// nothing in production ever called them. What is left is the one export with
// a live consumer (`percentiles` ← components/traces/ShapesView.tsx).

// ── Quantiles ────────────────────────────────────────────────────────────────

// quantileSorted returns the q-th (0..1) quantile of an ascending-sorted array
// via linear interpolation between order statistics. O(1) given a sorted input.
export function quantileSorted(sorted: number[], q: number): number {
  const n = sorted.length;
  if (n === 0) return NaN;
  if (n === 1) return sorted[0];
  const pos = (n - 1) * Math.min(1, Math.max(0, q));
  const lo = Math.floor(pos);
  const hi = Math.ceil(pos);
  if (lo === hi) return sorted[lo];
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (pos - lo);
}

// percentiles computes several quantiles in one sort pass. `qs` are 0..1.
export function percentiles(values: number[], qs: number[]): number[] {
  const sorted = values.filter(Number.isFinite).slice().sort((a, b) => a - b);
  return qs.map(q => quantileSorted(sorted, q));
}
