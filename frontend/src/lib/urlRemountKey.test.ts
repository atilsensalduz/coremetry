import { describe, it, expect } from 'vitest';
import { ownedParamSig, nextUrlRemountKey } from './urlRemountKey';

// v0.10.1218 — Traces: sayfa açıkken gelen dış URL değişimi remount eder.
const OWN = ['service', 'filters', 'page'];

describe('ownedParamSig', () => {
  it('sahip olunmayan param ve sıra imzayı değiştirmez', () => {
    expect(ownedParamSig('service=a&range=1h&page=2', OWN)).toBe(ownedParamSig('page=2&env=prod&service=a&s_x=y', OWN));
  });
  it('boş değer yok sayılır, sahip param değişimi imzayı değiştirir', () => {
    expect(ownedParamSig('service=a&filters=', OWN)).toBe(ownedParamSig('service=a', OWN));
    expect(ownedParamSig('service=a', OWN)).not.toBe(ownedParamSig('service=b', OWN));
  });
});

describe('nextUrlRemountKey', () => {
  const a = ownedParamSig('service=a', OWN);
  const b = ownedParamSig('service=b', OWN);
  const c = ownedParamSig('service=c', OWN);
  it('ilk mount, aynı imza, kendi yankısı → key sabit', () => {
    const s0 = nextUrlRemountKey(null, a, null);
    expect(nextUrlRemountKey(s0, a, null)).toBe(s0);
    expect(nextUrlRemountKey(s0, b, b).key).toBe(s0.key);
  });
  it('dış değişim → yeni key; eski yankı yutulmaz', () => {
    const s0 = nextUrlRemountKey(null, a, null);
    const s1 = nextUrlRemountKey(s0, b, b);        // kendi yazımı
    const s2 = nextUrlRemountKey(s1, c, b);        // saved view
    expect(s2.key).not.toBe(s0.key);
    const s3 = nextUrlRemountKey(s2, a, b);        // geri tuşu, eski imza
    expect(s3.key).not.toBe(s2.key);
  });
});
