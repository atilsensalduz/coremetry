// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { HttpDonut } from './httpMetricsViz';

// v0.10.1258 — operatör: "pasta dilimleri tıklanamıyor, tooltip yok".
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
let root: Root | null = null;
let host: HTMLDivElement | null = null;
afterEach(() => { act(() => root?.unmount()); host?.remove(); root = null; host = null; });

function mount(onSelect?: (l: string) => void) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => root!.render(
    <HttpDonut slices={[{ label: '/a', value: 75, pct: 75 }, { label: '/b', value: 25, pct: 25 }]} fmt={v => String(v)} onSelect={onSelect} />,
  ));
  return host;
}

describe('HttpDonut', () => {
  it('dilim üzerine gelince ad + yüzde görünür; dilimde başlık (tooltip) var', () => {
    const h = mount();
    const slices = h.querySelectorAll('circle');
    expect(slices[1].querySelector('title')?.textContent).toBe('/b: 25 (%25.0)');
    act(() => { slices[1].dispatchEvent(new MouseEvent('mouseover', { bubbles: true })); });
    expect(h.textContent).toContain('%25.0');
  });
  it('dilim ve lejant tıkı onSelect çağırır', () => {
    const onSelect = vi.fn();
    const h = mount(onSelect);
    act(() => { h.querySelectorAll('circle')[0].dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    act(() => { h.querySelectorAll('button')[1].click(); });
    expect(onSelect.mock.calls.map(c => c[0])).toEqual(['/a', '/b']);
  });
});
