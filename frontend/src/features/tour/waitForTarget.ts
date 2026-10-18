// waitForTarget.ts — v0.10.1334 (rehberli tur, R1). Bir adımın hedef
// elemanını BEKLER: önce anında bak, sonra MutationObserver(document.body,
// {childList, subtree}) ile her DOM değişiminde yeniden bak. "Var" demek
// yetmez — görünür olmalı (getBoundingClientRect sıfır değil): gizli bir
// kopya ya da display:none'daki tablo halkayı (0,0)'a çizerdi.
//
// Yedek hedef (`fallback`): asıl hedef (ör. ilk satır) FALLBACK_GRACE_MS
// içinde belirmezse ve yedek (tablonun kendisi) görünürse yedeğe düşülür —
// veri yüklenirken tabloya erken kilitlenmemek için kısa bir pay, boş tabloda
// ise 8 sn beklememek için.
//
// Zaman aşımı (TARGET_TIMEOUT_MS) → {status:'timeout'}: koç kartı notla
// sürer, İleri açık (tur ASLA takılmaz). AbortSignal → {status:'aborted'}
// (adım değişti / tur bitti). Zamanlayıcı enjekte edilebilir; testler
// vi.useFakeTimers() ile sürer.

import { TARGET_TIMEOUT_MS } from './steps';

export const FALLBACK_GRACE_MS = 1500;

export type WaitResult =
  | { status: 'found'; el: Element; via: 'target' | 'fallback' }
  | { status: 'timeout' }
  | { status: 'aborted' };

export interface WaitTimers {
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (id: unknown) => void;
}

export interface WaitOptions {
  fallback?: string;
  signal?: AbortSignal;
  timeoutMs?: number;
  graceMs?: number;
  root?: ParentNode & Node;
  timers?: WaitTimers;
  isVisible?: (el: Element) => boolean;
}

export function isRectVisible(el: Element): boolean {
  const r = el.getBoundingClientRect();
  return r.width > 0 && r.height > 0;
}

const defaultTimers: WaitTimers = {
  setTimeout: (fn, ms) => globalThis.setTimeout(fn, ms),
  clearTimeout: id => globalThis.clearTimeout(id as ReturnType<typeof globalThis.setTimeout>),
};

function query(root: ParentNode, sel: string | undefined): Element | null {
  if (!sel) return null;
  try {
    return root.querySelector(sel);
  } catch {
    return null; // geçersiz seçici = hedef yok (asla fırlatma)
  }
}

export function waitForTarget(selector: string, opts: WaitOptions = {}): Promise<WaitResult> {
  const root = opts.root ?? (typeof document !== 'undefined' ? document.body : null);
  const timers = opts.timers ?? defaultTimers;
  const visible = opts.isVisible ?? isRectVisible;
  const timeoutMs = opts.timeoutMs ?? TARGET_TIMEOUT_MS;
  const graceMs = Math.min(opts.graceMs ?? FALLBACK_GRACE_MS, timeoutMs);

  return new Promise<WaitResult>(resolve => {
    if (!root) { resolve({ status: 'timeout' }); return; }
    if (opts.signal?.aborted) { resolve({ status: 'aborted' }); return; }

    let settled = false;
    let graceOver = false;
    let mo: MutationObserver | null = null;
    let timeoutId: unknown = null;
    let graceId: unknown = null;

    const finish = (r: WaitResult) => {
      if (settled) return;
      settled = true;
      mo?.disconnect();
      if (timeoutId !== null) timers.clearTimeout(timeoutId);
      if (graceId !== null) timers.clearTimeout(graceId);
      opts.signal?.removeEventListener('abort', onAbort);
      resolve(r);
    };
    const onAbort = () => finish({ status: 'aborted' });

    const check = (): boolean => {
      const t = query(root, selector);
      if (t && visible(t)) { finish({ status: 'found', el: t, via: 'target' }); return true; }
      if (graceOver) {
        const f = query(root, opts.fallback);
        if (f && visible(f)) { finish({ status: 'found', el: f, via: 'fallback' }); return true; }
      }
      return false;
    };

    if (check()) return;

    opts.signal?.addEventListener('abort', onAbort);
    if (typeof MutationObserver !== 'undefined') {
      mo = new MutationObserver(() => { check(); });
      mo.observe(root, { childList: true, subtree: true });
    }
    if (opts.fallback) {
      graceId = timers.setTimeout(() => { graceId = null; graceOver = true; check(); }, graceMs);
    }
    timeoutId = timers.setTimeout(() => {
      timeoutId = null;
      graceOver = true;
      if (!check()) finish({ status: 'timeout' });
    }, timeoutMs);
  });
}
