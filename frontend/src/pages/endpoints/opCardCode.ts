import type { DataTableColumn } from '@/lib/dataTable';
import type { AICodeContext, CallSite, DTOField, OpCallEdge, OperationCardCode } from '@/lib/types';
import { targetLabel } from './opCallTreeCols';

// opCardCode — v0.10.1506 (operasyon kartı R4, kod yarısı). "Kod" açılır
// bölümünün SAF yarısı: çağrı yeri kolonları, DTO satırları, eşleşen ağaç
// satırı, dürüst not, kaynak satırı (CodeSourceFooter) alt kümesi.
// Bileşen (OperationCardCodeSection.tsx) yalnız çizer.
//
// KOD GÖVDESİ YOK: payload'da yalnız konum / imza / alan adı + tipi /
// alıcı.metot / literal rota-topic var (Go: operationCardCodeDTO). Burada
// da hiçbir alan "içerik" diye çizilmez.

/** Çağrı yeri kolonları — etiketler çeviriden. Sunucu sırası dosya:satır. */
export function callSiteCols(tr: (k: string) => string): DataTableColumn<CallSite>[] {
  return [
    { id: 'loc', label: tr('endpoint.opcard.code.colLoc'), sortValue: r => `${r.path}:${String(r.line).padStart(6, '0')}`, naturalDir: 'asc', flex: true, minWidth: 180, priority: 1 },
    { id: 'kind', label: tr('endpoint.opcard.code.colKind'), sortValue: r => r.kind, naturalDir: 'asc', width: 92, minWidth: 70, priority: 2 },
    { id: 'callee', label: tr('endpoint.opcard.code.colCallee'), sortValue: r => r.callee, naturalDir: 'asc', width: 220, minWidth: 140, priority: 1 },
    { id: 'target', label: tr('endpoint.opcard.code.colTarget'), sortValue: r => r.target ?? '', naturalDir: 'asc', width: 180, minWidth: 110, priority: 3 },
    { id: 'match', label: tr('endpoint.opcard.code.colMatch'), sortValue: r => r.matchedChild, width: 200, minWidth: 120, priority: 2 },
  ];
}

/** fileTail — SAF: yolun son iki parçası ("service/OrderServiceImpl.java"). */
export function fileTail(path: string): string {
  const segs = path.split('/').filter(Boolean);
  return segs.slice(-2).join('/');
}

/** matchedEdge — SAF: çağrı yerinin eşleştiği ağaç satırı (yoksa null). */
export function matchedEdge(site: CallSite, children: OpCallEdge[] | null | undefined): OpCallEdge | null {
  if (!children || site.matchedChild < 0 || site.matchedChild >= children.length) return null;
  return children[site.matchedChild];
}

/** matchedLabel — SAF: "payments-api · POST /payments/authorize". */
export function matchedLabel(e: OpCallEdge): string {
  return e.op ? `${targetLabel(e.target)} · ${e.op}` : targetLabel(e.target);
}

/** dtoRows — SAF: KeyValue satırları (anahtar = Owner.alan, değer = tip). */
export function dtoRows(fields: DTOField[]): { id: string; k: string; v: string; mono: boolean }[] {
  return fields.map(f => ({ id: `${f.owner}.${f.name}`, k: `${f.owner}.${f.name}`, v: f.type, mono: true }));
}

/** codeNote — SAF: başta gösterilecek dürüst satır ('' = yok). Code Search
 *  kapalı / giriş bulunamadı / kısmi okuma → sunucunun gerekçesi. */
export function codeNote(code: OperationCardCode, tr: (k: string) => string): string {
  if (code.outcome === 'search-off') return code.reason || tr('endpoint.opcard.code.searchOff');
  if (!code.entry) return code.reason || tr('endpoint.opcard.code.noEntry');
  return code.outcome === 'ok' ? '' : (code.reason ?? '');
}

/** footerContext — SAF: CodeSourceFooter'ın AICodeContext alt kümesi (repo /
 *  dal / sürüm / commit / giriş dosyası / depo linki). Giriş yoksa null. */
export function footerContext(code: OperationCardCode): AICodeContext | null {
  if (!code.entry || !code.repo) return null;
  return {
    repo: code.repo,
    ...(code.branch ? { branch: code.branch } : {}),
    ...(code.source ? { source: code.source } : {}),
    ...(code.browseUrl ? { browseUrl: code.browseUrl } : {}),
    ...(code.version ? { version: code.version } : {}),
    ...(code.commit ? { commit: code.commit } : {}),
    files: [{
      path: code.entry.path, fromLine: code.entry.fromLine, toLine: code.entry.toLine,
      ...(code.entry.url ? { url: code.entry.url } : {}), kind: 'operation' as const,
    }],
  };
}

/** callSiteKindGlyph — SAF: tür işareti (renk değil; renk yalnız sapmada). */
export function callSiteKindGlyph(kind: string): string {
  switch (kind) {
    case 'db': return '⛁';
    case 'messaging': return '✉';
    case 'grpc': return '⇉';
    default: return '⇄';
  }
}
