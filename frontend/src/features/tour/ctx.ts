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
