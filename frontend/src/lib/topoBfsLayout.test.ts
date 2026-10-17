// v0.10.1261 — operatör 2026-10-17: aynı sütunda birbirini çağıran
// servisler / iç içe namespace kutuları.
//
// Eski test yerleşimi v0.8.296 referans kopyasıyla BİREBİR karşılaştırıyordu
// (BFS en kısa mesafe sütunu). Algoritma bilerek değişti (en uzun yol katmanı
// + namespace şeritleri); bu dosya artık yeni DEĞİŞMEZLERİ çiviler:
//   • döngüsüz grafta her kenar sütunu kesin artırır, aynı sütun kenarı yok
//   • döngü sonlanır, geri kenar sağdan sola, deterministik
//   • lavabo en sağdaki çağıranının hemen sağında (ASAP)
//   • şeritler çakışmaz, her üye kendi şeridinde; firstBand en üstte
//   • yalıtık düğümler yerleşir; aynı sütunda düğümler satır aralığıyla ayrık
//   • determinizm — aynı girdi ve KARIŞTIRILMIŞ girdi aynı çıktıyı verir
import { describe, it, expect } from 'vitest';
import { bfsBarycenterLayout, MAX_LAYERS, type LayoutResult } from './topoBfsLayout';

type Node = { service: string; spanCount: number };
type Edge = { caller: string; callee: string };

const PITCH = 46;
const N = (...s: string[]): Node[] => s.map(service => ({ service, spanCount: 10 }));
const E = (...pairs: string[]): Edge[] => pairs.map(p => {
  const [caller, callee] = p.split('>');
  return { caller, callee };
});
const run = (nodes: Node[], edges: Edge[], bandOf?: (s: string) => string | undefined, firstBand?: string) =>
  bfsBarycenterLayout(nodes, edges, 800, 600, PITCH, { bandOf, firstBand });

function synth(nodes: number, edgesPerNode: number, seed = 7): { nodes: Node[]; edges: Edge[] } {
  const ns = Array.from({ length: nodes }, (_, i) => ({ service: `svc-${i}`, spanCount: 1000 - (i % 997) }));
  const es: Edge[] = [];
  for (let i = 0; i < nodes; i++) {
    for (let k = 1; k <= edgesPerNode; k++) {
      const j = (i * seed + k * 13) % nodes;
      if (j !== i) es.push({ caller: `svc-${i}`, callee: `svc-${j}` });
    }
  }
  return { nodes: ns, edges: es };
}
// Rastgele DAG: yalnız i<j kenarları (döngü yok).
function synthDag(n: number, deg: number): { nodes: Node[]; edges: Edge[] } {
  const ns = Array.from({ length: n }, (_, i) => ({ service: `d${i}`, spanCount: i }));
  const es: Edge[] = [];
  let s = 12345;
  const rnd = () => ((s = (s * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
  for (let i = 0; i < n; i++) for (let k = 0; k < deg; k++) {
    const j = i + 1 + Math.floor(rnd() * Math.min(6, n - i - 1));
    if (j < n) es.push({ caller: `d${i}`, callee: `d${j}` });
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
function snapshot(r: LayoutResult) {
  return {
    lw: r.lw, lh: r.lh,
    pos: [...r.pos.entries()].sort(([a], [b]) => (a < b ? -1 : 1)),
    bands: r.bands,
    back: [...r.backEdges].sort(),
  };
}
function expectNoOverlapInColumns(r: LayoutResult) {
  const byX = new Map<number, number[]>();
  for (const p of r.pos.values()) (byX.get(p.x) ?? byX.set(p.x, []).get(p.x)!).push(p.y);
  for (const ys of byX.values()) {
    ys.sort((a, b) => a - b);
    for (let i = 1; i < ys.length; i++) expect(ys[i] - ys[i - 1]).toBeGreaterThanOrEqual(PITCH - 1e-9);
  }
}

describe('katman — döngüsüz graf', () => {
  it('ileri kenarlar sütunu kesin artırır; aynı sütun kenarı yok (eski BFS: a,b aynı sütun)', () => {
    // gw→a, gw→b, a→b: BFS a ve b'yi d=1'e koyardı → dikey kenar.
    const nodes = N('gw', 'a', 'b', 'c', 'd');
    const edges = E('gw>a', 'gw>b', 'a>b', 'b>c', 'a>c', 'c>d', 'gw>d');
    const r = run(nodes, edges);
    for (const e of edges) {
      expect(r.layer.get(e.callee)!, `${e.caller}→${e.callee}`).toBeGreaterThan(r.layer.get(e.caller)!);
      expect(r.pos.get(e.callee)!.x).toBeGreaterThan(r.pos.get(e.caller)!.x);
    }
    expect(r.backEdges.size).toBe(0);
    expect([...'gw a b c d'.split(' ')].map(s => r.layer.get(s))).toEqual([0, 1, 2, 3, 4]);
  });

  it('rastgele DAG 300 düğüm: tüm kenarlar soldan sağa', () => {
    const { nodes, edges } = synthDag(300, 3);
    const r = run(nodes, edges);
    expect(r.backEdges.size).toBe(0);
    for (const e of edges) {
      if (r.layer.get(e.caller)! >= MAX_LAYERS - 1) continue; // güvenlik tavanı kıstırması
      expect(r.layer.get(e.callee)!).toBeGreaterThan(r.layer.get(e.caller)!);
    }
    expectNoOverlapInColumns(r);
  });

  it('lavabo (db) en sağdaki çağıranının hemen sağında, son sütuna itilmez', () => {
    const nodes = N('gw', 'a', 'b', 'c', 'db:orders');
    const edges = E('gw>a', 'a>b', 'b>c', 'a>db:orders');
    const r = run(nodes, edges);
    expect(r.layer.get('db:orders')).toBe(r.layer.get('a')! + 1);
    expect(r.layer.get('c')).toBe(3);
  });

  it('yalıtık düğümler yerleşir, ilk sütunda', () => {
    const r = run(N('a', 'b', 'lonely', 'island'), E('a>b'));
    expect(r.pos.size).toBe(4);
    expect(r.layer.get('lonely')).toBe(0);
    expect(r.layer.get('island')).toBe(0);
    expectNoOverlapInColumns(r);
  });

  it('bilinmeyen uçlu ve öz-döngü kenarlar yok sayılır', () => {
    const r = run(N('a', 'b'), E('a>a', 'ghost>a', 'a>b', 'b>ghost'));
    expect(r.layer.get('a')).toBe(0);
    expect(r.layer.get('b')).toBe(1);
    expect(r.backEdges.size).toBe(0);
  });
});

describe('katman — döngüler', () => {
  it('kök yok (x→y→z→x): sonlanır, tek geri kenar, kalanlar soldan sağa', () => {
    const nodes = [{ service: 'x', spanCount: 5 }, { service: 'y', spanCount: 9 }, { service: 'z', spanCount: 1 }];
    const edges = E('x>y', 'y>z', 'z>x');
    const r = run(nodes, edges);
    expect(r.pos.size).toBe(3);
    // En yüksek hacimli (y) DFS kökü → z→x... değil: y→z→x→y, geri kenar x>y.
    expect([...r.backEdges]).toEqual(['x|y']);
    expect(r.layer.get('y')).toBe(0);
    expect(r.layer.get('z')).toBe(1);
    expect(r.layer.get('x')).toBe(2);
    expect(r.pos.get('x')!.x).toBeGreaterThan(r.pos.get('y')!.x); // geri kenar sağdan sola
  });

  it('çift yönlü A↔B kökten: geri kenar B→A', () => {
    const r = run(N('gw', 'a', 'b'), E('gw>a', 'a>b', 'b>a'));
    expect([...r.backEdges]).toEqual(['b|a']);
    expect(r.layer.get('b')).toBeGreaterThan(r.layer.get('a')!);
  });

  it('geri kenar olmayan her kenar sütunu artırır (yoğun döngülü sentetik)', () => {
    const { nodes, edges } = synth(120, 4);
    const r = run(nodes, edges);
    for (const e of edges) {
      if (r.backEdges.has(`${e.caller}|${e.callee}`) || e.caller === e.callee) continue;
      if (r.layer.get(e.caller)! >= MAX_LAYERS - 1) continue;
      expect(r.layer.get(e.callee)!).toBeGreaterThan(r.layer.get(e.caller)!);
    }
  });

  it('500/40 tavan grafiği: sonlanır, her düğüm yerleşir, sütun sayısı tavanda', { timeout: 15_000 }, () => {
    const { nodes, edges } = synth(500, 40, 11);
    const r = run(nodes, edges);
    expect(r.pos.size).toBe(500);
    expect(Math.max(...r.layer.values())).toBeLessThanOrEqual(MAX_LAYERS - 1);
    expectNoOverlapInColumns(r);
  });
});

describe('namespace şeritleri', () => {
  const nsMap: Record<string, string> = {
    'crm-api': 'crm', 'crm-db-proxy': 'crm', 'crm-worker': 'crm',
    'sales-web': 'sales', 'sales-api': 'sales', 'sales-report': 'sales',
    'billing': 'pay', 'ledger': 'pay',
  };
  const nodes = N(...Object.keys(nsMap), 'ext:stripe', 'db:pg', 'loner');
  const edges = E(
    'sales-web>sales-api', 'sales-api>crm-api', 'sales-api>sales-report', 'sales-report>crm-api',
    'crm-api>crm-db-proxy', 'crm-api>crm-worker', 'crm-db-proxy>db:pg',
    'sales-api>billing', 'billing>ledger', 'ledger>ext:stripe', 'loner>crm-api',
  );
  const bandOf = (s: string) => nsMap[s];

  it('şeritler çakışmaz ve her üye kendi şeridinin içinde', () => {
    const r = run(nodes, edges, bandOf);
    const keys = r.bands.map(b => b.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys[keys.length - 1]).toBeNull(); // grupsuz şerit en altta
    for (let i = 1; i < r.bands.length; i++) expect(r.bands[i].y0).toBeGreaterThanOrEqual(r.bands[i - 1].y1);
    for (const n of nodes) {
      const key = bandOf(n.service) ?? null;
      const b = r.bands.find(x => x.key === key)!;
      const p = r.pos.get(n.service)!;
      expect(p.y - PITCH / 2, n.service).toBeGreaterThanOrEqual(b.y0);
      expect(p.y + PITCH / 2, n.service).toBeLessThanOrEqual(b.y1);
    }
    expect(r.bands.find(b => b.key === 'crm')!.members).toBe(3);
    expectNoOverlapInColumns(r);
  });

  it('şerit yüksekliği = en kalabalık sütun × satır aralığı (+ başlık payı)', () => {
    const r = run(nodes, edges, bandOf);
    const crm = r.bands.find(b => b.key === 'crm')!;
    // crm-db-proxy ve crm-worker aynı sütunda → 2 satır
    expect(crm.rows).toBe(2);
  });

  it('şeritler varken katman değişmezi korunur', () => {
    const r = run(nodes, edges, bandOf);
    for (const e of edges) expect(r.layer.get(e.callee)!).toBeGreaterThan(r.layer.get(e.caller)!);
  });

  it('firstBand (odaklı servisin ns) en üstte', () => {
    expect(run(nodes, edges, bandOf, 'pay').bands[0].key).toBe('pay');
    expect(run(nodes, edges, bandOf, 'crm').bands[0].key).toBe('crm');
  });

  it('firstBand yoksa en kalabalık şerit (eşitlikte ad) başta, sonra en bağlı', () => {
    const r = run(nodes, edges, bandOf);
    // crm=3, sales=3 → ad: crm; sales crm'e 2 kenarla, pay 0 → sales ikinci.
    expect(r.bands.map(b => b.key)).toEqual(['crm', 'sales', 'pay', null]);
  });

  it('bandOf yoksa tek grupsuz şerit (saf katman)', () => {
    const r = run(nodes, edges);
    expect(r.bands).toHaveLength(1);
    expect(r.bands[0].key).toBeNull();
  });

  it('tüm düğümler adlı şeritte ise grupsuz şerit yok', () => {
    const r = run(N('a1', 'a2', 'b1', 'b2'), E('a1>a2', 'a2>b1', 'b1>b2'), s => s[0]);
    expect(r.bands.map(b => b.key)).toEqual(['a', 'b']);
  });
});

describe('determinizm', () => {
  const cases: { name: string; nodes: Node[]; edges: Edge[]; bandOf?: (s: string) => string | undefined }[] = [
    { name: 'döngü + ada', nodes: N('x', 'y', 'z', 'island', 'island2'), edges: E('x>y', 'y>z', 'z>x', 'island>island2', 'island2>island') },
    { name: 'sentetik 300/10', ...synth(300, 10) },
    { name: 'sentetik 200/6 şeritli', ...synth(200, 6), bandOf: s => (Number(s.slice(4)) % 5 === 0 ? undefined : `ns${Number(s.slice(4)) % 4}`) },
  ];
  for (const c of cases) {
    it(`${c.name}: aynı girdi → aynı çıktı; karıştırılmış girdi → aynı çıktı`, { timeout: 15_000 }, () => {
      const a = snapshot(run(c.nodes, c.edges, c.bandOf));
      const b = snapshot(run(c.nodes, c.edges, c.bandOf));
      const s = snapshot(run(shuffled(c.nodes, 3), shuffled(c.edges, 5), c.bandOf));
      expect(b).toEqual(a);
      expect(s).toEqual(a);
    });
  }
});

describe('sınır durumları', () => {
  it('boş graf', () => {
    const r = run([], []);
    expect(r.pos.size).toBe(0);
    expect(r.lw).toBe(800);
    expect(r.lh).toBe(600);
  });
  it('tek düğüm ortada', () => {
    const r = run(N('solo'), []);
    expect(r.pos.get('solo')).toEqual({ x: 400, y: 300 });
  });
  it('yerleşim alanı içerikle büyür (kalabalık sütun)', () => {
    const many = N('root', ...Array.from({ length: 30 }, (_, i) => `c${i}`));
    const r = run(many, many.slice(1).map(n => ({ caller: 'root', callee: n.service })));
    expect(r.lh).toBeGreaterThanOrEqual(30 * PITCH);
    expectNoOverlapInColumns(r);
  });
});

// v0.10.1265 — operatör 2026-10-17: "orijinal servis en altta çıkıyor, kendi
// namespace gözükmüyor; sol başta durabilir". `pin` = odak düğümü.
describe('odak sabitleme (pin)', () => {
  const nodes = N('focus', 'a', 'b', 'c', 'x', 'y', 'ext');
  const edges = E('focus>a', 'focus>b', 'focus>c', 'focus>x', 'x>ext', 'y>ext', 'a>y');
  const nsMap: Record<string, string> = { a: 'pay', b: 'pay', x: 'dep', y: 'dep' };

  it('grupsuz odak: grupsuz şerit EN ÜSTTE, odak sol sütunda ilk satırda', () => {
    const r = bfsBarycenterLayout(nodes, edges, 800, 600, PITCH, { bandOf: s => nsMap[s], pin: 'focus' });
    expect(r.bands[0].key).toBeNull();
    expect(r.layer.get('focus')).toBe(0);
    const fy = r.pos.get('focus')!.y;
    for (const n of nodes) {
      if (n.service !== 'focus' && r.layer.get(n.service) === 0) expect(fy).toBeLessThan(r.pos.get(n.service)!.y);
    }
    expect(fy).toBeLessThan(r.bands[0].y1);
  });

  it('tek üyeli odak namespace şeridi firstBand ile en üstte', () => {
    const bandOf = (s: string) => (s === 'focus' ? 'mobile' : nsMap[s]);
    const r = bfsBarycenterLayout(nodes, edges, 800, 600, PITCH, { bandOf, firstBand: 'mobile', pin: 'focus' });
    expect(r.bands[0].key).toBe('mobile');
    expect(r.bands[0].members).toBe(1);
    expect(r.bands[r.bands.length - 1].key).toBeNull();
  });

  it('pin hücre içinde barycenter süpürmelerinden sonra da ilk satır', () => {
    const r = bfsBarycenterLayout(N('z', 'focus', 'q'), E('z>q', 'focus>q'), 800, 600, PITCH, { pin: 'focus' });
    expect(r.pos.get('focus')!.y).toBeLessThan(r.pos.get('z')!.y);
  });
});
