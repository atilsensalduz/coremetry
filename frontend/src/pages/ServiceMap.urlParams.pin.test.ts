import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1234 — Topology kontrolleri URL'den türetilir (yerel kopya yok).
const src = readFileSync(resolve(__dirname, 'ServiceMap.tsx'), 'utf8');

describe('Topology URL state', () => {
  it('TOPO_URL_SPEC kullanılır', () => {
    expect(src).toContain('useUrlParams(TOPO_URL_SPEC)');
  });
  it.each(['samples', 'diff', 'topN', 'clusterPick'])('%s için useState yok', k => {
    expect(src).not.toMatch(new RegExp(`const \\[${k}, set\\w+\\] = useState`));
  });
});
