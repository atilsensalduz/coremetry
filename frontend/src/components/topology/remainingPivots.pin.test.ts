import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1237 — kalan elle yazılmış pivotlar ekrandaki env'i taşır.
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('kalan pivotlar env taşır', () => {
  it.each([
    ['FocusedNeighborhood.tsx'],
    ['../../features/anomalies/ExceptionPodsPanel.tsx'],
    ['../../pages/service/ServiceSignalTabs.tsx'],
  ])('%s', f => {
    expect(read(f)).toMatch(/pctx, \{ carry: \['env'\] \}\)/);
  });
});
