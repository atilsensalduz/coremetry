// v0.10.1346 (operatör-bildirimli, /cosre ekran görüntüsü) — sohbet tablosu
// hücre kararları. "Kolon adı kısa ama satırlardaki değerler uzun olduğunda
// tablo kötü gözüküyor": "LOGIN_14300.aspx" L/O/GI/N_/14/30/0./as/px,
// "1526" 152/6, "AKTİF" AKTİ/F, "KAPALI" KAP/ALI diye harf harf kırılıyordu.
// Kısa değer ASLA bölünmez; uzun kimlik yalnız `_ . / -` SONRASINDA kırılır.
import { describe, it, expect } from 'vitest';
import { cellNoWrap, cellText, numericColumn, plainCell, wbrSegments, WBR_MIN } from './mdTableCell';

describe('wbrSegments — kırılma noktası yalnız uzun kelimede, ayraçtan SONRA', () => {
  const cases: [string, string[]][] = [
    ['LOGIN_14300.aspx', ['LOGIN_', '14300.', 'aspx']],
    ['AKTİF', ['AKTİF']],
    ['KAPALI', ['KAPALI']],
    ['1526', ['1526']],
    ['a_b.c', ['a_b.c']], // kısa kelime: ayraç olsa da dokunulmaz
    ['1.234.567.890,12', ['1.234.567.890,12']], // sayı bölünmez
    ['Giriş LOGIN_14300.aspx sayfası', ['Giriş LOGIN_', '14300.', 'aspx sayfası']],
    ['/api/v1/login-form', ['/api/', 'v1/', 'login-', 'form']],
    ['com__deep..name', ['com__', 'deep..', 'name']], // ardışık ayraç tek kırılma
    ['trailing_sep_', ['trailing_', 'sep_']], // son karakterden sonra kırılma yok
    ['', ['']],
  ];
  it.each(cases)('%j', (input, want) => {
    const got = wbrSegments(input);
    expect(got).toEqual(want);
    expect(got.join('')).toBe(input); // metin asla değişmez
  });

  it(`${WBR_MIN} karakterden kısa hiçbir kelimeye kırılma noktası eklenmez`, () => {
    for (const v of ['AKTİF', 'KAPALI', '1526', 'SLA_1', 'a.b/c-d_e']) {
      expect(wbrSegments(v)).toEqual([v]);
    }
  });
});

describe('cellNoWrap — kısa değer ve tek kelimelik kimlik tek satır', () => {
  const cases: [string, boolean][] = [
    ['AKTİF', true],
    ['KAPALI', true],
    ['1526', true],
    ['LOGIN_14300.aspx', true],
    ['İnternet Şube Giriş', true], // ≤24, boşluklu
    ['com.example.payments.CheckoutService', true], // tek kelime ≤40
    ['com.example.payments.internal.CheckoutServiceImpl.run', false], // tek kelime >40
    ['Müşteri internet şubesine kullanıcı adı ve şifre ile giriş yapar', false], // düzyazı sarar
    ['', false],
  ];
  it.each(cases)('%j → %s', (v, want) => expect(cellNoWrap(v)).toBe(want));
});

describe('numericColumn — tamamı sayı olan kolon sağa yaslanır', () => {
  const rows = [['1', '1526', 'AKTİF', ''], ['2', '1.234', 'KAPALI', ''], ['3', '', 'AKTİF', '']];
  it('sayı kolonu (boş hücre yok sayılır)', () => {
    expect(numericColumn(rows, 0)).toBe(true);
    expect(numericColumn(rows, 1)).toBe(true);
  });
  it('metin kolonu ve tamamen boş kolon sayı değil', () => {
    expect(numericColumn(rows, 2)).toBe(false);
    expect(numericColumn(rows, 3)).toBe(false);
  });
});

describe('plainCell / cellText', () => {
  it('düz metin hücresi metnini döner, işaretli hücre null', () => {
    expect(plainCell('LOGIN_14300.aspx')).toBe('LOGIN_14300.aspx');
    expect(plainCell('**AKTİF**')).toBeNull();
    expect(plainCell('`x`')).toBeNull();
  });
  it('cellText işaretleri düşürür', () => {
    expect(cellText('**AKTİF**')).toBe('AKTİF');
  });
});
