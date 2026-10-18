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
  it('/trace — kimlik solda, süre · sayılar · başlangıç sağdaki kümede (bu sırayla)', () => {
    const r = clusterRange(trace);
    for (const n of ['<SvcBadge name={root.serviceName} />', '<CopyButton value={id}',
      '<span className="badge b-err">ERROR</span>', 'error span{errSpans']) {
      expectBefore(trace, n, r);
    }
    const dur = trace.indexOf('className="trace-summary__dur"', r[0]);
    const counts = trace.indexOf('spans · {svcCount}', r[0]);
    const start = trace.indexOf('{tsLong(root.startTime)}', r[0]);
    for (const [n, i] of [['süre', dur], ['sayılar', counts], ['başlangıç', start]] as const) {
      expect(i, `${n} kümede değil`).toBeGreaterThan(r[0]);
      expect(i, `${n} kümede değil`).toBeLessThan(r[1]);
    }
    expect(dur).toBeLessThan(counts);
    expect(counts).toBeLessThan(start);
    // Oracle operasyon satırı kümeden SONRA, kendi tam genişlik satırında.
    const ops = trace.indexOf('<div className="trace-summary__ops">');
    expect(ops).toBeGreaterThan(r[1]);
    // Taşınan iki öğe artık inline style değil, sınıf.
    expect(trace).not.toContain("style={{ color: 'var(--text2)', fontSize: 12 }}>{spans.length} spans");
    expect(trace).not.toContain("fontVariantNumeric: 'tabular-nums' }} title=\"Trace başlangıcı");
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
