import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1235 — kalan iki "Copy link" yüzeyi de aralığı sabitler.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('copy link sabitleme', () => {
  it('Trace iç link', () => {
    expect(read('../pages/Trace.tsx')).toContain('copyToClipboard(absoluteShareHref(window.location.href, Date.now()))');
  });
  it('MetricPanel panel linki', () => {
    expect(read('MetricPanel.tsx')).toContain('copyToClipboard(absoluteShareHref(window.location.origin + href, Date.now()))');
  });
});
