import { describe, it, expect } from 'vitest';
import { normalizeMathEscapes } from './mathEscapes';

// v0.10.12 — operatör bildirimi: "Problemi açıklarken right arrow right
// arrow yazıyor". LLM çıktısı deterministik değil, o yüzden düzeltmenin
// kendisi deterministik ve tablo-testli.

describe('normalizeMathEscapes', () => {
  it('OPERATÖRÜN GÖRDÜĞÜ satır — sarmalı ok zinciri', () => {
    // Ekrandaki gerçek şekil. Servis adları anonimleştirildi; korunan
    // şey biçim.
    const seen = 'svc-a $\\rightarrow$ svc-b $\\rightarrow$ svc-c';
    expect(normalizeMathEscapes(seen)).toBe('svc-a → svc-b → svc-c');
  });

  const cases: Array<[string, string, string]> = [
    ['çıplak komut', 'a \\rightarrow b', 'a → b'],
    ['sarmalı komut', 'a $\\rightarrow$ b', 'a → b'],
    ['boşluklu sarmal', 'a $ \\rightarrow $ b', 'a → b'],
    ['kısa ok', 'a \\to b', 'a → b'],
    ['çift ok', 'a \\Rightarrow b', 'a ⇒ b'],
    ['çarpı', '3 \\times 4', '3 × 4'],
    ['yaklaşık', 'p \\approx 0.5', 'p ≈ 0.5'],
    ['küçük eşit', 'n \\leq 5', 'n ≤ 5'],
    ['yunan harfi', '\\Delta t', 'Δ t'],
    ['çoklu değişim', 'a \\to b \\to c', 'a → b → c'],
  ];
  for (const [name, input, want] of cases) {
    it(name, () => expect(normalizeMathEscapes(input)).toBe(want));
  }

  // ── DOKUNULMAMASI gerekenler ──────────────────────────────────────────
  describe('tanınmayan girdi AYNEN kalır', () => {
    const untouched = [
      ['dolar işareti METİNDE', 'fiyat $5 ve $10'],
      ['shell değişkeni', 'echo $PATH ve $HOME'],
      ['ters bölü ama komut değil', 'C:\\tmp\\dosya'],
      ['bilinmeyen LaTeX komutu', 'a \\frac{1}{2} b'],
      ['ters bölü hiç yok — erken çıkış', 'düz bir cümle'],
      ['boş', ''],
    ];
    for (const [name, input] of untouched) {
      it(name, () => expect(normalizeMathEscapes(input)).toBe(input));
    }
  });

  // ── AYIRT EDİCİ VAKALAR ───────────────────────────────────────────────
  it('uzun ad önce eşleşir — \\leq geriye "q" bırakmaz', () => {
    // `\le` deseni önce denenirse `\leq` → "≤q" olur. Sıralama bu yüzden
    // uzunluğa göre; bu test o sıralamayı çiviliyor.
    expect(normalizeMathEscapes('n \\leq 5')).toBe('n ≤ 5');
    expect(normalizeMathEscapes('n \\geq 5')).toBe('n ≥ 5');
  });

  it('komut adının HARFLE devamı eşleşmez', () => {
    // `\tokenizer` içindeki `\to`yu çevirmek metni bozardı. Sınır
    // HARF: LaTeX komut adları yalnız harften oluşur.
    expect(normalizeMathEscapes('\\tokenizer')).toBe('\\tokenizer');
    expect(normalizeMathEscapes('\\rightarrows')).toBe('\\rightarrows');
  });

  it('RAKAMLA devam eden komut EŞLEŞİR — LaTeX semantiği', () => {
    // İlk yazımda bunu ihlal sandım; yanlıştı. LaTeX'te komut adı
    // harfte biter, yani `\times2` gerçekten "× 2" demek. Testi koda
    // uydurmak değil, kodu doğrulamak: bu davranış DOĞRU.
    expect(normalizeMathEscapes('\\times2')).toBe('×2');
  });

  it('karışık dolar içeriğinde YALNIZ komut çevrilir', () => {
    // `$5 \times 3$` — sarmal deseni tutmuyor (içerik tek komut değil),
    // o yüzden dolarlar kalıyor ama komut çevriliyor. Yarım iş, ama
    // YANLIŞ iş değil: metnin dolarları operatörün yazdığı olabilir.
    expect(normalizeMathEscapes('$5 \\times 3$')).toBe('$5 × 3$');
  });
});

// ── v0.10.1345 — operatör bildirimi (prod /cosre, gemma4 31b) ───────────
// "ACT (Active) $\rightarrow$ RCV (Received) $\rightarrow$ DIP …" —
// "Rightarrow'u düzelt". Yardımcı vardı ama sohbet balonu çağırmıyordu;
// artık TEK yardımcı her model-markdown yüzeyinde (gerekçe mathEscapes.ts).
describe('normalizeMathEscapes — v0.10.1345 genişletme', () => {
  it('PROD SATIRI aynen', () => {
    const prod = 'ACT (Active) $\\rightarrow$ RCV (Received) $\\rightarrow$ DIP (Dispatched)';
    expect(normalizeMathEscapes(prod)).toBe('ACT (Active) → RCV (Received) → DIP (Dispatched)');
  });

  const cases: Array<[string, string, string]> = [
    ['\\leftrightarrow', 'a $\\leftrightarrow$ b', 'a ↔ b'],
    ['\\Leftarrow', 'a \\Leftarrow b', 'a ⇐ b'],
    ['\\Leftrightarrow', 'a \\Leftrightarrow b', 'a ⇔ b'],
    ['\\longrightarrow', 'a $\\longrightarrow$ b', 'a ⟶ b'],
    ['\\mapsto', 'k \\mapsto v', 'k ↦ v'],
    ['\\uparrow / \\downarrow', 'p95 $\\uparrow$ rps $\\downarrow$', 'p95 ↑ rps ↓'],
    ['\\gets', 'x \\gets y', 'x ← y'],
    ['\\ge / \\le', 'a \\ge b \\le c', 'a ≥ b ≤ c'],
    ['\\neq / \\ne', 'a \\neq b \\ne c', 'a ≠ b ≠ c'],
    ['\\sim', '\\sim 200ms', '~ 200ms'],
    ['\\cdot / \\pm', '2 \\cdot 3 \\pm 1', '2 · 3 ± 1'],
    ['\\infty', 'TTL $\\infty$', 'TTL ∞'],
    ['\\degree', '90\\degree', '90°'],
    ['\\% kaçışı', 'hata oranı 12\\%', 'hata oranı 12%'],
    ['\\& kaçışı', 'R\\&D', 'R&D'],
    ['\\_ kelime içinde', 'service\\_name alanı', 'service_name alanı'],
    ['sarmal + sayı soyulur', 'gecikme $\\geq 5$ sn', 'gecikme ≥ 5 sn'],
    ['sarmal + yüzde', 'oran $\\geq 5\\%$', 'oran ≥ 5%'],
    ['boşluklu sarmal', 'a $ \\to $ b', 'a → b'],
    ['\\( … \\) sarmal', 'a \\( \\rightarrow \\) b', 'a → b'],
    ['\\( … \\) karışık da soyulur', '\\(x \\geq 5\\)', 'x ≥ 5'],
    ['çok satır', 'a $\\to$ b\nc $\\to$ d', 'a → b\nc → d'],
  ];
  for (const [name, input, want] of cases) {
    it(name, () => expect(normalizeMathEscapes(input)).toBe(want));
  }

  it('KARAR — "$x \\geq 5$": sembol çevrilir, x ve dolarlar KALIR', () => {
    // Harfle başlayan `$…$` `$HOME` gibi metin olabilir; dolar soymak
    // geri alınamaz. Okunması gereken sembol yine de düzelir.
    expect(normalizeMathEscapes('$x \\geq 5$')).toBe('$x ≥ 5$');
  });

  describe('para tutarları DOKUNULMAZ', () => {
    const money: Array<[string, string, string]> = [
      ['tek tutar', 'maliyet $5', 'maliyet $5'],
      ['iki tutar', '$10 and $20', '$10 and $20'],
      ['tutardan tutara ok', '$10 \\to $20', '$10 → $20'],
      ['tutar sonra sarmal ok', 'fiyat $5 sonra $\\to$ x', 'fiyat $5 sonra → x'],
      ['harfli sarmal soyulmaz', '$\\to B costs $5', '$→ B costs $5'],
    ];
    for (const [name, input, want] of money) {
      it(name, () => expect(normalizeMathEscapes(input)).toBe(want));
    }
  });

  describe('KOD DOKUNULMAZ', () => {
    it('satır içi kod', () => {
      expect(normalizeMathEscapes('ok $\\to$ ve `a \\to b` kod'))
        .toBe('ok → ve `a \\to b` kod');
    });
    it('çift backtick koşusu', () => {
      expect(normalizeMathEscapes('``x ` \\to``  \\to')).toBe('``x ` \\to``  →');
    });
    it('eşi olmayan backtick düz metin', () => {
      expect(normalizeMathEscapes('a ` \\to b')).toBe('a ` → b');
    });
    it('çitli blok (girintili çit dahil)', () => {
      const md = 'önce \\to\n```latex\n$\\rightarrow$ \\_x\n```\nsonra \\to\n  ```\n  \\geq\n  ```';
      expect(normalizeMathEscapes(md))
        .toBe('önce →\n```latex\n$\\rightarrow$ \\_x\n```\nsonra →\n  ```\n  \\geq\n  ```');
    });
    it('kapanmamış çit metnin sonuna kadar kod', () => {
      expect(normalizeMathEscapes('a \\to\n```\n\\to', { streaming: true })).toBe('a →\n```\n\\to');
    });
  });

  it('kelime sınırındaki \\_ markdown kaçışı olarak kalır (\\_\\_init\\_\\_ kalın olmasın)', () => {
    expect(normalizeMathEscapes('\\_\\_init\\_\\_')).toBe('\\_\\_init\\_\\_');
  });

  describe('AKIŞ — birikmiş metin, sondaki yarım komut gizlenir', () => {
    const full = 'ACT (Active) $\\rightarrow$ RCV (Received)';
    it('her önek çöp üretmez: ham LaTeX ya da yanlış sembol yok', () => {
      for (let n = 1; n <= full.length; n++) {
        const out = normalizeMathEscapes(full.slice(0, n), { streaming: true });
        expect(out, `önek ${n}: ${full.slice(0, n)}`).not.toMatch(/\\[a-z]/);
        expect(out).not.toContain('$→');
      }
    });
    it('"$\\righ" sınırı → gizli; tamamlanınca ok', () => {
      expect(normalizeMathEscapes('ACT $\\righ', { streaming: true })).toBe('ACT ');
      expect(normalizeMathEscapes('ACT $\\rightarrow$ R', { streaming: true })).toBe('ACT → R');
    });
    it('"\\le" sonda ≤ diye basılmaz (\\leftarrow olabilir)', () => {
      expect(normalizeMathEscapes('a \\le', { streaming: true })).toBe('a ');
      expect(normalizeMathEscapes('a \\leftarrow b', { streaming: true })).toBe('a ← b');
    });
    it('akış bitince (streaming yok) sondaki komut çevrilir', () => {
      expect(normalizeMathEscapes('a \\le')).toBe('a ≤');
    });
  });
});
