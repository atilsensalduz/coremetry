import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1233 — servis sekmelerinin elle yazılmış trace pivotları ekrandaki
// env (+ cluster) bağlamını ortak taşıyıcıdan alır; Overview → metrics
// sayfa aralığını URL'de olmasa da taşır.
const read = (f: string) => readFileSync(resolve(__dirname, f), 'utf8');

describe('servis sekmesi pivotları', () => {
  it('OperationsTable iki traces linki env+cluster taşır', () => {
    expect(read('OperationsTable.tsx').match(/pctx, \{ carry: \['env', 'cluster'\] \}\)/g)?.length).toBe(2);
  });
  it('ServicePodsTable traces linkleri env taşır', () => {
    expect(read('ServicePodsTable.tsx').match(/pctx, \{ carry: \['env'\] \}\)/g)?.length).toBe(2);
  });
  it('Overview metrics linki aralığı hep taşır', () => {
    expect(read('Overview.tsx')).toContain('`&range=${encodeURIComponent(r || encodeRange(range))}`');
  });
});
