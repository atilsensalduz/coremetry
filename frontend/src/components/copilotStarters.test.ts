import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// v0.9.652 — boş sohbetteki başlangıç çipleri (operatör isteği).
//
// v0.9.579'da operatör bunları "çıkar" demişti: SEKİZ sabit soru bir
// MENÜYDÜ, araç değil. Karar değişti ama gerekçe ölmedi — üç çip, ve
// üçü de operatörün adıyla istediği sorular.
//
// Bu testler v0.9.579'un dersini çiviliyor: liste büyürse menü hissi
// geri gelir.

const src = readFileSync(resolve(__dirname, './CopilotChat.tsx'), 'utf8');

function arrayLiteral(name: string): string[] {
  const i = src.indexOf(`const ${name} = [`);
  if (i < 0) return [];
  const body = src.slice(src.indexOf('[', i) + 1, src.indexOf('];', i));
  // SATIR bazlı: her eleman kendi satırında. Karakter bazlı bir regex,
  // "Takımımın exception'ları?" içindeki KESME İŞARETİNİ tırnak sanıp
  // çipi ikiye bölüyordu — testi yazarken bulundu.
  return body.split('\n')
    .map(l => l.trim())
    .filter(l => l && !l.startsWith('//'))
    .map(l => l.replace(/,\s*$/, ''))
    .filter(l => /^['"]/.test(l))
    .map(l => l.slice(1, -1));
}

describe('başlangıç çipleri', () => {
  const starters = arrayLiteral('STARTERS');

  it('çipler okunabiliyor', () => {
    expect(starters.length).toBeGreaterThan(0);
  });

  // v0.9.579'un dersi: sekiz çip menüydü. v0.10.702 (operatör: "önerileri
  // zenginleştirelim") — tavan BEŞ; her çip LLM'siz yönlenir
  // (copilot_starters_test.go Go tarafında pinler), menü değil araç.
  it('en fazla BEŞ çip — menü hissi eşiği', () => {
    expect(starters.length).toBeLessThanOrEqual(5);
  });

  // v0.10.702 — eklenen iki konu.
  // v0.10.1249 (operatör) — "açık problemleri" yerine takım exception'ları;
  // filo geneli "son 1 saatteki log hataları" çok servisli kurulumda
  // anlamsızdı, çıkarıldı. Geri gelmesin.
  it("takım exception'ları var; açık problemler ve filo log hataları yok", () => {
    const joined = starters.join(' | ').toLowerCase();
    expect(joined).toContain("exception'ları");
    expect(joined).not.toContain('açık problemleri');
    expect(joined).not.toContain('log hata');
  });

  // Operatörün adıyla istediği üç soru.
  it('operatörün istediği konuları kapsıyor', () => {
    const joined = starters.join(' | ').toLowerCase();
    expect(joined).toContain('takımım');
    expect(joined).toContain('exception');
    expect(joined).toContain('yavaş');
  });

  // Operatör: "son deploy etkisine gerek yok".
  it('deploy sorusu YOK', () => {
    expect(starters.join(' ').toLowerCase()).not.toContain('deploy');
  });
});

describe('follow-up listesi', () => {
  const followups = arrayLiteral('FOLLOWUPS');

  it('statik follow-up listesinden de deploy çıktı', () => {
    expect(followups.join(' ').toLowerCase()).not.toContain('deploy');
  });

  it('follow-up listesi boşalmadı', () => {
    expect(followups.length).toBeGreaterThan(0);
  });
});

// v0.10.1249 (operatör: "hep 2 açık P1 var, hatalı") — karşılama P1'i Inbox'ın
// okuma-anı önceliğinden sayar (/api/problems ham önceliği değil) ve Inbox'a bağlar.
describe('karşılama P1 kaynağı', () => {
  it('useInbox prio=P1, yalnız karşılama ekrandayken; tıkı Inbox P1', () => {
    expect(src).toContain("useInbox({ status: 'open', prio: 'P1', limit: 50 },");
    expect(src).not.toContain("useProblems({ status: 'open', priority: ['P1']");
    expect(src).toContain('<Link to="/inbox?prio=P1"');
  });
});
