// apiErrorText.test.ts — v0.10.1341: kaydetme hatası formda tek satır.
//
// Sözleşme: `HTTP NNN: ` öneki düşer; gövde {error} JSON'uysa sunucunun
// cümlesi; değilse kalan düz metin; boşsa fallback. Hiçbir hata boş metne
// yutulmaz.
import { describe, expect, it } from 'vitest';
import { apiErrorText } from './api';

describe('apiErrorText', () => {
  it('HTTP öneki + {error} JSON → yalnız sunucu cümlesi', () => {
    const e = new Error('HTTP 400: {"error":"runbookUrl must be an absolute http(s) URL or an app path such as \\"/runbook?id=…\\""}\n');
    expect(apiErrorText(e)).toBe('runbookUrl must be an absolute http(s) URL or an app path such as "/runbook?id=…"');
  });

  it('alan adlı katalog hatası aynen gelir', () => {
    expect(apiErrorText(new Error('HTTP 400: {"error":"customLinks[2].url: must be …"}'))).toBe('customLinks[2].url: must be …');
  });

  it('düz metin gövde (http.Error) öneksiz döner', () => {
    expect(apiErrorText(new Error('HTTP 400: invalid body: unexpected EOF\n'))).toBe('invalid body: unexpected EOF');
  });

  it('JSON ama error alanı yok → gövde metni', () => {
    expect(apiErrorText(new Error('HTTP 500: {"status":"x"}'))).toBe('{"status":"x"}');
  });

  it('bozuk JSON → düz metin', () => {
    expect(apiErrorText(new Error('HTTP 502: {oops'))).toBe('{oops');
  });

  it('boş gövde / boş hata → fallback', () => {
    expect(apiErrorText(new Error('HTTP 500: '))).toBe('Request failed');
    expect(apiErrorText(new Error(''), 'Kaydedilemedi')).toBe('Kaydedilemedi');
    expect(apiErrorText(undefined)).toBe('Request failed');
    expect(apiErrorText(new Error('HTTP 400: {"error":"  "}'), 'x')).toBe('{"error":"  "}');
  });

  it('HTTP öneki olmayan hata (timeout) aynen', () => {
    expect(apiErrorText(new Error('Request timed out after 30s'))).toBe('Request timed out after 30s');
    expect(apiErrorText('plain string')).toBe('plain string');
  });
});
