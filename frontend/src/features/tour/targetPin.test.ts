// targetPin.test.ts — v0.10.1334 rehberli tur çapa pini. Bir refactor
// Services tablosundan `data-tour="services-table"`ı düşürürse tur SESSİZCE
// zaman aşımına düşer (kart "veri henüz yok" der, halka hiç çizilmez) ve
// dört kapının hiçbiri bunu görmez. Bu dosya kaynak düzeyinde pinler:
//   1. her AÇIK adımın hedef/yedek çapası TOUR_ANCHORS'ta ve sahibi dosyada
//      literal olarak duruyor;
//   2. src'de sahipsiz data-tour yok (her literal TOUR_ANCHORS'ta), dinamik
//      `data-tour={…}` yalnız izinli dosyada;
//   3. TOUR_ANCHORS'ta ölü giriş yok (her çapa sahibinde gerçekten var).
import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, resolve, relative } from 'node:path';
import { TOUR_ANCHORS, TOUR_STEPS, type TourCtx } from './steps';

const SRC = resolve(__dirname, '../..');
const rel = (p: string) => relative(SRC, p).split('\\').join('/');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir)) {
    if (e === 'node_modules') continue;
    const p = join(dir, e);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e) && !/\.test\.tsx?$/.test(e)) out.push(p);
  }
  return out;
}

// Zengin bağlam: veri var, copilot açık → yalnız 'not_yet' kapatır.
const RICH: TourCtx = {
  service: 'svc', range: null, serviceCount: 10, spans5m: 100, environments: 1, copilotEnabled: true, returnTo: '/',
};
const ENABLED = TOUR_STEPS.filter(s => s.requires(RICH) !== 'not_yet');
const anchorsOf = (sel: string | null | undefined) =>
  [...(sel ?? '').matchAll(/\[data-tour="([^"]+)"\]/g)].map(m => m[1]);
const read = (file: string) => readFileSync(join(SRC, file), 'utf8');
const hasLiteral = (src: string, id: string) =>
  src.includes(`data-tour="${id}"`) || src.includes(`'${id}'`) || src.includes(`"${id}"`);

// Dinamik `data-tour={…}` (harita üzerinden) taşıyabilen dosyalar.
const DYNAMIC_OK = new Set(['pages/Services.tsx']);

describe('tour çapaları (v0.10.1334)', () => {
  it('R1: açık adımlar karşılama + servisler', () => {
    expect(ENABLED.map(s => s.id)).toEqual(['welcome', 'services']);
  });

  it('her açık adımın hedefi [data-tour="…"] ile başlıyor ya da null', () => {
    for (const s of ENABLED) {
      if (s.target === null) continue;
      expect(s.target.startsWith('[data-tour="'), `${s.id}: ${s.target}`).toBe(true);
      if (s.fallbackTarget) expect(s.fallbackTarget.startsWith('[data-tour="'), `${s.id} yedek`).toBe(true);
    }
  });

  it('her açık adımın çapası sahibi dosyada literal olarak duruyor', () => {
    for (const s of ENABLED) {
      for (const id of [...anchorsOf(s.target), ...anchorsOf(s.fallbackTarget)]) {
        const owner = TOUR_ANCHORS[id];
        expect(owner, `${s.id}: "${id}" TOUR_ANCHORS'ta yok`).toBeTruthy();
        expect(hasLiteral(read(owner), id), `${owner} içinde data-tour="${id}" yok`).toBe(true);
      }
    }
  });

  it('TOUR_ANCHORS\'ta ölü giriş yok', () => {
    for (const [id, owner] of Object.entries(TOUR_ANCHORS)) {
      expect(hasLiteral(read(owner), id), `${owner} içinde "${id}" yok — ölü çapa`).toBe(true);
    }
  });

  it('src\'de sahipsiz data-tour yok; dinamik kullanım yalnız izinli dosyada', () => {
    const orphans: string[] = [];
    const dynamic: string[] = [];
    for (const f of walk(SRC)) {
      const src = readFileSync(f, 'utf8');
      for (const m of src.matchAll(/data-tour="([^"]+)"/g)) {
        // Seçici dizgeleri (steps.ts) de bu kalıba uyar; onlar beyan, çapa değil.
        if (rel(f) === 'features/tour/steps.ts') continue;
        if (TOUR_ANCHORS[m[1]] !== rel(f)) orphans.push(`${rel(f)}: ${m[1]}`);
      }
      if (/data-tour=\{/.test(src) && !DYNAMIC_OK.has(rel(f))) dynamic.push(rel(f));
    }
    expect(orphans).toEqual([]);
    expect(dynamic).toEqual([]);
  });
});
