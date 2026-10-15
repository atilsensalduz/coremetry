import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';

// Clusters → Nodes: node başına pod sayısı (operatör isteği, 2026-10-15).
// Ölçülemeyen node '—' (0 gerçek değer, bilinmeyen sıralamada en altta).
describe('Nodes tablosu Pods kolonu', () => {
  const src = readFileSync(new URL('../Clusters.tsx', import.meta.url), 'utf8');
  it('kolon tanımı + hücre', () => {
    expect(src).toContain("{ id: 'pods',     label: 'Pods',    sortValue: r => r.pods ?? -1");
    expect(src).toContain(`col="pods" row={r} value={r.pods == null ? '—' : fmtNum(r.pods)}`);
  });
  it('hücre sırası kolon sırasıyla aynı (role → pods → cpuCores)', () => {
    const block = src.slice(src.indexOf('ndt.sortedRows.map'));
    expect(block.indexOf('col="pods"')).toBeLessThan(block.indexOf('col="cpuCores"'));
  });
});
