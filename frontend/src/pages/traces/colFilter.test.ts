import { describe, it, expect } from 'vitest';
import { applyColFilter, colFilterKind, colFilterStatus, type ColFilterState } from './colFilter';
import { encodeFilters, decodeFilters } from '@/lib/urlState';

// v0.10.1511 — trace list column filters: the pure writer into the EXISTING
// /traces filter model (no new URL params).

const empty: ColFilterState = { service: '', op: '', hasError: false, minMs: '', maxMs: '', filters: [] };

describe('colFilterKind', () => {
  it('maps fixed columns and treats every other id as an attribute column', () => {
    expect(colFilterKind('service')).toBe('service');
    expect(colFilterKind('operation')).toBe('operation');
    expect(colFilterKind('status')).toBe('status');
    expect(colFilterKind('duration')).toBe('duration');
    expect(colFilterKind('time')).toBeNull();
    expect(colFilterKind('spans')).toBeNull();
    expect(colFilterKind('FUNCTION_CODE')).toBe('attr');
    expect(colFilterKind('openshift.cluster.name')).toBe('attr');
  });
});

describe('applyColFilter — attribute column', () => {
  it('include writes key = value and replaces whatever the column had', () => {
    let s = applyColFilter(empty, 'FUNCTION_CODE', { kind: 'include', value: 'A1' });
    expect(s.filters).toEqual([{ k: 'FUNCTION_CODE', op: '=', v: ['A1'] }]);
    s = applyColFilter(s, 'FUNCTION_CODE', { kind: 'include', value: 'B2' });
    expect(s.filters).toEqual([{ k: 'FUNCTION_CODE', op: '=', v: ['B2'] }]);
  });
  // Trace-level negation (follow-up to v0.10.1511): − writes NOT HAS ("the
  // trace has no span with this value"), never the span-level != / NOT IN.
  it('exclude writes key NOT HAS [value]; repeated excludes merge into the same chip', () => {
    let s = applyColFilter(empty, 'CHANNEL_CODE', { kind: 'exclude', value: 'MOBILE' });
    expect(s.filters).toEqual([{ k: 'CHANNEL_CODE', op: 'NOT HAS', v: ['MOBILE'] }]);
    s = applyColFilter(s, 'CHANNEL_CODE', { kind: 'exclude', value: 'WEB' });
    expect(s.filters).toEqual([{ k: 'CHANNEL_CODE', op: 'NOT HAS', v: ['MOBILE', 'WEB'] }]);
    s = applyColFilter(s, 'CHANNEL_CODE', { kind: 'exclude', value: 'WEB' });
    expect(s.filters).toEqual([{ k: 'CHANNEL_CODE', op: 'NOT HAS', v: ['MOBILE', 'WEB'] }]);
  });
  it('a legacy != / NOT IN chip (old shared link) reads as excluded and folds into NOT HAS', () => {
    const legacy = { ...empty, filters: [{ k: 'CHANNEL_CODE', op: 'NOT IN' as const, v: ['MOBILE', 'WEB'] }] };
    expect(colFilterStatus(legacy, 'CHANNEL_CODE')).toMatchObject({ active: true, excluded: ['MOBILE', 'WEB'] });
    const s = applyColFilter(legacy, 'CHANNEL_CODE', { kind: 'exclude', value: 'API' });
    expect(s.filters).toEqual([{ k: 'CHANNEL_CODE', op: 'NOT HAS', v: ['MOBILE', 'WEB', 'API'] }]);
    // A hand-typed `!=` on ANOTHER key is never rewritten.
    const other = { ...empty, filters: [{ k: 'FUNCTION_CODE', op: '!=' as const, v: ['A'] }] };
    expect(applyColFilter(other, 'CHANNEL_CODE', { kind: 'exclude', value: 'WEB' }).filters)
      .toEqual([{ k: 'FUNCTION_CODE', op: '!=', v: ['A'] }, { k: 'CHANNEL_CODE', op: 'NOT HAS', v: ['WEB'] }]);
  });
  it('multi writes key IN (…); one value degrades to =; none clears', () => {
    expect(applyColFilter(empty, 'FUNCTION_CODE', { kind: 'multi', values: ['A', 'B', 'A'] }).filters)
      .toEqual([{ k: 'FUNCTION_CODE', op: 'IN', v: ['A', 'B'] }]);
    expect(applyColFilter(empty, 'FUNCTION_CODE', { kind: 'multi', values: ['A'] }).filters)
      .toEqual([{ k: 'FUNCTION_CODE', op: '=', v: ['A'] }]);
    const s = applyColFilter(empty, 'FUNCTION_CODE', { kind: 'include', value: 'A' });
    expect(applyColFilter(s, 'FUNCTION_CODE', { kind: 'multi', values: [] }).filters).toEqual([]);
  });
  it('clear removes only this column’s chips; other columns survive in order', () => {
    const other = { k: 'CHANNEL_CODE', op: '=' as const, v: ['WEB'] };
    let s: ColFilterState = { ...empty, filters: [other] };
    s = applyColFilter(s, 'FUNCTION_CODE', { kind: 'include', value: 'A' });
    s = applyColFilter(s, 'FUNCTION_CODE', { kind: 'clear' });
    expect(s.filters).toEqual([other]);
  });
  it('never touches service / op / duration / errors', () => {
    const s0: ColFilterState = { service: 'orders-api', op: 'GET /x', hasError: true, minMs: '5', maxMs: '9', filters: [] };
    const s = applyColFilter(s0, 'FUNCTION_CODE', { kind: 'include', value: 'A' });
    expect({ ...s, filters: [] }).toEqual(s0);
  });
});

describe('applyColFilter — service / operation / status / duration', () => {
  it('service include uses the picker slot; exclude and multi use service.name chips', () => {
    let s = applyColFilter(empty, 'service', { kind: 'include', value: 'orders-api' });
    expect(s.service).toBe('orders-api');
    expect(s.filters).toEqual([]);
    s = applyColFilter(s, 'service', { kind: 'exclude', value: 'orders-api' });
    expect(s.service).toBe('');
    expect(s.filters).toEqual([{ k: 'service.name', op: 'NOT HAS', v: ['orders-api'] }]);
    s = applyColFilter(s, 'service', { kind: 'multi', values: ['a-api', 'b-api'] });
    expect(s.service).toBe('');
    expect(s.filters).toEqual([{ k: 'service.name', op: 'IN', v: ['a-api', 'b-api'] }]);
    s = applyColFilter(s, 'service', { kind: 'clear' });
    expect(s).toEqual(empty);
  });
  it('operation include uses the op slot (exact-match chip); clear empties it', () => {
    let s = applyColFilter(empty, 'operation', { kind: 'include', value: 'POST /pay' });
    expect(s.op).toBe('POST /pay');
    s = applyColFilter(s, 'operation', { kind: 'multi', values: ['A', 'B'] });
    expect(s.op).toBe('');
    expect(s.filters).toEqual([{ k: 'operation', op: 'IN', v: ['A', 'B'] }]);
    expect(applyColFilter(s, 'operation', { kind: 'clear' })).toEqual(empty);
  });
  it('status toggles hasError; duration writes minMs/maxMs', () => {
    expect(applyColFilter(empty, 'status', { kind: 'errors', on: true }).hasError).toBe(true);
    expect(applyColFilter({ ...empty, hasError: true }, 'status', { kind: 'clear' }).hasError).toBe(false);
    const d = applyColFilter(empty, 'duration', { kind: 'duration', min: ' 100 ', max: '' });
    expect([d.minMs, d.maxMs]).toEqual(['100', '']);
    expect(applyColFilter(d, 'duration', { kind: 'clear' })).toEqual(empty);
  });
  it('non-filterable columns are a no-op', () => {
    expect(applyColFilter(empty, 'time', { kind: 'include', value: 'x' })).toBe(empty);
  });
});

describe('round-trip through the URL codec', () => {
  it('every action survives encodeFilters → decodeFilters unchanged', () => {
    let s = applyColFilter(empty, 'FUNCTION_CODE', { kind: 'multi', values: ['A1', 'B 2', 'ç/ü'] });
    s = applyColFilter(s, 'CHANNEL_CODE', { kind: 'exclude', value: 'MOBILE' });
    s = applyColFilter(s, 'http.status_code', { kind: 'include', value: '500' });
    s = applyColFilter(s, 'service', { kind: 'exclude', value: 'orders-api' });
    const wire = new URLSearchParams({ filters: encodeFilters(s.filters) }).toString();
    const back = decodeFilters(new URLSearchParams(wire).get('filters'));
    expect(back).toEqual(s.filters);
    // and the status read back from the decoded URL is the same
    for (const col of ['FUNCTION_CODE', 'CHANNEL_CODE', 'http.status_code', 'service']) {
      expect(colFilterStatus({ ...s, filters: back }, col)).toEqual(colFilterStatus(s, col));
    }
  });
  it('clearing the last column filter yields an empty filters param', () => {
    const s = applyColFilter(applyColFilter(empty, 'FUNCTION_CODE', { kind: 'include', value: 'A' }), 'FUNCTION_CODE', { kind: 'clear' });
    expect(encodeFilters(s.filters)).toBe('');
  });
});

describe('colFilterStatus', () => {
  it('reports active / selected / excluded for the header indicator', () => {
    expect(colFilterStatus(empty, 'FUNCTION_CODE').active).toBe(false);
    const s = applyColFilter(empty, 'FUNCTION_CODE', { kind: 'multi', values: ['A', 'B'] });
    expect(colFilterStatus(s, 'FUNCTION_CODE')).toMatchObject({ active: true, selected: ['A', 'B'], summary: '= A, B' });
    const x = applyColFilter(empty, 'FUNCTION_CODE', { kind: 'exclude', value: 'A' });
    expect(colFilterStatus(x, 'FUNCTION_CODE')).toMatchObject({ active: true, excluded: ['A'], summary: '≠ A' });
    expect(colFilterStatus({ ...empty, service: 'orders-api' }, 'service')).toMatchObject({ active: true, selected: ['orders-api'] });
    expect(colFilterStatus({ ...empty, op: 'GET /x' }, 'operation').active).toBe(true);
    expect(colFilterStatus({ ...empty, hasError: true }, 'status').active).toBe(true);
    expect(colFilterStatus({ ...empty, minMs: '100' }, 'duration').summary).toBe('≥ 100 ms');
  });
  it('a hand-typed LIKE chip on the column still marks it filtered', () => {
    const s: ColFilterState = { ...empty, filters: [{ k: 'FUNCTION_CODE', op: 'LIKE', v: ['A'] }] };
    expect(colFilterStatus(s, 'FUNCTION_CODE')).toMatchObject({ active: true, summary: 'custom' });
    expect(applyColFilter(s, 'FUNCTION_CODE', { kind: 'clear' }).filters).toEqual([]);
  });
});
