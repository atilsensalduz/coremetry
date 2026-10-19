// useTypewriter — v0.10.1384: büyük parçada gelen cevabı hızlı "yazarak" açar.
//
// Sözleşme (ayrıntı: revealSchedule.ts):
//   • `arrival` yoksa metin OLDUĞU GİBİ döner — gerçek token akışı, arşivden
//     geri yüklenen / devralınan tur, önbellekten gelen açıklama: efekt yok.
//   • Zaman tabanlı: görünür uzunluk = f(şimdi − arrival.at). Bileşen açılış
//     sırasında yeniden bağlanırsa kaldığı yerden sürer; süre geçmişse hiç
//     açılmaz (çekmeceyi kapatıp açmak cevabı yeniden yazdırmaz).
//   • Tek rAF döngüsü, tek durum; çizim yalnız kesme noktası değişince.
//   • prefers-reduced-motion (ya da matchMedia/rAF yok) → açılma yok.
//   • `finish()` = tıkla-bitir: tamamı anında.
//   • `stickRef` verilirse ve kaydırma kabı açılış adımından önce dipteyse
//     adım sonrası dibe yapışır (stickToBottom'un delta davranışının aynısı;
//     o kanca yalnız `turns` değişiminde koşar, açılış adımlarını görmez).
import { useCallback, useLayoutEffect, useRef, useState, type RefObject } from 'react';
import type { ChatArrival } from '@/lib/types';
import { revealDurationMs, revealIndexAt, revealStops } from './revealSchedule';
import { findScrollParent, isNearBottom } from './stickToBottom';

export interface Typewriter {
  /** Ekranda olması gereken metin (açılırken önek). */
  text: string;
  /** Açılış sürüyor: çizim `streaming=true` ile yapılmalı (yarım satır/çit kuralları). */
  revealing: boolean;
  /** Tıkla-bitir: açılışı keser, tamamı görünür. */
  finish: () => void;
}

/** Hareket serbest mi? Tarayıcı dışında / reduced-motion'da hayır. */
export function revealMotionAllowed(): boolean {
  if (typeof window === 'undefined') return false;
  if (typeof window.matchMedia !== 'function' || typeof window.requestAnimationFrame !== 'function') return false;
  try {
    return !window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  } catch {
    return false;
  }
}

export function useTypewriter(
  text: string,
  arrival: ChatArrival | undefined,
  opts?: { stickRef?: RefObject<HTMLElement | null> },
): Typewriter {
  // Tek durum: açılan varışın kimliği + görünür uzunluk. null = tamamı.
  const [vis, setVis] = useState<{ at: number; len: number } | null>(null);
  const textRef = useRef(text);
  textRef.current = text;
  const visRef = useRef(vis);
  visRef.current = vis;
  const finishedAt = useRef<number | null>(null);
  const cancelRef = useRef<(() => void) | null>(null);
  const nearRef = useRef(false);
  const stickRef = opts?.stickRef;
  const at = arrival?.at;
  const from = arrival?.from ?? 0;

  // useLayoutEffect: ilk kare boyamadan ÖNCE önek'e iner — tam metin bir
  // kare bile parlamaz. Bağımlılık yalnız varış kimliği: metin büyümesi
  // döngüyü yeniden kurmaz, döngü textRef'ten okur.
  useLayoutEffect(() => {
    cancelRef.current?.();
    cancelRef.current = null;
    if (at == null || finishedAt.current === at || !revealMotionAllowed()) {
      if (visRef.current) setVis(null);
      return;
    }
    // Önceki açılış yarımken yeni büyük parça geldiyse: görünenden devam
    // (önceki parçanın açılmamış kuyruğu birden belirmesin).
    const prev = visRef.current;
    const start = prev && prev.at !== at ? Math.min(from, prev.len) : from;
    let stopsFor = '';
    let stops: number[] = [];
    let raf = 0;
    let lastLen = -1;
    let scroller: HTMLElement | null | undefined;
    const tick = () => {
      const t = textRef.current;
      if (t !== stopsFor) { stops = revealStops(t, start); stopsFor = t; }
      const dur = revealDurationMs(t.length - start);
      const p = dur > 0 ? (Date.now() - at) / dur : 1;
      const len = revealIndexAt(stops, start, p);
      if (p >= 1 || len >= t.length) {
        raf = 0;
        cancelRef.current = null;
        setVis(null);
        return;
      }
      if (len !== lastLen) {
        lastLen = len;
        if (stickRef) {
          if (scroller === undefined) scroller = findScrollParent(stickRef.current);
          nearRef.current = !!scroller && isNearBottom(scroller);
        }
        setVis({ at, len });
      }
      raf = window.requestAnimationFrame(tick);
    };
    // Süresi geçmiş varış (yeniden bağlanan bileşen) hiç açılmaz.
    if (Date.now() - at >= revealDurationMs(text.length - start)) {
      if (visRef.current) setVis(null);
      return;
    }
    tick();
    cancelRef.current = () => { if (raf) window.cancelAnimationFrame(raf); raf = 0; };
    return () => { cancelRef.current?.(); cancelRef.current = null; };
    // text bilerek dışarıda: döngü textRef'i okur (yukarıdaki not).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [at, from]);

  // Açılış adımı DOM'a yazıldıktan sonra: adımdan önce dipteyse dibe yapış.
  const len = vis?.len;
  useLayoutEffect(() => {
    if (len == null || !stickRef || !nearRef.current) return;
    const el = findScrollParent(stickRef.current);
    if (el) el.scrollTop = el.scrollHeight;
  }, [len, stickRef]);

  const finish = useCallback(() => {
    if (at == null) return;
    finishedAt.current = at;
    cancelRef.current?.();
    cancelRef.current = null;
    setVis(null);
  }, [at]);

  const revealing = vis !== null && vis.at === at && vis.len < text.length;
  return { text: revealing ? text.slice(0, vis.len) : text, revealing, finish };
}
