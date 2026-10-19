// @vitest-environment jsdom
//
// v0.10.1377 — metrik kipi ortam süzgeci bandı (adlar SENTETİK): uygulandı →
// b-info, uygulanamadı / çözülemedi → b-warn, bekliyor → b-gray; env/cluster
// seçili değilse hiçbir şey çizilmez (bayt bayt eski sayfa).
import { describe, it, expect, afterEach } from 'vitest';
import { createRoot, type Root } from 'react-dom/client';
import { act } from 'react';
import { HttpEnvScopeBand } from './httpMetricsViz';
import type { HttpEnvScopeView } from '@/lib/httpEnvScopeNote';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root | null = null;
let host: HTMLDivElement | null = null;

function render(view: HttpEnvScopeView) {
  host = document.createElement('div');
  document.body.appendChild(host);
  root = createRoot(host);
  act(() => { root!.render(<HttpEnvScopeBand view={view} />); });
  return host.querySelector('[data-testid="http-env-scope-band"]') as HTMLElement | null;
}

afterEach(() => {
  act(() => { root?.unmount(); });
  host?.remove();
  root = null; host = null;
});

describe('HttpEnvScopeBand (v0.10.1377)', () => {
  it('off → bant yok', () => {
    expect(render({ state: 'off' })).toBeNull();
    expect(host!.innerHTML).toBe('');
  });

  it('uygulandı → b-info, etiket=değer, title PromQL eşleyicisini taşır', () => {
    const el = render({
      state: 'applied', envSel: 'deployment_environment_name="prod-eu-a"',
      scope: {
        label: 'deployment_environment_name', source: 'default', applied: true, matcher: 'deployment_environment_name="prod-eu-a"',
        terms: [{ role: 'env', want: 'eu-a', label: 'deployment_environment_name', values: ['prod-eu-a'], tier: 'prefix' }],
      },
    })!;
    expect(el.className).toContain('b-info');
    expect(el.dataset.state).toBe('applied');
    expect(el.textContent).toBe('Ortam süzgeci metrikte uygulandı: deployment_environment_name=prod-eu-a (env eu-a → prod-eu-a, önek eşleşmesi)');
    expect(el.getAttribute('title')).toContain('PromQL: deployment_environment_name="prod-eu-a"');
    expect(el.getAttribute('title')).toContain('Ortam etiketi deployment_environment_name (varsayılan)');
  });

  it('uygulanamadı / çözülemedi → b-warn + TÜM ortamlar', () => {
    const un = render({
      state: 'unresolved',
      scope: {
        label: 'deployment_environment_name', source: 'configured', applied: false,
        terms: [{ role: 'cluster', want: 'eu-z', label: 'deployment_environment_name', tier: 'none' }],
      },
    })!;
    expect(un.className).toContain('b-warn');
    expect(un.textContent).toContain('metrikte bu ortam yok: deployment_environment_name değerleri arasında "eu-z" eşleşmedi');
    expect(un.textContent).toContain('seriler TÜM ortamları kapsıyor');
    act(() => { root?.unmount(); });
    host?.remove();
    const err = render({ state: 'error', reason: 'VM etiket sorgusu başarısız: timeout' })!;
    expect(err.className).toContain('b-warn');
    expect(err.textContent).toBe('Ortam süzgeci çözülemedi (VM etiket sorgusu başarısız: timeout) — süzgeç uygulanmadı, seriler TÜM ortamları kapsıyor');
  });

  it('bekliyor → nötr', () => {
    const el = render({ state: 'pending' })!;
    expect(el.className).toContain('b-gray');
    expect(el.textContent).toBe('Ortam süzgeci çözülüyor…');
  });
});
