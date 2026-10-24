// useMediaQuery — v0.10.1501 (/logs Kibana-Discover dilim 3). useNarrow'un
// (v0.9.988) genellenmiş hâli: herhangi bir media query'yi izler, yeniden
// boyutlandırma / cihaz döndürmede güncellenir. useIsNarrow artık bunun
// üstünde tek satır; eşik sayıları (640 / 1280) CSS katmanıyla aynı kalır
// (`styles/mobileBreakpoints.test.ts`).
//
// jsdom'da `window.matchMedia` YOK → `false` (useNarrow'un "yokluğa karşı
// geniş ekran" varsayımı korunur: dar sorgu false, geniş sorgu da false —
// mount testleri bugünkü davranışı görür, yeni kurulum gerekmez).
import { useEffect, useState } from 'react';

export function readMediaQuery(query: string): boolean {
  if (typeof window === 'undefined') return false;
  if (typeof window.matchMedia !== 'function') return false;
  return window.matchMedia(query).matches;
}

export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => readMediaQuery(query));
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const mq = window.matchMedia(query);
    const apply = () => setMatches(mq.matches);
    apply();
    // `addEventListener` Safari 14+ ve tüm modern tarayıcılarda var; eski API
    // için geriye-uyum şimi EKLENMİYOR (CLAUDE.md).
    mq.addEventListener('change', apply);
    return () => mq.removeEventListener('change', apply);
  }, [query]);
  return matches;
}

// D2'nin üç adlı eşiğinden en genişi — globals.css `@media (min-width: 1280px)`.
export const WIDE_MIN_PX = 1280;
export const WIDE_QUERY = `(min-width: ${WIDE_MIN_PX}px)`;
