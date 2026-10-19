// @vitest-environment jsdom
// v0.10.1384 — tek tip AI "düşünüyor" göstergesi. Operatör: "CoSRE düşünüyor
// derken ve explain trace'teki dönme efektleri aynı değil."
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { AiThinking, AI_THINKING_LABEL } from './AiThinking';

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
});
afterEach(() => { act(() => root.unmount()); host.remove(); });

const mount = async (node: React.ReactNode) => { await act(async () => { root.render(node); }); };

describe('AiThinking', () => {
  it('varsayılan: row, role=status, kıvılcım ikonu + "Düşünüyor…"', async () => {
    await mount(<AiThinking />);
    const el = host.querySelector('[role="status"]')!;
    expect(el.className).toBe('ai-thinking ai-thinking--row');
    expect(el.tagName).toBe('SPAN');
    expect(el.textContent).toBe(AI_THINKING_LABEL);
    expect(AI_THINKING_LABEL).toBe('Düşünüyor…');
    expect(el.querySelector('.ai-thinking__icon[aria-hidden="true"] svg')).toBeTruthy();
    expect(el.querySelector('.ai-thinking__label')?.textContent).toBe('Düşünüyor…');
  });
  it('block: div, etiket + ipucu + eşlik eden eylem', async () => {
    await mount(<AiThinking variant="block" label="CoSRE düşünüyor…" hint="cevap bekleniyor"><i id="act" /></AiThinking>);
    const el = host.querySelector('[role="status"]')!;
    expect(el.tagName).toBe('DIV');
    expect(el.className).toBe('ai-thinking ai-thinking--block');
    expect(el.querySelector('.ai-thinking__hint')?.textContent).toBe('cevap bekleniyor');
    expect(el.querySelector('#act')).toBeTruthy();
  });
  it('inline: düğme içi', async () => {
    await mount(<AiThinking variant="inline" />);
    expect(host.querySelector('[role="status"]')!.className).toBe('ai-thinking ai-thinking--inline');
  });
});

describe('AiThinking CSS — nabız (dönme yok) + reduced-motion', () => {
  const css = readFileSync(resolve(__dirname, '..', '..', 'styles', 'globals.css'), 'utf8');
  const rules = css.split('\n').filter(l => l.includes('.ai-thinking'));
  it('ikon nabız atar, hiçbir ai-thinking kuralı dönmez', () => {
    expect(css).toMatch(/\.ai-thinking__icon \{[^}]*animation: ai-think-pulse/);
    expect(css).toMatch(/@keyframes ai-think-pulse/);
    expect(rules.join('\n')).not.toMatch(/\bspin\b|rotate\(/);
    expect(css).toMatch(/@keyframes ai-think-pulse[^\n]*/);
    expect(/@keyframes ai-think-pulse[^\n]*rotate/.test(css)).toBe(false);
  });
  it('etiket parıltısı + prefers-reduced-motion dalı animasyonu kapatır', () => {
    expect(css).toMatch(/\.ai-thinking__label \{[^}]*animation: ai-think-shimmer/);
    const rm = css.split('@media (prefers-reduced-motion: reduce)').slice(1).join('\n');
    expect(rm).toMatch(/\.ai-thinking \.ai-thinking__icon, \.ai-thinking \.ai-thinking__label \{ animation: none; \}/);
  });
  it('eski .cm-thinking ailesi silindi (bileşen gidince CSS de gider)', () => {
    expect(css).not.toMatch(/^\.cm-thinking/m);
    expect(css).not.toMatch(/cm-shimmer/);
  });
});
