// @vitest-environment jsdom
//
// tracesDurationBold — v0.10.1351 (operatör: "Traces sayfasında trace süresi
// bold olabilir.").
//
// NE ÇİVİLİYOR:
//   • /traces liste görünümünün Duration hücresi (DurationBar) değeri tablo
//     standardının vurgu sınıfıyla (.cell-strong = 600) + tabular-nums
//     (.trace-dur-val) çizer; renk nötr --text kalır (K5 / v0.10.922), gecikme
//     çubuğu (.ov-minibar) yanında durur.
//   • Yeni bir punto/ağırlık icat edilmez: .trace-dur-val font-size ve
//     font-weight yazmaz (T4 tek yazı boyu; ağırlık tek kaynaktan).
//   • Toplu görünümün yüzdelik hücreleri (avg/p50/p95/p99) kalınlaşmaz —
//     istek trace başına süre içindi.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { DurationBar } from '@/components/traces/shared';

const CSS = readFileSync(resolve(__dirname, '../styles/globals.css'), 'utf8');
const TRACES = readFileSync(resolve(__dirname, 'Traces.tsx'), 'utf8');

let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null; host = null;
});

function render(ms: number, err = false) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(<DurationBar ms={ms} err={err} max={2000} />));
  return host;
}

describe('Traces Duration hücresi kalın (v0.10.1351)', () => {
  it.each([[0.23, '0.23ms'], [471.02, '471.02ms'], [1150, '1.15s']])(
    '%s ms → "%s" vurgu sınıfıyla, nötr renkte, çubuk yanında',
    (ms, label) => {
      const el = render(ms);
      const val = [...el.querySelectorAll('span')].find(s => s.textContent === label);
      expect(val, `${label} değeri çizilmeli`).toBeDefined();
      expect(val!.classList.contains('cell-strong')).toBe(true);
      expect(val!.classList.contains('trace-dur-val')).toBe(true);
      expect(val!.style.color).toBe('var(--text)');
      expect(el.querySelector('.ov-minibar > i')).not.toBeNull();
    },
  );

  it('hatalı trace de aynı nötr kalın değeri taşır (renk çubukta)', () => {
    const el = render(300, true);
    const val = [...el.querySelectorAll('span')].find(s => s.textContent === '300.00ms')!;
    expect(val.classList.contains('cell-strong')).toBe(true);
    expect(val.style.color).toBe('var(--text)');
    expect((el.querySelector('.ov-minibar > i') as HTMLElement).style.background).toBe('var(--err)');
  });

  it('CSS: .cell-strong 600, .trace-dur-val yalnız tabular-nums', () => {
    expect(CSS).toMatch(/\n\.cell-strong \{ font-weight: 600; \}/);
    const i = CSS.indexOf('.trace-dur-val {');
    expect(i).toBeGreaterThan(-1);
    const rule = CSS.slice(i, CSS.indexOf('}', i));
    expect(rule).toContain('tabular-nums');
    expect(rule).not.toContain('font-size');
    expect(rule).not.toContain('font-weight');
  });

  it("liste hücresi DurationBar'dan; toplu yüzdelikler kalın değil", () => {
    expect(TRACES).toContain("case 'duration':  return <DurationBar ms={t.durationMs}");
    expect(TRACES).toContain('<td className="num">{fmtFixed(a.p95Ms, 1)}ms</td>');
  });
});
