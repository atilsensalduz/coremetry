// opCardCode — v0.10.1506 (operasyon kartı R4): "Kod" bölümünün saf yarısı.
import { describe, it, expect } from 'vitest';
import type { OperationCardCode, OpCallEdge } from '@/lib/types';
import { callSiteCols, codeNote, dtoRows, fileTail, footerContext, matchedEdge, matchedLabel } from './opCardCode';

const tr = (k: string) => k;
const base: OperationCardCode = {
  outcome: 'ok', entry: { path: '/a/b/C.java', fromLine: 3, toLine: 9, signature: 's()', found: 'route', url: 'https://x' },
  repo: 'orders-api', branch: 'release', request: [], response: [], callSites: [], stats: { fetched: 1, searches: 1 },
};
const edge = { depth: 1, parentKey: 'entry', key: 'k', kind: 'db', protocol: 'db', target: 'db:oracle', op: 'SELECT orders' } as OpCallEdge;

describe('opCardCode', () => {
  it('kolonlar: her kolonda minWidth + priority', () => {
    for (const c of callSiteCols(tr)) {
      expect(c.minWidth).toBeGreaterThan(0);
      expect(c.priority).toBeGreaterThan(0);
    }
  });
  it('fileTail', () => {
    expect(fileTail('/src/main/java/x/OrderServiceImpl.java')).toBe('x/OrderServiceImpl.java');
    expect(fileTail('A.java')).toBe('A.java');
  });
  it('matchedEdge / matchedLabel: -1 ve sınır dışı null', () => {
    const s = { path: '/a', line: 1, kind: 'db', callee: 'r.x', matchedChild: 0 };
    expect(matchedEdge(s, [edge])).toBe(edge);
    expect(matchedEdge({ ...s, matchedChild: -1 }, [edge])).toBeNull();
    expect(matchedEdge({ ...s, matchedChild: 3 }, [edge])).toBeNull();
    expect(matchedEdge(s, null)).toBeNull();
    expect(matchedLabel(edge)).toBe('oracle · SELECT orders');
  });
  it('dtoRows: Owner.alan → tip, mono', () => {
    expect(dtoRows([{ owner: 'A', name: 'x', type: 'List<B>', nested: true }])).toEqual([{ id: 'A.x', k: 'A.x', v: 'List<B>', mono: true }]);
  });
  it('codeNote: search-off / giriş yok / kısmi / ok', () => {
    expect(codeNote({ ...base, outcome: 'search-off', entry: null, reason: 'R' }, tr)).toBe('R');
    expect(codeNote({ ...base, outcome: 'search-off', entry: null }, tr)).toBe('endpoint.opcard.code.searchOff');
    expect(codeNote({ ...base, outcome: 'tree-miss', entry: null }, tr)).toBe('endpoint.opcard.code.noEntry');
    expect(codeNote({ ...base, outcome: 'partial', reason: 'eksik' }, tr)).toBe('eksik');
    expect(codeNote({ ...base, reason: 'not' }, tr)).toBe('');
  });
  it('footerContext: giriş dosyası tek satır; giriş yoksa null', () => {
    const f = footerContext({ ...base, version: '1.4.2', commit: 'c0ffee' });
    expect(f?.files).toEqual([{ path: '/a/b/C.java', fromLine: 3, toLine: 9, url: 'https://x', kind: 'operation' }]);
    expect(f?.version).toBe('1.4.2');
    expect(footerContext({ ...base, entry: null })).toBeNull();
  });
});
