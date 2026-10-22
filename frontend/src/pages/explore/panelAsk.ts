import { encodeFilterGroup, encodeFilters } from '@/lib/urlState';
import type { MetricPanelAsk } from '@/lib/types';
import { effectiveFilterGroup, effectiveFilters, hasGroupedFilter, queryDesc, queryUnit, type BuilderQuery } from './model';

// panelAsk — v0.10.1435 (Panelden "Bu grafiği açıkla"). Explore sorgu
// panelinin ✨ yükü: panelin fetch'iyle (useExploreQueries exploreQueryFn)
// AYNI parametreler — span → /api/spans/metric alanları (filters | filterGroup,
// dsl, field), metric → /api/metrics/query alanları. Sunucu bunları HTTP
// uçlarının kendi filtre yapılarına çözer (explain_metric_panel.go); ham sorgu
// dili yok, Explore'da PromQL yok. Formül / üretmeyen sorgu → null (✨ yok).
export function exploreQueryAsk(q: BuilderQuery): MetricPanelAsk | null {
  if (!q.enabled) return null;
  const title = `Explore · ${q.letter} · ${queryDesc(q)}`;
  const unit = queryUnit(q) || undefined;
  const groupBy = q.splitBy.join(',') || undefined;
  const filters = effectiveFilters(q);
  if (q.source === 'span') {
    const grouped = hasGroupedFilter(q);
    return {
      src: 'span', agg: q.agg, field: q.metric || undefined, groupBy,
      filters: grouped ? undefined : (filters.length ? JSON.stringify(filters) : undefined),
      filterGroup: grouped ? (encodeFilterGroup(effectiveFilterGroup(q)) || undefined) : undefined,
      dsl: q.dsl.trim() || undefined, unit, title,
    };
  }
  if (!q.metric) return null;
  return {
    src: 'builder', metricName: q.metric, agg: q.agg, groupBy,
    filters: filters.length ? encodeFilters(filters) : undefined, unit, title,
  };
}

/**
 * exploreSelectedSeries — SAF: Explore'da bir harfin SEÇİLİ serileri. GroupTable
 * izolasyonu (gizlenmeyen ≤3 seri, en az biri gizli) önce; yoksa odaklanan
 * (hover) satır. Anahtarlar `${harf}:${etiket}`.
 */
export function exploreSelectedSeries(letter: string, labels: readonly string[], hiddenKeys: ReadonlySet<string>, focusKey: string | null): string[] {
  const shown = labels.filter(l => !hiddenKeys.has(`${letter}:${l}`));
  if (labels.length > 1 && shown.length > 0 && shown.length < labels.length && shown.length <= 3) return shown;
  if (focusKey && focusKey.startsWith(`${letter}:`)) return [focusKey.slice(letter.length + 1)];
  return [];
}
