// engine.ts — v0.10.1334 (rehberli tur, R1) · v0.10.1336 (R2: alt adımlar +
// not gerekçeleri). SAF indirgeyici: DOM yok, zamanlayıcı yok, ağ yok.
// Navigasyon, hedef bekleme ve kalıcılık useTour/TourProvider'da; burası yalnız
// "hangi adımın hangi karesindeyiz, neden atlandı, tur nasıl bitti" sorusunu
// cevaplar (engine.test tablo testleri).
//
// Atlama kuralı: START bağlamı bir kez çözer ve HER adımın `requires`
// gerekçesini yazar — NOTE_REASONS'takiler `noted`a (adım GÖSTERİLİR, tek
// ortalanmış not karesi), kalanlar `skipped`e (gösterge kesik). NEXT/BACK
// yalnız açık adımlar arasında yürür; adımın kareleri (alt adımlar) önce
// kendi içinde: NEXT son kareden sonraki adıma, BACK ilk kareden ÖNCEKİ
// adımın SON karesine. TARGET_TIMEOUT adımı ATLAMAZ (kart notla sürer, İleri
// açık — asla takılmaz); yalnız o karenin `timedOut`una işler. LOCATION_LEFT
// = kullanıcı tur dışı bir yere gitti → tur 'skipped' biter ve returnTo'ya
// GERİ ÇEKİLMEZ (kullanıcının seçimi).

import {
  NOTE_REASONS, TOUR_STEPS, noteFrame, stepFrames,
  type TourCtx, type TourFrame, type TourSkipReason, type TourStep, type TourStepId,
} from './steps';

export type TourRunStatus = 'idle' | 'running' | 'done';
/** Bitişin nedeni: finish → completed; skip/left → skipped. */
export type TourExit = 'finish' | 'skip' | 'left';

export interface TourState {
  status: TourRunStatus;
  index: number;
  /** v0.10.1336 — adımın içindeki kare (alt adım) sırası. */
  sub: number;
  skipped: Partial<Record<TourStepId, TourSkipReason>>;
  /** v0.10.1336 — gösterilen ama not kartına dönen adımlar (NOTE_REASONS). */
  noted: Partial<Record<TourStepId, TourSkipReason>>;
  /** Kare anahtarı (TourFrame.key) → zaman aşımı. Tek kareli adımda anahtar = adım kimliği. */
  timedOut: Partial<Record<string, true>>;
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
  /** `frame` = kare anahtarı; verilmezse adım kimliği (tek kareli adım). */
  | { type: 'TARGET_TIMEOUT'; id: TourStepId; frame?: string }
  | { type: 'LOCATION_LEFT' };

export const initialTourState: TourState = {
  status: 'idle', index: 0, sub: 0, skipped: {}, noted: {}, timedOut: {}, returnTo: null, ctx: null, exit: null,
};

/** v0.10.1336 — bu durumda adımın kareleri (not gerekçeliyse tek not karesi). */
export function framesOf(s: Pick<TourState, 'noted'>, step: TourStep): readonly TourFrame[] {
  const note = s.noted[step.id];
  return note ? [noteFrame(step, note)] : stepFrames(step);
}

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
    const noted: TourState['noted'] = {};
    for (const st of steps) {
      const r = st.requires(a.ctx);
      if (!r) continue;
      if (NOTE_REASONS.has(r)) noted[st.id] = r;
      else skipped[st.id] = r;
    }
    const base: TourState = {
      status: 'running', index: 0, sub: 0, skipped, noted, timedOut: {}, returnTo: a.ctx.returnTo, ctx: a.ctx, exit: null,
    };
    const first = nextOpen(steps, skipped, -1, 1);
    return first < 0 ? done(base, 'finish') : { ...base, index: first };
  }
  if (s.status !== 'running') return s;
  switch (a.type) {
    case 'NEXT': {
      const cur = steps[s.index];
      if (cur && s.sub < framesOf(s, cur).length - 1) return { ...s, sub: s.sub + 1 };
      const n = nextOpen(steps, s.skipped, s.index, 1);
      return n < 0 ? done(s, 'finish') : { ...s, index: n, sub: 0 };
    }
    case 'BACK': {
      if (s.sub > 0) return { ...s, sub: s.sub - 1 };
      const p = nextOpen(steps, s.skipped, s.index, -1);
      return p < 0 ? s : { ...s, index: p, sub: framesOf(s, steps[p]).length - 1 };
    }
    case 'SKIP_TOUR':
      return done(s, 'skip');
    case 'FINISH':
      return done(s, 'finish');
    case 'TARGET_TIMEOUT': {
      // Bayat zaman aşımı (adım ya da kare çoktan değişti) yok sayılır.
      const key = a.frame ?? a.id;
      const fr = currentFrame(s, steps);
      if (steps[s.index]?.id !== a.id || fr?.key !== key || s.timedOut[key]) return s;
      return { ...s, timedOut: { ...s.timedOut, [key]: true } };
    }
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

/** v0.10.1336 — şu anki kare (alt adım); tur çalışmıyorsa null. */
export function currentFrame(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): TourFrame | null {
  const st = currentStep(s, steps);
  if (!st) return null;
  const fr = framesOf(s, st);
  return fr[Math.min(s.sub, fr.length - 1)] ?? null;
}

/** v0.10.1336 — {i, n}: adımın kaçıncı karesi (1 tabanlı) / kaç kare. */
export function frameProgress(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): { i: number; n: number } {
  const st = currentStep(s, steps);
  if (!st) return { i: 0, n: 0 };
  const n = framesOf(s, st).length;
  return { i: Math.min(s.sub, n - 1) + 1, n };
}

export function hasPrev(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): boolean {
  return s.status === 'running' && (s.sub > 0 || nextOpen(steps, s.skipped, s.index, -1) >= 0);
}

/** Sonraki açık adım yok VE adımın son karesi → birincil düğme "Bitir". */
export function isLastOpen(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): boolean {
  if (s.status !== 'running') return false;
  const st = steps[s.index];
  const lastFrame = !st || s.sub >= framesOf(s, st).length - 1;
  return lastFrame && nextOpen(steps, s.skipped, s.index, 1) < 0;
}

export type IndicatorKind = 'done' | 'current' | 'upcoming' | 'skipped';
export interface IndicatorSeg { id: TourStepId; kind: IndicatorKind; reason?: TourSkipReason }

/** "Adım N / 5" göstergesi: her zaman TÜM adımlar (kapalılar 'skipped'; not gerekçesi ipucunda). */
export function indicator(s: TourState, steps: readonly TourStep[] = TOUR_STEPS): IndicatorSeg[] {
  return steps.map((st, i) => {
    const reason = s.skipped[st.id];
    if (reason) return { id: st.id, kind: 'skipped', reason };
    const note = s.noted[st.id];
    const kind: IndicatorKind = i < s.index ? 'done' : i === s.index ? 'current' : 'upcoming';
    return note ? { id: st.id, kind, reason: note } : { id: st.id, kind };
  });
}
