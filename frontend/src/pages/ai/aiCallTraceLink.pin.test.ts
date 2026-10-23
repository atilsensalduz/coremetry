// v0.10.1463 (CoSRE D1) — /ai çağrı çekmecesinin "Trace" linki: yalnız
// traceId doluyken (sunucu yalnız ÖRNEKLENMİŞ trace'i yazar) ve tek
// /trace?id= kurucusundan (traceHref) geçer.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { traceHref } from '@/lib/traceHref';

describe('v0.10.1463 — ai_calls satırından trace linki', () => {
  it('link traceId kapısının arkasında ve traceHref ile', () => {
    const src = readFileSync(resolve(__dirname, '../AIObservability.tsx'), 'utf8');
    expect(src).toMatch(/\{call\.traceId && \(/);
    expect(src).toContain('<Link to={traceHref(call.traceId)}');
    expect(src).not.toContain('/trace?id=${call.traceId}');
  });
  it('hedef /trace sayfası', () => {
    expect(traceHref('0af7651916cd43dd8448eb211c80319c')).toBe('/trace?id=0af7651916cd43dd8448eb211c80319c');
  });
});
