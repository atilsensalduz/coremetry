import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1224 — operatör: Infrastructure'da cluster adına basınca cluster
// sayfasına gidiyordu; ad satır tıkının parçası (pod'ları açar), detay ↗'de.
const src = readFileSync(resolve(__dirname, 'ServiceInfraTab.tsx'), 'utf8');

describe('Infra cluster satırı', () => {
  it('ad hücresi tıkı yutmaz, detay linki ↗', () => {
    expect(src).not.toMatch(/<td className="mono" onClick=\{e => e\.stopPropagation\(\)\}>\s*<Link to=\{entityHref\(\{ type: 'cluster'/);
    expect(src).toMatch(/title="Cluster detayı" aria-label=\{`\$\{r\.cluster\} cluster detayı`\}>↗<\/Link>/);
  });
});
