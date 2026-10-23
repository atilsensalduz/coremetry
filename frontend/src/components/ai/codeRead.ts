import { useCallback, useEffect, useState } from 'react';

// codeRead.ts — v0.10.1461 (operasyon kartı planı R2, "bağımsız sohbette kod
// turu"): composer'daki "Kod" hapı. deepThink.ts'in ikizi: durum kullanıcı
// başına localStorage'da, VARSAYILAN KAPALI (kayıt yoksa ya da depolama
// atarsa kapalı); anahtar depolamasız da bellekte çalışır. Açıkken istek
// gövdesine context.code: true gider (internal/api/chat_code_turn.go: tur yerel
// modele gider, o turda dış MCP araçları kurulmaz); kapalıyken gövde bayt bayt
// eski. Sunucu kod okuyamıyorsa (config.codeRead.available=false) hap devre
// dışıdır ve tooltip gerekçeyi söyler; kayıtlı tercih korunur ama gönderilmez.
//
// Aynı sekmedeki composer'lar aynı kullanıcı için senkron kalır (dinleyici kümesi).

const CODE_PREFIX = 'cosre.codeRead.';

export function codeReadKey(user: string | undefined | null): string {
  return CODE_PREFIX + (user || 'anon');
}

export function readCodeRead(user: string | undefined | null): boolean {
  try {
    return window.localStorage.getItem(codeReadKey(user)) === '1';
  } catch {
    return false;
  }
}

const listeners = new Set<(user: string, on: boolean) => void>();

export function writeCodeRead(user: string | undefined | null, on: boolean): void {
  try { window.localStorage.setItem(codeReadKey(user), on ? '1' : '0'); } catch { /* yok — bellekte */ }
  const me = user || 'anon';
  listeners.forEach(l => l(me, on));
}

/** Kullanıcının "Kod" tercihi + değiştirici (kalıcı, sekme içi senkron). */
export function useCodeRead(user: string | undefined | null): [boolean, (on: boolean) => void] {
  const [state, setState] = useState(() => ({ user: user || 'anon', on: readCodeRead(user) }));
  const me = user || 'anon';
  // Kimlik geç gelebilir (/api/auth/me): değişince kayıtlı tercih yeniden okunur.
  if (state.user !== me) setState({ user: me, on: readCodeRead(user) });
  useEffect(() => {
    const l = (u: string, on: boolean) => { if (u === me) setState({ user: me, on }); };
    listeners.add(l);
    return () => { listeners.delete(l); };
  }, [me]);
  const set = useCallback((on: boolean) => {
    setState({ user: me, on });
    writeCodeRead(me, on);
  }, [me]);
  return [state.user === me ? state.on : readCodeRead(user), set];
}
