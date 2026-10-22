import { useCallback } from 'react';
import { formatAiParam, METRIC_PANEL_SELECTED_MAX, type AISubject } from '@/lib/aiSubject';
import { t } from '@/lib/i18n';
import { stageMetricPanelAsk } from '@/lib/pageContextOverlay';
import type { PanelMenuAction } from '@/lib/chart/panelMenu';
import type { MetricPanelAsk } from '@/lib/types';
import { useAiSubject } from './useAiSubject';
import { useCopilotEnabled } from './useCopilotEnabled';

// metricPanelAsk — v0.10.1435 (Panelden "Bu grafiği açıkla"; brush aralığı ve
// seçili seri bağlamda). Üç yüzey (Dashboard panel menüsü, Explore sorgu
// paneli, Metrics editörü önizlemesi) AYNI yoldan sorar:
//   1. özne = KONUM + pencere + brush + ≤3 seçili seri (metricPanelSubject, SAF);
//   2. panelin sorgusu (MetricPanelAsk) stageMetricPanelAsk ile bırakılır;
//   3. `?ai=metric_panel:…` yazılır (useAiSubject, replace:true) — CopilotChat
//      onu tek seferlik niyet olarak tüketir, yükü alır, sohbet turu gönderir.

/** Brush ya da pencere: ns uçları (ms'ye hizalı). */
export interface NsWindow { fromNs: number; toNs: number }

const msAlign = (ns: number, up: boolean): number => (up ? Math.ceil(ns / 1e6) : Math.floor(ns / 1e6)) * 1e6;

/**
 * metricPanelSubject — SAF: konum + pencere + (pencereye kırpılmış) brush +
 * ≤3 seçili seri → özne. Brush pencere dışına taşarsa kırpılır, boş kalırsa
 * yok sayılır; pencerenin tamamını kaplayan brush da brush değildir.
 */
export function metricPanelSubject(loc: string, win: NsWindow, brush: NsWindow | null | undefined, series: readonly string[]): AISubject | null {
  const fromNs = msAlign(win.fromNs, false), toNs = msAlign(win.toNs, true);
  if (!(fromNs > 0) || !(toNs > fromNs)) return null;
  let brushFromNs = 0, brushToNs = 0;
  if (brush) {
    const bf = Math.max(msAlign(brush.fromNs, false), fromNs);
    const bt = Math.min(msAlign(brush.toNs, true), toNs);
    if (bt > bf && !(bf === fromNs && bt === toNs)) { brushFromNs = bf; brushToNs = bt; }
  }
  const sel = series.map(s => s.trim()).filter(Boolean).slice(0, METRIC_PANEL_SELECTED_MAX);
  return { kind: 'metric_panel', id: loc, fromNs, toNs, brushFromNs, brushToNs, series: sel };
}

/** useMetricPanelAsk — copilot açıksa `ask(özne, yük)`: yükü bırakır, `?ai=` yazar. */
export function useMetricPanelAsk(): { enabled: boolean; ask: (s: AISubject, panel: MetricPanelAsk) => void } {
  const enabled = useCopilotEnabled() === true;
  const [, setAi] = useAiSubject();
  const ask = useCallback((s: AISubject, panel: MetricPanelAsk) => {
    stageMetricPanelAsk(formatAiParam(s), panel);
    setAi(s);
  }, [setAi]);
  return { enabled, ask };
}

/** metricPanelMenuAction — CorePanel ⋯ menüsünün devredilen satırı (panelMenu sözleşmesi). */
export function metricPanelMenuAction(onClick: () => void): PanelMenuAction {
  return { key: 'ask-cosre', label: `✨ ${t('ai.askCosre')}`, onClick };
}
