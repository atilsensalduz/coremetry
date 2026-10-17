import { describe, it, expect } from 'vitest';
import { edgeArrow, edgePathD, lanePathD, pathArrow, cubicD, cubicArrow } from './topoArrow';

// v0.9.1032 — topoloji kenar oku matematiği. Kenar şekli
// TopologyFlowGraph ile sözleşmeli (M a C (mx,a.y) (mx,b.y) b);
// bu testler o sözleşmenin nokta+tanjant yarısını çiviliyor.

describe('edgeArrow (v0.9.1032)', () => {
  it('yatay kenar: ok akış yönünde (sağa, 0°) ve eğri üzerinde', () => {
    const r = edgeArrow({ x: 0, y: 0 }, { x: 100, y: 0 }, 0.78);
    expect(r.angle).toBeCloseTo(0, 5);
    expect(r.y).toBeCloseTo(0, 5);
    expect(r.x).toBeGreaterThan(50);
    expect(r.x).toBeLessThan(100);
  });

  it('sola akan kenar: 180°', () => {
    const r = edgeArrow({ x: 100, y: 0 }, { x: 0, y: 0 }, 0.78);
    expect(Math.abs(r.angle)).toBeCloseTo(180, 5);
  });

  it('aynı kolon (dikey S): tanjant dikey, işaret dy yönünde', () => {
    const down = edgeArrow({ x: 50, y: 0 }, { x: 50, y: 100 }, 0.5);
    expect(down.angle).toBeCloseTo(90, 5);
    const up = edgeArrow({ x: 50, y: 100 }, { x: 50, y: 0 }, 0.5);
    expect(up.angle).toBeCloseTo(-90, 5);
  });

  it('reverse: aynı nokta, 180° dönük açı (çift yönlü kenarın kaynak oku)', () => {
    const f = edgeArrow({ x: 0, y: 0 }, { x: 100, y: 40 }, 0.22);
    const r = edgeArrow({ x: 0, y: 0 }, { x: 100, y: 40 }, 0.22, true);
    expect(r.x).toBeCloseTo(f.x, 9);
    expect(r.y).toBeCloseTo(f.y, 9);
    expect(((r.angle - f.angle) % 360 + 360) % 360).toBeCloseTo(180, 5);
  });

  it('uçlarda tanjant yatay (kontrol noktaları mx üstünde) — pil önü oku yana bakmaz', () => {
    // t→1 ucunda türev (b − c2) = (b.x − mx, 0): açı 0 (b sağdaysa).
    const r = edgeArrow({ x: 0, y: 0 }, { x: 100, y: 80 }, 0.999);
    expect(Math.abs(r.angle)).toBeLessThan(1);
  });

  it('dejenere a≈b: patlamaz, +x düşüşü', () => {
    const r = edgeArrow({ x: 5, y: 5 }, { x: 5, y: 5 }, 0.78);
    expect(Number.isFinite(r.angle)).toBe(true);
    expect(r.x).toBeCloseTo(5, 9);
    expect(r.y).toBeCloseTo(5, 9);
  });
});

// v0.10.1269 — operatör 2026-10-17: çok sütun atlayan kenarlar düğümlerin
// arkasından geçiyordu. Kenar yolu artık nokta zinciri (edgePathD); iki
// noktada eski tek eğri dizesi BİREBİR korunur.
describe('edgePathD / pathArrow (v0.10.1269)', () => {
  it('iki nokta: eski "M a C (mx,a.y) (mx,b.y) b" dizesinin aynısı', () => {
    const a = { x: 10, y: 20 }, b = { x: 210, y: 80 };
    expect(edgePathD([a, b])).toBe('M 10 20 C 110 20, 110 80, 210 80');
  });
  it('ara noktalı yol her noktadan yatay tanjantla geçer', () => {
    const d = edgePathD([{ x: 0, y: 0 }, { x: 100, y: 50 }, { x: 200, y: 50 }]);
    expect(d).toBe('M 0 0 C 50 0, 50 50, 100 50 C 150 50, 150 50, 200 50');
  });
  it('pathArrow iki noktada edgeArrow ile aynı', () => {
    const a = { x: 0, y: 0 }, b = { x: 100, y: 40 };
    expect(pathArrow([a, b], 0.78)).toEqual(edgeArrow(a, b, 0.78));
    expect(pathArrow([a, b], 0.22, true)).toEqual(edgeArrow(a, b, 0.22, true));
  });
  it('pathArrow çok noktada: t=0.5 orta segmentte, ara noktada yatay', () => {
    const pts = [{ x: 0, y: 0 }, { x: 100, y: 0 }, { x: 200, y: 0 }, { x: 300, y: 0 }];
    const m = pathArrow(pts, 0.5);
    expect(m.x).toBeCloseTo(150, 5);
    expect(m.angle).toBeCloseTo(0, 5);
    const end = pathArrow(pts, 0.9);
    expect(end.x).toBeGreaterThan(200);
  });
});

// v0.10.1294 — peer yayı (odak sütun yerleşimi): genel kübik.
describe('cubicArrow / cubicD (v0.10.1294)', () => {
  it('yatay-tanjant kontrol noktalarıyla edgeArrow\'un aynısı', () => {
    const a = { x: 0, y: 0 }, b = { x: 100, y: 40 };
    for (const t of [0, 0.22, 0.5, 0.78, 1]) {
      const g = cubicArrow(a, { x: 50, y: 0 }, { x: 50, y: 40 }, b, t);
      const e = edgeArrow(a, b, t);
      expect(g.x).toBeCloseTo(e.x, 9);
      expect(g.y).toBeCloseTo(e.y, 9);
      expect(g.angle).toBeCloseTo(e.angle, 9);
    }
  });
  it('dış yay ortası pil kenarının dışında, tepe noktasında dikey akar', () => {
    const a = { x: 85, y: 0 }, b = { x: 85, y: 64 };
    const cx = (85 + 22) / 0.75; // x=0 düğümü: kontrol x = x + (85+22)/0.75
    const m = cubicArrow(a, { x: cx, y: 0 }, { x: cx, y: 64 }, b, 0.5);
    expect(m.x).toBeGreaterThan(85);
    expect(m.y).toBeCloseTo(32, 9);
    expect(Math.abs(m.angle)).toBeCloseTo(90, 6);
    expect(cubicD(a, { x: cx, y: 0 }, { x: cx, y: 64 }, b)).toMatch(/^M 85 0 C [\d.]+ 0, [\d.]+ 64, 85 64$/);
  });
});

// v0.10.1305 — Genel servis haritası: düzleştirilmiş zincirin şerit yolu.
describe('lanePathD (v0.10.1305)', () => {
  const bends = (d: string) => (d.match(/C /g) ?? []).length;
  it('aynı y\'li ara noktalar: giriş kübiği + düz L + çıkış kübiği → ≤2 büküm', () => {
    const pts = [{ x: 0, y: 0 }, { x: 240, y: 100 }, { x: 480, y: 100 }, { x: 720, y: 100 }, { x: 960, y: 20 }];
    const d = lanePathD(pts);
    expect(bends(d)).toBeLessThanOrEqual(2);
    expect(d).toBe('M 0 0 C 120 0, 120 100, 240 100 L 480 100 L 720 100 C 840 100, 840 20, 960 20');
  });
  it('y\'si farklı ara noktalar yatay-tanjantlı kübikle bağlanır (edgePathD ile aynı)', () => {
    const pts = [{ x: 0, y: 0 }, { x: 240, y: 100 }, { x: 480, y: 150 }, { x: 720, y: 0 }];
    expect(lanePathD(pts)).toBe(edgePathD(pts));
  });
  it('2 nokta: edgePathD\'nin aynısı', () => {
    const pts = [{ x: 0, y: 0 }, { x: 240, y: 60 }];
    expect(lanePathD(pts)).toBe(edgePathD(pts));
  });
});
