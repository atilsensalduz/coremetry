'use client';
import { useEffect, useState } from 'react';
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
// Panel starts collapsed by default. The header strip is always
// shown (title + metadata + open trace link) so the operator can
// scan the page; clicking the ▶ toggle expands the waterfall body
// and triggers the fetch lazily — until the operator opens it,
// /service?name=… makes zero structure-related round-trips.
export function ServiceStructure({ service, since = '10m' }: {
  service: string;
  since?: string;
}) {
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<{
    spans?: SpanRow[];
    traceId?: string;
    bestSpans?: number;
    sampledFrom: number;
    totalSpans: number;
  } | null | undefined>(undefined);
  const [selected, setSelected] = useState<string | null>(null);

  // Re-fetch when the toggle flips on, when the service or window
  // changes. While closed we keep the previous payload around so
  // re-opening with the same params doesn't refetch.
  useEffect(() => {
    if (!open || !service) return;
    setData(undefined);
    api.serviceStructure(service, since, 50)
      .then(setData)
      .catch(() => setData(null));
  }, [open, service, since]);

  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, marginBottom: 14,
    }}>
      {/* Header strip — clickable to toggle. Always visible so the
          operator sees the panel's purpose without expanding. */}
      <button type="button" onClick={() => setOpen(o => !o)}
        style={{
          display: 'flex', alignItems: 'center', gap: 12,
          width: '100%', padding: 14,
          background: 'transparent', border: 'none', cursor: 'pointer',
          textAlign: 'left', color: 'var(--text)',
          borderBottom: open ? '1px solid var(--border)' : 'none',
        }}>
        <span style={{
          width: 14, color: 'var(--text2)', fontSize: 11,
          fontFamily: 'ui-monospace, monospace',
        }}>{open ? '▼' : '▶'}</span>
        <span style={{ fontSize: 12, color: 'var(--text2)', fontWeight: 600 }}>
          Structure for <span style={{ color: 'var(--text)' }}>{service}</span>
        </span>
        {open && data && data.spans && (
          <span style={{ fontSize: 11, color: 'var(--text3)' }}>
            sampled from {data.sampledFrom} trace{data.sampledFrom === 1 ? '' : 's'}
            {' · '}{fmtNum(data.totalSpans)} spans inspected
          </span>
        )}
        <span style={{ flex: 1 }} />
        {open && data?.traceId && (
          <span
            role="link"
            tabIndex={0}
            onClick={e => { e.stopPropagation(); window.location.href = `/trace?id=${data.traceId}`; }}
            onKeyDown={e => { if (e.key === 'Enter') { e.stopPropagation(); window.location.href = `/trace?id=${data.traceId}`; } }}
            style={{
              fontSize: 11, color: 'var(--accent2)', textDecoration: 'none',
              background: 'var(--bg3)', border: '1px solid var(--border)',
              borderRadius: 4, padding: '3px 10px', cursor: 'pointer',
            }}>
            Open representative trace ↗
          </span>
        )}
        {!open && (
          <span style={{ fontSize: 11, color: 'var(--text3)', fontStyle: 'italic' }}>
            click to expand
          </span>
        )}
      </button>

      {open && (
        <div style={{ padding: 14, paddingTop: 10 }}>
          {data === undefined && (
            <div style={{ minHeight: 120, display: 'grid', placeItems: 'center' }}>
              <Spinner />
            </div>
          )}
          {(data === null || (data && (!data.spans || data.spans.length === 0))) && (
            <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic', padding: '12px 4px' }}>
              No traces involving <code>{service}</code> in this window.
            </div>
          )}
          {data?.spans && data.spans.length > 0 && (
            <TraceWaterfall
              spans={data.spans}
              selectedId={selected}
              onSelect={setSelected}
              // Drilldown convention — collapsed by default so the
              // operator sees the top-level shape first; expand each
              // row with ▶ to drill down.
              defaultCollapsed
              // Bucket sibling spans with the same (service, name)
              // into a single ×N row — keeps tight loops (N+1 DB
              // queries, parallel fetches) from drowning the
              // structural shape. Representative subtree comes from
              // the longest member.
              groupSimilar />
          )}
        </div>
      )}
    </div>
  );
}

