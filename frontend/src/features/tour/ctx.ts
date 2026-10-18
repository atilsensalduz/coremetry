// ctx.ts — v0.10.1334 (rehberli tur). Tur bağlamı tur BAŞLARKEN bir kez
// çözülür (TourProvider.begin); bu dosya o çözümün saf yarısı.
import type { TourCtx } from './steps';
import type { TourTiles } from './queries';

/** SAF: karo modeli + konum → bağlam. tiles null = okunamadı (-1: bilinmiyor). */
export function buildTourCtx(tiles: TourTiles | null, here: { pathname: string; search: string }, copilotEnabled: boolean): TourCtx {
  return {
    service: tiles?.busiest?.name ?? null,
    range: new URLSearchParams(here.search).get('range'),
    serviceCount: tiles ? tiles.totalServices : -1,
    spans5m: tiles?.spans ?? 0,
    environments: tiles?.environments ?? 0,
    copilotEnabled,
    returnTo: here.pathname + here.search,
  };
}

// v0.10.1336 — copilot yapılandırması bilinmiyorsa (ilk /api/copilot/config
// henüz dönmedi) tur en çok COPILOT_WAIT_MS bekler, sonra KAPALI sayar: adım
// 5 "CoSRE bu kurulumda kapalı" not kartına döner, tur asla takılmaz.
// Zamanlayıcı enjekte edilebilir (engine.test sahte zamanlayıcıyla sürer).
export interface CtxTimers {
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (id: unknown) => void;
}

const realTimers: CtxTimers = {
  setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
  clearTimeout: id => globalThis.clearTimeout(id as ReturnType<typeof globalThis.setTimeout>),
};

/** SAF-ish: p `ms` içinde çözülmezse (ya da reddedilirse) `fallback`. */
export function withTimeout<T>(p: Promise<T>, ms: number, fallback: T, timers: CtxTimers = realTimers): Promise<T> {
  return new Promise<T>(resolve => {
    let settled = false;
    const id = timers.setTimeout(() => { if (!settled) { settled = true; resolve(fallback); } }, ms);
    p.then(
      v => { if (!settled) { settled = true; timers.clearTimeout(id); resolve(v); } },
      () => { if (!settled) { settled = true; timers.clearTimeout(id); resolve(fallback); } },
    );
  });
}
