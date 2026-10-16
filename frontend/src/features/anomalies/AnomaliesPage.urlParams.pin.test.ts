import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1232 — exception araması ve açık satırlar URL'den türetilir
// (denetim: arama hiç URL'de değildi, ?exception= yalnız mount'ta okunuyordu).
const src = readFileSync(resolve(__dirname, 'AnomaliesPage.tsx'), 'utf8');

describe('Problems URL state', () => {
  it('EXC_URL_SPEC q + exception + page', () => {
    expect(src).toMatch(/const EXC_URL_SPEC = \{ q: strParam\(\), exception: csvParam\(\), page: intParam\(0\) \}/);
    expect(src).toContain('useUrlParams(EXC_URL_SPEC)');
  });
  it('yerel kopya yok', () => {
    expect(src).not.toMatch(/const \[committedSearch, setCommittedSearch\] = useState/);
    expect(src).not.toMatch(/const \[expanded, setExpanded\] = useState/);
  });
});
