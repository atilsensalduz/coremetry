// v0.10.1357 — paylaşılan GFM tablo çözücüsü (mdTableParse.ts) SAF testleri.
//
// Çözücü sohbetten (chatMarkdown.ts) taşındı; RenderedMarkdown (CopilotExplain /
// ExplainBody) de aynı fonksiyonu çağırıyor. Bu dosya kapının kendisini
// piner: başlık çubukla başlar, ayraç TAM ve hücre sayısı eşit, yarım satır
// tabloya alınmaz; chatMarkdown'ın tablo bloğu birebir bu çıktı.
import { describe, expect, it } from 'vitest';
import { alignOf, isDelimRow, isRowLine, parseTableAt, splitRow } from './mdTableParse';
import { parseChatBlocks, splitRow as chatSplitRow } from './chatMarkdown';

const all = (lines: readonly string[]) => (k: number) => k < lines.length;

describe('parseTableAt (v0.10.1357)', () => {
  const L = ['| Servis | p95 | durum |', '|:---|---:|:---:|', '| checkout | 480 | ok |', '| payment | 1200 |', 'sonra düzyazı'];

  it('başlık + hizalar + gövde; kısa satır doldurulur, tablodan sonraki satır indeksi döner', () => {
    const r = parseTableAt(L, 0, all(L));
    expect(r).not.toBeNull();
    expect(r!.table.head).toEqual(['Servis', 'p95', 'durum']);
    expect(r!.table.align).toEqual(['left', 'right', 'center']);
    expect(r!.table.rows).toEqual([['checkout', '480', 'ok'], ['payment', '1200', '']]);
    expect(r!.next).toBe(4);
  });

  it('ayraç yoksa / hücre sayısı tutmazsa / başlık çubukla başlamazsa tablo YOK', () => {
    expect(parseTableAt(['| a | b |', 'düz'], 0, () => true)).toBeNull();
    expect(parseTableAt(['| a | b |', '|---|'], 0, () => true)).toBeNull();
    expect(parseTableAt(['hızlı | yavaş', '---|---'], 0, () => true)).toBeNull();
    expect(parseTableAt(['| önemli | not |', '---'], 0, () => true)).toBeNull();
  });

  it('ayraç satırı YARIMSA tablo başlamaz (akış: başlık o ana dek düz metin)', () => {
    const lines = ['| a | b |', '|---|---|'];
    expect(parseTableAt(lines, 0, k => k < lines.length && k !== 1)).toBeNull();
    expect(parseTableAt(lines, 0, all(lines))!.table.rows).toEqual([]);
  });

  it('yarım gövde satırı tabloya ALINMAZ; next onu gösterir', () => {
    const lines = ['| a | b |', '|---|---|', '| 1 | 2 |', '| 3 | 4'];
    const r = parseTableAt(lines, 0, k => k < lines.length && k !== 3)!;
    expect(r.table.rows).toEqual([['1', '2']]);
    expect(r.next).toBe(3);
  });

  it('kaçırılmış çubuk ve kod aralığındaki çubuk bölmez; fazla hücre korunur', () => {
    expect(splitRow('| a \\| b | c |')).toEqual(['a | b', 'c']);
    expect(splitRow('| `p95 | p99` | 12 |')).toEqual(['`p95 | p99`', '12']);
    const lines = ['| a | b |', '|---|---|', '| 1 | 2 | 3 |'];
    expect(parseTableAt(lines, 0, all(lines))!.table.rows).toEqual([['1', '2', '3']]);
  });

  it('yardımcı kapılar', () => {
    expect(isRowLine('  | x |')).toBe(true);
    expect(isRowLine('x | y')).toBe(false);
    expect(isDelimRow('---|---:', 2)).toBe(true);
    expect(isDelimRow('| :-: | - |', 2)).toBe(true);
    expect(isDelimRow('| a | - |', 2)).toBe(false);
    expect(alignOf(':--')).toBe('left');
    expect(alignOf('--:')).toBe('right');
    expect(alignOf(':-:')).toBe('center');
  });
});

describe('sohbet çözücüsü aynı çözücüyü kullanır (v0.10.1357)', () => {
  it('chatMarkdown.splitRow geriye dönük dışa aktarım', () => {
    expect(chatSplitRow).toBe(splitRow);
  });

  it('parseChatBlocks tablo bloğu = parseTableAt çıktısı', () => {
    const lines = ['| Servis | p95 |', '|---|---:|', '| checkout | 480 |'];
    const b = parseChatBlocks(lines.join('\n') + '\n').find(x => x.kind === 'table');
    expect(b).toEqual({ kind: 'table', ...parseTableAt(lines, 0, all(lines))!.table });
  });
});
