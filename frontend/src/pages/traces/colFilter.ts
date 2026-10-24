import type { FilterExpr } from '@/lib/types';

// colFilter — v0.10.1511 (trace list column filters; prod user request, operator
// approval 2026-10-24: "Excel'deki gibi filtrelenebilsin, aynı fonksiyon
// kodu içeren trace'leri filtrelemek iyi olabilir").
//
// SAF: a header popover action → the NEXT state of the EXISTING /traces
// filter model. No new state, no new URL params: the page's own URL effect
// serialises the result (service / op / hasError / minMs / maxMs / filters),
// so chips, share links and saved views behave exactly as if the operator
// had typed the same filter into the bar.
//
// Mapping (one representation per column — the one the page already owns):
//   service    include → `service` (the picker's slot); exclude / multi →
//              a `service.name` chip (!= / IN). The picker holds one value.
//   operation  include → `op` (the operation picker's exact-match slot,
//              v0.10.752); exclude / multi → an `operation` chip.
//   status     errors on/off → `hasError`.
//   duration   min/max → `minMs` / `maxMs`.
//   attribute  `<key> = v` / `<key> NOT HAS [v…]` / `<key> IN (…)`. Every
//              write first drops the chips this column owns, so a column
//              carries at most one predicate.
//
// Include / multi: a trace matches when one of its spans matches the chip.
// Exclude (every column kind, service and operation included) writes the
// TRACE-level `NOT HAS` — "the trace has NO span with this value" — and
// repeated excludes merge into the same chip. It used to write `!=` / NOT IN,
// whose meaning is span-level ("a span whose value differs"), so a multi-span
// trace carrying the value in a child span stayed in the list. `!=` itself is
// unchanged (saved views / shared links keep their answer); a legacy `!=` /
// NOT IN chip on the column still reads as excluded and is folded into the
// NOT HAS chip on the next exclude. docs/DECISIONS.md (trace-level negation).

export type ColFilterKind = 'service' | 'operation' | 'status' | 'duration' | 'attr';

/** Which popover a list column gets; null = not filterable (time, spans). */
export function colFilterKind(colId: string): ColFilterKind | null {
  switch (colId) {
    case 'service': return 'service';
    case 'operation': return 'operation';
    case 'status': return 'status';
    case 'duration': return 'duration';
    case 'time':
    case 'spans': return null;
    default: return colId.trim() ? 'attr' : null;
  }
}

/** The slice of the page's filter state a column filter may touch. */
export interface ColFilterState {
  service: string;
  op: string;
  hasError: boolean;
  minMs: string;
  maxMs: string;
  filters: FilterExpr[];
}

export type ColFilterAction =
  | { kind: 'include'; value: string }
  | { kind: 'exclude'; value: string }
  | { kind: 'multi'; values: string[] }
  | { kind: 'clear' }
  | { kind: 'errors'; on: boolean }
  | { kind: 'duration'; min: string; max: string };

/** Chip key a column owns ('' for status / duration, which have no chip). */
export function colChipKey(colId: string): string {
  const k = colFilterKind(colId);
  if (k === 'service') return 'service.name';
  if (k === 'operation') return 'operation';
  if (k === 'attr') return colId.trim();
  return '';
}

// Chip keys that mean "the same column" (service.name vs service_name, the
// operation alias `name`). Dropping all of them keeps one predicate/column.
function ownedKeys(colId: string): Set<string> {
  const k = colFilterKind(colId);
  if (k === 'service') return new Set(['service.name', 'service_name', 'resource.service.name']);
  if (k === 'operation') return new Set(['operation', 'name']);
  const c = colChipKey(colId);
  return new Set(c ? [c] : []);
}

function dropOwned(filters: FilterExpr[], colId: string): FilterExpr[] {
  const keys = ownedKeys(colId);
  return filters.filter(f => !keys.has(f.k.trim()));
}

function uniq(values: string[]): string[] {
  const out: string[] = [];
  for (const v of values) if (v !== '' && !out.includes(v)) out.push(v);
  return out;
}

/** Values currently EXCLUDED on this column (from != / NOT IN chips). */
function excludedValues(filters: FilterExpr[], colId: string): string[] {
  const keys = ownedKeys(colId);
  return uniq(filters
    .filter(f => keys.has(f.k.trim()) && (f.op === 'NOT HAS' || f.op === '!=' || f.op === 'NOT IN'))
    .flatMap(f => f.v));
}

export function applyColFilter(s: ColFilterState, colId: string, a: ColFilterAction): ColFilterState {
  const kind = colFilterKind(colId);
  if (!kind) return s;
  if (kind === 'status') {
    if (a.kind === 'errors') return { ...s, hasError: a.on };
    if (a.kind === 'clear') return { ...s, hasError: false };
    return s;
  }
  if (kind === 'duration') {
    if (a.kind === 'duration') return { ...s, minMs: a.min.trim(), maxMs: a.max.trim() };
    if (a.kind === 'clear') return { ...s, minMs: '', maxMs: '' };
    return s;
  }
  const key = colChipKey(colId);
  const rest = dropOwned(s.filters, colId);
  // The picker slot a column owns (service / op); attribute columns have none.
  const slot: 'service' | 'op' | null = kind === 'service' ? 'service' : kind === 'operation' ? 'op' : null;
  const base: ColFilterState = slot ? { ...s, [slot]: '', filters: rest } : { ...s, filters: rest };
  switch (a.kind) {
    case 'include': {
      const v = a.value;
      if (!v) return s;
      if (slot) return { ...base, [slot]: v };
      return { ...base, filters: [...rest, { k: key, op: '=', v: [v] }] };
    }
    case 'exclude': {
      const v = a.value;
      if (!v) return s;
      const vals = uniq([...excludedValues(s.filters, colId), v]);
      const chip: FilterExpr = { k: key, op: 'NOT HAS', v: vals };
      return { ...base, filters: [...rest, chip] };
    }
    case 'multi': {
      const vals = uniq(a.values);
      if (vals.length === 0) return base;
      if (vals.length === 1) return applyColFilter(s, colId, { kind: 'include', value: vals[0] });
      return { ...base, filters: [...rest, { k: key, op: 'IN', v: vals }] };
    }
    case 'clear':
      return base;
    default:
      return s;
  }
}

/** What the header shows: is the column filtered, how, and which values are
 *  positively selected (pre-checks the popover's checkboxes). */
export interface ColFilterStatus {
  active: boolean;
  /** Short human summary for the indicator tooltip, e.g. "= A", "≠ A, B", "IN 3". */
  summary: string;
  selected: string[];
  excluded: string[];
}

export function colFilterStatus(s: ColFilterState, colId: string): ColFilterStatus {
  const none: ColFilterStatus = { active: false, summary: '', selected: [], excluded: [] };
  const kind = colFilterKind(colId);
  if (!kind) return none;
  if (kind === 'status') return s.hasError ? { ...none, active: true, summary: 'errors' } : none;
  if (kind === 'duration') {
    if (!s.minMs && !s.maxMs) return none;
    const summary = [s.minMs ? `≥ ${s.minMs} ms` : '', s.maxMs ? `≤ ${s.maxMs} ms` : ''].filter(Boolean).join(' ');
    return { ...none, active: true, summary };
  }
  const keys = ownedKeys(colId);
  const mine = s.filters.filter(f => keys.has(f.k.trim()));
  const slotVal = kind === 'service' ? s.service : kind === 'operation' ? s.op : '';
  const selected = uniq([
    ...(slotVal ? [slotVal] : []),
    ...mine.filter(f => f.op === '=' || f.op === 'IN').flatMap(f => f.v),
  ]);
  const excluded = excludedValues(s.filters, colId);
  // Any other operator the operator typed by hand (LIKE, >, EXISTS…) still
  // marks the column as filtered — the clear option removes it too.
  const other = mine.some(f => !['=', 'IN', '!=', 'NOT IN', 'NOT HAS'].includes(f.op));
  if (!selected.length && !excluded.length && !other) return none;
  const parts: string[] = [];
  if (selected.length) parts.push(selected.length > 2 ? `IN ${selected.length}` : `= ${selected.join(', ')}`);
  if (excluded.length) parts.push(excluded.length > 2 ? `NOT IN ${excluded.length}` : `≠ ${excluded.join(', ')}`);
  if (other && !parts.length) parts.push('custom');
  return { active: true, summary: parts.join(' · '), selected, excluded };
}
