import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1217 — emekli rotalar sorgu dizesini korumalı: statik
// <Navigate to="..."> paylaşılan linkin filtrelerini atıyordu.
const app = readFileSync(resolve(__dirname, 'App.tsx'), 'utf8');

describe('emekli rota yönlendirmeleri sorguyu korur', () => {
  for (const [from, to] of [['/deploys', '/rollouts'], ['/exceptions', '/problems'], ['/errors', '/problems']]) {
    it(from, () => {
      const line = app.split('\n').find(l => l.includes(`path="${from}"`)) ?? '';
      expect(line).toContain(`<QueryRedirect to="${to}" />`);
    });
  }
  it('QueryRedirect loc.search taşır', () => {
    expect(app).toMatch(/function QueryRedirect[\s\S]{0,200}\$\{loc\.search\}/);
  });
});
