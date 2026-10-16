// savedViewLimits.ts — v0.10.1236: kayıtlı görünüm / paylaşılan link sorgu
// dizesi tavanı. Sunucu (internal/api SavedViewQueryMax) aynı değerle 400 döner;
// istemci kaydetmeden önce söyler.
export const SAVED_VIEW_QUERY_MAX = 8192;

/** SAF: '' = sığar; aksi operatöre gösterilecek neden. */
export function savedViewTooLarge(qs: string): string {
  return qs.length > SAVED_VIEW_QUERY_MAX
    ? `Görünüm çok büyük (${qs.length} bayt, en çok ${SAVED_VIEW_QUERY_MAX}) — filtreleri azaltın`
    : '';
}
