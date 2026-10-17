// v0.10.1269 — operatör 2026-10-17: çok sütun atlayan kenarlar düğümlerin
// arkasından geçiyordu.
//
// Sugiyama sanal noktaları (lib/topoBfsLayout `routes`). Çivilenen
// değişmezler:
//   • uzun ileri kenarın her ara noktası kendi ara sütununun x'inde, sırayla
//   • hiçbir ara nokta aynı sütundaki gerçek düğüme satır aralığının
//     yarısından yakın değil (ve ara noktalar birbirine de)
//   • kısa kenar (1 sütun) ve geri kenar rota almaz
//   • tavan: toplam sanal nokta ≤ maxDummies; önce kısa kenarlar rota alır
//   • determinizm (karıştırılmış girdi dahil)
//   • şeritler çakışmaz, üyeler ve sanal noktalar kendi şeridinde
import { describe, it, expect } from 'vitest';
import { bfsBarycenterLayout, dummyCap, type LayoutResult, type LayoutOptions } from './topoBfsLayout';

type Node = { service: string; spanCount: number };
type Edge = { caller: string; callee: string };

const PITCH = 46;
const N = (...s: string[]): Node[] => s.map(service => ({ service, spanCount: 10 }));
const E = (...pairs: string[]): Edge[] => pairs.map(p => {
  const [caller, callee] = p.split('>');
  return { caller, callee };
});
const run = (nodes: Node[], edges: Edge[], opts: LayoutOptions = {}) =>
  bfsBarycenterLayout(nodes, edges, 800, 600, PITCH, opts);

// Rastgele DAG: yalnız i<j kenarları, açıklık ≤ 8 → bol uzun kenar.
function synthDag(n: number, deg: number, seed = 12345): { nodes: Node[]; edges: Edge[] } {
  const ns = Array.from({ length: n }, (_, i) => ({ service: `d${i}`, spanCount: i }));
  const es: Edge[] = [];
  let s = seed;
  const rnd = () => ((s = (s * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  for (let i = 0; i < n; i++) for (let k = 0; k < deg; k++) {
    const j = i + 1 + Math.floor(rnd() * Math.min(8, n - i - 1));
    if (j < n) es.push({ caller: `d${i}`, callee: `d${j}` });
  }
  return { nodes: ns, edges: es };
}
function synthCyclic(nodes: number, edgesPerNode: number): { nodes: Node[]; edges: Edge[] } {
  const ns = Array.from({ length: nodes }, (_, i) => ({ service: `svc-${i}`, spanCount: 1000 - (i % 997) }));
  const es: Edge[] = [];
  for (let i = 0; i < nodes; i++) for (let k = 1; k <= edgesPerNode; k++) {
    const j = (i * 7 + k * 13) % nodes;
    if (j !== i) es.push({ caller: `svc-${i}`, callee: `svc-${j}` });
  }
  return { nodes: ns, edges: es };
}
function shuffled<T>(a: T[], seed: number): T[] {
  const r = a.slice();
  let s = seed;
  for (let i = r.length - 1; i > 0; i--) {
    s = (s * 16807) % 2147483647;
    const j = s % (i + 1);
    [r[i], r[j]] = [r[j], r[i]];
  }
  return r;
}
const span = (r: LayoutResult, k: string) => {
  const [u, v] = k.split('|');
  return r.layer.get(v)! - r.layer.get(u)!;
};
/** sütun (katman) → x; en uzun yol katmanında her ara katmanda gerçek düğüm var. */
function colX(r: LayoutResult): Map<number, number> {
  const m = new Map<number, number>();
  for (const [s, l] of r.layer) m.set(l, r.pos.get(s)!.x);
  return m;
}
const dummyCount = (r: LayoutResult) => [...r.routes.values()].reduce((s, p) => s + p.length, 0);

function expectRouteInvariants(r: LayoutResult, edges: Edge[]) {
  const cx = colX(r);
  const fwdLong = new Set<string>();
  for (const e of edges) {
    const k = `${e.caller}|${e.callee}`;
    if (r.backEdges.has(k) || e.caller === e.callee) continue;
    if (span(r, k) > 1) fwdLong.add(k);
  }
  // rota yalnız uzun ileri kenarlarda
  for (const k of r.routes.keys()) expect(fwdLong.has(k), k).toBe(true);
  // her ara nokta sırasıyla ara sütunun x'inde
  for (const [k, pts] of r.routes) {
    const l0 = r.layer.get(k.split('|')[0])!;
    expect(pts.length).toBe(span(r, k) - 1);
    pts.forEach((p, i) => expect(p.x).toBe(cx.get(l0 + 1 + i)));
  }
  // aynı sütunda gerçek düğüm + ara noktalar: en az satır aralığı/2 ayrık
  const byX = new Map<number, { y: number; real: boolean }[]>();
  const add = (x: number, y: number, real: boolean) =>
    (byX.get(x) ?? byX.set(x, []).get(x)!).push({ y, real });
  for (const p of r.pos.values()) add(p.x, p.y, true);
  for (const pts of r.routes.values()) for (const p of pts) add(p.x, p.y, false);
  for (const col of byX.values()) {
    col.sort((a, b) => a.y - b.y);
    for (let i = 1; i < col.length; i++) {
      if (!col[i].real && !col[i - 1].real) {
        expect(col[i].y - col[i - 1].y).toBeGreaterThanOrEqual(PITCH / 2);
      } else {
        expect(col[i].y - col[i - 1].y).toBeGreaterThanOrEqual(PITCH - 1e-9);
      }
    }
  }
}

describe('sanal noktalar — uzun kenar rotası', () => {
  it('a→b→c→d + a→d: a→d iki ara nokta alır, b ve c sütunlarında, onlarla çakışmadan', () => {
    const edges = E('a>b', 'b>c', 'c>d', 'a>d');
    const r = run(N('a', 'b', 'c', 'd'), edges);
    const pts = r.routes.get('a|d')!;
    expect(pts).toHaveLength(2);
    expect(pts[0].x).toBe(r.pos.get('b')!.x);
    expect(pts[1].x).toBe(r.pos.get('c')!.x);
    expect(Math.abs(pts[0].y - r.pos.get('b')!.y)).toBeGreaterThanOrEqual(PITCH / 2);
    expect(Math.abs(pts[1].y - r.pos.get('c')!.y)).toBeGreaterThanOrEqual(PITCH / 2);
    expectRouteInvariants(r, edges);
  });

  it('kısa kenarlar (1 sütun) ve geri kenarlar rota almaz', () => {
    const edges = E('a>b', 'b>c', 'c>a', 'a>c');
    const r = run(N('a', 'b', 'c'), edges);
    expect(r.routes.has('a|b')).toBe(false);
    expect(r.routes.has('b|c')).toBe(false);
    for (const k of r.backEdges) expect(r.routes.has(k)).toBe(false);
    // a→c iki sütun atlar → tek ara nokta
    expect(r.routes.get('a|c')).toHaveLength(1);
  });

  it('zincir kenarlı DAG: hiç rota yok', () => {
    const r = run(N('a', 'b', 'c', 'd'), E('a>b', 'b>c', 'c>d'));
    expect(r.routes.size).toBe(0);
  });

  it('rastgele DAG 300: tüm rota değişmezleri', () => {
    const g = synthDag(300, 3);
    const r = run(g.nodes, g.edges);
    expect(r.routes.size).toBeGreaterThan(20);
    expectRouteInvariants(r, g.edges);
  });

  it('döngülü yoğun sentetik (200/6): değişmezler, geri kenar rotasız', () => {
    const g = synthCyclic(200, 6);
    const r = run(g.nodes, g.edges);
    expect(r.routes.size).toBeGreaterThan(0);
    expectRouteInvariants(r, g.edges);
  });
});

describe('tavan (cap) — tek eğriye geri düşüş', () => {
  it('maxDummies: toplam ≤ tavan; önce kısa kenarlar, en uzun düşer', () => {
    // a→b→c→d→e→f; a→c (1 ara), a→d (2), a→f (4)
    const edges = E('a>b', 'b>c', 'c>d', 'd>e', 'e>f', 'a>c', 'a>d', 'a>f');
    const full = run(N('a', 'b', 'c', 'd', 'e', 'f'), edges);
    expect(dummyCount(full)).toBe(7);
    const r = run(N('a', 'b', 'c', 'd', 'e', 'f'), edges, { maxDummies: 3 });
    expect(dummyCount(r)).toBeLessThanOrEqual(3);
    expect(r.routes.has('a|c')).toBe(true);
    expect(r.routes.has('a|d')).toBe(true);
    expect(r.routes.has('a|f')).toBe(false); // en uzun → eski tek eğri
    expectRouteInvariants(r, edges);
  });

  it('maxDummies 0: hiç rota yok (hepsi tek eğri)', () => {
    const g = synthDag(120, 3);
    const r = run(g.nodes, g.edges, { maxDummies: 0 });
    expect(r.routes.size).toBe(0);
  });

  it('varsayılan tavan 500/40 tavan grafiğinde tutar (≤ dummyCap(N))', { timeout: 15_000 }, () => {
    const g = synthCyclic(500, 40);
    const r = run(g.nodes, g.edges);
    expect(dummyCap(500)).toBe(2000);
    expect(dummyCount(r)).toBeGreaterThan(0);
    expect(dummyCount(r)).toBeLessThanOrEqual(dummyCap(500));
    expectRouteInvariants(r, g.edges);
  });

  it('dummyCap: küçük grafta taban 200, büyükte 3000 tavanı', () => {
    expect(dummyCap(10)).toBe(200);
    expect(dummyCap(100)).toBe(400);
    expect(dummyCap(5000)).toBe(3000);
  });
});

describe('determinizm', () => {
  it('aynı girdi ve karıştırılmış girdi → aynı rotalar', () => {
    const g = synthDag(150, 3, 99);
    const bandOf = (s: string) => (Number(s.slice(1)) % 3 === 0 ? undefined : `ns${Number(s.slice(1)) % 3}`);
    const snap = (r: LayoutResult) => JSON.stringify({
      routes: [...r.routes.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
      pos: [...r.pos.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
      bands: r.bands,
    });
    const a = snap(run(g.nodes, g.edges, { bandOf }));
    expect(snap(run(g.nodes, g.edges, { bandOf }))).toBe(a);
    expect(snap(run(shuffled(g.nodes, 3), shuffled(g.edges, 5), { bandOf }))).toBe(a);
  });
});

describe('şeritler + sanal noktalar', () => {
  const g = synthDag(160, 3, 7);
  const bandOf = (s: string) => {
    const i = Number(s.slice(1));
    return i % 4 === 0 ? undefined : `ns${i % 4}`;
  };
  const r = run(g.nodes, g.edges, { bandOf });
  const bandOfY = (y: number) => r.bands.findIndex(b => y >= b.y0 && y <= b.y1);

  it('şeritler çakışmaz; her üye kendi şeridinin içinde', () => {
    for (let i = 1; i < r.bands.length; i++) expect(r.bands[i].y0).toBeGreaterThanOrEqual(r.bands[i - 1].y1);
    for (const n of g.nodes) {
      const b = r.bands[bandOfY(r.pos.get(n.service)!.y)];
      expect(b.key).toBe(bandOf(n.service) ?? null);
    }
  });

  it('ara noktalar KAYNAĞIN şeridinde; şerit yüksekliği sanal satırları da taşır', () => {
    expect(r.routes.size).toBeGreaterThan(0);
    for (const [k, pts] of r.routes) {
      const src = k.split('|')[0];
      const want = bandOf(src) ?? null;
      for (const p of pts) expect(r.bands[bandOfY(p.y)].key).toBe(want);
    }
    for (const b of r.bands) expect(b.y1 - b.y0).toBeGreaterThanOrEqual(b.rows * PITCH);
    expectRouteInvariants(r, g.edges);
  });
});
