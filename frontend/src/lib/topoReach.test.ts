// v0.10.1305 — Genel servis haritası: hover yol vurgusu (lib/topoReach).
// Çivilenen: hop sınırı, düğüm tavanı, geri kenar atlanır, yön (yukarı =
// çağıranlar, aşağı = çağrılanlar), kenar iki ucu yoldaysa, determinizm.
import { describe, it, expect } from 'vitest';
import { reachPath } from './topoReach';

const E = (...pairs: string[]) => pairs.map(p => {
  const [caller, callee] = p.split('>');
  return { caller, callee };
});
const NONE = new Set<string>();

describe('reachPath', () => {
  // u2 → u1 → h → d1 → d2 → d3 ; yan: s → d1 (h'nin kardeşi, yolda değil)
  const chain = E('u2>u1', 'u1>h', 'h>d1', 'd1>d2', 'd2>d3', 's>d1');

  it('±2 hop: yukarı çağıranlar, aşağı çağrılanlar; 3. hop yok', () => {
    const r = reachPath('h', chain, NONE);
    expect([...r.nodes].sort()).toEqual(['d1', 'd2', 'h', 'u1', 'u2']);
    expect(r.nodes.has('d3')).toBe(false);
    expect(r.nodes.has('s')).toBe(false); // aşağı yürürken d1'in çağıranına dönülmez
    expect([...r.edges].sort()).toEqual(['d1|d2', 'h|d1', 'u1|h', 'u2|u1']);
  });

  it('maxHops parametresi', () => {
    expect([...reachPath('h', chain, NONE, 1).nodes].sort()).toEqual(['d1', 'h', 'u1']);
    expect(reachPath('h', chain, NONE, 3).nodes.has('d3')).toBe(true);
  });

  it('geri kenarlar (döngü kırılmış) yürünmez', () => {
    const r = reachPath('h', E('a>h', 'h>b', 'b>a'), new Set(['b|a']));
    expect([...r.nodes].sort()).toEqual(['a', 'b', 'h']);
    expect(r.edges.has('b|a')).toBe(false);
  });

  it('tavan: toplam düğüm (hover dahil) ≤ cap; yakın hop önce', () => {
    const fan = E(...Array.from({ length: 100 }, (_, i) => `h>c${String(i).padStart(3, '0')}`),
      ...Array.from({ length: 5 }, (_, i) => `p${i}>h`));
    const r = reachPath('h', fan, NONE, 2, 60);
    expect(r.nodes.size).toBe(60);
    for (let i = 0; i < 5; i++) expect(r.nodes.has(`p${i}`)).toBe(true); // yukarı önce
    for (const k of r.edges) {
      const [a, b] = k.split('|');
      expect(r.nodes.has(a) && r.nodes.has(b), k).toBe(true);
    }
  });

  it('determinizm: kenar sırası çıktıyı değiştirmez', () => {
    const fan = E(...Array.from({ length: 80 }, (_, i) => `h>c${i}`));
    const a = [...reachPath('h', fan, NONE, 2, 20).nodes].sort();
    const b = [...reachPath('h', fan.slice().reverse(), NONE, 2, 20).nodes].sort();
    expect(b).toEqual(a);
  });

  it('bilinmeyen / izole hover: yalnız kendisi', () => {
    const r = reachPath('ghost', chain, NONE);
    expect([...r.nodes]).toEqual(['ghost']);
    expect(r.edges.size).toBe(0);
  });
});
