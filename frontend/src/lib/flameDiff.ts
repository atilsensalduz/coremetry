import type { FlameNode } from './types';

// Per-frame diff between two flame graphs (current vs
// baseline). For each frame in the union of paths:
//   • baselineValue = value in baseline tree at that path
//     (0 if absent — it's a new frame in current)
//   • currentValue  = value in current  tree at that path
//     (0 if absent — frame disappeared, e.g. an inlined or
//     removed call)
//   • delta         = current - baseline
//   • pct           = delta / max(baseline, 1) — avoids /0
//     when the frame is brand-new
//
// Width semantics: each diff node's `value` is `max(current,
// baseline)` so the rendered shape is a faithful union and
// "frame got smaller" is visually obvious (the frame still
// occupies its old footprint, painted green).
//
// Identity: same `name` (and matching path) = same frame. We
// do NOT match on file/line because a small refactor that
// moves a function from line 42 → line 47 shouldn't be
// reported as "removed in current, added in current with
// new line". The downside is that two unrelated functions
// with the same name on the same call path collide; in
// practice profilers already use fully-qualified names so
// this is rarely an issue.

export interface DiffNode {
  name: string;
  file?: string;
  line?: number;
  // Render width — max of current and baseline so the diff
  // shape is the union of the two flames.
  value: number;
  current: number;   // value in the "current" profile (or 0)
  baseline: number;  // value in the "baseline" profile (or 0)
  delta: number;     // current - baseline (signed, in samples)
  pct: number;       // delta / max(baseline, 1)
  children?: DiffNode[];
}

export function diffFlame(current: FlameNode, baseline: FlameNode): DiffNode {
  return diffPair(current, baseline);
}

function diffPair(cur: FlameNode | undefined, base: FlameNode | undefined): DiffNode {
  // At least one side must be present — the caller (top-level
  // or recursive merge) only invokes diffPair on a paired
  // entry, so this is purely a typing convenience.
  const node: FlameNode = cur ?? base!;
  const c = cur?.value ?? 0;
  const b = base?.value ?? 0;
  const delta = c - b;
  const pct = b > 0 ? delta / b : (c > 0 ? 1 : 0);

  // Merge children by name. We index baseline-side first
  // because lookup is O(1), then walk current's children and
  // emit the pair. Any baseline-only children get their own
  // emit at the end.
  const baseKids = new Map<string, FlameNode>();
  for (const k of base?.children ?? []) baseKids.set(k.name, k);

  const out: DiffNode[] = [];
  const seen = new Set<string>();
  for (const k of cur?.children ?? []) {
    const m = baseKids.get(k.name);
    if (m) seen.add(k.name);
    out.push(diffPair(k, m));
  }
  for (const [name, k] of baseKids) {
    if (seen.has(name)) continue;
    out.push(diffPair(undefined, k));
  }

  return {
    name: node.name,
    file: cur?.file ?? base?.file,
    line: cur?.line ?? base?.line,
    value: Math.max(c, b),
    current: c,
    baseline: b,
    delta,
    pct,
    children: out.length > 0 ? out : undefined,
  };
}

// diffColor — pick a fill colour for a diff node by its pct
// change. Three bands per direction so a small fluctuation
// reads as neutral while a real shift reads as red/green.
//
//   ≥ +50%   #d73a3a   strong red
//   ≥ +15%   #f0703f   orange-red
//   ≥ +5%    #f5b343   amber
//   ±5%      neutral grey
//   ≤ -5%    #6dbf5b   light green
//   ≤ -15%   #3c9b53   green
//   ≤ -50%   #207a3a   strong green
export function diffColor(pct: number): string {
  if (pct >= 0.50) return '#d73a3a';
  if (pct >= 0.15) return '#f0703f';
  if (pct >= 0.05) return '#f5b343';
  if (pct <= -0.50) return '#207a3a';
  if (pct <= -0.15) return '#3c9b53';
  if (pct <= -0.05) return '#6dbf5b';
  return '#7d8590';
}
