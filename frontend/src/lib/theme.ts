// theme.ts — v0.10.1177: tema döngüsü (üst çubuk ThemeToggle + kullanıcı menüsü ortak).
import { setRaw } from '@/lib/storage';

export type Theme = 'dark' | 'light' | 'redhat';

const STORAGE_KEY = 'coremetry-theme';

export const THEME_NEXT: Record<Theme, Theme> = { dark: 'light', light: 'redhat', redhat: 'dark' };
export const THEME_GLYPH: Record<Theme, string> = { dark: '☾', light: '☀', redhat: '⬢' };
const LABEL: Record<Theme, string> = {
  dark: 'Dark', light: 'Light', redhat: 'Red Hat',
};

/** v0.10.1177 — kullanıcı menüsü de aynı döngüyü kullanır (Sidebar). */
export const THEME_LABEL = LABEL;

/** Şu anki tema (<html data-theme>). */
export function currentTheme(): Theme {
  const t = document.documentElement.getAttribute('data-theme');
  return t === 'dark' || t === 'light' ? t : 'redhat';
}

/** Sıradaki temaya geçer, kalıcı yazar; yeni temayı döner. */
export function cycleTheme(): Theme {
  const next = THEME_NEXT[currentTheme()];
  document.documentElement.setAttribute('data-theme', next);
  setRaw(STORAGE_KEY, next);
  return next;
}

