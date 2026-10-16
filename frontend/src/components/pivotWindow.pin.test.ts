import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1221 — span→servis ve harita çekmecesi→endpoints pencereyi düşürüyordu
// (hedefler pencereyle sorgular; eski bir trace'ten servis sayfası yanlış saati açardı).
const read = (p: string) => readFileSync(resolve(__dirname, p), 'utf8');

describe('pivot penceresi taşınır', () => {
  it('SpanDetail künye servis linki span penceresini taşır', () => {
    expect(read('SpanDetail.tsx')).toContain('serviceHref(span.serviceName, { range: spanLinkCtx.window })');
  });
  it('ServiceMapNodeDrawer endpoints linki pencereyi taşır', () => {
    expect(read('ServiceMapNodeDrawer.tsx')).toMatch(/\/endpoints\?service=.*range=\$\{encodeURIComponent\(windowRangeParam\(win\)\)\}/);
  });
});
