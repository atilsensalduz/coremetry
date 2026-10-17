// topoArrow — v0.9.1032 (operatör isteği: "kesikli çizgiler var ama ok
// yönleri de olsa iyi olur"). TopologyFlowGraph kenarları yönlü
// (caller → callee) ama çizgide yön okunmuyordu; akış animasyonu yönü
// ima ediyor, durağan bakışta belirsiz.
//
// Kenar şekli TopologyFlowGraph ile SÖZLEŞMELİ:
//   M a  C (mx, a.y) (mx, b.y)  b      mx = (a.x + b.x) / 2
// Ok başı eğri ÜZERİNDE bir t noktasına, o noktadaki tanjanta hizalı
// konur. Düğüm pilleri (HTML) SVG'nin üstünde durduğundan uç noktaya
// (t=1) konan ok pilin altında kaybolur — hedefe yakın t (≈0.78)
// kolonlar arasındaki boşlukta kalır; kenar RED chip'i tam orta
// noktada (t=0.5) yaşadığından onunla da çakışmaz. Çift yönlü kenarda
// ikinci ok kaynağa yakın t'de (≈0.22) TERS tanjantla çizilir.
//
// SAF: DOM yok — de Casteljau yerine kapalı form nokta + türev.

export interface TopoPt { x: number; y: number }

export interface EdgeArrow {
  x: number;
  y: number;
  /** SVG rotate() derecesi — 0° = +x yönü (sağa). */
  angle: number;
}

/**
 * edgeArrow — kenarın t ∈ [0,1] noktası + akış yönü açısı.
 * `reverse: true` ters yön okunu (çift yönlü kenarın kaynak ucu) verir:
 * aynı nokta, 180° dönük açı.
 */
export function edgeArrow(a: TopoPt, b: TopoPt, t: number, reverse = false): EdgeArrow {
  const mx = (a.x + b.x) / 2;
  const c1 = { x: mx, y: a.y };
  const c2 = { x: mx, y: b.y };
  const u = 1 - t;
  // B(t) = u³a + 3u²t·c1 + 3ut²·c2 + t³b
  const x = u * u * u * a.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * b.x;
  const y = u * u * u * a.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * b.y;
  // B'(t) = 3u²(c1−a) + 6ut(c2−c1) + 3t²(b−c2)
  let dx = 3 * u * u * (c1.x - a.x) + 6 * u * t * (c2.x - c1.x) + 3 * t * t * (b.x - c2.x);
  let dy = 3 * u * u * (c1.y - a.y) + 6 * u * t * (c2.y - c1.y) + 3 * t * t * (b.y - c2.y);
  // Dejenere (a ≈ b): tanjant sıfır — kiriş yönüne, o da sıfırsa +x'e düş.
  if (Math.abs(dx) < 1e-9 && Math.abs(dy) < 1e-9) {
    dx = b.x - a.x;
    dy = b.y - a.y;
    if (Math.abs(dx) < 1e-9 && Math.abs(dy) < 1e-9) dx = 1;
  }
  let angle = (Math.atan2(dy, dx) * 180) / Math.PI;
  if (reverse) angle += 180;
  return { x, y, angle };
}

// ── v0.10.1269 — çok noktalı kenar yolu ──────────────────────────────────
// Operatör 2026-10-17: çok sütun atlayan kenarlar düğümlerin arkasından
// geçiyordu. Yerleşim (lib/topoBfsLayout `routes`) uzun kenara ara sütun
// noktaları verir; kenar a → w1 … wk → b üzerinden YATAY-TANJANTLI kübik
// zincir olarak çizilir: her ardışık (p, q) çifti için
//   C (mx, p.y) (mx, q.y) q      mx = (p.x + q.x) / 2
// Yani iki noktalı yol eski tek eğrinin BİREBİR aynısı (edgeArrow sözleşmesi
// korunur); her ara noktada tanjant yatay → zincir pürüzsüz (G1), ara nokta
// kendi satır yuvasında sütunu yatay keser, komşu düğüme sürtünmez.
// Catmull-Rom yerine bu: aynı noktalardan geçer ama sütunda dikey salınım
// yapmaz (CR tanjantı eğik girip yuvanın dışına taşabilirdi).

const r2 = (v: number) => Math.round(v * 100) / 100;

/** a → … → b noktalarından geçen SVG path `d` dizesi. */
export function edgePathD(pts: readonly TopoPt[]): string {
  if (pts.length === 0) return '';
  const p0 = pts[0];
  if (pts.length === 2) {
    // eski tek eğrinin dizesi birebir (yuvarlama yok — eski çıktıyla aynı)
    const b = pts[1];
    const mx = (p0.x + b.x) / 2;
    return `M ${p0.x} ${p0.y} C ${mx} ${p0.y}, ${mx} ${b.y}, ${b.x} ${b.y}`;
  }
  let d = `M ${r2(p0.x)} ${r2(p0.y)}`;
  for (let i = 1; i < pts.length; i++) {
    const p = pts[i - 1], q = pts[i];
    const mx = r2((p.x + q.x) / 2);
    d += ` C ${mx} ${r2(p.y)}, ${mx} ${r2(q.y)}, ${r2(q.x)} ${r2(q.y)}`;
  }
  return d;
}

// ── v0.10.1305 — şerit yolu (genel harita, düzleştirilmiş zincir) ────────
// lib/topoBfsLayout zincir düzleştirmesinden sonra uzun kenarın ara noktaları
// çoğunlukla AYNI y'de: yol a → w1 kübik (yatay tanjant), w1 … wk düz L,
// wk → b kübik. En çok iki büküm; y'si farklı ardışık ara noktalar (düzleşme
// olmadıysa) edgePathD'nin yatay-tanjantlı kübiğiyle bağlanır. Geometri
// edgePathD ile BİREBİR aynı (eşit y'li yatay-tanjant kübik = doğru parçası)
// → pathArrow ok/chip konumu değişmeden geçerli.

/** a → w1 … wk → b şerit yolunun SVG `d` dizesi. */
export function lanePathD(pts: readonly TopoPt[]): string {
  if (pts.length <= 2) return edgePathD(pts);
  const p0 = pts[0];
  let d = `M ${r2(p0.x)} ${r2(p0.y)}`;
  const last = pts.length - 1;
  for (let i = 1; i <= last; i++) {
    const p = pts[i - 1], q = pts[i];
    const inner = i > 1 && i < last; // ara nokta → ara nokta
    if (inner && Math.abs(q.y - p.y) < 0.01) {
      d += ` L ${r2(q.x)} ${r2(q.y)}`;
      continue;
    }
    const mx = r2((p.x + q.x) / 2);
    d += ` C ${mx} ${r2(p.y)}, ${mx} ${r2(q.y)}, ${r2(q.x)} ${r2(q.y)}`;
  }
  return d;
}

/**
 * pathArrow — çok noktalı yolun t ∈ [0,1] noktası + akış açısı. t, kiriş
 * uzunluğuyla ağırlıklı segmente dağıtılır (CSS offset-distance yüzdesine
 * yakın), segment içinde edgeArrow. İki noktada edgeArrow'un aynısı.
 */
export function pathArrow(pts: readonly TopoPt[], t: number, reverse = false): EdgeArrow {
  if (pts.length <= 2) return edgeArrow(pts[0], pts[pts.length - 1], t, reverse);
  let total = 0;
  const len: number[] = [];
  for (let i = 1; i < pts.length; i++) {
    const l = Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y);
    len.push(l); total += l;
  }
  if (total <= 0) return edgeArrow(pts[0], pts[pts.length - 1], t, reverse);
  let target = Math.min(1, Math.max(0, t)) * total;
  for (let i = 0; i < len.length; i++) {
    if (target <= len[i] || i === len.length - 1) {
      const lt = len[i] > 0 ? Math.min(1, target / len[i]) : 0;
      return edgeArrow(pts[i], pts[i + 1], lt, reverse);
    }
    target -= len[i];
  }
  return edgeArrow(pts[0], pts[pts.length - 1], t, reverse);
}

// ── v0.10.1294 — genel kübik (odak sütun yerleşiminin peer yayı) ───────────
// Aynı sütundaki iki düğüm arasındaki peer kenarı dış olukta TEK kübik yaydır
// (lib/topoFocusLayout ctrl): edgePathD'nin yatay-tanjant sözleşmesi aynı x'te
// iki ucu dikey bir çizgiye indirirdi. Ok ve chip yayın kendisinden okunur.

/** M a C c1, c2, b — tek kübik segmentin path `d` dizesi. */
export function cubicD(a: TopoPt, c1: TopoPt, c2: TopoPt, b: TopoPt): string {
  return `M ${r2(a.x)} ${r2(a.y)} C ${r2(c1.x)} ${r2(c1.y)}, ${r2(c2.x)} ${r2(c2.y)}, ${r2(b.x)} ${r2(b.y)}`;
}

/** cubicArrow — genel kübiğin t noktası + akış açısı (edgeArrow'un genel hâli). */
export function cubicArrow(a: TopoPt, c1: TopoPt, c2: TopoPt, b: TopoPt, t: number, reverse = false): EdgeArrow {
  const u = 1 - t;
  const x = u * u * u * a.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * b.x;
  const y = u * u * u * a.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * b.y;
  let dx = 3 * u * u * (c1.x - a.x) + 6 * u * t * (c2.x - c1.x) + 3 * t * t * (b.x - c2.x);
  let dy = 3 * u * u * (c1.y - a.y) + 6 * u * t * (c2.y - c1.y) + 3 * t * t * (b.y - c2.y);
  if (Math.abs(dx) < 1e-9 && Math.abs(dy) < 1e-9) {
    dx = b.x - a.x;
    dy = b.y - a.y;
    if (Math.abs(dx) < 1e-9 && Math.abs(dy) < 1e-9) dx = 1;
  }
  let angle = (Math.atan2(dy, dx) * 180) / Math.PI;
  if (reverse) angle += 180;
  return { x, y, angle };
}
