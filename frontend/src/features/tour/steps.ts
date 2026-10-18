// steps.ts — v0.10.1334 (uygulama içi rehberli tur, R1). SAF: DOM yok,
// React yok. Turun BEŞ adımı burada beyan edilir; R1'de yalnız 1–2 açık,
// 3–5 beyanlı ama `requires → 'not_yet'` ile kapalı (gösterge yine "Adım N
// / 5", kapalılar kesik çizgili). R2 bir adımı açarken yalnız `requires`
// gövdesini ve hedef çapasını değiştirir — motor/örtü dokunulmaz.
//
// Bağlam (`TourCtx`) tur BAŞLARKEN bir kez çözülür (TourProvider): adımın
// rotası ve açık/kapalı kararı turun ortasında değişmez — gösterge
// zıplamaz, Geri her zaman aynı adıma döner.
//
// Hedef sözleşmesi: `target` bir `[data-tour="…"]` seçicisi (ya da onunla
// başlayan bir alt-seçici) veya null (ortalanmış kart). Çapa kimliklerinin
// sahibi dosyalar TOUR_ANCHORS'ta; targetPin.test her açık adımın çapasının
// sahibinde GERÇEKTEN durduğunu ve src'de sahipsiz data-tour kalmadığını
// pinler.

import { serviceHref } from '@/lib/serviceHref';

export type TourStepId = 'welcome' | 'services' | 'operations' | 'traces' | 'cosre';

/** Bir adımın atlanma gerekçesi (gösterge ipucu + i18n `tour.note.<reason>`). */
export type TourSkipReason = 'not_yet' | 'no_services';

export type TourPlacement = 'center' | 'top' | 'bottom';

export interface TourCtx {
  /** En yoğun servis (5 dk); veri yoksa null. */
  service: string | null;
  /** Turu başlatan sayfanın ?range= değeri (yoksa null → sayfa varsayılanı). */
  range: string | null;
  /** Toplam servis; -1 = bilinmiyor (okuma düştü → adımlar açık kalır, zaman aşımı notu). */
  serviceCount: number;
  /** Σ spanCount (son 5 dk, en çok 50 servis). */
  spans5m: number;
  environments: number;
  copilotEnabled: boolean;
  /** Tur bitince dönülecek konum (pathname + search). */
  returnTo: string;
}

export interface TourStep {
  id: TourStepId;
  /** Adımın sayfası; null = bulunduğun yerde kal. */
  route: (ctx: TourCtx) => string | null;
  target: string | null;
  /** Hedef belirmezse (ör. ilk satır yok) halkanın düşeceği seçici. */
  fallbackTarget?: string;
  placement: TourPlacement;
  titleKey: string;
  bodyKey: string;
  /** null = adım açık; aksi hâlde atlanma gerekçesi. */
  requires: (ctx: TourCtx) => TourSkipReason | null;
}

/** Tur içeriği anlamlı biçimde değişince artar (PUT /api/tour/state version). */
export const TOUR_VERSION = 1;

/** Bekleme tavanı: hedef bu sürede görünmezse kart notla sürer (asla takılmaz). */
export const TARGET_TIMEOUT_MS = 8000;

/** data-tour çapası → sahibi dosya (src'ye göre). targetPin.test okur. */
export const TOUR_ANCHORS: Record<string, string> = {
  'services-table': 'pages/Services.tsx',
  'services-health-dot': 'pages/Services.tsx',
  'services-col-spans': 'pages/Services.tsx',
  'services-col-errors': 'pages/Services.tsx',
  'services-col-p99': 'pages/Services.tsx',
  'services-col-delta': 'pages/Services.tsx',
};

const noServices = (ctx: TourCtx): TourSkipReason | null => (ctx.serviceCount === 0 ? 'no_services' : null);

function withRange(path: string, ctx: TourCtx): string {
  return ctx.range ? `${path}?range=${encodeURIComponent(ctx.range)}` : path;
}

export const TOUR_STEPS: readonly TourStep[] = [
  {
    id: 'welcome',
    route: () => null,
    target: null,
    placement: 'center',
    titleKey: 'tour.welcome.title',
    bodyKey: 'tour.welcome.body',
    requires: () => null,
  },
  {
    id: 'services',
    route: ctx => withRange('/services', ctx),
    // İlk veri satırı (Services.tsx her <tr>'ye data-row-idx basar; toplam
    // satırı .agg-row'da bu damga yok). Satır yoksa tablonun kendisi.
    target: '[data-tour="services-table"] tbody tr[data-row-idx="0"]',
    fallbackTarget: '[data-tour="services-table"]',
    placement: 'bottom',
    titleKey: 'tour.services.title',
    bodyKey: 'tour.services.body',
    requires: noServices,
  },
  // ── R2 — beyanlı, kapalı ────────────────────────────────────────────
  {
    id: 'operations',
    route: ctx => (ctx.service ? serviceHref(ctx.service, { range: ctx.range, tab: 'operations' }) : null),
    target: null,
    placement: 'bottom',
    titleKey: 'tour.operations.title',
    bodyKey: 'tour.operations.body',
    requires: ctx => noServices(ctx) ?? 'not_yet',
  },
  {
    id: 'traces',
    route: ctx => withRange('/traces', ctx),
    target: null,
    placement: 'bottom',
    titleKey: 'tour.traces.title',
    bodyKey: 'tour.traces.body',
    requires: ctx => noServices(ctx) ?? 'not_yet',
  },
  {
    id: 'cosre',
    route: () => null,
    target: null,
    placement: 'center',
    titleKey: 'tour.cosre.title',
    bodyKey: 'tour.cosre.body',
    requires: () => 'not_yet',
  },
];

export const TOUR_TOTAL = TOUR_STEPS.length;

/** Turun kendi çerçeve metinleri (i18n.test hepsinin iki katalogda olduğunu pinler). */
export const TOUR_CHROME_KEYS = [
  'tour.chrome.dialog', 'tour.chrome.stepOf', 'tour.chrome.progress',
  'tour.chrome.back', 'tour.chrome.next', 'tour.chrome.finish', 'tour.chrome.skip',
  'tour.chrome.restart', 'tour.chrome.loading',
  'tour.note.timeout', 'tour.note.not_yet', 'tour.note.no_services',
  'tour.welcome.unavailable',
  'tour.welcome.tile.services', 'tour.welcome.tile.spans', 'tour.welcome.tile.spansTop',
  'tour.welcome.tile.busiest', 'tour.welcome.tile.envs',
] as const;

/** stepOf şablonu: "Step {n} of {total}". */
export function formatStepOf(template: string, n: number, total: number): string {
  return template.replace('{n}', String(n)).replace('{total}', String(total));
}
