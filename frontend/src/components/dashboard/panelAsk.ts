import type { MetricPanelAsk, MetricPanelConfig, Panel, PromqlPanelConfig, SpanMetricPanelConfig } from '@/lib/types';
import { applyVarsToMetric, applyVarsToPromql, applyVarsToSpan } from './PanelRenderer';

// panelAsk — v0.10.1435 (Panelden "Bu grafiği açıkla"). Dashboard panelinin
// ✨ yükü: panelin EKRANDA çalışan (değişkenleri çözülmüş) sorgusu + değişken
// değerleri. Sunucu (internal/api/explain_metric_panel.go) kayıtlı paneli
// kendisi yükler, YALNIZ `vars`'a güvenir ve kendi çözümünü (metric_panel_vars.go,
// bu dosyanın kullandığı applyVarsTo* ikizi) bununla karşılaştırır —
// uyuşmazlık = dürüst ret. Alan kümesi türün sunucu çözümüyle birebir: builder
// field/dsl taşımaz, PromQL yalnız sorguyu taşır.

/** ASKABLE_PANEL_TYPES — ✨ yalnız zaman serisi panellerinde. */
export const ASKABLE_PANEL_TYPES: ReadonlySet<string> = new Set(['metric', 'spanmetric', 'promql']);

export function dashboardPanelAsk(panel: Panel, vars: Record<string, string>): MetricPanelAsk | null {
  const title = panel.title || undefined;
  switch (panel.type) {
    case 'promql': {
      const c = applyVarsToPromql(panel.config as PromqlPanelConfig, vars);
      return { src: 'promql', promql: c.query, unit: c.unit, title, vars };
    }
    case 'metric': {
      const c = applyVarsToMetric(panel.config as MetricPanelConfig, vars);
      if (c.promql !== undefined) return { src: 'promql', promql: c.promql, unit: c.unit, title, vars };
      return {
        src: 'builder', metricName: c.metricName, service: c.service, agg: c.agg,
        groupBy: c.groupBy, filters: c.filters, unit: c.unit, title, vars,
      };
    }
    case 'spanmetric': {
      const c = applyVarsToSpan(panel.config as SpanMetricPanelConfig, vars);
      return { src: 'span', agg: c.agg, field: c.field, groupBy: c.groupBy, dsl: c.dsl, filters: c.filters, unit: c.unit, title, vars };
    }
  }
  return null;
}
