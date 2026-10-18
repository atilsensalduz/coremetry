// filterBarSearch.pin — v0.10.1332 (operatör onaylı mockup "Filtre barı ·
// Search son filtrenin arkasında").
//
// Ne çiviliyor (kaynak): üç liste sayfasının filtre barında
//   · Search SON filtrenin hemen arkasında, md boy (size="sm" yok — üç barda
//     tek Search boyu) ve yanında "Enter ↵" ipucu (`EnterHint`);
//   · Services: Search Δ prior'dan SONRA, Reset'ten ÖNCE; Reset sağda
//     (`pc-push`); Search sarmalı `data-pc-pin` (telefonda panele inmez —
//     mekanizma pageControlsCollapse.test'te mount ile çivili);
//   · Traces: trace-id kutusunda "Go" düğmesi YOK (Enter + ✕ kaldı); filtre
//     barında Search Root'tan sonra, boşluk + görünüm anahtarından önce;
//   · Logs: Reset / ↻ / Copy link / Live tail Search'ten sonraki sağ kümede
//     (`.pc-right`); Live'ın satır-içi marginLeft'i kalktı.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const read = (rel: string) => readFileSync(resolve(__dirname, '..', rel), 'utf8');
const stripComments = (s: string) =>
  s.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '');

// İlk `<PageControls` açılışından kapanışına kadar.
function bar(src: string): string {
  const a = src.indexOf('<PageControls');
  expect(a, 'PageControls bulunamadı').toBeGreaterThanOrEqual(0);
  const b = src.indexOf('</PageControls>', a);
  expect(b).toBeGreaterThan(a);
  return src.slice(a, b);
}

// `<Button …>Search</Button>` açılış etiketi (tek primary).
function searchTag(seg: string): string {
  const m = /<Button\b(?:=>|[^>])*?variant="primary"(?:=>|[^>])*?>Search<\/Button>/.exec(seg);
  expect(m, 'Search primary bulunamadı').not.toBeNull();
  return m![0];
}

function at(seg: string, needle: string): number {
  const i = seg.indexOf(needle);
  expect(i, `"${needle}" bulunamadı`).toBeGreaterThanOrEqual(0);
  return i;
}

describe('v0.10.1332 — Services filtre barı', () => {
  const seg = bar(stripComments(read('pages/Services.tsx')));

  it('sıra: picker → Min spans → Min P99 → selectler → Errors only → Δ prior → Search → Reset', () => {
    const order = [
      'placeholder="Filter services…"', 'placeholder="Min spans"', 'placeholder="Min P99 (ms)"',
      'All owner teams', 'All SRE teams', 'All clusters', 'All namespaces',
      'Errors only', 'Δ prior', '>Search</Button>', '>Reset</Button>',
    ].map(n => at(seg, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
  });

  it('Search md (size="sm" yok), Enter ipucu yanında, sarmal data-pc-pin', () => {
    expect(searchTag(seg)).not.toMatch(/size="sm"/);
    expect(seg).toMatch(/<span className="search-go" data-pc-pin>\s*<Button variant="primary"(?:=>|[^>])*?>Search<\/Button>\s*<EnterHint /);
  });

  it('Reset sağda (pc-push), sarmalın DIŞINDA', () => {
    expect(seg).toMatch(/<Button variant="secondary" className="pc-push" onClick=\{reset\}>Reset<\/Button>/);
    expect(seg.indexOf('>Reset</Button>')).toBeGreaterThan(seg.indexOf('</span>', at(seg, 'search-go')));
  });

  it('Enter iki sayı kutusunda da apply — ipucu dürüst', () => {
    expect((seg.match(/onKeyDown=\{e => \{ if \(e\.key === 'Enter'\) apply\(\); \}\}/g) ?? []).length).toBe(2);
    expect(seg).toMatch(/onEnter=\{apply\}/);
  });
});

describe('v0.10.1332 — Traces', () => {
  const src = stripComments(read('pages/Traces.tsx'));

  it('trace-lookup: Go düğmesi yok, Enter + ✕ temizle + Enter ipucu var', () => {
    const a = at(src, '<div className="trace-lookup">');
    const box = src.slice(a, src.indexOf('</div>', a));
    expect(box).not.toMatch(/>Go<\/Button>/);
    expect(box).not.toMatch(/<Button\b/);
    expect(box).toMatch(/onKeyDown=\{e => e\.key === 'Enter' && apply\(\)\}/);
    expect(box).toMatch(/aria-label="Trace ID'yi temizle"/);
    expect(box).toMatch(/<EnterHint /);
  });

  it('filtre barı: Search md + ipucu, Root\'tan SONRA, boşluk + görünüm anahtarından ÖNCE', () => {
    const seg = bar(src);
    expect(searchTag(seg)).not.toMatch(/size="sm"/);
    expect(seg).toMatch(/<span className="search-go">\s*<Button variant="primary"(?:=>|[^>])*?>Search<\/Button>\s*<EnterHint /);
    const iRoot = at(seg, '>Root</span>');
    const iSearch = at(seg, '>Search</Button>');
    const iSpacer = at(seg, "<span style={{ marginLeft: 'auto' }} />");
    const iSeg = at(seg, 'aria-label="Trace görünümü"');
    expect(iRoot).toBeLessThan(iSearch);
    expect(iSearch).toBeLessThan(iSpacer);
    expect(iSpacer).toBeLessThan(iSeg);
  });
});

describe('v0.10.1332 — Logs filtre barı', () => {
  const seg = bar(stripComments(read('pages/Logs.tsx')));

  it('Search md + ipucu, son filtreden (◆ With trace) SONRA', () => {
    expect(searchTag(seg)).not.toMatch(/size="sm"/);
    expect(seg).toMatch(/<span className="search-go">\s*<Button variant="primary"(?:=>|[^>])*?>Search<\/Button>\s*<EnterHint /);
    expect(at(seg, '◆ With trace')).toBeLessThan(at(seg, '>Search</Button>'));
  });

  it('Reset · ↻ · Copy link · Live tail sağ kümede (.pc-right), Search\'ten SONRA', () => {
    const a = at(seg, '<span className="pc-right">');
    expect(a).toBeGreaterThan(at(seg, '>Search</Button>'));
    // Sağ kümenin kapanışı: barın son `</span>`ı.
    const group = seg.slice(a, seg.lastIndexOf('</span>'));
    const order = ['>Reset</Button>', 'aria-label="Yenile"', 'label="Copy link"', "'▶ Live tail'"]
      .map(n => at(group, n));
    expect(order).toEqual([...order].sort((x, y) => x - y));
    // Kümenin dışında (önünde) bu eylemler kalmadı.
    const before = seg.slice(0, a);
    for (const n of ['>Reset</Button>', 'aria-label="Yenile"', '<ShareButton', 'Live tail']) {
      expect(before, n).not.toContain(n);
    }
  });

  it('sağa itme tek yerde: barda satır-içi marginLeft: auto yok', () => {
    expect(seg).not.toMatch(/marginLeft:\s*'auto'/);
  });
});

describe('v0.10.1332 — CSS', () => {
  const css = read('styles/globals.css');
  const block = css.slice(css.indexOf('v0.10.1332 BEGIN'), css.indexOf('v0.10.1332 END'));

  it('işaretli tek blok, .kbd token\'larla (10px mono, 1px kenar, 2px alt kenar)', () => {
    expect(block.length).toBeGreaterThan(0);
    const kbd = /\.kbd \{([^}]*)\}/.exec(block)?.[1] ?? '';
    expect(kbd).toMatch(/font-family: var\(--font-mono\)/);
    expect(kbd).toMatch(/font-size: var\(--fs-2xs\)/);
    expect(kbd).toMatch(/border: 1px solid var\(--border-strong\)/);
    expect(kbd).toMatch(/border-bottom-width: 2px/);
    expect(kbd).not.toMatch(/#[0-9a-f]{3,8}\b/i);
  });

  it('.pc-right sağa itiyor; telefonda panelde sıfırlanıyor', () => {
    expect(block).toMatch(/\.pc-right \{[^}]*margin-left: auto/);
    expect(block).toMatch(/\.pc-push \{ margin-left: auto; \}/);
    expect(block).toMatch(/@media \(max-width: 640px\) \{[\s\S]*\.pc-panel \.pc-right,\s*\.pc-panel \.pc-push \{ margin-left: 0; \}/);
  });
});
