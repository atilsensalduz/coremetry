// Span alignment between two traces — pairs spans whose
// structural path matches so the trace-compare page can show
// per-operation deltas (e.g. "the database query is 12 ms
// slower in B").
//
// Path = sequence of (service.name, displayName) tuples from
// the root down to the span. Two spans match iff their paths
// are equal. Deeper paths are checked first so a refactor
// that renamed a sibling doesn't accidentally match the wrong
// span.
//
// Tight-loop disambiguation: when two siblings share the same
// (service, name) they get an index suffix (#0, #1, …) in the
// order their start-times appear. This matches Datadog's
// "merge-by-position" semantics for tight loops.
//
// Performance: O(N) build of (path → span) Map per side, O(N)
// alignment walk. At 10k spans on each side that's ~20k Map
// inserts + 20k lookups; sub-millisecond in JS. No Web Worker
// needed at the trace sizes Coremetry sees in practice
// (per-trace span count is bounded by service depth, ≤1k).

export interface AlignSpan {
  spanId: string;
  parentId: string;
  service: string;
  name: string;
  startTime: number;       // ns
  duration: number;        // ns
  statusCode?: string;     // "ok" | "error" | "unset"
}

export interface AlignedPair {
  // Stable path key, useful for the UI to render a stable list
  // even after re-sort.
  pathKey: string;
  // Display path (joined with " / ") — for the table label.
  pathLabel: string;
  // Either side may be null when the span exists only on the
  // other (added or removed in B vs A).
  a: AlignSpan | null;
  b: AlignSpan | null;
  // Δ = b.duration - a.duration; null when one side is null.
  deltaNs: number | null;
  // Relative change (deltaNs / a.duration). null if a missing
  // or duration zero. Used for the "+25%" style label.
  pctChange: number | null;
}

export interface AlignResult {
  // Sorted by abs(deltaNs) desc (largest absolute change
  // first). Pairs with one side null are sorted to the bottom
  // (they're informational rather than regression candidates).
  pairs: AlignedPair[];
  // Counts for the header summary.
  matched: number;
  onlyInA: number;
  onlyInB: number;
}

interface IndexedSpan extends AlignSpan {
  pathKey: string;
  pathLabel: string;
}

export function alignTraces(aSpans: AlignSpan[], bSpans: AlignSpan[]): AlignResult {
  const aIdx = buildPathIndex(aSpans);
  const bIdx = buildPathIndex(bSpans);

  const pairs: AlignedPair[] = [];
  // Walk A's keys first, pair with B if present.
  for (const [key, aSpan] of aIdx) {
    const bSpan = bIdx.get(key) ?? null;
    pairs.push(toPair(key, aSpan, bSpan));
  }
  // Then add any B-only spans (haven't been paired yet).
  for (const [key, bSpan] of bIdx) {
    if (!aIdx.has(key)) {
      pairs.push(toPair(key, null, bSpan));
    }
  }

  const matched = pairs.filter(p => p.a && p.b).length;
  const onlyInA = pairs.filter(p => p.a && !p.b).length;
  const onlyInB = pairs.filter(p => !p.a && p.b).length;

  pairs.sort((p, q) => {
    // Pairs with both sides come first.
    const pBoth = !!(p.a && p.b);
    const qBoth = !!(q.a && q.b);
    if (pBoth !== qBoth) return pBoth ? -1 : 1;
    // Then by abs(delta) desc.
    const pd = p.deltaNs == null ? 0 : Math.abs(p.deltaNs);
    const qd = q.deltaNs == null ? 0 : Math.abs(q.deltaNs);
    return qd - pd;
  });

  return { pairs, matched, onlyInA, onlyInB };
}

function toPair(key: string, a: IndexedSpan | null, b: IndexedSpan | null): AlignedPair {
  const pathLabel = (a ?? b)?.pathLabel ?? key;
  const deltaNs = (a && b) ? b.duration - a.duration : null;
  let pctChange: number | null = null;
  if (a && b && a.duration > 0) pctChange = (b.duration - a.duration) / a.duration;
  return {
    pathKey: key,
    pathLabel,
    a: a ? {
      spanId: a.spanId, parentId: a.parentId, service: a.service,
      name: a.name, startTime: a.startTime, duration: a.duration,
      statusCode: a.statusCode,
    } : null,
    b: b ? {
      spanId: b.spanId, parentId: b.parentId, service: b.service,
      name: b.name, startTime: b.startTime, duration: b.duration,
      statusCode: b.statusCode,
    } : null,
    deltaNs,
    pctChange,
  };
}

function buildPathIndex(spans: AlignSpan[]): Map<string, IndexedSpan> {
  // Sort by start-time so siblings get stable #N indices.
  const sorted = [...spans].sort((a, b) => a.startTime - b.startTime);
  // childrenOf[spanId] = ordered list of children
  const byId = new Map<string, AlignSpan>();
  const childrenOf = new Map<string, AlignSpan[]>();
  for (const s of sorted) {
    byId.set(s.spanId, s);
    if (s.parentId) {
      const list = childrenOf.get(s.parentId);
      if (list) list.push(s);
      else childrenOf.set(s.parentId, [s]);
    }
  }

  // Memoised path → string. Each span's path is its parent's
  // path + its own (service, name, sibling-index).
  const pathOf = new Map<string, { key: string; label: string }>();

  function compute(s: AlignSpan): { key: string; label: string } {
    const cached = pathOf.get(s.spanId);
    if (cached) return cached;

    let parentKey = '';
    let parentLabel = '';
    const parent = s.parentId ? byId.get(s.parentId) : null;
    if (parent) {
      const p = compute(parent);
      parentKey = p.key;
      parentLabel = p.label;
    }

    // Sibling index: among children of `parent` (or among
    // roots if no parent) sharing the same (service, name),
    // which start-time-rank does THIS span have?
    const siblings = parent
      ? (childrenOf.get(parent.spanId) ?? [])
      : sorted.filter(x => !x.parentId || !byId.has(x.parentId));
    const sameKindBefore = siblings.filter(
      x => x.spanId !== s.spanId
        && x.service === s.service
        && x.name === s.name
        && x.startTime <= s.startTime,
    );
    const idx = sameKindBefore.length;

    const segment = `${s.service}::${s.name}#${idx}`;
    const labelSeg = `${s.service} / ${s.name}${idx > 0 ? ` #${idx}` : ''}`;
    const out = {
      key: parentKey ? parentKey + '|' + segment : segment,
      label: parentLabel ? parentLabel + ' › ' + labelSeg : labelSeg,
    };
    pathOf.set(s.spanId, out);
    return out;
  }

  const out = new Map<string, IndexedSpan>();
  for (const s of sorted) {
    const p = compute(s);
    out.set(p.key, { ...s, pathKey: p.key, pathLabel: p.label });
  }
  return out;
}
