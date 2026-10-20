// paramHref — v0.10.1414 (tablo satırları gerçek link). URL-parametreli çekmece
// açan satırın adresi, `setSearchParams(prev => next.set(k, v), { replace })`
// yazımının varacağı adresle AYNI: yabancı parametreler korunur, anahtar
// varsa üzerine yazılır, değer kodlanır.
import { describe, it, expect } from 'vitest';
import { paramHref } from './paramHref';

describe('paramHref (v0.10.1414)', () => {
  it('yabancı parametreleri korur, anahtarı sona ekler', () => {
    expect(paramHref('/hosts', new URLSearchParams('range=1h&env=prod'), 'host', 'host-a'))
      .toBe('/hosts?range=1h&env=prod&host=host-a');
  });
  it('var olan anahtarın değerini değiştirir (ikinci kopya yok)', () => {
    expect(paramHref('/external', new URLSearchParams('host=old&range=1h'), 'host', 'api.example.test'))
      .toBe('/external?host=api.example.test&range=1h');
  });
  it('değeri kodlar; boş sorguda yalnız anahtar', () => {
    expect(paramHref('/hosts', new URLSearchParams(), 'host', 'a b&c')).toBe('/hosts?host=a+b%26c');
  });
  it('kaynağı değiştirmez', () => {
    const prev = new URLSearchParams('range=1h');
    paramHref('/hosts', prev, 'host', 'h');
    expect(prev.toString()).toBe('range=1h');
  });
});
