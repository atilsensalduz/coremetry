import { useEffect, useMemo, useState } from 'react';
import { MultiLineChart, type DeployMarker } from './MultiLineChart';
import { Spinner } from './Spinner';
import { api } from '@/lib/api';
import { useServiceDeploys, useSLOs } from '@/lib/queries';
import { timeRangeToNs } from '@/lib/utils';
import type { SpanMetricSeries, TimeRange } from '@/lib/types';

// ServiceCharts — three core trend panels for the focused
// service: throughput (RPS by operation), error rate (%) by
// operation, and P99 latency by operation. Pulls SLOs for the
// service and paints horizontal threshold lines on the
// matching panel (latency SLO → P99 panel; availability SLO →
// error rate panel). Pulls deploys for the service and paints
// dashed vertical markers on every chart so the operator can
// read "did this regression coincide with a deploy" in one
// glance.
//
// All three charts share a syncKey so hovering one paints the
// crosshair on the other two — Datadog dashboard convention,
// turns the three panels into one synchronised view.

export function ServiceCharts({ service, range }: {
  service: string;
  range: TimeRange;
}) {
  // Memoise the time bounds so a render doesn't churn the
  // query keys (same trick the Logs page uses — Date.now() in
  // timeRangeToNs makes naive use unstable).
  const { from, to } = useMemo(() => timeRangeToNs(range), [range]);

  const [rpsSeries, setRpsSeries] = useState<SpanMetricSeries[] | null>(null);
  const [errSeries, setErrSeries] = useState<SpanMetricSeries[] | null>(null);
  const [p99Series, setP99Series] = useState<SpanMetricSeries[] | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const dsl = `service.name = "${service.replace(/"/g, '\\"')}"`;
    Promise.all([
      api.spanMetric({ agg: 'rate',       dsl, from, to, groupBy: 'name' }),
      api.spanMetric({ agg: 'error_rate', dsl, from, to, groupBy: 'name' }),
      api.spanMetric({ agg: 'p99',        dsl, from, to, groupBy: 'name', field: 'duration_ms' }),
    ]).then(([rps, err, p99]) => {
      if (cancelled) return;
      setRpsSeries(rps ?? []);
      setErrSeries(err ?? []);
      setP99Series(p99 ?? []);
    }).catch(() => {
      if (cancelled) return;
      setRpsSeries([]); setErrSeries([]); setP99Series([]);
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => { cancelled = true; };
  }, [service, from, to]);

  // Deploy markers for this service in the visible window.
  const deploysQ = useServiceDeploys(service, from, to);
  const deployMarkers: DeployMarker[] | undefined = useMemo(() => {
    if (!deploysQ.data) return undefined;
    return deploysQ.data.map(d => ({
      timeUnixNs: d.timeUnixNs,
      label: d.version,
      description: `${d.spanCount.toLocaleString()} spans since first seen`,
    }));
  }, [deploysQ.data]);

  // SLO-derived thresholds for this service. Latency SLOs
  // surface on the P99 panel; availability SLOs surface on
  // the error-rate panel (as the error budget %).
  const slosQ = useSLOs();
  const { latencyThresholds, errorThresholds } = useMemo(() => {
    const lat: { value: number; label: string; severity: 'warn' | 'err' }[] = [];
    const err: { value: number; label: string; severity: 'warn' | 'err' }[] = [];
    for (const slo of slosQ.data ?? []) {
      if (slo.service !== service) continue;
      // Service-wide SLOs apply on every panel; operation-
      // scoped ones still get drawn here because the panel
      // groups by operation, so the line is meaningful when
      // the matching operation's series is on screen. The
      // label includes the operation name so the operator
      // sees which series the line belongs to.
      const opSuffix = slo.operation ? ` (${slo.operation})` : '';
      if (slo.sliType === 'latency') {
        lat.push({
          value: slo.thresholdMs,
          label: `SLO < ${slo.thresholdMs}ms${opSuffix}`,
          severity: 'err',
        });
      } else if (slo.sliType === 'availability') {
        const errBudgetPct = (1 - slo.target) * 100;
        err.push({
          value: errBudgetPct,
          label: `err ≤ ${errBudgetPct.toFixed(2)}%${opSuffix}`,
          severity: 'err',
        });
      }
    }
    return {
      latencyThresholds: lat.length > 0 ? lat : undefined,
      errorThresholds:   err.length > 0 ? err : undefined,
    };
  }, [slosQ.data, service]);

  const syncKey = `service:${service}`;

  if (loading) {
    return (
      <div style={{
        background: 'var(--bg1)', border: '1px solid var(--border)',
        borderRadius: 8, padding: 14, marginBottom: 14,
        minHeight: 200, display: 'grid', placeItems: 'center',
      }}>
        <Spinner />
      </div>
    );
  }

  return (
    <div style={{
      display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)',
      gap: 10, marginBottom: 14,
    }}>
      <ChartCard title="RPS by operation">
        <MultiLineChart series={rpsSeries ?? []} unit="rps"
                        height={140}
                        deploys={deployMarkers}
                        syncKey={syncKey} />
      </ChartCard>
      <ChartCard title="Error rate by operation">
        <MultiLineChart series={errSeries ?? []} unit="%"
                        height={140}
                        deploys={deployMarkers}
                        thresholds={errorThresholds}
                        syncKey={syncKey} />
      </ChartCard>
      <ChartCard title="P99 latency by operation">
        <MultiLineChart series={p99Series ?? []} unit="ms"
                        height={140}
                        deploys={deployMarkers}
                        thresholds={latencyThresholds}
                        syncKey={syncKey} />
      </ChartCard>
    </div>
  );
}

function ChartCard({ title, children }: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, padding: 10,
      minWidth: 0, // allow flex/grid children to shrink
    }}>
      <div style={{
        fontSize: 11, fontWeight: 600, color: 'var(--text2)',
        letterSpacing: '0.3px', textTransform: 'uppercase',
        marginBottom: 4,
      }}>
        {title}
      </div>
      {children}
    </div>
  );
}
