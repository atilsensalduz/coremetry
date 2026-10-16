import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1229 — Services filtreleri URL'den türetilir; yerel kopya / yazıcı
// efekti geri gelmesin (tek-yön sınıfı).
const src = readFileSync(resolve(__dirname, 'Services.tsx'), 'utf8');

describe('Services URL state', () => {
  it('useUrlParams(SERVICES_URL_SPEC) kullanır', () => {
    expect(src).toContain('useUrlParams(SERVICES_URL_SPEC)');
  });
  it('filtrelerin useState kopyası yok', () => {
    for (const k of ['errorsOnly', 'minSpans', 'minP99', 'ownerTeam', 'sreTeam', 'cluster', 'namespace', 'committedFilter']) {
      expect(src).not.toMatch(new RegExp(`const \\[${k}, set\\w+\\] = useState`));
    }
  });
});
