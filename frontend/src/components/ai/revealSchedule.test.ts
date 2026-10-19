// v0.10.1384 — yazma efekti saf çekirdeği (revealSchedule.ts). Operatör:
// "Cevabı yazarken yazma efekti olsa hızlı — daha çok LLM cevabı değil
// gerçekten düşünüyor hissiyatı olur." Sözleşme: yalnız büyük parça, ≤1,2 sn,
// kelime sınırı, markdown güvenli, gerçek token akışına dokunmaz.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  REVEAL_CAP_MS, REVEAL_MAX_STEPS, REVEAL_MIN_CHUNK, REVEAL_MIN_MS,
  commonPrefixLen, nextArrival, revealDurationMs, revealIndexAt, revealStops, withArrival,
} from './revealSchedule';
import type { ChatTurn } from '@/lib/types';

const words = (n: number) => Array.from({ length: n }, (_, i) => `kelime${i}`).join(' ');

describe('nextArrival — hangi metin açılır', () => {
  it('tüm cevap (answer) öncesinde akış yokken: uzunluktan bağımsız from=0', () => {
    expect(nextArrival('', 'kısa cevap', undefined, 1000, true)).toEqual({ at: 1000, from: 0 });
    expect(nextArrival('', words(200), undefined, 5, true)).toEqual({ at: 5, from: 0 });
  });
  it('gerçek token akışı (küçük delta) damga ALMAZ — çift efekt yok', () => {
    let text = '';
    let a: ReturnType<typeof nextArrival>;
    for (const tok of ['Mer', 'haba', ', checkout ', 'servisi ', 'sağlıklı.']) {
      a = nextArrival(text, text + tok, a, 1, false);
      text += tok;
    }
    expect(a).toBeUndefined();
    // Akışın sonundaki otoriter answer aynı metni yollar: damga yine yok.
    expect(nextArrival(text, text, undefined, 2, true)).toBeUndefined();
  });
  it(`≥${REVEAL_MIN_CHUNK} karakterlik tek delta: önekten itibaren açılır`, () => {
    const pre = 'Önsöz. ';
    const big = 'x'.repeat(REVEAL_MIN_CHUNK);
    expect(nextArrival(pre, pre + big, undefined, 7, false)).toEqual({ at: 7, from: pre.length });
    expect(nextArrival(pre, pre + big.slice(1), undefined, 7, false)).toBeUndefined();
  });
  it('büyük parça açılırken gelen küçük delta / aynı answer mevcut damgayı korur', () => {
    const prev = { at: 3, from: 0 };
    expect(nextArrival('abc', 'abcd', prev, 9, false)).toBe(prev);
    expect(nextArrival('abc', 'abc', prev, 9, true)).toBe(prev);
  });
  it('reset delta (metin baştan) büyükse from=0', () => {
    expect(nextArrival('önsöz düşünce', 'y'.repeat(300), undefined, 4, false)).toEqual({ at: 4, from: 0 });
  });
  it('commonPrefixLen', () => {
    expect(commonPrefixLen('abcd', 'abxy')).toBe(2);
    expect(commonPrefixLen('', 'a')).toBe(0);
    expect(commonPrefixLen('aa', 'aa')).toBe(2);
  });
});

describe('withArrival — tur nesnesi', () => {
  it('damga yoksa alan HİÇ eklenmez', () => {
    const t = withArrival({ role: 'assistant' as const, text: 'a' }, 'ab', false, 1);
    expect(t).toEqual({ role: 'assistant', text: 'ab' });
    expect('arrivedAt' in t).toBe(false);
  });
  it('tüm cevap damga alır; sonraki aynı metin damgayı değiştirmez', () => {
    const t1 = withArrival<ChatTurn>({ role: 'assistant', text: '' }, 'cevap', true, 10);
    expect(t1.arrivedAt).toEqual({ at: 10, from: 0 });
    const t2 = withArrival(t1, 'cevap', true, 99);
    expect(t2.arrivedAt).toBe(t1.arrivedAt);
  });
});

describe('revealDurationMs — ≈1,2 sn tavan, uzunluktan bağımsız', () => {
  it('tavan ve taban', () => {
    expect(revealDurationMs(8000)).toBe(REVEAL_CAP_MS);
    expect(revealDurationMs(50_000)).toBe(REVEAL_CAP_MS);
    expect(revealDurationMs(10)).toBe(REVEAL_MIN_MS);
    expect(revealDurationMs(0)).toBe(0);
    expect(REVEAL_CAP_MS).toBeLessThanOrEqual(1200);
  });
});

describe('revealStops — kelime sınırı, markdown güvenli', () => {
  const visible = (text: string, from = 0) => revealStops(text, from).map(s => text.slice(0, s));

  it('düzyazı kelime sınırında kesilir; son nokta metnin tamamı', () => {
    const t = 'checkout servisi son bir saatte hata veriyor';
    const v = visible(t);
    expect(v[v.length - 1]).toBe(t);
    for (const p of v.slice(0, -1)) expect(p.endsWith(' ')).toBe(true);
  });
  it('**kalın iki kelime**, `kod parçası` ve [bağlantı metni](/x y) ortasında kesilmez', () => {
    const t = 'Önce **çok önemli karar** sonra `select * from t` ve [servis sayfası](/services/a b) bitti son';
    for (const p of visible(t)) {
      expect((p.match(/\*\*/g) ?? []).length % 2, p).toBe(0);
      expect((p.match(/`/g) ?? []).length % 2, p).toBe(0);
      expect((p.match(/\[/g) ?? []).length, p).toBe((p.match(/\)/g) ?? []).length);
    }
  });
  it('kod çiti ve tablo satırı içinde yalnız SATIR SONU', () => {
    const t = 'Giriş metni burada\n```sql\nselect a, b from t where x = 1\n```\n| Servis | p95 |\n|---|---:|\n| checkout api | 480 |\nSon söz geldi';
    // Yapı satırlarının [başlangıç, bitiş] karakter aralıkları (çit içi dahil).
    const structural: Array<[number, number]> = [];
    let pos = 0; let inFence = false;
    for (const line of t.split('\n')) {
      const fence = line.startsWith('```');
      if (inFence || fence || line.startsWith('|')) structural.push([pos, pos + line.length]);
      if (fence) inFence = !inFence;
      pos += line.length + 1;
    }
    const stops = revealStops(t, 0);
    for (const st of stops) {
      const last = st - 1; // görünen son karakter
      const inside = structural.some(([a, b]) => last >= a && last < b);
      expect(inside, JSON.stringify(t.slice(0, st))).toBe(false);
    }
    expect(stops.length).toBeGreaterThan(5);
  });
  it(`adım sayısı ≤ ${REVEAL_MAX_STEPS}+1 (8 KB cevapta bile çizim fırtınası yok)`, () => {
    const t = words(1500);
    const s = revealStops(t, 0);
    expect(s.length).toBeLessThanOrEqual(REVEAL_MAX_STEPS + 1);
    expect(s[s.length - 1]).toBe(t.length);
    for (let i = 1; i < s.length; i++) expect(s[i]).toBeGreaterThan(s[i - 1]);
  });
  it('from sonrası: önek zaten görünür, noktalar ondan büyük', () => {
    const t = 'görünen önek ' + words(30);
    const s = revealStops(t, 13);
    expect(s.every(x => x > 13)).toBe(true);
    expect(revealStops(t, t.length)).toEqual([]);
  });
});

describe('revealIndexAt — doğrusal ilerleme, tekdüze', () => {
  it('p=0 → from, p≥1 → tamamı, arada artan ve hep bir kesme noktası', () => {
    const t = words(80);
    const s = revealStops(t, 0);
    expect(revealIndexAt(s, 0, 0)).toBe(0);
    expect(revealIndexAt(s, 0, 1)).toBe(t.length);
    expect(revealIndexAt(s, 0, 7)).toBe(t.length);
    let prev = 0;
    for (let p = 0.05; p < 1; p += 0.05) {
      const i = revealIndexAt(s, 0, p);
      expect(i).toBeGreaterThanOrEqual(prev);
      if (i > 0) expect(s).toContain(i);
      prev = i;
    }
    expect(revealIndexAt(s, 0, 0.5)).toBeGreaterThan(t.length * 0.3);
  });
});

describe('kablolama — yalnız CANLI tur damga alır', () => {
  const hook = readFileSync(resolve(__dirname, 'useChatThread.ts'), 'utf8');
  const persist = readFileSync(resolve(__dirname, 'chatPersist.ts'), 'utf8');
  it('delta ve answer işleyicileri withArrival kullanır', () => {
    expect(hook).toContain('patchLast(t => withArrival(t, applyChatDelta(t.text, e), false));');
    expect(hook).toContain('patchLast(t => ({ ...withArrival(t, e.text, true),');
  });
  it('arşivden geri yükleme (restoreTurns) damga yazmaz → geçmiş tur yazılmaz', () => {
    expect(persist).not.toContain('arrivedAt');
  });
});
