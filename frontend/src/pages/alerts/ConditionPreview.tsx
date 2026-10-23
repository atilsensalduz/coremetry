import { useMemo } from 'react';
import { MultiLineChart } from '@/components/MultiLineChart';
import { comparatorSide } from '@/lib/chart/thresholdLines';
import { useAlertPreviewSeries } from '@/lib/queries';
import { alertSeriesToSpan } from '@/features/anomalies/alertMetricSeries';
import type { AlertRule } from '@/lib/types';
import { AlertSourceBadge } from '@/components/AlertSourceBadge';
import { previewUnit, wouldFireCount } from '@/lib/alertSource';

// ConditionPreview — the prototype's live Alert-rule preview: the rule's metric
// over the last hour with a dashed threshold line + a "would have fired N×"
// count scoped to the rule's service. Lets the operator tune the threshold
// against real data before saving.
// Split out of the Alerts.tsx monolith (v0.8.252 refactor) verbatim.
//
// v0.10.1489 — dizi GET /api/alert-rules/preview-series'ten: değerlendiricinin
// KENDİ kaynağı (VictoriaMetrics örneklenmemiş histogramı ya da span) ve
// taslağın penceresi; kaynak rozetle gösterilir — önizleme ≠ canlı olmasın.
// Eskiden /api/metrics/resolve (spanmetrics, kova başına ham değer, yalnız dört
// metrik) okunuyordu. Kural servis başına değerlendirildiği için servis seçilmeden
// dizi çizilmez.
export function ConditionPreview({ draft }: { draft: Partial<AlertRule> }) {
  const metric = draft.metric ?? 'error_rate';
  const svc = draft.service ?? '';
  const windowSec = draft.windowSec || 300;
  const threshold = Number(draft.threshold ?? 0);
  const comparator = draft.comparator ?? '>';
  const sevTone: 'warn' | 'err' = draft.severity === 'critical' ? 'err' : 'warn';
  const unit = previewUnit(metric);

  // Fixed 1h lookback captured once → no now()-per-render refetch churn.
  const win = useMemo(() => {
    const to = Date.now() * 1_000_000;
    return { from: to - 3600 * 1_000_000_000, to };
  }, []);
  const args = useMemo(
    () => (svc ? { service: svc, metric, windowSec, fromNs: win.from, toNs: win.to } : null),
    [svc, metric, windowSec, win.from, win.to]);
  const q = useAlertPreviewSeries(args);
  const data = q.data;
  const series = useMemo(() => (data ? alertSeriesToSpan(data) : []), [data]);
  const pts = data?.points ?? [];
  const wouldFire = wouldFireCount(pts.map(p => p.v), comparator, threshold);

  const box = { height: 130, display: 'grid', placeItems: 'center', color: 'var(--text3)', fontSize: 12 } as const;
  return (
    <div style={{ marginTop: 12, border: '1px solid var(--border)', borderRadius: 8, background: 'var(--bg2)', padding: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
        <span style={{ fontSize: 11, fontWeight: 700, letterSpacing: '0.5px', textTransform: 'uppercase', color: 'var(--text2)' }}>Condition preview</span>
        <span style={{ fontSize: 11, color: 'var(--text3)' }}>last 1h · {svc || 'servis seçin'} · {windowSec / 60} dk pencere</span>
        {data?.source && <AlertSourceBadge source={data.source} />}
        <span style={{ marginLeft: 'auto', fontSize: 12, fontWeight: 700, color: wouldFire > 0 ? `var(--${sevTone})` : 'var(--text3)' }}>
          would have fired {wouldFire}×<span style={{ fontWeight: 500, color: 'var(--text3)' }}> ({pts.length} buckets)</span>
        </span>
      </div>
      {!svc ? (
        <div style={box}>Önizleme için servis seçin — kural servis başına değerlendirilir.</div>
      ) : q.isLoading ? (
        <div style={box}>Loading…</div>
      ) : q.isError ? (
        <div style={box}>Önizleme okunamadı.</div>
      ) : data === null ? (
        <div style={box}>Bu metrik için önizleme dizisi yok.</div>
      ) : pts.filter(p => p.v != null).length < 2 ? (
        <div style={box}>No data for {svc} in the last hour</div>
      ) : (
        <MultiLineChart series={series} unit={unit} height={130}
          thresholds={[{ value: threshold, label: `${comparator} ${threshold}`, severity: sevTone, side: comparatorSide(comparator) }]} />
      )}
    </div>
  );
}
