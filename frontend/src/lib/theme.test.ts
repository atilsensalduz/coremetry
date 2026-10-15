// @vitest-environment jsdom
// theme.test.ts — v0.10.1177: kullanıcı menüsü tema döngüsü + viewer menüsü.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cycleTheme, currentTheme } from './theme';

describe('theme', () => {
  it('dark → light → redhat → dark döngüsü <html data-theme> üstünde', () => {
    document.documentElement.setAttribute('data-theme', 'dark');
    expect(cycleTheme()).toBe('light');
    expect(cycleTheme()).toBe('redhat');
    expect(cycleTheme()).toBe('dark');
    expect(currentTheme()).toBe('dark');
  });
  it('viewer menüsünde şifre değiştirme yok (rol kapısı)', () => {
    const src = readFileSync(resolve(__dirname, '../components/Sidebar.tsx'), 'utf8');
    expect(src).toMatch(/user\.role !== 'viewer' && \(\s*<MenuItem icon="⚿"/);
  });
});
