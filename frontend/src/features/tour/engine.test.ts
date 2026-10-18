// engine.test.ts — v0.10.1334 rehberli tur motoru (saf indirgeyici) tabloları:
// sınırlar (BACK ilk adımda, NEXT son açık adımda → done), requires atlaması
// (gösterge hep 5 segment, kapalılar 'skipped'), TARGET_TIMEOUT (atlamaz,
// bayatı yok sayar), LOCATION_LEFT (skipped, returnTo'ya dönmez), Esc yolu
// (SKIP_TOUR → skipped), returnTo taşınır; oto-başlatma kapısı + karo özeti.
import { describe, it, expect } from 'vitest';
import {
  tourReducer, initialTourState, persistedOutcome, shouldReturn, currentStep,
  hasPrev, isLastOpen, indicator, type TourAction, type TourState,
} from './engine';
import { TOUR_STEPS, TOUR_TOTAL, formatStepOf, type TourCtx } from './steps';
import { shouldAutoStart, isTourBlockedLocation, type AutoStartInput } from './autoStart';
import { summarizeTiles, tourWindow } from './queries';
import { buildTourCtx } from './ctx';
import type { Service } from '@/lib/types';

const CTX: TourCtx = {
  service: 'checkout', range: '1h', serviceCount: 12, spans5m: 9000, environments: 2,
  copilotEnabled: true, returnTo: '/inbox?x=1',
};
const EMPTY: TourCtx = { ...CTX, service: null, serviceCount: 0, spans5m: 0, environments: 0 };

function run(actions: TourAction[], s: TourState = initialTourState): TourState {
  return actions.reduce((acc, a) => tourReducer(acc, a), s);
}

describe('engine — START ve requires atlaması', () => {
  it('beş adım beyanlı; R1\'de yalnız 1–2 açık', () => {
    expect(TOUR_TOTAL).toBe(5);
    expect(TOUR_STEPS.map(s => s.id)).toEqual(['welcome', 'services', 'operations', 'traces', 'cosre']);
    const s = run([{ type: 'START', ctx: CTX }]);
    expect(s.status).toBe('running');
    expect(s.index).toBe(0);
    expect(s.skipped).toEqual({ operations: 'not_yet', traces: 'not_yet', cosre: 'not_yet' });
    expect(s.returnTo).toBe('/inbox?x=1');
  });

  it('servis yokken 2–4 no_services, 5 not_yet; tur yalnız karşılama', () => {
    const s = run([{ type: 'START', ctx: EMPTY }]);
    expect(s.skipped).toEqual({ services: 'no_services', operations: 'no_services', traces: 'no_services', cosre: 'not_yet' });
    expect(isLastOpen(s)).toBe(true);
    expect(run([{ type: 'NEXT' }], s)).toMatchObject({ status: 'done', exit: 'finish' });
  });

  it('serviceCount bilinmiyor (-1) → servis adımı AÇIK kalır (zaman aşımıyla sürer)', () => {
    const s = run([{ type: 'START', ctx: { ...CTX, serviceCount: -1 } }]);
    expect(s.skipped.services).toBeUndefined();
  });

  it('çalışırken ikinci START yok sayılır; bittikten sonra yeniden başlar', () => {
    const s1 = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }]);
    expect(run([{ type: 'START', ctx: EMPTY }], s1)).toBe(s1);
    const done = run([{ type: 'SKIP_TOUR' }], s1);
    const again = run([{ type: 'START', ctx: CTX }], done);
    expect(again).toMatchObject({ status: 'running', index: 0, exit: null });
  });
});

describe('engine — NEXT / BACK sınırları, gösterge hep 5', () => {
  const cases: { name: string; actions: TourAction[]; index: number; status: string; exit: string | null }[] = [
    { name: 'BACK ilk adımda yerinde', actions: [{ type: 'BACK' }], index: 0, status: 'running', exit: null },
    { name: 'NEXT → servisler', actions: [{ type: 'NEXT' }], index: 1, status: 'running', exit: null },
    { name: 'NEXT, BACK → karşılama', actions: [{ type: 'NEXT' }, { type: 'BACK' }], index: 0, status: 'running', exit: null },
    { name: 'NEXT ×2 → 3–5 atlanır, biter', actions: [{ type: 'NEXT' }, { type: 'NEXT' }], index: 1, status: 'done', exit: 'finish' },
    { name: 'FINISH', actions: [{ type: 'FINISH' }], index: 0, status: 'done', exit: 'finish' },
  ];
  for (const c of cases) {
    it(c.name, () => {
      const s = run([{ type: 'START', ctx: CTX }, ...c.actions]);
      expect(s.index).toBe(c.index);
      expect(s.status).toBe(c.status);
      expect(s.exit).toBe(c.exit);
    });
  }

  it('gösterge: her adımda 5 segment, 3–5 skipped (kesik)', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }]);
    const seg = indicator(s);
    expect(seg).toHaveLength(5);
    expect(seg.map(x => x.kind)).toEqual(['done', 'current', 'skipped', 'skipped', 'skipped']);
    expect(seg[2].reason).toBe('not_yet');
    expect(formatStepOf('Step {n} of {total}', s.index + 1, TOUR_TOTAL)).toBe('Step 2 of 5');
  });

  it('hasPrev / isLastOpen / currentStep', () => {
    const s0 = run([{ type: 'START', ctx: CTX }]);
    expect(hasPrev(s0)).toBe(false);
    expect(isLastOpen(s0)).toBe(false);
    expect(currentStep(s0)?.id).toBe('welcome');
    const s1 = run([{ type: 'NEXT' }], s0);
    expect(hasPrev(s1)).toBe(true);
    expect(isLastOpen(s1)).toBe(true); // "Bitir"
    expect(currentStep(run([{ type: 'FINISH' }], s1))).toBeNull();
  });

  it('çalışmıyorken eylemler no-op', () => {
    for (const a of [{ type: 'NEXT' }, { type: 'BACK' }, { type: 'SKIP_TOUR' }, { type: 'FINISH' }, { type: 'LOCATION_LEFT' }] as TourAction[]) {
      expect(tourReducer(initialTourState, a)).toBe(initialTourState);
    }
  });
});

describe('engine — TARGET_TIMEOUT', () => {
  it('geçerli adımı işaretler, ATLAMAZ (İleri açık kalır)', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }, { type: 'TARGET_TIMEOUT', id: 'services' }]);
    expect(s.timedOut).toEqual({ services: true });
    expect(s.index).toBe(1);
    expect(s.status).toBe('running');
    expect(run([{ type: 'NEXT' }], s)).toMatchObject({ status: 'done', exit: 'finish' });
  });

  it('bayat zaman aşımı (başka adım) yok sayılır', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'TARGET_TIMEOUT', id: 'services' }]);
    expect(s.timedOut).toEqual({});
  });
});

describe('engine — bitiş: outcome + returnTo', () => {
  it('Esc yolu = SKIP_TOUR → skipped, returnTo\'ya dönülür', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }, { type: 'SKIP_TOUR' }]);
    expect(persistedOutcome(s)).toBe('skipped');
    expect(shouldReturn(s)).toBe(true);
    expect(s.returnTo).toBe('/inbox?x=1');
  });

  it('Bitir → completed, returnTo\'ya dönülür', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }, { type: 'NEXT' }]);
    expect(persistedOutcome(s)).toBe('completed');
    expect(shouldReturn(s)).toBe(true);
  });

  it('LOCATION_LEFT → skipped, returnTo\'ya GERİ ÇEKİLMEZ', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'NEXT' }, { type: 'LOCATION_LEFT' }]);
    expect(s).toMatchObject({ status: 'done', exit: 'left' });
    expect(persistedOutcome(s)).toBe('skipped');
    expect(shouldReturn(s)).toBe(false);
  });

  it('çalışırken outcome yok', () => {
    expect(persistedOutcome(run([{ type: 'START', ctx: CTX }]))).toBeNull();
  });
});

describe('autoStart — oto-başlatma kapısı', () => {
  const OK: AutoStartInput = {
    enabled: true, status: 'none', newUser: true, sessionAutoStarted: false, localSeen: false,
    pathname: '/inbox', search: '',
  };
  const cases: [string, Partial<AutoStartInput>, boolean][] = [
    ['tüm kapılar açık', {}, true],
    ['branding kapalı', { enabled: false }, false],
    ['tamamlanmış', { status: 'completed' }, false],
    ['atlanmış', { status: 'skipped' }, false],
    ['eski kullanıcı', { newUser: false }, false],
    ['sekmede zaten başladı', { sessionAutoStarted: true }, false],
    ['tarayıcıda görüldü (flaş bekçisi)', { localSeen: true }, false],
    ['/login', { pathname: '/login' }, false],
    ['kiosk-çıplak trace', { pathname: '/trace', search: '?kiosk=1' }, false],
    ['dashboard kiosk', { pathname: '/dashboard', search: '?id=1&kiosk=1' }, false],
    ['cosre-çıplak', { pathname: '/cosre' }, false],
    ['public status', { pathname: '/public-status' }, false],
    ['?tour=0', { search: '?tour=0' }, false],
    ['?tour=1 engel değil', { search: '?tour=1' }, true],
  ];
  for (const [name, patch, want] of cases) {
    it(name, () => { expect(shouldAutoStart({ ...OK, ...patch })).toBe(want); });
  }
  it('menüden başlatma da kromsuz kabukta engelli', () => {
    expect(isTourBlockedLocation('/cosre', '')).toBe(true);
    expect(isTourBlockedLocation('/services', '')).toBe(false);
  });
});

describe('queries — karo özeti + pencere', () => {
  const svc = (name: string, spanCount: number) => ({ name, spanCount } as Service);
  it('toplam, Σ span, en yoğun, ilk-50 bayrağı, ortam sayısı', () => {
    const t = summarizeTiles({ services: [svc('a', 10), svc('b', 30), svc('c', 5)], hasMore: true, total: 140 }, { environments: ['prod', 'uat'], total: 3 });
    expect(t).toEqual({ totalServices: 140, spans: 45, spansTopOnly: true, busiest: { name: 'b', spanCount: 30 }, environments: 3 });
  });
  it('boş veri → sıfırlar (no_services yolu)', () => {
    expect(summarizeTiles({ services: [], hasMore: false }, null))
      .toEqual({ totalServices: 0, spans: 0, spansTopOnly: false, busiest: null, environments: 0 });
  });
  it('pencere 5 dk, dakikaya oturur, ns', () => {
    const w = tourWindow(Date.UTC(2026, 9, 18, 12, 7, 42, 500));
    expect(w.to).toBe(Date.UTC(2026, 9, 18, 12, 7) * 1e6);
    expect(w.to - w.from).toBe(5 * 60_000 * 1e6);
  });
  it('bağlam: okunamayan karo → serviceCount -1; range URL\'den', () => {
    expect(buildTourCtx(null, { pathname: '/inbox', search: '?range=6h' }, false))
      .toMatchObject({ serviceCount: -1, range: '6h', service: null, returnTo: '/inbox?range=6h' });
  });
});
