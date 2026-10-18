// engine.ts — v0.10.1334 (rehberli tur, R1). SAF indirgeyici: DOM yok,
// zamanlayıcı yok, ağ yok. Navigasyon, hedef bekleme ve kalıcılık
// useTour/TourProvider'da; burası yalnız "hangi adımdayız, neden atlandı,
// tur nasıl bitti" sorusunu cevaplar (engine.test tablo testleri).
//
// Atlama kuralı: START bağlamı bir kez çözer ve HER adımın `requires`
// gerekçesini `skipped`e yazar — gösterge baştan "5 adımın 3'ü kapalı"
// gösterir, NEXT/BACK yalnız açık adımlar arasında yürür. TARGET_TIMEOUT
// adımı ATLAMAZ (kart notla sürer, İleri açık — asla takılmaz); yalnız
// `timedOut`a işler. LOCATION_LEFT = kullanıcı tur dışı bir yere gitti →
// tur 'skipped' biter ve returnTo'ya GERİ ÇEKİLMEZ (kullanıcının seçimi).

import { TOUR_STEPS, type TourCtx, type TourSkipReason, type TourStep, type TourStepId } from './steps';

export type TourRunStatus = 'idle' | 'running' | 'done';
/** Bitişin nedeni: finish → completed; skip/left → skipped. */
export type TourExit = 'finish' | 'skip' | 'left';

export interface TourState {
  status: TourRunStatus;
  index: number;
  skipped: Partial<Record<TourStepId, TourSkipReason>>;
  timedOut: Partial<Record<TourStepId, true>>;
  returnTo: string | null;
  ctx: TourCtx | null;
  exit: TourExit | null;
}

export type TourAction =
  | { type: 'START'; ctx: TourCtx }
  | { type: 'NEXT' }
  | { type: 'BACK' }
  | { type: 'SKIP_TOUR' }
  | { type: 'FINISH' }
  | { type: 'TARGET_TIMEOUT'; id: TourStepId }
  | { type: 'LOCATION_LEFT' };

export const initialTourState: TourState = {
  status: 'idle', index: 0, skipped: {}, timedOut: {}, returnTo: null, ctx: null, exit: null,
};

function nextOpen(steps: readonly TourStep[], skipped: TourState['skipped'], from: number, dir: 1 | -1): number {
  for (let i = from + dir; i >= 0 && i < steps.length; i += dir) {
    if (!skipped[steps[i].id]) return i;
  }
  return -1;
}

function done(s: TourState, exit: TourExit): TourState {
  return { ...s, status: 'done', exit };
}

export function tourReducer(s: TourState, a: TourAction, steps: readonly TourStep[] = TOUR_STEPS): TourState {
  if (a.type === 'START') {
    if (s.status === 'running') return s; // çift başlatma yok sayılır
    const skipped: TourState['skipped'] = {};
    for (const st of steps) {
      const r = st.requires(a.ctx);
      if (r) skipped[st.id] = r;
    }
    const base: TourState = {
      status: 'running', index: 0, skipped, timedOut: {}, returnTo: a.ctx.returnTo, ctx: a.ctx, exit: null,
    };
    const first = nextOpen(steps, skipped, -1, 1);
    return first < 0 ? done(base, 'finish') : { ...base, index: first };
  }
  if (s.status !== 'running') return s;
  switch (a.type) {
    case 'NEXT': {
      const n = nextOpen(steps, s.skipped, s.index, 1);
      return n < 0 ? done(s, 'finish') : { ...s, index: n };
    }
    case 'BACK': {
      const p = nextOpen(steps, s.skipped, s.index, -1);
      return p < 0 ? s : { ...s, index: p };
    }
    case 'SKIP_TOUR':
      return done(s, 'skip');
    case 'FINISH':
      return done(s, 'finish');
    case 'TARGET_TIMEOUT':
      // Bayat zaman aşımı (adım çoktan değişti) yok sayılır.
      if (steps[s.index]?.id !== a.id || s.timedOut[a.id]) return s;
      return { ...s, timedOut: { ...s.timedOut, [a.id]: true } };
    case 'LOCATION_LEFT':
      return done(s, 'left');
  }
  return s;
}

/** Sunucuya yazılacak son durum. */
export function persistedOutcome(s: TourState): 'completed' | 'skipped' | null {
  if (s.status !== 'done' || !s.exit) return null;
  return s.exit === 'finish' ? 'completed' : 'skipped';
}

/** Bitince returnTo'ya dönülsün mü (LOCATION_LEFT → hayır). */
export function shouldReturn(s: TourState): boolean {
  return s.status === 'done' && s.exit !== 'left' && !!s.returnTo;
}

export function currentStep(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): TourStep | null {
  return s.status === 'running' ? steps[s.index] ?? null : null;
}

export function hasPrev(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): boolean {
  return s.status === 'running' && nextOpen(steps, s.skipped, s.index, -1) >= 0;
}

/** Sonraki açık adım yok → birincil düğme "Bitir". */
export function isLastOpen(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): boolean {
  return s.status === 'running' && nextOpen(steps, s.skipped, s.index, 1) < 0;
}

export type IndicatorKind = 'done' | 'current' | 'upcoming' | 'skipped';
export interface IndicatorSeg { id: TourStepId; kind: IndicatorKind; reason?: TourSkipReason }

/** "Adım N / 5" göstergesi: her zaman TÜM adımlar (kapalılar 'skipped'). */
export function indicator(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): IndicatorSeg[] {
  return steps.map((st, i) => {
    const reason = s.skipped[st.id];
    if (reason) return { id: st.id, kind: 'skipped', reason };
    if (i < s.index) return { id: st.id, kind: 'done' };
    if (i === s.index) return { id: st.id, kind: 'current' };
    return { id: st.id, kind: 'upcoming' };
  });
}
