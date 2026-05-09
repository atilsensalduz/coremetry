import { useEffect, useRef, useState } from 'react';
import {
  Chart, BarController, BarElement, LinearScale, CategoryScale,
  Tooltip,
} from 'chart.js';
import { api } from '@/lib/api';
import type { TimeRange } from '@/lib/types';
import { timeRangeToNs } from '@/lib/utils';

Chart.register(BarController, BarElement, LinearScale, CategoryScale, Tooltip);

// TraceVolumeHistogram renders a stacked-bar strip showing total
// span volume bucketed across the active time range, with the error
// share painted in red on top of the OK share. The visual ratio of
// red to gray on any bar IS the error rate at that moment, so an
// operator scanning the strip immediately sees error spikes without
// reading any percentage.
//
// Two parallel /api/spans/metric calls (count + errors) populate the
// chart; differencing yields the OK count per bucket. We pick a step
// that gives ~40 buckets across the range, capped to the API's
// minimum step (1s) and a reasonable max (5m) so a 24h window doesn't
// produce thousands of bars.
//
// Filters argument matches the /traces page's current DSL/filters so
// the histogram tracks the same predicate as the table below.
export function TraceVolumeHistogram({ range, dsl, filters }: {
  range: TimeRange;
  dsl?: string;
  filters?: string;
}) {
  const ref = useRef<HTMLCanvasElement>(null);
  const chartRef = useRef<Chart | null>(null);
  const [stats, setStats] = useState<{ total: number; errors: number } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const { from, to } = timeRangeToNs(range);
    // Aim for ~40 buckets across the visible window. The /api/spans/metric
    // endpoint expects step in seconds.
    const windowSec = Math.max(60, Math.round((to - from) / 1e9));
    const targetBuckets = 40;
    let step = Math.round(windowSec / targetBuckets);
    if (step < 1)   step = 1;
    if (step > 300) step = 300;

    setError(null);
    Promise.all([
      api.spanMetric({ agg: 'count',  dsl, filters, from, to, step }),
      api.spanMetric({ agg: 'errors', dsl, filters, from, to, step }),
    ]).then(([total, errs]) => {
      if (cancelled) return;
      // The series response is grouped — each ungrouped query returns
      // a single SpanMetricSeries with all buckets in `points`.
      const totalPoints = total?.[0]?.points ?? [];
      const errorPoints = errs?.[0]?.points ?? [];
      // Align by bucket time so missing-error-rows don't shift.
      const errMap = new Map(errorPoints.map(p => [p.time, p.value]));
      const labels: string[] = [];
      const okData:  number[] = [];
      const errData: number[] = [];
      let totalAll = 0, errAll = 0;
      for (const p of totalPoints) {
        const e = errMap.get(p.time) ?? 0;
        const ok = Math.max(0, p.value - e);
        labels.push(formatBucket(p.time));
        okData.push(ok);
        errData.push(e);
        totalAll += p.value;
        errAll   += e;
      }
      setStats({ total: totalAll, errors: errAll });
      drawChart(labels, okData, errData);
    }).catch((e) => {
      if (!cancelled) setError(e instanceof Error ? e.message : String(e));
    });

    return () => { cancelled = true; };
  }, [range, dsl, filters]);

  function drawChart(labels: string[], okData: number[], errData: number[]) {
    if (!ref.current) return;
    chartRef.current?.destroy();
    chartRef.current = new Chart(ref.current, {
      type: 'bar',
      data: {
        labels,
        datasets: [
          {
            // Slate-tinted base — same band as the rest of the
            // muted UI palette. Slight outline-via-gap effect by
            // setting a small categoryPercentage gap below.
            label: 'ok',
            data: okData,
            backgroundColor: 'rgba(126,142,161,0.55)',
            hoverBackgroundColor: 'rgba(126,142,161,0.85)',
            borderWidth: 0,
            stack: 'all',
            // Round top corners only when this is the topmost
            // segment (i.e. there are no errors stacked above).
            // chart.js can't do per-bar conditional radius cheaply,
            // so we leave this 0 and rely on the error layer for
            // the rounded top.
            borderRadius: 0,
          },
          {
            // Clean red — slightly deeper than #ff5252 so it reads
            // as "alert" without screaming. Top corners rounded for
            // the Datadog/Honeycomb look; falls back to a flat top
            // when this bar has zero errors (segment is invisible).
            label: 'errors',
            data: errData,
            backgroundColor: '#dc4a4a',
            hoverBackgroundColor: '#ec5a5a',
            borderWidth: 0,
            stack: 'all',
            borderRadius: { topLeft: 2, topRight: 2, bottomLeft: 0, bottomRight: 0 },
            borderSkipped: false,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        // Wider bars — reduces the negative-space jitter that made
        // the previous version feel sparse on long time ranges.
        datasets: { bar: { categoryPercentage: 0.92, barPercentage: 0.95 } },
        layout: { padding: { top: 4, right: 4, bottom: 0, left: 0 } },
        scales: {
          x: {
            stacked: true,
            ticks: { maxTicksLimit: 8, color: '#7d8693', font: { size: 10 }, autoSkip: true },
            grid: { display: false },
            border: { display: false },
          },
          y: {
            stacked: true,
            beginAtZero: true,
            ticks: { maxTicksLimit: 3, color: '#7d8693', font: { size: 10 } },
            grid: { color: 'rgba(125,140,160,0.10)' },
            border: { display: false },
          },
        },
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: 'rgba(20,24,30,0.95)',
            borderColor: 'rgba(125,140,160,0.30)',
            borderWidth: 1,
            padding: 10,
            cornerRadius: 4,
            titleColor: '#e6edf3',
            bodyColor: '#c9d1d9',
            titleFont: { size: 11, weight: 600 },
            bodyFont: { size: 11 },
            displayColors: false,
            callbacks: {
              title: (items) => items[0]?.label ?? '',
              label: () => '',
              afterBody: (items) => {
                const i = items[0]?.dataIndex ?? 0;
                const ok = okData[i] ?? 0;
                const err = errData[i] ?? 0;
                const tot = ok + err;
                const rate = tot > 0 ? (err / tot * 100).toFixed(2) : '0.00';
                return [
                  `total · ${tot.toLocaleString()}`,
                  `errors · ${err.toLocaleString()}`,
                  `error rate · ${rate}%`,
                ];
              },
            },
          },
        },
      },
    });
  }

  // Cleanup on unmount.
  useEffect(() => () => { chartRef.current?.destroy(); }, []);

  const errRate = stats && stats.total > 0
    ? (stats.errors / stats.total * 100).toFixed(2)
    : null;

  return (
    <div style={{
      background: 'var(--bg2)',
      border: '1px solid var(--border)',
      borderRadius: 8,
      padding: 12,
      marginBottom: 10,
    }}>
      {/* Header row — Datadog/Uptrace/Honeycomb pattern: title on
          the left, headline KPIs (total + errors + rate) as quietly
          styled stat tiles on the right. The error-rate tile turns
          red when non-zero so a glance is enough. */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 8, padding: '0 2px' }}>
        <span style={{
          fontSize: 11, color: 'var(--text2)', fontWeight: 700,
          letterSpacing: '0.5px', textTransform: 'uppercase',
        }}>
          Span volume
        </span>
        <span style={{ flex: 1 }} />
        {stats && (
          <>
            <Stat label="total"  value={stats.total.toLocaleString()} />
            <Stat label="errors" value={stats.errors.toLocaleString()}
                  tone={stats.errors > 0 ? 'err' : 'mute'} />
            <Stat label="error rate" value={errRate ? `${errRate}%` : '—'}
                  tone={stats && stats.errors > 0 ? 'err' : 'mute'} emphasised />
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4, fontSize: 10, color: 'var(--text3)' }}>
              <span style={{ width: 8, height: 8, background: 'rgba(126,142,161,0.7)', borderRadius: 2 }} />
              ok
              <span style={{ width: 8, height: 8, background: '#dc4a4a', borderRadius: 2, marginLeft: 6 }} />
              error
            </span>
          </>
        )}
      </div>
      <div style={{ height: 100, position: 'relative' }}>
        {error && (
          <div style={{ color: 'var(--err)', fontSize: 11, padding: 8 }}>{error}</div>
        )}
        <canvas ref={ref} />
      </div>
    </div>
  );
}

// Stat — small label-over-value tile used in the histogram header.
// Matches the style typical APM dashboards use for inline KPIs:
// uppercase muted label, bold value below, optional emphasis colour
// when the metric is "alert-worthy".
function Stat({ label, value, tone, emphasised }: {
  label: string; value: string; tone?: 'err' | 'mute' | 'ok'; emphasised?: boolean;
}) {
  const valueColor =
    tone === 'err' ? 'var(--err)'
    : tone === 'ok'  ? 'var(--ok)'
    : 'var(--text)';
  return (
    <span style={{
      display: 'inline-flex',
      flexDirection: 'column',
      lineHeight: 1.2,
      gap: 1,
      paddingLeft: 12,
      borderLeft: '1px solid var(--border)',
    }}>
      <span style={{
        fontSize: 9, color: 'var(--text3)',
        fontWeight: 600, letterSpacing: '0.5px', textTransform: 'uppercase',
      }}>
        {label}
      </span>
      <span style={{
        fontSize: emphasised ? 14 : 13,
        fontWeight: 600, color: valueColor,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
        fontVariantNumeric: 'tabular-nums',
      }}>
        {value}
      </span>
    </span>
  );
}

// HH:MM formatter used for the X-axis bucket labels.
function formatBucket(ns: number): string {
  const d = new Date(ns / 1e6);
  return `${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`;
}
