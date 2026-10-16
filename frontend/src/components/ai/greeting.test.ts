import { describe, it, expect } from 'vitest';
import { greetHello, greetErrors, agoTR } from './greeting';

// v0.9.528 — karşılama saf olduğu için sözleşmesi burada pinli.
// Kritik nokta: karşılama bir İDDİA taşıyor ("3 açık P1 var"). Yanlış
// iddia, operatörün sohbeti kapatmasına yol açar — bu yüzden yükleme
// hâli ile "gerçekten sıfır" hâli ASLA aynı cümleye düşmemeli.

const NOW = 1_800_000_000_000; // sabit "şimdi" (ms)
const nsAgo = (mins: number) => (NOW - mins * 60_000) * 1e6;

describe('greetHello', () => {
  it('adı varsa isimle hitap eder', () => {
    expect(greetHello('Fatih')).toBe('Merhaba Fatih.');
  });
  it('ad yoksa isimsiz — uydurmaz', () => {
    expect(greetHello()).toBe('Merhaba 👋');
    expect(greetHello('')).toBe('Merhaba 👋');
    expect(greetHello('   ')).toBe('Merhaba 👋');
  });
});

describe('agoTR', () => {
  it('her birim ayrı ayrı doğru — birim karışması tekrar eden hata sınıfı', () => {
    expect(agoTR(nsAgo(0.2), NOW)).toBe('az önce');
    expect(agoTR(nsAgo(1), NOW)).toBe('1 dk önce');
    expect(agoTR(nsAgo(12), NOW)).toBe('12 dk önce');
    expect(agoTR(nsAgo(59), NOW)).toBe('59 dk önce');
    expect(agoTR(nsAgo(60), NOW)).toBe('1 sa önce');
    expect(agoTR(nsAgo(300), NOW)).toBe('5 sa önce');
    expect(agoTR(nsAgo(60 * 24), NOW)).toBe('1 gün önce');
    expect(agoTR(nsAgo(60 * 24 * 3), NOW)).toBe('3 gün önce');
  });

  it('gelecek zaman damgası negatife düşmez (saat kayması)', () => {
    expect(agoTR((NOW + 60_000) * 1e6, NOW)).toBe('az önce');
  });
});

// v0.10.1254 — sayı Inbox toplamından (liste limit'le kırpılır).

// v0.10.1255 — karşılama: son 1 saat hata + exception grubu (P1 yerine).
describe('greetErrors', () => {
  it('yüklenmeden boş, sıfırda sakin, değerde sayılar', () => {
    expect(greetErrors(undefined)).toBe('');
    expect(greetErrors({ errors1h: 0, exceptionGroups1h: 0 })).toMatch(/hata yok/);
    expect(greetErrors({ errors1h: 12345, exceptionGroups1h: 87 })).toBe(`Son 1 saatte ${(12345).toLocaleString('tr-TR')} hata · 87 exception grubu`);
  });
});
