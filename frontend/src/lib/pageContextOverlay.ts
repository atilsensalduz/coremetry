// pageContextOverlay.ts — v0.10.1435 (Panelden "Bu grafiği açıkla"; brush
// aralığı ve seçili seri bağlamda).
//
// pageContext.ts sayfa bağlamını URL'den SAF türetir; URL'de yaşamayan iki
// seçim oradan görünmez: grafikte brush'lanan aralık (Explore / Metrics
// editörünün yerel zoom'u, dashboard'un zoom yığını) ve lejantta seçilen seri.
// "bu tepe neden oluştu", "seçili seride ne oldu" soruları tam olarak bunlara
// işaret eder. Bu modül sayfanın o seçimleri YAYINLADIĞI tek yer (modül
// mağazası; durum kütüphanesi yok): sayfa usePublishPageOverlay ile yazar,
// CopilotChat usePageOverlay ile okur ve withPageOverlay ile context.page'e
// katar (Go: agentctx.PageContext.Brush / SelectedSeries; chat_tool_scope.go
// deixis'te araç penceresini brush yapar).
//
// İkinci görev — ✨ yükü: panel açıklamasının kodeği (`?ai=metric_panel:…`)
// KONUM taşır, sorgu taşımaz (400 rune; adresten sorgu koşturulmaz). Panelin
// sorgusu ✨ tıklandığı anda stageMetricPanelAsk ile bırakılır; CopilotChat'in
// `?ai=` effect'i takeMetricPanelAsk ile TEK SEFERLİK alır (≤60 sn; aynı kodek).
//
// Saf çekirdek + küçük React kancaları; vitest node'da sınanır
// (pageContextOverlay.test.ts).
import { useEffect, useSyncExternalStore } from 'react';
import type { MetricPanelAsk, PageContext } from './types';

export interface PageOverlay {
  /** Mutlak ms; pencerenin içindeki operatör seçimi. */
  brush?: { fromMs: number; toMs: number };
  selectedSeries?: string[];
}

export const OVERLAY_SERIES_MAX = 5;
export const OVERLAY_SERIES_RUNES = 120;
const STAGE_TTL_MS = 60_000;

let current: PageOverlay | null = null;
let owner = 0;
let nextOwner = 1;
const listeners = new Set<() => void>();

function emit(): void {
  for (const l of listeners) l();
}

/** normalizeOverlay — SAF: geçersiz brush düşer, seri adları kırpılır (≤5 × 120); boşsa null. */
export function normalizeOverlay(o: PageOverlay | null | undefined): PageOverlay | null {
  if (!o) return null;
  const out: PageOverlay = {};
  const b = o.brush;
  if (b && Number.isFinite(b.fromMs) && Number.isFinite(b.toMs) && b.fromMs > 0 && b.toMs > b.fromMs) {
    out.brush = { fromMs: Math.floor(b.fromMs), toMs: Math.ceil(b.toMs) };
  }
  const sel = (o.selectedSeries ?? [])
    .map(s => [...(s ?? '').trim()].slice(0, OVERLAY_SERIES_RUNES).join(''))
    .filter(Boolean)
    .slice(0, OVERLAY_SERIES_MAX);
  if (sel.length) out.selectedSeries = sel;
  return out.brush || out.selectedSeries ? out : null;
}

const overlayKey = (o: PageOverlay | null): string => (o ? JSON.stringify(o) : '');

/** setPageOverlay — mağazaya yazar (aynı içerikse dinleyici tetiklenmez). */
export function setPageOverlay(o: PageOverlay | null): void {
  const n = normalizeOverlay(o);
  if (overlayKey(n) === overlayKey(current)) return;
  current = n;
  emit();
}

export function getPageOverlay(): PageOverlay | null {
  return current;
}

export function subscribePageOverlay(fn: () => void): () => void {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}

/** usePageOverlay — CopilotChat: yayınlanan seçimler. */
export function usePageOverlay(): PageOverlay | null {
  return useSyncExternalStore(subscribePageOverlay, getPageOverlay, getPageOverlay);
}

/**
 * usePublishPageOverlay — sayfa: seçimlerini yayınlar; içerik değişince
 * yeniden yazar, unmount'ta (hâlâ sahibiyse) temizler — başka sayfaya geçen
 * operatörün sorusuna eski sayfanın brush'ı karışmaz.
 */
export function usePublishPageOverlay(o: PageOverlay | null): void {
  const key = overlayKey(normalizeOverlay(o));
  useEffect(() => {
    const me = nextOwner++;
    owner = me;
    setPageOverlay(key ? (JSON.parse(key) as PageOverlay) : null);
    return () => {
      if (owner === me) setPageOverlay(null);
    };
  }, [key]);
}

/** withPageOverlay — SAF: URL'den türeyen sayfa bağlamına yayınlanan seçimleri katar. */
export function withPageOverlay(page: PageContext, o: PageOverlay | null): PageContext {
  const n = normalizeOverlay(o);
  if (!n) return page;
  return { ...page, ...(n.brush ? { brush: n.brush } : {}), ...(n.selectedSeries ? { selectedSeries: n.selectedSeries } : {}) };
}

// ── ✨ yükü: tek seferlik bırakma ───────────────────────────────────────────

let staged: { key: string; panel: MetricPanelAsk; at: number } | null = null;

/** stageMetricPanelAsk — ✨ tıklanınca panelin sorgusunu kodeğin anahtarıyla bırakır. */
export function stageMetricPanelAsk(subjectKey: string, panel: MetricPanelAsk, now: number = Date.now()): void {
  staged = { key: subjectKey, panel, at: now };
}

/** takeMetricPanelAsk — aynı kodek + ≤60 sn ise yükü TEK SEFER verir (yoksa undefined: derin link). */
export function takeMetricPanelAsk(subjectKey: string, now: number = Date.now()): MetricPanelAsk | undefined {
  const s = staged;
  if (!s || s.key !== subjectKey || now - s.at > STAGE_TTL_MS) return undefined;
  staged = null;
  return s.panel;
}

/** Yalnız testler. */
export function resetPageOverlayForTest(): void {
  current = null;
  owner = 0;
  staged = null;
  listeners.clear();
}
