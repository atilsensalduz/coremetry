// @vitest-environment jsdom
// v0.10.1501 — useMediaQuery (useNarrow'un genellenmiş hâli): ilk değer
// senkron okunur, `change` olayı günceller, matchMedia yoksa false.
import { describe, it, expect, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { useMediaQuery, readMediaQuery, WIDE_QUERY, WIDE_MIN_PX } from './useMediaQuery';
import { NARROW_QUERY } from './useNarrow';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null; let host: HTMLDivElement | null = null;
const realMM = window.matchMedia;
afterEach(() => {
  act(() => { root?.unmount(); }); host?.remove(); root = null; host = null;
  window.matchMedia = realMM;
});

function fakeMM(initial: Record<string, boolean>) {
  const listeners = new Map<string, Set<() => void>>();
  const state = { ...initial };
  window.matchMedia = ((q: string) => ({
    get matches() { return !!state[q]; }, media: q, onchange: null,
    addListener: () => {}, removeListener: () => {},
    addEventListener: (_: string, fn: () => void) => { if (!listeners.has(q)) listeners.set(q, new Set()); listeners.get(q)!.add(fn); },
    removeEventListener: (_: string, fn: () => void) => { listeners.get(q)?.delete(fn); },
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return { set: (q: string, v: boolean) => { state[q] = v; listeners.get(q)?.forEach(f => f()); } };
}

function Probe({ q }: { q: string }) {
  return <span data-v={String(useMediaQuery(q))} />;
}
const value = () => host!.querySelector('span')!.getAttribute('data-v');

describe('useMediaQuery', () => {
  it('eşik CSS katmanıyla aynı (1280)', () => {
    expect(WIDE_MIN_PX).toBe(1280);
    expect(WIDE_QUERY).toBe('(min-width: 1280px)');
  });
  it('ilk değer senkron, change olayı günceller', () => {
    const mm = fakeMM({ [WIDE_QUERY]: true });
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
    act(() => { root!.render(<Probe q={WIDE_QUERY} />); });
    expect(value()).toBe('true');
    act(() => { mm.set(WIDE_QUERY, false); });
    expect(value()).toBe('false');
    expect(readMediaQuery(NARROW_QUERY)).toBe(false);
  });
  it('matchMedia yoksa false (jsdom varsayılanı)', () => {
    (window as { matchMedia?: unknown }).matchMedia = undefined;
    expect(readMediaQuery(WIDE_QUERY)).toBe(false);
    host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host);
    act(() => { root!.render(<Probe q={WIDE_QUERY} />); });
    expect(value()).toBe('false');
  });
});
