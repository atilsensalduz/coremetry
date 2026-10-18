// autoStart.ts — v0.10.1334 (rehberli tur, R1). SAF oto-başlatma kapısı.
// Tur kendiliğinden YALNIZ şu hepsi doğruysa açılır:
//   enabled (branding.onboardingTour ≠ 'off') · sunucu status 'none' ·
//   newUser (hesap ≤ 7 gün) · bu sekmede henüz oto-başlamadı
//   (sessionStorage cm.tour.autoStarted) · bu tarayıcıda bitirilmedi/
//   atlanmadı (localStorage coremetry.tour.seen.<uid> — flaş bekçisi) ·
//   kromlu kabuk (kiosk-çıplak / ?kiosk=1 / cosre-çıplak / public değil,
//   AppShell.tsx dalları) · rota /login değil · ?tour=0 yok.
// Menüden "Turu yeniden başlat" bu kapıların HİÇBİRİNE bakmaz.

import { isPublicPath } from '@/lib/auth-paths';
import { isKioskBare } from '@/lib/kioskMode';
import { isCosrePage } from '@/lib/cosrePage';
import type { TourPersistedStatus } from '@/lib/types';

/** Turun bu konumda HİÇ çizilmeyeceği kabuklar (oto ve menü fark etmez). */
export function isTourBlockedLocation(pathname: string, search: string): boolean {
  if (pathname === '/login' || isPublicPath(pathname)) return true;
  if (isKioskBare(pathname, search) || isCosrePage(pathname)) return true;
  const q = new URLSearchParams(search);
  return q.get('kiosk') === '1';
}

export interface AutoStartInput {
  enabled: boolean;
  status: TourPersistedStatus;
  newUser: boolean;
  sessionAutoStarted: boolean;
  localSeen: boolean;
  pathname: string;
  search: string;
}

export function shouldAutoStart(i: AutoStartInput): boolean {
  if (!i.enabled || i.status !== 'none' || !i.newUser) return false;
  if (i.sessionAutoStarted || i.localSeen) return false;
  if (isTourBlockedLocation(i.pathname, i.search)) return false;
  return new URLSearchParams(i.search).get('tour') !== '0';
}
