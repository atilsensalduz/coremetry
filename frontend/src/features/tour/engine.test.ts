// engine.test.ts — v0.10.1334 rehberli tur motoru (saf indirgeyici) tabloları:
// sınırlar (BACK ilk adımda, NEXT son açık adımda → done), requires atlaması
// (gösterge hep 5 segment, kapalılar 'skipped'), TARGET_TIMEOUT (atlamaz,
// bayatı yok sayar), LOCATION_LEFT (skipped, returnTo'ya dönmez), Esc yolu
// (SKIP_TOUR → skipped), returnTo taşınır; oto-başlatma kapısı + karo özeti.
// v0.10.1336 (R2) — adım 3–5 açık; alt adım (kare) yürüyüşü İleri/Geri,
// kare anahtarlı zaman aşımı, copilot_disabled NOT yolu (adım atlanmaz,
// ortalanmış not karesi + Bitir), copilot bekleme tavanı, adım 4 rotası.
import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  tourReducer, initialTourState, persistedOutcome, shouldReturn, currentStep, currentFrame, frameProgress,
  hasPrev, isLastOpen, indicator, type TourAction, type TourState,
} from './engine';
import {
  TOUR_STEPS, TOUR_TOTAL, COPILOT_WAIT_MS, formatStepOf, formatCosrePrompt, stepFrames, tracesTourHref,
  type TourCtx,
} from './steps';
import { shouldAutoStart, isTourBlockedLocation, type AutoStartInput } from './autoStart';
import { summarizeTiles, tourWindow } from './queries';
import { buildTourCtx, withTimeout } from './ctx';
import { decodeFilters } from '@/lib/urlState';
import type { Service } from '@/lib/types';

afterEach(() => { vi.useRealTimers(); });

const CTX: TourCtx = {
  service: 'checkout', range: '1h', serviceCount: 12, spans5m: 9000, environments: 2,
  copilotEnabled: true, returnTo: '/inbox?x=1',
};
const EMPTY: TourCtx = { ...CTX, service: null, serviceCount: 0, spans5m: 0, environments: 0 };
const NO_AI: TourCtx = { ...CTX, copilotEnabled: false };

function run(actions: TourAction[], s: TourState = initialTourState): TourState {
  return actions.reduce((acc, a) => tourReducer(acc, a), s);
}
const NEXT: TourAction = { type: 'NEXT' };
const BACK: TourAction = { type: 'BACK' };
const nexts = (n: number): TourAction[] => Array.from({ length: n }, () => NEXT);
/** (adım, kare) konumu. */
const at = (s: TourState) => `${currentStep(s)?.id ?? '-'}:${currentFrame(s)?.key ?? '-'}`;

// Kare sayıları: welcome 1, services 1, operations 4, traces 3, cosre 2 → 11.
const FRAME_COUNT = TOUR_STEPS.reduce((n, st) => n + stepFrames(st).length, 0);

describe('engine — START ve requires (R2: 3–5 açık)', () => {
  it('beş adım beyanlı; veri + copilot varken hepsi açık', () => {
    expect(TOUR_TOTAL).toBe(5);
    expect(TOUR_STEPS.map(s => s.id)).toEqual(['welcome', 'services', 'operations', 'traces', 'cosre']);
    const s = run([{ type: 'START', ctx: CTX }]);
    expect(s.status).toBe('running');
    expect(s).toMatchObject({ index: 0, sub: 0, skipped: {}, noted: {} });
    expect(s.returnTo).toBe('/inbox?x=1');
    expect(FRAME_COUNT).toBe(11);
  });

  it('servis yokken 2–4 no_services; CoSRE (açık) kalır', () => {
    const s = run([{ type: 'START', ctx: EMPTY }]);
    expect(s.skipped).toEqual({ services: 'no_services', operations: 'no_services', traces: 'no_services' });
    expect(isLastOpen(s)).toBe(false);
    const s1 = run([NEXT], s);
    expect(at(s1)).toBe('cosre:cosre.fab');
    expect(hasPrev(s1)).toBe(true);
    expect(at(run([BACK], s1))).toBe('welcome:welcome');
  });

  it('serviceCount bilinmiyor (-1) → servis/traces AÇIK; servis adı yoksa operations atlanır', () => {
    const s = run([{ type: 'START', ctx: { ...CTX, serviceCount: -1 } }]);
    expect(s.skipped).toEqual({});
    const s2 = run([{ type: 'START', ctx: { ...CTX, serviceCount: -1, service: null } }]);
    expect(s2.skipped).toEqual({ operations: 'no_services' });
  });

  it('çalışırken ikinci START yok sayılır; bittikten sonra yeniden başlar', () => {
    const s1 = run([{ type: 'START', ctx: CTX }, NEXT]);
    expect(run([{ type: 'START', ctx: EMPTY }], s1)).toBe(s1);
    const done = run([{ type: 'SKIP_TOUR' }], s1);
    const again = run([{ type: 'START', ctx: CTX }], done);
    expect(again).toMatchObject({ status: 'running', index: 0, sub: 0, exit: null });
  });
});

describe('engine — alt adımlar (kareler)', () => {
  it('İleri tüm kareleri sırayla yürür, son karede Bitir', () => {
    const seen: string[] = [];
    let s = run([{ type: 'START', ctx: CTX }]);
    while (s.status === 'running') { seen.push(at(s)); s = run([NEXT], s); }
    expect(seen).toEqual([
      'welcome:welcome', 'services:services',
      'operations:operations.header', 'operations:operations.tab', 'operations:operations.impact', 'operations:operations.rows',
      'traces:traces.filters', 'traces:traces.minms', 'traces:traces.table',
      'cosre:cosre.fab', 'cosre:cosre.composer',
    ]);
    expect(s).toMatchObject({ status: 'done', exit: 'finish' });
  });

  it('Geri: önce adımın içinde, ilk karede ÖNCEKİ adımın SON karesine', () => {
    const s = run([{ type: 'START', ctx: CTX }, ...nexts(6)]); // traces.filters
    expect(at(s)).toBe('traces:traces.filters');
    const b1 = run([BACK], s);
    expect(at(b1)).toBe('operations:operations.rows');
    expect(at(run([BACK, BACK, BACK], b1))).toBe('operations:operations.header');
    expect(at(run([BACK, BACK, BACK, BACK], b1))).toBe('services:services');
  });

  it('frameProgress / hasPrev / isLastOpen karelerde', () => {
    const s = run([{ type: 'START', ctx: CTX }, ...nexts(3)]); // operations.tab
    expect(frameProgress(s)).toEqual({ i: 2, n: 4 });
    expect(hasPrev(s)).toBe(true);
    const fab = run([{ type: 'START', ctx: CTX }, ...nexts(9)]);
    expect(at(fab)).toBe('cosre:cosre.fab');
    expect(isLastOpen(fab)).toBe(false); // kare kaldı → İleri
    const composer = run([NEXT], fab);
    expect(isLastOpen(composer)).toBe(true); // Bitir
    expect(currentFrame(composer)).toMatchObject({ effect: 'cosre-prefill', keepFocus: '[data-tour="cosre-composer"] textarea' });
  });

  it('gösterge karelerden bağımsız: hep 5 segment, adım başına bir', () => {
    const s = run([{ type: 'START', ctx: CTX }, ...nexts(4)]); // operations.impact
    expect(indicator(s).map(x => x.kind)).toEqual(['done', 'done', 'current', 'upcoming', 'upcoming']);
    expect(formatStepOf('Step {n} of {total}', s.index + 1, TOUR_TOTAL)).toBe('Step 3 of 5');
  });

  it('çalışmıyorken eylemler no-op', () => {
    for (const a of [NEXT, BACK, { type: 'SKIP_TOUR' }, { type: 'FINISH' }, { type: 'LOCATION_LEFT' }] as TourAction[]) {
      expect(tourReducer(initialTourState, a)).toBe(initialTourState);
    }
  });

  it('BACK ilk adımda yerinde; FINISH her yerde biter', () => {
    const s0 = run([{ type: 'START', ctx: CTX }]);
    expect(run([BACK], s0)).toBe(s0);
    expect(hasPrev(s0)).toBe(false);
    expect(run([...nexts(4), { type: 'FINISH' }], s0)).toMatchObject({ status: 'done', exit: 'finish' });
  });
});

describe('engine — copilot_disabled: adım ATLANMAZ, not kartı + Bitir', () => {
  it('cosre noted, skipped değil; tek ortalanmış not karesi', () => {
    const s = run([{ type: 'START', ctx: NO_AI }]);
    expect(s.skipped).toEqual({});
    expect(s.noted).toEqual({ cosre: 'copilot_disabled' });
    const last = run(nexts(9), s);
    expect(currentStep(last)?.id).toBe('cosre');
    expect(currentFrame(last)).toEqual({ key: 'cosre.note', target: null, placement: 'center', bodyKey: 'tour.note.copilot_disabled' });
    expect(frameProgress(last)).toEqual({ i: 1, n: 1 });
    expect(isLastOpen(last)).toBe(true);
    const seg = indicator(last);
    expect(seg[4]).toEqual({ id: 'cosre', kind: 'current', reason: 'copilot_disabled' });
    expect(run([NEXT], last)).toMatchObject({ status: 'done', exit: 'finish' });
    expect(persistedOutcome(run([NEXT], last))).toBe('completed');
  });

  it('servis yok + copilot kapalı → karşılama ve not kartı', () => {
    const s = run([{ type: 'START', ctx: { ...EMPTY, copilotEnabled: false } }, NEXT]);
    expect(at(s)).toBe('cosre:cosre.note');
    expect(run([BACK], s).index).toBe(0);
  });

  it('BACK not karesinden traces\'in son karesine', () => {
    const s = run([{ type: 'START', ctx: NO_AI }, ...nexts(9), BACK]);
    expect(at(s)).toBe('traces:traces.table');
  });

  it('copilot bekleme: bilinmiyorsa COPILOT_WAIT_MS sonra kapalı; gelirse değeri', async () => {
    vi.useFakeTimers();
    const never = new Promise<boolean>(() => {});
    const p = withTimeout(never, COPILOT_WAIT_MS, false);
    let v: boolean | null = null;
    void p.then(x => { v = x; });
    await vi.advanceTimersByTimeAsync(COPILOT_WAIT_MS - 1);
    expect(v).toBeNull();
    await vi.advanceTimersByTimeAsync(1);
    expect(v).toBe(false);
    expect(await withTimeout(Promise.resolve(true), COPILOT_WAIT_MS, false)).toBe(true);
    expect(await withTimeout(Promise.reject(new Error('x')), COPILOT_WAIT_MS, false)).toBe(false);
  });
});

describe('engine — TARGET_TIMEOUT (kare anahtarlı)', () => {
  it('geçerli kareyi işaretler, ATLAMAZ (İleri açık kalır)', () => {
    const s = run([{ type: 'START', ctx: CTX }, NEXT, { type: 'TARGET_TIMEOUT', id: 'services' }]);
    expect(s.timedOut).toEqual({ services: true });
    expect(s.index).toBe(1);
    expect(s.status).toBe('running');
    expect(at(run([NEXT], s))).toBe('operations:operations.header');
  });

  it('operations satırları 8 sn\'de gelmedi → o karede not, adım sürer', () => {
    const s = run([{ type: 'START', ctx: CTX }, ...nexts(5), { type: 'TARGET_TIMEOUT', id: 'operations', frame: 'operations.rows' }]);
    expect(at(s)).toBe('operations:operations.rows');
    expect(s.timedOut).toEqual({ 'operations.rows': true });
    expect(at(run([NEXT], s))).toBe('traces:traces.filters');
  });

  it('bayat zaman aşımı (başka adım ya da başka kare) yok sayılır', () => {
    const s = run([{ type: 'START', ctx: CTX }, { type: 'TARGET_TIMEOUT', id: 'services' }]);
    expect(s.timedOut).toEqual({});
    const s2 = run([{ type: 'START', ctx: CTX }, ...nexts(3), { type: 'TARGET_TIMEOUT', id: 'operations', frame: 'operations.header' }]);
    expect(s2.timedOut).toEqual({});
  });
});

describe('engine — bitiş: outcome + returnTo', () => {
  it('Esc yolu = SKIP_TOUR → skipped, returnTo\'ya dönülür', () => {
    const s = run([{ type: 'START', ctx: CTX }, NEXT, { type: 'SKIP_TOUR' }]);
    expect(persistedOutcome(s)).toBe('skipped');
    expect(shouldReturn(s)).toBe(true);
    expect(s.returnTo).toBe('/inbox?x=1');
  });

  it('Bitir → completed, returnTo\'ya dönülür', () => {
    const s = run([{ type: 'START', ctx: CTX }, ...nexts(FRAME_COUNT)]);
    expect(persistedOutcome(s)).toBe('completed');
    expect(shouldReturn(s)).toBe(true);
  });

  it('LOCATION_LEFT → skipped, returnTo\'ya GERİ ÇEKİLMEZ', () => {
    const s = run([{ type: 'START', ctx: CTX }, NEXT, { type: 'LOCATION_LEFT' }]);
    expect(s).toMatchObject({ status: 'done', exit: 'left' });
    expect(persistedOutcome(s)).toBe('skipped');
    expect(shouldReturn(s)).toBe(false);
  });

  it('çalışırken outcome yok', () => {
    expect(persistedOutcome(run([{ type: 'START', ctx: CTX }]))).toBeNull();
  });
});

describe('steps — R2 rotaları ve CoSRE sorusu', () => {
  it('adım 3: serviceHref + tab=operations, pencere taşınır', () => {
    const r = TOUR_STEPS[2].route(CTX)!;
    const u = new URL(r, 'http://x');
    expect(u.pathname).toBe('/service');
    expect(u.searchParams.get('name')).toBe('checkout');
    expect(u.searchParams.get('tab')).toBe('operations');
    expect(u.searchParams.get('range')).toBe('1h');
  });

  it('adım 4: Traces.tsx\'in okuduğu biçim (service, filters, minMs, view, range)', () => {
    const u = new URL(tracesTourHref(CTX), 'http://x');
    expect(u.pathname).toBe('/traces');
    expect(u.searchParams.get('service')).toBe('checkout');
    expect(decodeFilters(u.searchParams.get('filters'))).toEqual([{ k: 'status_code', op: '=', v: ['error'] }]);
    expect(u.searchParams.get('minMs')).toBe('500');
    expect(u.searchParams.get('view')).toBe('list');
    expect(u.searchParams.get('range')).toBe('1h');
    const bare = new URL(tracesTourHref({ service: null, range: null }), 'http://x');
    expect(bare.searchParams.has('service')).toBe(false);
    expect(bare.searchParams.has('range')).toBe(false);
  });

  it('CoSRE sorusu gerçek servisle şablonlanır', () => {
    expect(formatCosrePrompt('Why is {svc} slow?', 'checkout')).toBe('Why is checkout slow?');
    expect(formatCosrePrompt('Son 1 saatte {svc} hatalı mı?', 'a-b')).toBe('Son 1 saatte a-b hatalı mı?');
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
