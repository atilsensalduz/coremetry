import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1347 — operatör (prod /trace ekran görüntüsü): "Üstteki ms cinsinden
// süre ve tarih sağa dayalı olabilir." Özet şeridinde kimlik/durum (servis
// rozeti · trace id + kopyala · ERROR · hatalı span sayısı) SOLDA kalır;
// ölçüler (⏱ süre · span/servis sayısı · başlangıç) margin-left:auto
// kümesinde (.trace-summary__measures) SAĞA dayalı. ≤640px'te küme sol
// kümenin altına, sola dayalı iner. Kiosk ve public trace başlığı aynı kümeyi
// kullanır. Pin: ölçüler kümenin İÇİNDE, kimlik öğeleri kümeden ÖNCE.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');
const trace = read('Trace.tsx');
const kiosk = read('TraceKiosk.tsx');
const pub = read('PublicTrace.tsx');
const css = read('../styles/globals.css');

const OPEN = '<span className="trace-summary__measures">';

/** Ölçü kümesinin [başlangıç, bitiş) aralığı — iç içe <span> sayılarak. */
function clusterRange(src: string): [number, number] {
  const start = src.indexOf(OPEN);
  expect(start, 'ölçü kümesi yok').toBeGreaterThan(-1);
  expect(src.indexOf(OPEN, start + 1), 'tek küme olmalı').toBe(-1);
  const re = /<span\b|<\/span>/g;
  re.lastIndex = start;
  let depth = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src)) !== null) {
    if (m[0] === '</span>') {
      depth--;
      if (depth === 0) return [start, m.index];
    } else if (!src.slice(m.index, src.indexOf('>', m.index) + 1).endsWith('/>')) {
      depth++;
    }
  }
  throw new Error('ölçü kümesi kapanmıyor');
}

function expectInside(src: string, needle: string, [a, b]: [number, number]) {
  const i = src.indexOf(needle, a);
  expect(i, `${needle} kümede değil`).toBeGreaterThan(a);
  expect(i, `${needle} kümede değil`).toBeLessThan(b);
}

function expectBefore(src: string, needle: string, [a]: [number, number]) {
  const i = src.indexOf(needle);
  expect(i, `${needle} yok`).toBeGreaterThan(-1);
  expect(i, `${needle} ölçü kümesinden önce (solda) olmalı`).toBeLessThan(a);
}

describe('trace başlığı: ölçüler sağa dayalı kümede (v0.10.1347)', () => {
  // v0.10.1514 (trace başlığı A, operatör onayı 2026-10-24) — /trace v0.10.1347
  // kümesinden iki satırlı karta geçti: satır 1 solda kimlik (breadcrumb ·
  // başlık · rozet · id + kopyala · ERROR · hatalı span), SAĞDA TraceMeasures
  // (SÜRE · KRİTİK YOL · SPAN · BAŞLANGIÇ); satır 2'de kimlik çipleri, sağda
  // operasyon kartı + CoSRE'ye sor. Sıra ve yerleşim burada çivili.
  // v0.10.1522 (operatör, prod: "üstteki bar çok büyük… Ask CoSRE solda, Log
  // İzleme sağda") — başlık + meta tek satır (.trace-hdr__headline); satır 2'de
  // çiplerin altında TEK eylem satırı: CoSRE → kart → gateway → dış linkler.
  it('/trace — satır 1: kimlik solda, TraceMeasures sağda; satır 2: çipler, sonra eylem satırı', () => {
    const row1 = trace.indexOf('<div className="trace-hdr__row1">');
    const ident = trace.indexOf('<div className="trace-hdr__ident">');
    const meas = trace.indexOf('<TraceMeasures totalNs={totalNs}');
    const row2 = trace.indexOf('<div className="trace-hdr__row2">');
    expect(row1).toBeGreaterThan(-1);
    expect(ident).toBeGreaterThan(row1);
    expect(meas).toBeGreaterThan(ident);
    expect(row2).toBeGreaterThan(meas);
    expect(trace.indexOf('<TraceMeasures', meas + 1), 'tek küme olmalı').toBe(-1);
    // Kimlik öğeleri ölçülerden ÖNCE (solda), sırasıyla.
    const order = ['<nav className="crumbs trace-hdr__crumbs"', '<h2 className="trace-hdr__title"',
      '<SvcBadge name={root.serviceName} />', '<CopyButton value={id}',
      '<span className="badge b-err">ERROR</span>', 'error span{errSpans'].map(n => trace.indexOf(n, ident));
    for (const i of order) { expect(i).toBeGreaterThan(ident); expect(i).toBeLessThan(meas); }
    for (let k = 1; k < order.length; k++) expect(order[k]).toBeGreaterThan(order[k - 1]);
    // Başlık ve meta (rozet · id · durum) aynı satırda: ikisi de headline içinde.
    const headline = trace.indexOf('<div className="trace-hdr__headline">', ident);
    expect(headline).toBeGreaterThan(order[0]);
    expect(headline).toBeLessThan(order[1]);
    expect(trace.indexOf('<div className="trace-hdr__meta">', headline)).toBeLessThan(order[2]);
    // Başlık: ellipsis + tam ad title'da.
    expect(trace).toContain('<h2 className="trace-hdr__title" title={root ? displaySpanName(root) : id}>');
    // Ölçüler sayfanın elindeki veriden: kritik yol kök duvar süresi, span/servis sayısı.
    const m = trace.slice(meas, trace.indexOf('/>', meas));
    for (const n of ['criticalPath.rootWallNs', 'criticalPath?.spanCount', 'spanCount={spans.length}', 'svcCount={svcCount}', 'startNs={root?.startTime}']) {
      expect(m).toContain(n);
    }
    // Satır 2: çip listesi → eylem satırı (solda CoSRE, kart çipi, gateway;
    // sağda dış linkler).
    const ops = trace.indexOf('<div className="trace-summary__ops" role="list"');
    const act = trace.indexOf('<div className="trace-hdr__actrow" data-testid="trace-hdr-actions">');
    const ask = trace.indexOf('<div className="trace-hdr__ask">');
    expect(ops).toBeGreaterThan(row2);
    expect(act).toBeGreaterThan(ops);
    expect(ask).toBeGreaterThan(act);
    const card = trace.indexOf('data-testid="trace-opcard-chip"');
    const gw = trace.indexOf('data-testid="trace-opcard-gateway"');
    const cosre = trace.indexOf("<AIExplainButton subject={{ kind: 'trace', id }} emphasis=\"strong\"");
    expect(cosre).toBeGreaterThan(ask);
    expect(card).toBeGreaterThan(cosre);
    expect(gw).toBeGreaterThan(card);
    expect(trace.indexOf('<AIExplainButton', cosre + 1), 'tek CoSRE düğmesi').toBe(-1);
    // Eski 1514 yerleşimi (CoSRE sağ kümenin sonunda) geri gelmez.
    expect(trace).not.toMatch(/trace-opcard-gateway[\s\S]{0,800}<AIExplainButton subject=\{\{ kind: 'trace'/);
    // Compare / Logs / Metrics / Share gri alanda (kartın DIŞINDA, önünde) kalır.
    const actions = trace.indexOf('<span className="crumbs-actions">{traceActions}</span>');
    expect(actions).toBeGreaterThan(-1);
    expect(actions).toBeLessThan(trace.indexOf('<section className="trace-summary trace-hdr"'));
    // Eski tek satır şerit ve gri alandaki yinelenen kritik yol metni geri gelmez.
    expect(trace).not.toContain('<span className="trace-summary__measures">');
    expect(trace).not.toContain('Critical path · {criticalPath.spanCount}');
    expect(trace).not.toContain("style={{ color: 'var(--text2)', fontSize: 12 }}>{spans.length} spans");
  });

  it('/trace eylem satırı — CoSRE (AIExplainButton) dış link düğmesinden ÖNCE; dış linkler kartın İÇİNDE, altta ayrı satır yok', () => {
    const sec = trace.indexOf('<section className="trace-summary trace-hdr"');
    const secEnd = trace.indexOf('</section>', sec);
    const act = trace.indexOf('<div className="trace-hdr__actrow" data-testid="trace-hdr-actions">');
    expect(act).toBeGreaterThan(sec);
    const cosre = trace.indexOf('<AIExplainButton', act);
    const ext = trace.indexOf('<div className="trace-extlinks">', act);
    const btns = trace.indexOf('<ExternalLinkButtons spans={spans ?? []} traceId={id} selectedSpanId={selectedId} />', act);
    expect(cosre, 'CoSRE eylem satırında').toBeGreaterThan(act);
    expect(ext, 'dış linkler CoSRE\'den sonra (sağda)').toBeGreaterThan(cosre);
    expect(btns).toBeGreaterThan(ext);
    expect(btns, 'dış linkler başlık kartının içinde').toBeLessThan(secEnd);
    // Tek çağrı yeri: kartın altındaki ayrı .trace-extlinks satırı kalktı.
    expect(trace.split('<ExternalLinkButtons ').length - 1).toBe(1);
    expect(trace.split('<div className="trace-extlinks">').length - 1).toBe(1);
  });

  it('/trace ölçü bileşeni — SÜRE · KRİTİK YOL · SPAN · BAŞLANGIÇ bu sırayla, i18n etiketli', () => {
    const src = read('../components/traces/TraceMeasures.tsx');
    const keys = ['trace.hdr.duration', 'trace.hdr.critical', 'trace.hdr.spans', 'trace.hdr.start'].map(k => src.indexOf(`tr('${k}')`));
    for (const i of keys) expect(i).toBeGreaterThan(-1);
    for (let k = 1; k < keys.length; k++) expect(keys[k]).toBeGreaterThan(keys[k - 1]);
    expect(src).toContain('<dl className="trace-measures"');
    expect(src).not.toMatch(/useQuery|api\./); // ek istek yok
  });

  it('CSS — trace başlığı bloğu: ≤1280px ölçüler alta, ≤640px çipler sarar, yana kayma yok', () => {
    const b = css.indexOf('v0.10.1514 BEGIN — trace başlığı');
    expect(b, 'işaretli blok yok').toBeGreaterThan(-1);
    const block = css.slice(b, css.indexOf('v0.10.1514 END', b));
    const m1280 = /@media \(max-width: 1280px\) \{([\s\S]*?)\n\}/.exec(block);
    expect(m1280, '1280px katmanı yok').toBeTruthy();
    expect(m1280![1]).toMatch(/\.trace-hdr__row1 \{[^}]*flex-direction: column/);
    expect(m1280![1]).toMatch(/\.trace-measures \{[^}]*justify-content: flex-start/);
    const m640 = /@media \(max-width: 640px\) \{([\s\S]*?)\n\}/.exec(block);
    expect(m640, '640px katmanı yok').toBeTruthy();
    expect(m640![1]).toMatch(/\.trace-hdr__row2 > \.trace-summary__ops \{[^}]*flex-basis: 100%/);
    // Taşmayan çip: kök max-width 100%, değer ellipsis; id kelime ortasından kırılır.
    expect(block).toMatch(/\.idchip \{[^}]*max-width: 100%/);
    expect(block).toMatch(/\.idchip__val \{[^}]*text-overflow: ellipsis/);
    expect(block).toMatch(/\.trace-hdr__id \{[^}]*overflow-wrap: anywhere/);
    expect(block).toMatch(/\.trace-hdr__title \{[^}]*text-overflow: ellipsis/);
    expect(block).toContain('.trace-extlinks:empty { display: none; }');
    // v0.10.1522 — sıkı kart: başlık --fs-lg, ölçü sayısı --fs-xl (≈18px, eski
    // 1.2×xl değil), etiket --fs-3xs, kimlik çipi 26px; eylem satırında dış
    // linkler sağa dayalı, ≤640px'te sola iner.
    expect(block).toMatch(/\.trace-hdr__title \{[^}]*font-size: var\(--fs-lg\)/);
    expect(block).toMatch(/\.trace-measures dd \{[^}]*font-size: var\(--fs-xl\)/);
    expect(block).not.toContain('calc(var(--fs-xl) * 1.2)');
    expect(block).toMatch(/\.trace-measures dt \{[^}]*font-size: var\(--fs-3xs\)/);
    expect(block).toMatch(/\.idchip \{[^}]*min-height: calc\(var\(--hit-min\) \+ var\(--sp-1\)\)/);
    expect(block).toMatch(/\.trace-hdr__headline \{[^}]*display: flex[^}]*flex-wrap: wrap/);
    expect(block).toMatch(/\.trace-hdr__actrow \{[^}]*display: flex[^}]*flex-wrap: wrap/);
    expect(block).toMatch(/\.trace-hdr__actrow > \.trace-extlinks \{[^}]*margin-left: auto/);
    expect(m640![1]).toMatch(/\.trace-hdr__actrow > \.trace-extlinks \{[^}]*margin-left: 0/);
    // Yalnız token: blokta hex/rgb renk yok.
    expect(block).not.toMatch(/#[0-9a-fA-F]{3,8}\b|rgba?\(/);
  });

  it('kiosk — aynı küme; id ve kaynak rozetleri solda', () => {
    const r = clusterRange(kiosk);
    for (const n of ['<SvcBadge name={root.serviceName} />', '<span className="badge b-err">ERROR</span>',
      'error span{errSpans', '<code className="trace-kiosk__id">', "bundle.source === 'tempo'", 'bundle.spanCapped']) {
      expectBefore(kiosk, n, r);
    }
    for (const n of ['className="trace-summary__dur"', 'spans · {svcCount}', '{tsLong(root.startTime)}']) {
      expectInside(kiosk, n, r);
    }
  });

  it('public trace — aynı küme; id ve durum solda', () => {
    const r = clusterRange(pub);
    for (const n of ['{data.traceId}', '<span className="badge b-err">ERROR</span>']) expectBefore(pub, n, r);
    for (const n of ['className="trace-summary__dur"', '{data.spans.length} spans', '{tsLong(root.startTime)}']) {
      expectInside(pub, n, r);
    }
  });

  it('CSS — küme margin-left:auto; ≤640px\'te sol kümenin altına, sola dayalı', () => {
    const i = css.indexOf('.trace-summary__measures {');
    expect(i).toBeGreaterThan(-1);
    const rule = css.slice(i, css.indexOf('}', i));
    expect(rule).toContain('margin-left: auto');
    const b = css.indexOf('v0.10.1347 BEGIN');
    const block = css.slice(b, css.indexOf('v0.10.1347 END', b));
    const mq = /@media \(max-width: 640px\) \{\s*\.trace-summary__measures \{([^}]*)\}/.exec(block);
    expect(mq, '640px katmanı yok').toBeTruthy();
    expect(mq![1]).toContain('margin-left: 0');
    expect(mq![1]).toContain('flex-basis: 100%');
    expect(block).toContain('.trace-summary__ops:empty { display: none; }');
  });
});
