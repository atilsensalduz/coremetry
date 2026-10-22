import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import type { Panel, PanelType } from '@/lib/types';
import { dashboardPanelAsk } from './panelAsk';

// v0.10.1435 (Panelden "Bu grafiği açıkla") — dashboard panelinin ${var}
// genişletmesi Go ikiziyle (internal/api/metric_panel_vars.go
// substitutePanelVars) AYNI altın tablodan sınanır. Sunucu kayıtlı paneli
// kendisi çözer ve istemcinin yüküyle karşılaştırır; ayrışma = her dashboard
// ✨'ı "kayıtlı panelle uyuşmuyor" der.
interface Golden {
  cases: { name: string; type: PanelType; config: Record<string, unknown>; vars: Record<string, string>; want: Record<string, string> }[];
}
const golden = JSON.parse(readFileSync(resolve(__dirname, '../../../../internal/api/testdata/panel_vars_golden.json'), 'utf8')) as Golden;
const FIELDS = ['src', 'promql', 'metricName', 'service', 'agg', 'groupBy', 'filters', 'filterGroup', 'dsl', 'field'] as const;

describe('panel değişken genişletmesi = Go altın tablosu', () => {
  it('tablo dolu', () => { expect(golden.cases.length).toBeGreaterThanOrEqual(8); });
  for (const c of golden.cases) {
    it(c.name, () => {
      const panel = { id: 'p1', type: c.type, title: 'T', width: 2, config: c.config } as unknown as Panel;
      const got = dashboardPanelAsk(panel, c.vars);
      expect(got).not.toBeNull();
      for (const f of FIELDS) {
        expect(((got as unknown as Record<string, string | undefined>)[f] ?? '').trim(), `${c.name} · ${f}`).toBe((c.want[f] ?? '').trim());
      }
      expect(got?.vars).toEqual(c.vars);
    });
  }
  it('zaman serisi olmayan panel → null', () => {
    expect(dashboardPanelAsk({ id: 'm', type: 'markdown', title: '', width: 2, config: { content: 'x' } } as unknown as Panel, {})).toBeNull();
  });
});
