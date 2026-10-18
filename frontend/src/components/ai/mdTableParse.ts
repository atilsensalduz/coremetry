// mdTableParse — v0.10.1357: GFM tablo çözümü, SAF (mdTableParse.test.ts piner).
//
// v0.10.1346'nın notu ("CopilotExplain markdown tabloyu hiç çizmiyor — ayrı
// iş"): istisna/trace açıklaması ve kod incelemesi cevabındaki `| a | b |`
// tablosu RenderedMarkdown'da (components/Markdown.tsx) düz paragraf
// satırları olarak görünüyordu. Çözücü sohbetin (chatMarkdown.ts,
// v0.9.1148) çözücüsüydü; buraya TAŞINDI ve iki renderer da bunu çağırıyor —
// ikinci bir tablo çözücüsü YOK. Çizim tarafı da ortak: MdTable.tsx.
//
// Burada React yok, DOM yok: yalnız satır dizisi → tablo verisi.

export type MdTableAlign = 'left' | 'center' | 'right';

/** Çözülmüş tablo: başlık hücreleri, kolon hizaları, gövde satırları (ham markdown hücreler). */
export interface MdTableData { head: string[]; align: MdTableAlign[]; rows: string[][] }

/** Satır bir tablo satırı gibi mi duruyor: `|` ile BAŞLIYOR mu. */
export function isRowLine(line: string): boolean {
  return line.trim().startsWith('|');
}

// Ayraç satırı: `|---|---:|`. Kural GFM'in kendisi — hücrelerin HEPSİ
// tire (istenirse iki yanında hizalama iki noktası) ve hücre SAYISI
// başlıkla aynı.
//
// Sayı eşitliği asıl kapı: onsuz `| önemli | not |` satırının altındaki
// bir `---` yatay çizgisi ayraç sanılır ve düzyazı boş bir tabloya
// dönüşür. Baştaki çubuğu ŞART koşmuyoruz (model bazen `---|---:`
// yazıyor) — yanlış-pozitife karşı asıl çapa BAŞLIK satırının çubukla
// başlaması (isRowLine), yani "hızlı | yavaş" gibi bir cümle hiçbir
// hâlde tablo başlatmıyor.
export function isDelimRow(line: string, ncols: number): boolean {
  const s = line.trim();
  if (!/-/.test(s)) return false;
  if (!/^[|\s:-]+$/.test(s)) return false;
  const cells = splitRow(s);
  return cells.length === ncols && cells.every(c => /^:?-+:?$/.test(c));
}

// splitRow — bir tablo satırını hücrelere böler. İki tuzağı biliyor:
//   `\|`      → kaçırılmış çubuk, hücre İÇERİĞİ (ayırıcı değil).
//   `` `a|b` `` → kod aralığındaki çubuk bölmez; aksi hâlde model
//              `p95 | p99` gibi bir kod parçası yazdığında kolonlar
//              kayardı.
export function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith('|')) s = s.slice(1);
  if (s.endsWith('|') && !s.endsWith('\\|')) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = '';
  let inCode = false;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === '\\' && s[i + 1] === '|') { cur += '|'; i++; continue; }
    if (c === '`') { inCode = !inCode; cur += c; continue; }
    if (c === '|' && !inCode) { cells.push(cur.trim()); cur = ''; continue; }
    cur += c;
  }
  cells.push(cur.trim());
  return cells;
}

export function alignOf(cell: string): MdTableAlign {
  const s = cell.trim();
  const l = s.startsWith(':');
  const r = s.endsWith(':');
  if (l && r) return 'center';
  if (r) return 'right';
  return 'left';
}

/**
 * parseTableAt — `lines[i]` bir tablonun başlığıysa tabloyu çözer.
 *
 * Kapı: `|…` başlığı + hemen ardından TAM bir ayraç satırı. `full(k)` =
 * k. satır var VE akışın yarım (henüz `\n` gelmemiş) satırı değil; ayraç
 * yarımken tablo BAŞLAMAZ (başlık o ana dek düz metin), yarım gövde satırı
 * tabloya ALINMAZ — yarım bir satırı satır olarak basmak kolon
 * genişliklerini her delta'da zıplatır. Kısa satır doldurulur; FAZLA hücre
 * KORUNUR (içerik yutmayız — tablo bir kolon geniş çizilir, o kolonun
 * başlığı boş kalır).
 *
 * Dönüş: tablo + tablodan sonraki ilk satırın indeksi; tablo yoksa null.
 */
export function parseTableAt(
  lines: readonly string[], i: number, full: (k: number) => boolean,
): { table: MdTableData; next: number } | null {
  const line = lines[i];
  const head = line !== undefined && isRowLine(line) ? splitRow(line) : null;
  if (!head || !full(i + 1) || !isDelimRow(lines[i + 1], head.length)) return null;
  const align = splitRow(lines[i + 1]).map(alignOf);
  const rows: string[][] = [];
  let j = i + 2;
  while (full(j) && isRowLine(lines[j])) {
    const cells = splitRow(lines[j]);
    while (cells.length < head.length) cells.push('');
    rows.push(cells);
    j++;
  }
  return { table: { head, align, rows }, next: j };
}
