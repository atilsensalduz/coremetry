// aiVoteRemoved.ratchet.test.ts — v0.10.1393 kapısı.
//
// Operatör: "Faydalı faydasız kaldıralım." AI cevaplarının altındaki 👍/👎
// (faydalı / faydasız) rayı HER yüzeyden kaldırıldı — ✨ Explain kartı, sohbet
// balonu, AI çekmecesi, CoSRE, insight kartları, RCA paneli, sayfa-içi AI
// anlatımları. Politika: oy zayıf sinyal; ne çalışma zamanı kararını ne
// değerlendirme etiketini sürer, dolayısıyla toplamak için arayüz koymayız.
//
// Bu ratchet rayın GERİ SIZMASINI yakalar: üretim kaynağında (test dışı) oy
// atomu, oy POST'u ya da faydalı/faydasız düğmesi görünmez. Geçmiş oylar /ai
// sayfasında OKUNUR (Geri bildirim / Oylanan çağrı kolonları) — o okumalar
// bu desenlere takılmaz (GET /api/ai/feedback/negative, exchangeView).
import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';

const SRC = resolve(__dirname, '..', '..');

function walk(dir: string, out: string[] = []): string[] {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = resolve(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(e.name) && !/\.test\.tsx?$/.test(e.name)) out.push(p);
  }
  return out;
}

const stripComments = (s: string) =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

// Her desen bir geri-sızma biçimi: atomun adı, istemci metodu, oy ucu
// (yalnız tam yol — /negative okuması serbest), emoji ikonlu düğme ve
// eski etiket / soru cümleleri.
const BANNED: Array<[RegExp, string]> = [
  [/AIFeedbackButtons/, 'oy atomu (AIFeedbackButtons)'],
  [/postAIFeedback/, 'oy istemci metodu (postAIFeedback)'],
  [/['"`]\/api\/ai\/feedback['"`?]/, 'oy yazma ucu (POST /api/ai/feedback)'],
  [/icon=\{?["'`](👍|👎)/, '👍/👎 ikonlu düğme'],
  [/[Ff]aydal[ıi] işaretle|[Ff]aydasız işaretle/, 'faydalı/faydasız düğme etiketi'],
  [/yararlı mıydı\?|Bu değerlendirme doğru mu\?/, 'oy sorusu'],
];

describe('v0.10.1393 — AI yüzeylerinde 👍/👎 yok', () => {
  it('oy atomu dosyası silindi', () => {
    expect(existsSync(resolve(SRC, 'components/ai/AIFeedbackButtons.tsx'))).toBe(false);
  });

  it('hiçbir üretim kaynağı oy rayı çizmez ya da oy göndermez', () => {
    const offenders: string[] = [];
    for (const f of walk(SRC)) {
      const s = stripComments(readFileSync(f, 'utf8'));
      for (const [re, why] of BANNED) {
        if (re.test(s)) offenders.push(`${f.replace(SRC, '')}: ${why}`);
      }
    }
    expect(offenders).toEqual([]);
  });
});
