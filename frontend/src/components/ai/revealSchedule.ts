// revealSchedule — v0.10.1384 (operatör: "Cevabı yazarken yazma efekti olsa
// hızlı — daha çok LLM cevabı değil gerçekten düşünüyor hissiyatı olur").
//
// SAF çekirdek: React yok, DOM yok. Kanca (useTypewriter.ts) zamanı ve
// çizimi yönetir; ne zaman, nereye kadar, hangi sınırda kesileceği burada.
//
// Kurallar (operatör sözleşmesi):
//   • Yalnız BÜYÜK PARÇA açılır: tüm cevap tek seferde (yönlendirilmiş /
//     tamponlu cevap) ya da ≥ REVEAL_MIN_CHUNK karakterlik tek delta. Gerçek
//     token akışı (küçük delta'lar) olduğu gibi kalır — çift efekt yok.
//   • Toplam süre uzunluktan bağımsız ≤ REVEAL_CAP_MS (≈1,2 sn): 8 KB'lık
//     bir rapor da kısa bir cevap da operatörü bekletmez.
//   • Kesme noktası KELİME sınırı; markdown güvenli: açık `kod`, **kalın**,
//     [bağlantı](…) ortasında kesilmez; kod çiti ve tablo satırı içinde yalnız
//     SATIR SONUNDA kesilir. Yarım satır / açık çit / yarım tablo kararı
//     çözücünün akış kuralında (chatMarkdown.ts, Markdown.tsx) — açılırken
//     çizim `streaming=true` ile yapılır, yani aynı kurallar yeniden kullanılır.
//   • Adım sayısı ≤ REVEAL_MAX_STEPS: karakter başına çizim fırtınası yok.
import type { ChatArrival } from '@/lib/types';

/** Bu kadar ya da daha uzun tek delta "büyük parça" sayılır. */
export const REVEAL_MIN_CHUNK = 240;
/** Uzunluktan bağımsız toplam açılma tavanı (ms). */
export const REVEAL_CAP_MS = 1200;
/** Kısa cevabın bile algılanır bir yazılışı olsun diye taban (ms). */
export const REVEAL_MIN_MS = 220;
/** Tavan altındaki hız: karakter başına ms (≈300 karakter tavana değer). */
export const REVEAL_MS_PER_CHAR = 4;
/** Bir açılıştaki en fazla çizim adımı (çözücü önbelleğini de korur). */
export const REVEAL_MAX_STEPS = 48;

/** a ile b'nin ortak önek uzunluğu. */
export function commonPrefixLen(a: string, b: string): number {
  const n = Math.min(a.length, b.length);
  let i = 0;
  while (i < n && a.charCodeAt(i) === b.charCodeAt(i)) i++;
  return i;
}

/**
 * nextArrival — metin `prevText` → `nextText` olurken yeni bir varış damgası
 * gerekiyor mu? `whole` = cevabın tamamı (answer çerçevesi). Damga YALNIZ
 * büyük parçada değişir; aksi hâlde `prev` aynen döner (küçük delta'lar ve
 * akışı tekrarlayan nihai `answer` mevcut açılışı bozmaz).
 */
export function nextArrival(
  prevText: string, nextText: string, prev: ChatArrival | undefined, now: number, whole: boolean,
): ChatArrival | undefined {
  const common = commonPrefixLen(prevText, nextText);
  const added = nextText.length - common;
  if (added <= 0) return prev;
  // Tüm cevap, öncesinde hiç metin akmamışken geldi: uzunluğu ne olursa olsun aç.
  if (whole && prevText.trim() === '') return { at: now, from: 0 };
  if (added >= REVEAL_MIN_CHUNK) return { at: now, from: common };
  return prev;
}

/** Açılacak karakter sayısı → süre (ms), [REVEAL_MIN_MS, REVEAL_CAP_MS]. */
export function revealDurationMs(chars: number): number {
  if (chars <= 0) return 0;
  return Math.min(REVEAL_CAP_MS, Math.max(REVEAL_MIN_MS, chars * REVEAL_MS_PER_CHAR));
}

const FENCE_RE = /^\s{0,3}(`{3,}|~{3,})/;

/**
 * revealStops — `from` sonrasındaki güvenli kesme noktaları (artan, tekil;
 * son eleman her zaman text.length). Nokta = görünür önekin uzunluğu.
 */
export function revealStops(text: string, from: number): number[] {
  const len = text.length;
  const start = Math.max(0, Math.min(from, len));
  if (start >= len) return [];
  const raw: number[] = [];
  let inFence = false;
  let lineStart = 0;
  while (lineStart < len) {
    const nl = text.indexOf('\n', lineStart);
    const lineEnd = nl < 0 ? len : nl;
    const next = nl < 0 ? len : nl + 1;
    const line = text.slice(lineStart, lineEnd);
    const fence = FENCE_RE.test(line);
    const table = line.trimStart().startsWith('|');
    if (inFence || fence || table) {
      // Çit/tablo: yalnız satır sonu. Yapıyı kuran karakterler tek tek akmaz.
      raw.push(next);
      if (fence) inFence = !inFence;
    } else {
      // Düzyazı: boşluk koşusunun SONU, satır-içi işaret dengedeyken.
      let code = false; let bold = false; let link = 0; let url = false;
      for (let i = 0; i < line.length; i++) {
        const c = line[i];
        if (c === '`') { code = !code; continue; }
        if (code) continue;
        if (c === '*' && line[i + 1] === '*') { bold = !bold; i++; continue; }
        if (c === '[') { link++; continue; }
        if (c === ']' && link > 0) { link--; if (line[i + 1] === '(') { url = true; i++; } continue; }
        if (c === ')' && url) { url = false; continue; }
        if ((c === ' ' || c === '\t') && line[i + 1] !== ' ' && line[i + 1] !== '\t') {
          if (!bold && link === 0 && !url) raw.push(lineStart + i + 1);
        }
      }
      raw.push(next);
    }
    lineStart = next;
  }
  if (raw[raw.length - 1] !== len) raw.push(len);
  // Önce `from` sonrası + tekil, sonra karakter kovalarına seyrelt.
  const out: number[] = [];
  const minGap = (len - start) / REVEAL_MAX_STEPS;
  let last = start;
  for (const s of raw) {
    if (s <= start) continue;
    if (s === len) { if (out[out.length - 1] !== len) out.push(len); break; }
    if (s - last >= minGap) { out.push(s); last = s; }
  }
  if (out[out.length - 1] !== len) out.push(len);
  return out;
}

/**
 * revealIndexAt — ilerleme p∈[0,1] için görünür önek uzunluğu: hedef
 * karakterin altındaki en büyük kesme noktası (doğrusal hız). p≥1 → tamamı.
 */
export function revealIndexAt(stops: readonly number[], from: number, progress: number): number {
  if (stops.length === 0) return from;
  const end = stops[stops.length - 1];
  const start = Math.max(0, Math.min(from, end));
  if (progress >= 1) return end;
  if (progress <= 0) return start;
  const target = start + (end - start) * progress;
  let lo = 0; let hi = stops.length - 1; let best = start;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (stops[mid] <= target) { best = stops[mid]; lo = mid + 1; } else hi = mid - 1;
  }
  return best;
}

/**
 * withArrival — useChatThread'in akış işleyicileri için: metni yaz ve
 * gerekiyorsa varış damgasını güncelle. Damga yoksa alan HİÇ eklenmez
 * (tur nesnesi bayt bayt eskisi gibi kalır).
 */
export function withArrival<T extends { text?: string; arrivedAt?: ChatArrival }>(
  t: T, nextText: string, whole: boolean, now: number = Date.now(),
): T {
  const a = nextArrival(t.text ?? '', nextText, t.arrivedAt, now, whole);
  return a && a !== t.arrivedAt ? { ...t, text: nextText, arrivedAt: a } : { ...t, text: nextText };
}
