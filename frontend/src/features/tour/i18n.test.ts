// i18n.test.ts — v0.10.1334 rehberli tur metinleri: kullanılan her tour.*
// anahtarı (adım başlık/gövdeleri + çerçeve + notlar) İKİ katalogda da var
// ve iki katalogdaki tour.* kümeleri birebir aynı. Eksik TR anahtarı sessizce
// İngilizce'ye düşer (lib/i18n.ts t() geri dönüşü) — hiçbir kapı görmez.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';
import { TOUR_CHROME_KEYS, TOUR_STEPS } from './steps';

const SRC = readFileSync(resolve(__dirname, '../../lib/i18n.ts'), 'utf8');
const enStart = SRC.indexOf('const EN: Catalog = {');
const trStart = SRC.indexOf('const TR: Catalog = {');
const end = SRC.indexOf('const CATALOGS');
const block = (a: number, b: number) => SRC.slice(a, b);
const tourKeys = (s: string) => new Set([...s.matchAll(/^\s*'(tour\.[\w.]+)'\s*:/gm)].map(m => m[1]));

const EN = tourKeys(block(enStart, trStart));
const TR = tourKeys(block(trStart, end));

const USED = [
  ...TOUR_STEPS.flatMap(s => [s.titleKey, s.bodyKey]),
  ...TOUR_CHROME_KEYS,
  ...(['not_yet', 'no_services'] as const).map(r => `tour.note.${r}`),
];

describe('tour i18n (v0.10.1334)', () => {
  it('katalog blokları bulundu', () => {
    expect(enStart).toBeGreaterThan(-1);
    expect(trStart).toBeGreaterThan(enStart);
    expect(end).toBeGreaterThan(trStart);
    expect(EN.size).toBeGreaterThan(0);
  });

  it('kullanılan her tour.* anahtarı EN ve TR\'de', () => {
    const missing = USED.flatMap(k => [!EN.has(k) ? `EN ${k}` : '', !TR.has(k) ? `TR ${k}` : '']).filter(Boolean);
    expect(missing).toEqual([]);
  });

  it('EN ve TR tour.* kümeleri birebir', () => {
    expect([...EN].sort()).toEqual([...TR].sort());
  });

  it('TR çevirisi İngilizce\'ye düşmüyor (çalışma zamanı)', () => {
    for (const k of USED) {
      expect(t(k, 'tr'), k).not.toBe(k);
      expect(t(k, 'en'), k).not.toBe(k);
    }
    expect(t('tour.note.no_services', 'tr')).toContain('Henüz servis raporlamıyor');
  });

  it('stepOf şablonu iki dilde {n} ve {total} taşıyor', () => {
    for (const lang of ['en', 'tr'] as const) {
      expect(t('tour.chrome.stepOf', lang)).toContain('{n}');
      expect(t('tour.chrome.stepOf', lang)).toContain('{total}');
    }
  });
});
