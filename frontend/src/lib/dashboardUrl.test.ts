// v0.9.779 — /dashboard adres kuralları.
//
// REZERVE SET. Dashboard.tsx'teki değişken aynası rezerve olmayan HER
// paramı siliyor. 'refresh' / 'kiosk' listeye girmezse effect paramı her
// yazımda yok eder; auto-refresh açılır ve bir sonraki render'da
// kendiliğinden kapanır — sessiz, "bazen çalışıyor" görünen bir hata
// sınıfı.
//
// v0.10.1430 — aralık ayrıştırması (parseRefreshParam/refreshLabel/
// REFRESH_CHOICES) global seçiciye taşındı; tabloları lib/autoRefresh.test.ts
// taşıyor. 'refresh' rezerve kalır: param artık GLOBAL yazılıyor ve ayna
// onu silerse seçim bu sayfada sessizce düşer.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { DASHBOARD_RESERVED_PARAMS, isDashboardVariableParam } from './dashboardUrl';

describe('DASHBOARD_RESERVED_PARAMS', () => {
  it('sayfanın kendi paramlarını kapsar (refresh + kiosk dahil)', () => {
    for (const k of ['id', 'edit', 'range', 'refresh', 'kiosk']) {
      expect(DASHBOARD_RESERVED_PARAMS.has(k)).toBe(true);
    }
  });

  it('rezerve param değişken değeri SAYILMAZ', () => {
    expect(isDashboardVariableParam('refresh')).toBe(false);
    expect(isDashboardVariableParam('kiosk')).toBe(false);
    expect(isDashboardVariableParam('range')).toBe(false);
  });

  it('pano değişkeni adları değişken değeri sayılır', () => {
    expect(isDashboardVariableParam('service')).toBe(true);
    expect(isDashboardVariableParam('namespace')).toBe(true);
  });
});

describe('v0.10.1430 — Dashboard kendi auto-refresh mekaniğini taşımaz', () => {
  it('RefreshControl / refreshTick / parseRefreshParam Dashboard.tsx\'te yok; edit modu global saati tutar', () => {
    const src = readFileSync(resolve(__dirname, '../pages/Dashboard.tsx'), 'utf8');
    const code = src.replace(/\{\/\*[\s\S]*?\*\/\}/g, '').replace(/^\s*\/\/.*$/gm, '');
    expect(code).not.toMatch(/function RefreshControl|<RefreshControl|refreshTick|parseRefreshParam|REFRESH_CHOICES/);
    expect(code).toContain("useRefreshHold(editing, 'edit')");
    const pr = readFileSync(resolve(__dirname, '../components/dashboard/PanelRenderer.tsx'), 'utf8')
      .replace(/^\s*\/\/.*$/gm, '');
    expect(pr).not.toContain('refreshTick');
  });
});
