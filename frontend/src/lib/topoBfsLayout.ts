// topoBfsLayout.ts — topoloji yerleşim çekirdeği, SAF.
//
// v0.10.1261 — operatör 2026-10-17: "Topoloji temelde 3 sütun ve aynı
// sütundaki servisler sık sık birbirini çağırıyor, çok karışık görünüyor"
// + iç içe binen namespace kutuları. Operatör seçimi "Katman + NS şeritleri":
//
//  1. KATMAN — sütun artık kökten EN KISA BFS mesafesi değil, döngüleri
//     kırılmış DAG üzerinde EN UZUN yol katmanı. BFS'te A ve B aynı d'de
//     erişilip A→B ise ikisi aynı sütuna düşüyor, kenar dikey çiziliyordu.
//     Şimdi: döngüler deterministik DFS ile kırılır (gri düğüme giden kenar
//     = geri kenar), kalan her kenar KESİNLİKLE soldan sağa gider; aynı
//     sütunda iki düğüm arasında kenar olamaz (geri kenarlar hariç — onlar
//     sağdan sola çizilir ve ayrı desen alır). Lavabolar (db/queue/ext,
//     çıkışı olmayan düğümler) en sağ kenara itilmez: en sağdaki
//     çağıranının hemen sağ sütununa oturur (ASAP) — kenar kısa kalır,
//     bağımlılık çağıranının yanında okunur; hepsini son sütuna yığmak
//     uzun, bant kesen kenarlar üretirdi.
//  2. NS ŞERİTLERİ — `bandOf` verilirse her AÇIK namespace tam genişlikte
//     yatay bir şerittir; üyeler şerit içinde katman sütunlarına oturur.
//     (v0.10.1268: üyelik çağıranda — lib/topoBands.ts; tek namespace'ten
//     çağrılan bağımlılık da o şeride düşer. Çekirdek jenerik kalır.)
//     Şeritler üst üste dizilir, ASLA çakışmaz; şeridi olmayanlar (dış
//     bağımlılıklar, namespace'siz servisler, katlı süper-düğümler) en
//     alttaki "grupsuz" şeritte. Şerit yüksekliği = şeridin en kalabalık
//     sütunu × satır aralığı. Sıra: `firstBand` (odaklı servisin ns'i)
//     başta, sonra yerleşmiş şeritlere en çok kenarla bağlı olan (açgözlü,
//     eşitlikte üye sayısı ↓ sonra ad) — kenar kesişimini azaltır, tam
//     deterministik. `bandOf` yoksa tek şerit = saf katmanlı yerleşim.
//  3. Sütun içi sıra: barycenter, üç tarama (sol→sağ, sağ→sol, sol→sağ),
//     yalnız kendi şerit hücresinde sıralar.
//
// DETERMİNİZM: düğüm adları sıralanıp indekslenir, komşuluk listeleri
// sıralanıp tekilleştirilir → çıktı girdinin düğüm/kenar SIRASINDAN
// bağımsızdır (testte karıştırılmış girdiyle pinli).
//
// PERF (v0.10.133 bütçesi, TopologyFlowGraph.perf.test.tsx 500/20k):
// komşuluk bir kez kurulur; DFS + Kahn O(V+E), her tarama O(E + n log n).
// Derinlik tavanı (MAX_LAYERS) yalnız güvenlik sınırıdır — patolojik
// yoğun grafta yerleşim genişliğini sınırlar; aşılırsa fazlası son sütuna
// kıstırılır (o durumda aynı sütun kenarı oluşabilir).
//
// (Dosya/işlev adı "bfs" tarihsel: perf belgeleri bu yolu anıyor.)
//
// v0.10.1269 — operatör 2026-10-17: çok sütun atlayan kenarlar düğümlerin
// arkasından geçiyordu. Sugiyama'nın standart çaresi: k>1 sütun atlayan her
// İLERİ kenar için ara sütunlara k−1 SANAL (dummy) nokta konur; sanal
// noktalar şerit hücresinde gerçek düğümlerle birlikte barycenter
// sıralamasına girer ve kendi satır yuvasını alır → aynı sütundaki gerçek
// düğümle asla çakışmaz (hücre içi aralık = satır aralığı). Çıktı
// `routes` ("caller|callee" → ara noktalar); bileşen kenarı bu noktalardan
// geçen yatay-tanjantlı kübik zincir olarak çizer (lib/topoArrow edgePathD).
//  • ŞERİT: sanal nokta KAYNAĞIN şeridinde yaşar. Şeritler yatay, sütunlar
//    dikey; şerit değiştirme yalnız iki sütun ARASINDA olur (orada düğüm
//    yok). Kaynak şeridinde kalan zincir tek bir dikey geçişle (son
//    segment, hedef sütununa girerken) hedef şeridine iner — yol en kısa
//    sınıfta (tek şerit geçişi), şeritler çakışmaz, çağrı çağıranın
//    namespace'inde okunur. Hedef şeridi de tek geçiş verirdi ama
//    şeritler arası kenarların çoğu grupsuz bağımlılıklara iner
//    (v0.10.1268 kuralı) → tüm sanal satırlar grupsuz şeridi şişirirdi.
//    Grupsuz şerit hiç seçilmez: alttadır, iki uzun dikey geçiş üretir.
//  • Geri kenarlar (döngü) rota almaz — eski tek eğri, noktalı desen.
//  • TAVAN: toplam sanal nokta ≤ maxDummies (varsayılan min(3000,
//    max(200, 4×N))). Aşılırsa kenarlar açıklık ↑ sırasıyla elenir: en
//    KISA olanlar önce rota alır (aynı bütçeyle en çok kenar düzelir),
//    en uzunlar eski tek eğriye düşer. Sıra (açıklık, caller, callee)
//    adı → deterministik.

export interface LayoutNode { service: string; spanCount: number }
export interface LayoutEdge { caller: string; callee: string }
export interface LayoutBand {
  /** namespace anahtarı; null = grupsuz şerit */
  key: string | null;
  y0: number;
  y1: number;
  /** şeridin en kalabalık sütunundaki satır sayısı (v0.10.1269: sanal
   *  noktalar — uzun kenarların ara yuvaları — dahil) */
  rows: number;
  /** şeritteki TÜM düğümler (bağımlılıklar dahil); başlık servis sayısı
   *  için lib/topoBands bandServiceCounts kullanılır. */
  members: number;
}
export interface LayoutResult {
  pos: Map<string, { x: number; y: number }>;
  lw: number;
  lh: number;
  /** düğüm → sütun (katman) */
  layer: Map<string, number>;
  /** yukarıdan aşağı, çakışmasız */
  bands: LayoutBand[];
  /** döngü kırmak için ters sayılan kenarlar, "caller|callee" */
  backEdges: Set<string>;
  /** v0.10.1269 — >1 sütun atlayan ileri kenarın ara noktaları (her ara
   *  sütunda bir tane, soldan sağa), "caller|callee". Kısa kenar, geri
   *  kenar ve tavan dışı kalan kenar burada YOK (tek eğri). */
  routes: Map<string, { x: number; y: number }[]>;
}
export interface LayoutOptions {
  bandOf?: (service: string) => string | undefined;
  firstBand?: string;
  /** v0.10.1265 — odak düğümü: şeridi EN ÜSTTE (grupsuzsa grupsuz şerit
   *  öne alınır), kendi sütun hücresinde İLK satırda. */
  pin?: string;
  /** v0.10.1269 — toplam sanal nokta tavanı (varsayılan dummyCap(N)). */
  maxDummies?: number;
}

export const MAX_LAYERS = 40;
export const BAND_PAD_TOP = 26;    // şerit başlığı (.topo-nshdr) payı
export const BAND_PAD_BOTTOM = 8;
export const UNGROUPED_PAD = 6;    // başlıksız grupsuz şerit (adlı şeritler varken)
export const BAND_GAP = 14;
const PAD_X = 110;
const COL_MIN_W = 180;
const STRETCH_MAX = 1.6;           // boş alan varsa satır aralığı en çok ×1.6 açılır

/** v0.10.1269 — varsayılan sanal nokta tavanı: min(3000, max(200, 4×N)). */
export const DUMMY_CAP_ABS = 3000;
export const dummyCap = (n: number) => Math.min(DUMMY_CAP_ABS, Math.max(200, 4 * n));

const cmpStr = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0);

export function bfsBarycenterLayout(
  nodes: LayoutNode[], edges: LayoutEdge[], width: number, height: number, rowPitch: number,
  opts: LayoutOptions = {},
): LayoutResult {
  // ── indeksleme (ada göre sıralı → girdi sırasından bağımsız) ─────────
  const spanOf = new Map<string, number>();
  for (const n of nodes) if (!spanOf.has(n.service)) spanOf.set(n.service, n.spanCount);
  const names = [...spanOf.keys()].sort(cmpStr);
  const N = names.length;
  const idx = new Map<string, number>();
  names.forEach((s, i) => idx.set(s, i));

  const outRaw: number[][] = Array.from({ length: N }, () => []);
  for (const e of edges) {
    const u = idx.get(e.caller), v = idx.get(e.callee);
    if (u === undefined || v === undefined || u === v) continue; // bilinmeyen uç / öz-döngü
    outRaw[u].push(v);
  }
  const out: number[][] = outRaw.map(l => {
    if (l.length < 2) return l;
    l.sort((a, b) => a - b);
    const d: number[] = [l[0]];
    for (let k = 1; k < l.length; k++) if (l[k] !== l[k - 1]) d.push(l[k]);
    return d;
  });
  const hasIn = new Uint8Array(N);
  for (let u = 0; u < N; u++) for (const v of out[u]) hasIn[v] = 1;

  // ── 1. döngü kırma: deterministik yinelemeli DFS ────────────────────
  // Başlangıç sırası: gelen kenarı olmayanlar (ad sırası), sonra kalanlar
  // hacim ↓ / ad (eski "döngüde en yüksek hacimli kök" sezgisi).
  const starts: number[] = [];
  for (let i = 0; i < N; i++) if (!hasIn[i]) starts.push(i);
  const rest: number[] = [];
  for (let i = 0; i < N; i++) if (hasIn[i]) rest.push(i);
  rest.sort((a, b) => (spanOf.get(names[b])! - spanOf.get(names[a])!) || a - b);
  starts.push(...rest);

  const state = new Uint8Array(N); // 0 beyaz, 1 gri, 2 siyah
  const isBack: Uint8Array[] = out.map(l => new Uint8Array(l.length));
  const stack: number[] = [];
  const iter: number[] = [];
  for (const s of starts) {
    if (state[s]) continue;
    state[s] = 1; stack.push(s); iter.push(0);
    while (stack.length) {
      const top = stack.length - 1;
      const u = stack[top];
      const k = iter[top];
      if (k < out[u].length) {
        iter[top] = k + 1;
        const v = out[u][k];
        if (state[v] === 0) { state[v] = 1; stack.push(v); iter.push(0); }
        else if (state[v] === 1) isBack[u][k] = 1;
      } else {
        state[u] = 2; stack.pop(); iter.pop();
      }
    }
  }
  const fOut: number[][] = Array.from({ length: N }, () => []);
  const fIn: number[][] = Array.from({ length: N }, () => []);
  const backEdges = new Set<string>();
  for (let u = 0; u < N; u++) {
    out[u].forEach((v, k) => {
      if (isBack[u][k]) { backEdges.add(`${names[u]}|${names[v]}`); return; }
      fOut[u].push(v); fIn[v].push(u);
    });
  }

  // ── 2. en uzun yol katmanı (Kahn) ────────────────────────────────────
  const layer = new Int32Array(N);
  const indeg = new Int32Array(N);
  for (let v = 0; v < N; v++) indeg[v] = fIn[v].length;
  const queue: number[] = [];
  for (let v = 0; v < N; v++) if (indeg[v] === 0) queue.push(v);
  for (let qi = 0; qi < queue.length; qi++) {
    const u = queue[qi];
    for (const v of fOut[u]) {
      const l = Math.min(MAX_LAYERS - 1, layer[u] + 1);
      if (l > layer[v]) layer[v] = l;
      if (--indeg[v] === 0) queue.push(v);
    }
  }
  let L = 1;
  for (let i = 0; i < N; i++) if (layer[i] + 1 > L) L = layer[i] + 1;

  // ── 2b. sanal noktalar (v0.10.1269) ─────────────────────────────────
  // Aday: açıklığı >1 olan ileri kenar. Açıklık ↑, sonra (u, v) indeksi
  // (ad sırası) — tavan dolunca kalan (daha uzun) kenarlar rota almaz.
  const cand: number[] = []; // [u, v, span] üçlüleri düz dizi
  for (let u = 0; u < N; u++) for (const v of fOut[u]) {
    const sp = layer[v] - layer[u];
    if (sp > 1) cand.push(u, v, sp);
  }
  const ci = Array.from({ length: cand.length / 3 }, (_, k) => k);
  ci.sort((p, q) => (cand[p * 3 + 2] - cand[q * 3 + 2]) || (cand[p * 3] - cand[q * 3]) || (cand[p * 3 + 1] - cand[q * 3 + 1]));
  const cap = Math.max(0, opts.maxDummies ?? dummyCap(N));
  const routed: number[] = []; // aday indeksleri, dummy sırası
  let D = 0;
  for (const k of ci) {
    const need = cand[k * 3 + 2] - 1;
    if (D + need > cap) break; // sıralı: sonrakiler de sığmaz
    routed.push(k); D += need;
  }
  routed.sort((p, q) => p - q); // (u, v) sırası → dummy indeksleri deterministik
  const V = N + D;
  // süpürme komşuluğu: gerçek düğümler fIn/fOut kopyası, rotalı kenarda
  // uç komşu yerine zincirin ilk/son sanal noktası.
  const sOut: number[][] = new Array(V);
  const sIn: number[][] = new Array(V);
  for (let i = 0; i < N; i++) { sOut[i] = fOut[i].slice(); sIn[i] = fIn[i].slice(); }
  const vLayer = new Int32Array(V);
  vLayer.set(layer);
  const ownerOf = new Int32Array(V); // sanal nokta → kaynak düğüm (şerit)
  const chains: { u: number; v: number; first: number }[] = [];
  let next = N;
  for (const k of routed) {
    const u = cand[k * 3], v = cand[k * 3 + 1], sp = cand[k * 3 + 2];
    const first = next;
    for (let s = 1; s < sp; s++) {
      const d = next++;
      vLayer[d] = layer[u] + s;
      ownerOf[d] = u;
      sIn[d] = [s === 1 ? u : d - 1];
      sOut[d] = [s === sp - 1 ? v : d + 1];
    }
    sOut[u][sOut[u].indexOf(v)] = first;
    sIn[v][sIn[v].indexOf(u)] = next - 1;
    chains.push({ u, v, first });
  }

  // ── 3. şeritler ──────────────────────────────────────────────────────
  const keyOf: (string | null)[] = names.map(s => opts.bandOf?.(s) ?? null);
  const named = new Map<string, number>(); // ad → üye sayısı
  for (const k of keyOf) if (k !== null) named.set(k, (named.get(k) ?? 0) + 1);
  const order: (string | null)[] = [];
  if (named.size > 0) {
    // şeritler arası kenar sayısı (yönsüz, geri kenarlar dahil)
    const link = new Map<string, Map<string, number>>();
    const bump = (a: string, b: string) => {
      let m = link.get(a);
      if (!m) { m = new Map(); link.set(a, m); }
      m.set(b, (m.get(b) ?? 0) + 1);
    };
    for (let u = 0; u < N; u++) for (const v of out[u]) {
      const a = keyOf[u], b = keyOf[v];
      if (a !== null && b !== null && a !== b) { bump(a, b); bump(b, a); }
    }
    const left = new Set(named.keys());
    const byWeight = (a: string, b: string) => (named.get(b)! - named.get(a)!) || cmpStr(a, b);
    let first = opts.firstBand && left.has(opts.firstBand) ? opts.firstBand : [...left].sort(byWeight)[0];
    const conn = new Map<string, number>();
    while (left.size) {
      order.push(first); left.delete(first);
      for (const [b, w] of link.get(first) ?? []) conn.set(b, (conn.get(b) ?? 0) + w);
      let best: string | null = null;
      for (const b of left) {
        if (best === null) { best = b; continue; }
        const d = (conn.get(b) ?? 0) - (conn.get(best) ?? 0);
        if (d > 0 || (d === 0 && byWeight(b, best) < 0)) best = b;
      }
      if (best === null) break;
      first = best;
    }
  }
  const hasUngrouped = keyOf.some(k => k === null);
  const pinIdx = opts.pin !== undefined ? names.indexOf(opts.pin) : -1;
  // v0.10.1265 — odak grupsuzsa grupsuz şerit EN ÜSTE (operatör: "orijinal
  // servis en altta çıkıyor").
  if (pinIdx >= 0 && keyOf[pinIdx] === null) order.unshift(null);
  else if (hasUngrouped || order.length === 0) order.push(null);
  const B = order.length;
  const bandIdx = new Map<string | null, number>();
  order.forEach((k, i) => bandIdx.set(k, i));
  const bandOfNode = new Int32Array(V);
  for (let i = 0; i < N; i++) bandOfNode[i] = bandIdx.get(keyOf[i])!;
  for (let d = N; d < V; d++) bandOfNode[d] = bandOfNode[ownerOf[d]]; // kaynağın şeridi

  // hücreler: cells[b][c] — başlangıç sırası ad (indeks artan), sanal
  // noktalar gerçeklerden sonra. Satır sayısı sanal satırları da sayar.
  const cells: number[][][] = Array.from({ length: B }, () => Array.from({ length: L }, () => []));
  for (let i = 0; i < V; i++) cells[bandOfNode[i]][vLayer[i]].push(i);
  const rows = cells.map(bc => Math.max(1, ...bc.map(c => c.length)));
  const members = new Array<number>(B).fill(0);
  for (let i = 0; i < N; i++) members[bandOfNode[i]]++;

  const anyNamed = named.size > 0;
  const padTop = (b: number) => (!anyNamed ? 0 : order[b] === null ? UNGROUPED_PAD : BAND_PAD_TOP);
  const padBot = (b: number) => (!anyNamed ? 0 : order[b] === null ? UNGROUPED_PAD : BAND_PAD_BOTTOM);
  let fixed = 0, totalRows = 0;
  for (let b = 0; b < B; b++) { fixed += padTop(b) + padBot(b); totalRows += rows[b]; }
  fixed += BAND_GAP * (B - 1);
  let pitch = rowPitch;
  if (fixed + totalRows * pitch < height) {
    pitch = Math.min(rowPitch * STRETCH_MAX, (height - fixed) / totalRows);
  }
  const total = fixed + totalRows * pitch;
  const lh = Math.max(height, total);
  let cursor = (lh - total) / 2;
  const bands: LayoutBand[] = [];
  const rowTop = new Float64Array(B);
  for (let b = 0; b < B; b++) {
    const y0 = cursor;
    rowTop[b] = y0 + padTop(b);
    const y1 = rowTop[b] + rows[b] * pitch + padBot(b);
    bands.push({ key: order[b], y0, y1, rows: rows[b], members: members[b] });
    cursor = y1 + BAND_GAP;
  }

  // ── 4. sütun içi barycenter (şerit hücresi içinde) ──────────────────
  const y = new Float64Array(V);
  const place = (b: number, list: number[]) => {
    const off = (rows[b] - list.length) / 2;
    list.forEach((i, k) => { y[i] = rowTop[b] + (off + k + 0.5) * pitch; });
  };
  const pinFront = (list: number[]) => {
    const k = pinIdx >= 0 ? list.indexOf(pinIdx) : -1;
    if (k > 0) { list.splice(k, 1); list.unshift(pinIdx); }
  };
  for (let b = 0; b < B; b++) for (let c = 0; c < L; c++) { pinFront(cells[b][c]); place(b, cells[b][c]); }

  const bary = new Float64Array(V);
  const sweep = (c: number, nb: number[][]) => {
    for (let b = 0; b < B; b++) {
      const list = cells[b][c];
      if (list.length < 2) continue;
      for (const i of list) {
        const ns = nb[i];
        if (ns.length === 0) { bary[i] = y[i]; continue; }
        let s = 0;
        for (const j of ns) s += y[j];
        bary[i] = s / ns.length;
      }
      list.sort((p, q) => (bary[p] - bary[q]) || (y[p] - y[q]) || p - q);
      pinFront(list);
      place(b, list);
    }
  };
  for (let c = 1; c < L; c++) sweep(c, sIn);
  for (let c = L - 2; c >= 0; c--) sweep(c, sOut);
  for (let c = 1; c < L; c++) sweep(c, sIn);

  // ── 5. x ve çıktı ────────────────────────────────────────────────────
  const lw = Math.max(width, PAD_X * 2 + (L - 1) * COL_MIN_W);
  const colW = L > 1 ? (lw - PAD_X * 2) / (L - 1) : 0;
  const xOf = (l: number) => (L === 1 ? lw / 2 : PAD_X + l * colW);
  const pos = new Map<string, { x: number; y: number }>();
  const layerMap = new Map<string, number>();
  for (let i = 0; i < N; i++) {
    pos.set(names[i], { x: xOf(layer[i]), y: y[i] });
    layerMap.set(names[i], layer[i]);
  }
  const routes = new Map<string, { x: number; y: number }[]>();
  for (const { u, v, first } of chains) {
    const pts: { x: number; y: number }[] = [];
    for (let d = first, n = layer[v] - layer[u] - 1; n > 0; d++, n--) pts.push({ x: xOf(vLayer[d]), y: y[d] });
    routes.set(`${names[u]}|${names[v]}`, pts);
  }
  return { pos, lw, lh, layer: layerMap, bands, backEdges, routes };
}
