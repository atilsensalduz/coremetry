import { useMemo, type ReactNode } from 'react';
import { cellNoWrap, cellText, numericColumn, plainCell, wbrSegments } from './mdTableCell'; // v0.10.1346 — tablo hücresi kararları
import type { MdTableData } from './mdTableParse';

// MdTable (v0.9.1148) — markdown tablosu GERÇEK tablo (sohbet markdown'ı
// tablo standardından muaf: kolonlar modelin o cevapta uydurduğu başlıklar).
// Kaydırma kabı `.table-wrap` (yatay taşma TABLONUN kabında), hizalama
// `.num`, zebra + kenarlık `.cm-md-table`.
//
// v0.10.1357 — ChatBubble.tsx'ten TAŞINDI: RenderedMarkdown (CopilotExplain /
// ExplainBody — istisna/trace açıklaması, kod incelemesi) da aynı tabloyu
// çiziyor, ikinci bir tablo bileşeni YOK. İki renderer'ın tek farkı satır içi
// işaretleme: `inline` hücrenin satır içi çizimini (sohbette MdInline +
// chatLinks politikası, Markdown.tsx'te renderInline + appLinkKind şema
// listesi) çağırana bırakır; link güvenliği her yüzeyin kendi kapısında
// kalır. `plain` hücrenin düz metin yolunu (<wbr>) seçer; varsayılan
// mdTableCell.plainCell.
export function MdTable({ block, inline, plain = plainCell }: {
  block: MdTableData;
  /** Hücrenin satır içi çizimi (başlık + işaretli gövde hücresi). */
  inline: (text: string) => ReactNode;
  /** İşaretsiz hücrenin metni (→ <wbr>'li düz çizim), işaretliyse null (→ `inline`). */
  plain?: (raw: string) => string | null;
}) {
  // v0.10.1346 — sayısal kolon `.num`, kısa değer `.cm-td-nw` (bölünmez), uzun kimlik <wbr> (mdTableCell.ts).
  const num = useMemo(() => block.head.map((_, i) => block.align[i] === 'right' || (block.align[i] === 'left' && numericColumn(block.rows, i))), [block]);
  const cells = useMemo(() => block.rows.map(r => r.map(c => ({ plain: plain(c), nw: cellNoWrap(cellText(c)) }))), [block.rows, plain]);
  const cls = (i: number) => (num[i] ? 'num' : block.align[i] === 'center' ? 'ta-c' : undefined);
  // 100+ satır kuralı (ev kısıtı): content-visibility bedelsiz.
  const heavy = block.rows.length > 100;
  const rowStyle = heavy
    ? { contentVisibility: 'auto' as const, containIntrinsicSize: '26px' }
    : undefined;
  return (
    <div className="table-wrap cm-md-tw">
      <table className="cm-md-table">
        <thead>
          <tr>{block.head.map((h, i) => <th key={i} className={cls(i)}>{inline(h)}</th>)}</tr>
        </thead>
        <tbody>
          {block.rows.map((r, k) => (
            <tr key={k} style={rowStyle}>
              {r.map((c, i) => { const m = cells[k][i]; return ( // v0.10.1346
                <td key={i} className={[cls(i), m.nw && 'cm-td-nw'].filter(Boolean).join(' ') || undefined}>
                  {m.plain === null ? inline(c) : <span>{wbrSegments(m.plain).flatMap((s, j) => (j ? [<wbr key={j} />, s] : [s]))}</span>}
                </td>); })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
