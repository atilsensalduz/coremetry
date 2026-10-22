import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.10.1253 — Overview metrik kipinde Failure rate yerine HTTP status kodları
// (VM); HTTP metriği yoksa eski OK/Errors paneline düşer.
const src = readFileSync(resolve(__dirname, 'Overview.tsx'), 'utf8');

describe('Overview HTTP Status paneli', () => {
  it('metrik kipinde status kodu sorgusu, başlık ve geri düşüş', () => {
    expect(src).toContain('statusCodeRateQuery(service,');
    // v0.10.1377 — env seçiliyse ortam eşleyicisi çözülene dek bekler.
    expect(src).toContain("enabled: metricMode && envReady,");
    expect(src).toContain('statusCodeRateQuery(service, Math.max(60, (to - from) / 1e9), envSel, family)'); // v0.10.1429 aile
    expect(src).toContain("title={showHttpStatus ? 'HTTP Status · metrik' : metricMode ? 'Failure rate · metrik'");
    expect(src).toMatch(/const showHttpStatus = metricMode && httpStatus\.series\.length > 0;/);
  });
  // v0.10.1259 — hata oranı hesaplanamıyorsa karo toplam isteği gösterir.
  it('Failure rate "—" yerine Toplam istek', () => {
    expect(src).toContain('{metricErrorsUnknown && totalReq !== null');
    expect(src).toContain('<KpiTile lab="Toplam istek"');
    expect(src).toContain("instant: '1'");
  });
});
