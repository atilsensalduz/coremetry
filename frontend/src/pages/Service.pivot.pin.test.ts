import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1231 — servis sayfası pivotları env + cluster'ı ortak bağlam
// taşıyıcısıyla (lib/pivotContext) taşır.
const src = readFileSync(resolve(__dirname, 'Service.tsx'), 'utf8');

describe('Service pivot bağlamı', () => {
  it.each(['/traces', '/logs', '/endpoints'])('%s DrillButton carry alır', to => {
    expect(src).toMatch(new RegExp(`<DrillButton to="${to}"[^>]*carry=\\{SERVICE_PIVOT_CARRY\\}`));
  });
  it('eski ?tab=traces yönlendirmesi bağlamı taşır', () => {
    expect(src).toMatch(/withPivotCtx\(`\/traces\?service=/);
  });
});
