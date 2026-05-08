'use client';
import { useEffect, useState } from 'react';
import { AggregatedStructure } from './AggregatedStructure';
import { Spinner } from './Spinner';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import type { AggSpanNode } from '@/lib/types';

// Grafana-Drilldown-style multi-trace path-aggregated structure.
// Each unique `(parent_path → service → operation)` triple appears
// once with `×N` for tight loops / fan-outs; bars are proportional
// to the average duration. The panel starts collapsed so /service
// makes zero structure-related round-trips until the operator opens
// it.
export function ServiceStructure({ service, since = '10m' }: {
  service: string;
  since?: string;
}) {
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<{
    roots?: AggSpanNode[];
    sampledFrom: number;
    totalSpans: number;
  } | null | undefined>(undefined);

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
        {open && data && (
          <span style={{ fontSize: 11, color: 'var(--text3)' }}>
            aggregated from {data.sampledFrom} trace{data.sampledFrom === 1 ? '' : 's'}
            {' · '}{fmtNum(data.totalSpans)} spans inspected
          </span>
        )}
        <span style={{ flex: 1 }} />
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
          {(data === null || (data && (!data.roots || data.roots.length === 0))) && (
            <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic', padding: '12px 4px' }}>
              No traces involving <code>{service}</code> in this window.
            </div>
          )}
          {data?.roots && data.roots.length > 0 && (
            <AggregatedStructure roots={data.roots} />
          )}
        </div>
      )}
    </div>
  );
}
