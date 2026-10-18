// mathEscapes — LLM'in bastığı LaTeX kaçışlarını okunur karaktere çevirir
// (v0.10.12, operatör bildirimi).
//
// Belirti: "Explain root cause" panelinde akış zinciri şöyle görünüyordu:
//
//     servis-a $\rightarrow$ servis-b $\rightarrow$ servis-c
//
// Yani ekranda ok yerine LaTeX kaynağı. Operatörün okuduğu tek satır —
// hatanın hangi servisten hangisine yayıldığı — gürültüye gömülüyordu.
//
// ── NEDEN RENDERER TARAFINDA ────────────────────────────────────────────
//
// Prompt'a "LaTeX kullanma" yazmak akla ilk gelen çözüm ve YETMEZ: bu
// kurulumda model yerel ve küçük, ve küçük modeller biçim talimatlarını
// güvenilir biçimde izlemiyor (hafıza: "küçük-model: prefetch+narrate >
// tool-loop"). Renderer tarafı ise modelden BAĞIMSIZ çalışır — bugün
// gemma, yarın başka bir model, aynı davranış.
//
// Prompt'a bir satır eklemek yine de zararsız ve gürültüyü kaynağında
// azaltır; ama TEK BAŞINA düzeltme sayılamaz, o yüzden bu dosya var.
//
// ── NEDEN GENEL BİR LaTeX AYRIŞTIRICI DEĞİL ─────────────────────────────
//
// Kapsam KASITLI olarak dar: bilinen komut listesi. `$...$` içindeki her
// şeyi işlemek, metindeki dolar işaretlerini (fiyat, `$PATH`, shell
// örneği) bozardı — operatör metni bunları gerçekten içeriyor. Bilinen
// komut yoksa dizge AYNEN kalıyor: tanınmayan girdi sessizce
// değiştirilmez.

// ── v0.10.1345 GENİŞLETME (operatör bildirimi, prod /cosre, gemma4 31b) ──
//
// Ekran: "ACT (Active) $\rightarrow$ RCV (Received) $\rightarrow$ DIP …".
// Operatör: "Rightarrow'u düzelt". Bu dosya v0.10.12'den beri VARDI ama
// yalnız RenderedMarkdown (CopilotExplain) çağırıyordu; sohbet balonu
// (renderMessage → parseChatBlocks) kendi çözücüsüyle çiziyor ve bu
// dosyadan HABERSİZDİ — aynı kusur ikinci yüzeyde geri geldi. Kök neden:
// düzeltme bir yüzeye takılmıştı, model-markdown SINIFINA değil.
//
// Artık TEK yardımcı, her model-markdown yüzeyi bunu çağırıyor:
//   - sohbet balonu (akış + son cevap) ve kullanıcı/paylaşılan tur:
//     ChatBubble.renderMessage; panoya kopya: chatMarkdown.chatPlainText;
//   - CopilotExplain / ExplainBody / her RenderedMarkdown + stripMarkdown.
//
// Kurallar (tablo testi: mathEscapes.test.ts):
//   1. KOD DOKUNULMAZ: ``` çitli bloklar (girintili çit dahil, kapanmamış
//      çit metnin sonuna kadar kod) ve `satır içi kod` aynen kalır.
//      Eskiden metnin TAMAMI çevriliyordu — kod örneğindeki `\to` da.
//   2. `$…$` dolarları YALNIZ içerik bir komutla BAŞLIYOR ve tamamı
//      komut / sayı / boşluk ise soyulur: "$\rightarrow$" → "→",
//      "$\geq 5$" → "≥ 5". "$5", "$10 and $20", "$10 \to $20" (para)
//      dolarlarını korur: dolardan hemen sonra rakam/harf para ya da
//      shell değişkeni olabilir, ayırt edilemez.
//   3. KARAR — "$x \geq 5$": komut çevrilir, x ve dolarlar KALIR →
//      "$x ≥ 5$". Harfle başlayan içerik `$HOME` gibi metin olabilir;
//      dolar soymak geri alınamaz bilgi kaybı, bırakmak yalnız görsel
//      gürültü. Okunması gereken sembol her durumda düzelir.
//   4. `\( … \)` LaTeX'e özgü (para karışıklığı yok): içerik çevrildikten
//      sonra ters bölü kalmadıysa sınırlayıcılar soyulur.
//   5. Kaçışlar: `\%` → %, `\&` → &; `\_` → _ yalnız KELİME İÇİNDE
//      (snake\_case). Kelime sınırındaki `\_` markdown kaçışı olarak kalır
//      — sohbet çözücüsü onu zaten çözüyor ve önden açmak `\_\_init\_\_`
//      → `__init__` (kalın!) yapardı.
//   6. AKIŞ: çağıran her çizimde BİRİKMİŞ metni verir (delta değil). Akış
//      sürerken metnin SONUNDAKİ yarım komut ("$\righ", "\le" — `\leq` ya
//      da `\leftarrow` olabilir) gizlenir; sonraki delta tamamlar. Böylece
//      "$\righ" ham görünmez, "\le" önce ≤ sonra ← diye zıplamaz.

/** Küçük modellerin fiilen bastığı komutlar. Genişletmeden ÖNCE ölç. */
const COMMANDS: Record<string, string> = {
  rightarrow: '→', to: '→', Rightarrow: '⇒', longrightarrow: '⟶', Longrightarrow: '⟹',
  leftarrow: '←', gets: '←', Leftarrow: '⇐', longleftarrow: '⟵',
  leftrightarrow: '↔', Leftrightarrow: '⇔', mapsto: '↦',
  uparrow: '↑', downarrow: '↓', implies: '⇒', iff: '⇔',
  times: '×', cdot: '·', div: '÷', pm: '±',
  approx: '≈', sim: '~', neq: '≠', ne: '≠',
  leq: '≤', le: '≤', geq: '≥', ge: '≥',
  ldots: '…', dots: '…', degree: '°',
  alpha: 'α', beta: 'β', delta: 'δ', Delta: 'Δ', sigma: 'σ', mu: 'μ',
  infty: '∞', pi: 'π',
};

// Uzun adlar ÖNCE denenmeli: `\le` deseni `\leq`in başını yiyebilir ve
// geriye bir `q` bırakır. Sıralama bu yüzden uzunluğa göre.
const NAMES = Object.keys(COMMANDS).sort((a, b) => b.length - a.length);
const CMD = `\\\\(?:${NAMES.join('|')})(?![a-zA-Z])`;
const CMD_RE = new RegExp(`\\\\(${NAMES.join('|')})(?![a-zA-Z])`, 'g');

// `$ \cmd … $` — dolar, boşluk, BİLİNEN komutla başlayan içerik. Sayı/harf
// ile başlayan `$…$` hiç eşleşmez (para/shell). Motor her `$`te yeniden
// denediği için "$5 then $\to$" içindeki ilk `$` ikinciyi yutmaz.
const WRAPPED_RE = new RegExp(`\\$([ \\t]*${CMD}[^$\\n]*?)\\$`, 'g');
// Soyulabilir içerik: komut / sayı (5, 0.5, 99%) / `\%` / boşluk — başka hiçbir şey.
const PURE_RE = new RegExp(`^[ \\t]*${CMD}(?:[ \\t]*(?:${CMD}|-?\\d+(?:[.,]\\d+)?%?|\\\\%))*[ \\t]*$`);
const PAREN_RE = /\\\(([^\n]*?)\\\)/g;
// Akışta sondaki yarım komut (+ onu açan dolar).
const TAIL_RE = /(?:\$[ \t]*)?\\[a-zA-Z]*$/;

function convertProse(s: string): string {
  if (!s.includes('\\')) return s;
  return s
    .replace(WRAPPED_RE, (m, inner: string) => (PURE_RE.test(inner) ? convertBare(inner).trim() : m))
    .replace(PAREN_RE, (m, inner: string) => {
      const c = convertBare(inner);
      return c.includes('\\') ? m : c.trim();
    })
    .replace(CMD_RE, (_m, name: string) => COMMANDS[name])
    .replace(/\\([%&])/g, '$1')
    .replace(/(?<=[\p{L}\p{N}])\\_(?=[\p{L}\p{N}])/gu, '_');
}

function convertBare(s: string): string {
  return s.replace(CMD_RE, (_m, name: string) => COMMANDS[name]).replace(/\\%/g, '%');
}

// convertLine — satır içi kod koşularını (`…`, ``…``) atlayarak çevirir.
// Eşi olmayan backtick düz metindir (iki çözücüyle aynı).
function convertLine(line: string): string {
  if (!line.includes('\\')) return line;
  if (!line.includes('`')) return convertProse(line);
  let out = '';
  let prose = '';
  let i = 0;
  while (i < line.length) {
    if (line[i] !== '`') { prose += line[i++]; continue; }
    let n = 0;
    while (line[i + n] === '`') n++;
    const fence = '`'.repeat(n);
    // Aynı uzunlukta (daha uzun koşunun parçası olmayan) kapanış ara.
    let j = line.indexOf(fence, i + n);
    while (j >= 0 && line[j + n] === '`') {
      let k = j;
      while (line[k] === '`') k++;
      j = line.indexOf(fence, k);
    }
    if (j < 0) { prose += fence; i += n; continue; }
    out += convertProse(prose) + line.slice(i, j + n);
    prose = '';
    i = j + n;
  }
  return out + convertProse(prose);
}

/**
 * normalizeMathEscapes — LaTeX kaçışlarını unicode'a çevirir; kod dokunulmaz.
 *
 * Saf: LLM çıktısı deterministik değil, o yüzden düzeltmenin kendisi
 * deterministik ve tablo-testli olmalı. `streaming`: metin hâlâ akıyor —
 * sondaki yarım komut gizlenir (kural 6).
 */
export function normalizeMathEscapes(s: string, opts?: { streaming?: boolean }): string {
  if (!s || !s.includes('\\')) return s;
  const lines = s.split('\n');
  let inFence = false;
  for (let k = 0; k < lines.length; k++) {
    const l = lines[k];
    if (l.trimStart().startsWith('```')) { inFence = !inFence; continue; }
    if (inFence) continue;
    let line = l;
    if (opts?.streaming && k === lines.length - 1) line = line.replace(TAIL_RE, '');
    lines[k] = convertLine(line);
  }
  return lines.join('\n');
}
