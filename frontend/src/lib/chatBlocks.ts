// chatBlocks.ts — v0.10.541 (Faz 3.3a): tipli blok biriktirme, SAF.
// Aynı id ikinci kez gelirse (final güncellemesi) yerine geçer; sıra seq.
import type { ChatTypedBlock, ChatAnswerLink, CosreChartSpecLike, ChatTraceListPayload, ChatOperationPayload, ChatEvidenceChartPayload, ChatEvidenceTablePayload } from './types';
import { isSafeAppPath } from './safeAppPath';

export function appendChatBlock(blocks: ChatTypedBlock[] | undefined, b: ChatTypedBlock): ChatTypedBlock[] {
  const rest = (blocks ?? []).filter(x => x.id !== b.id);
  rest.push(b);
  return rest.sort((a, c) => a.seq - c.seq);
}

export function chartBlocks(blocks: ChatTypedBlock[] | undefined): CosreChartSpecLike[] {
  return (blocks ?? []).filter(b => b.type === 'chart').map(b => b.payload as CosreChartSpecLike)
    .filter(p => p && typeof p.service === 'string' && typeof p.agg === 'string');
}

/** v0.10.1314 — ChatBubble http(s) olmayan her href'i `<Link to>` çizer; yalnız
 *  http(s) ya da isSafeAppPath geçen link kalır ("/\evil.com" react-router v6'da
 *  window.location.assign ile dışarı açılırdı). */
function linkHrefOk(href: string): boolean {
  return /^https?:\/\//i.test(href) || isSafeAppPath(href);
}

/** Link blokları cevap link çiplerine katılır; href tekil. Güvensiz href (iki kaynakta da) düşer. */
export function mergeBlockLinks(links: ChatAnswerLink[] | undefined, blocks: ChatTypedBlock[] | undefined): ChatAnswerLink[] {
  const out: ChatAnswerLink[] = (links ?? []).filter(l => typeof l.href === 'string' && linkHrefOk(l.href));
  const seen = new Set(out.map(l => l.href));
  for (const b of blocks ?? []) {
    if (b.type !== 'link') continue;
    const p = b.payload as Partial<ChatAnswerLink> | null;
    if (p && typeof p.href === 'string' && p.href && linkHrefOk(p.href) && !seen.has(p.href)) {
      seen.add(p.href);
      out.push({ label: typeof p.label === 'string' && p.label ? p.label : p.href, href: p.href });
    }
  }
  return out;
}

/** v0.10.688 — trace_list blokları (endpoint_traces.go); şekil doğrulanır, bozuk yük atlanır.
 *  v0.10.1272 — kanıt çipine bağlı (evidenceRef) listeler burada DEĞİL, çipin altında çizilir.
 *  v0.10.1314 — deepLink `<Link to>` olur: isSafeAppPath geçmeyen yük düşer. */
export function traceListBlocks(blocks: ChatTypedBlock[] | undefined): ChatTraceListPayload[] {
  return (blocks ?? []).filter(b => b.type === 'trace_list').map(b => b.payload as ChatTraceListPayload)
    .filter(p => p && Array.isArray(p.traces) && typeof p.deepLink === 'string' && isSafeAppPath(p.deepLink) && !p.evidenceRef);
}

/** v0.10.1272 — kanıt çipine bağlı sohbet içi blok (kanıt zarflı araçların verisi). */
export type EvidenceBlockView =
  | { type: 'chart'; payload: ChatEvidenceChartPayload }
  | { type: 'table'; payload: ChatEvidenceTablePayload }
  | { type: 'trace_list'; payload: ChatTraceListPayload };

/** evidenceBlocksByRef — evidenceRef → blok (şekil doğrulanır; bozuk yük düşer). SAF. */
export function evidenceBlocksByRef(blocks: ChatTypedBlock[] | undefined): Map<string, EvidenceBlockView> {
  const out = new Map<string, EvidenceBlockView>();
  for (const b of blocks ?? []) {
    const p = b.payload as { evidenceRef?: unknown } | null;
    const ref = p && typeof p.evidenceRef === 'string' ? p.evidenceRef : '';
    if (!ref) continue;
    if (b.type === 'chart') {
      const c = b.payload as ChatEvidenceChartPayload;
      if (Array.isArray(c.series) && c.series.length > 0) out.set(ref, { type: 'chart', payload: c });
    } else if (b.type === 'table') {
      const t = b.payload as ChatEvidenceTablePayload;
      if (Array.isArray(t.columns) && Array.isArray(t.rows) && t.rows.length > 0) out.set(ref, { type: 'table', payload: t });
    } else if (b.type === 'trace_list') {
      const l = b.payload as ChatTraceListPayload;
      if (Array.isArray(l.traces) && l.traces.length > 0 && typeof l.deepLink === 'string' && isSafeAppPath(l.deepLink)) out.set(ref, { type: 'trace_list', payload: l }); // v0.10.1314
    }
  }
  return out;
}

// operationBlocks — operasyon ön-getirme kartı (geçersiz yük düşer).
// v0.10.1314 — tracesHref `<Link to>` olur: isSafeAppPath geçmeyen yük düşer.
export function operationBlocks(blocks: ChatTypedBlock[] | undefined): ChatOperationPayload[] {
  return (blocks ?? []).filter(b => b.type === 'operation').map(b => b.payload as ChatOperationPayload)
    .filter(p => p && typeof p.operation === 'string' && typeof p.tracesHref === 'string' && isSafeAppPath(p.tracesHref) && p.current != null);
}
