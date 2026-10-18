// geometry.ts — v0.10.1334 (rehberli tur). Örtünün saf geometri sabitleri
// (TourOverlay.tsx yalnız bileşen dışa aktarsın — react-refresh).
import type { TipRect } from '@/lib/tipPlacement';

/** Halkanın hedefin çevresinde bıraktığı pay (px). */
export const RING_PAD = 6;
/** Halka ile kart arası boşluk (px) — placeTip `gap` genişletmesi. */
export const COACH_GAP = 12;

export function ringRect(r: TipRect): TipRect {
  return { left: r.left - RING_PAD, top: r.top - RING_PAD, width: r.width + 2 * RING_PAD, height: r.height + 2 * RING_PAD };
}
