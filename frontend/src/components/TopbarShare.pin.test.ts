import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1230 — her sayfada linki kopyala; kopya zaman aralığını sabitler.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');

describe('TopbarShare', () => {
  it('Topbar her sayfada render eder', () => {
    expect(read('Topbar.tsx')).toMatch(/<div className="topbar-prefs">\s*<TopbarShare \/>/);
  });
  it('kopya absoluteShareHref ile sabitlenir', () => {
    expect(read('TopbarShare.tsx')).toContain('absoluteShareHref(window.location.href, Date.now())');
  });
});
