import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1219 — Explore aralığı URL'den canlı okunur; yerel useState kopyası
// yalnız-aralık değişiminde eski aralığı URL'e geri yazıyordu.
const src = readFileSync(resolve(__dirname, 'Explore.tsx'), 'utf8');

describe('Explore range kaynağı', () => {
  it('useUrlRange kullanır, yerel TimeRange state yok', () => {
    expect(src).toMatch(/const \[range, setRange\] = useUrlRange\(/);
    expect(src).not.toMatch(/useState<TimeRange>/);
  });
});
