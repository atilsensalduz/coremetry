'use client';
import { useEffect, useState } from 'react';
import Link from 'next/link';
import { TraceWaterfall } from './TraceWaterfall';
import { Spinner } from './Spinner';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import type { SpanRow } from '@/lib/types';

// Grafana-Drilldown-style aggregated structure view. Picks a
// representative trace (the one with the most spans involving this
// service) from the last N candidates and renders it as a waterfall
// — same component the trace detail page uses, so service / op /
// duration / category badges all behave identically.
//
// The header reads "Structure for X · sampled from N traces · M spans"
// to make the sampling explicit; the displayed structure IS one real
// trace, not a synthetic averaged tree, but for typical SRE use that's
// what the operator wants to inspect anyway.
export function ServiceStructure({ service, since = '10m' }: {
  service: string;
  since?: string;
}) {
  const [data, setData] = useState<{
    spans?: SpanRow[];
    traceId?: string;
    bestSpans?: number;
    sampledFrom: number;
    totalSpans: number;
  } | null | undefined>(undefined);
  const [selected, setSelected] = useState<string | null>(null);

  useEffect(() => {
    if (!service) return;
    setData(undefined);
    api.serviceStructure(service, since, 50)
      .then(setData)
      .catch(() => setData(null));
  }, [service, since]);

  if (data === undefined) {
    return (
      <div style={{
        background: 'var(--bg1)', border: '1px solid var(--border)',
        borderRadius: 8, padding: 18, marginBottom: 14,
        minHeight: 120, display: 'grid', placeItems: 'center',
      }}>
        <Spinner />
      </div>
    );
  }
  if (data === null || !data.spans || data.spans.length === 0) {
    return (
      <div style={{
        background: 'var(--bg1)', border: '1px solid var(--border)',
        borderRadius: 8, padding: 18, marginBottom: 14,
      }}>
        <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 6, fontWeight: 600 }}>
          Service structure
        </div>
        <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic' }}>
          No traces involving <code>{service}</code> in this window.
        </div>
      </div>
    );
  }

  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, padding: 14, marginBottom: 14,
    }}>
      {/* Header — Grafana-Drilldown style metadata strip. */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 12, marginBottom: 10,
        paddingBottom: 8, borderBottom: '1px solid var(--border)',
      }}>
        <div style={{ fontSize: 12, color: 'var(--text2)', fontWeight: 600 }}>
          Structure for <span style={{ color: 'var(--text)' }}>{service}</span>
        </div>
        <div style={{ fontSize: 11, color: 'var(--text3)' }}>
          sampled from {data.sampledFrom} trace{data.sampledFrom === 1 ? '' : 's'}
          {' · '}
          {fmtNum(data.totalSpans)} spans inspected
        </div>
        <span style={{ flex: 1 }} />
        {data.traceId && (
          <Link href={`/trace?id=${data.traceId}`} style={{
            fontSize: 11, color: 'var(--accent2)', textDecoration: 'none',
            background: 'var(--bg3)', border: '1px solid var(--border)',
            borderRadius: 4, padding: '3px 10px',
          }}>
            Open representative trace ↗
          </Link>
        )}
      </div>
      <TraceWaterfall
        spans={data.spans}
        selectedId={selected}
        onSelect={setSelected} />
    </div>
  );
}
