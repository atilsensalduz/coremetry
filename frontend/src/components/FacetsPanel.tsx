import { useEffect, useState } from 'react';
import { Spinner } from './Spinner';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';
import type { Facet, FilterExpr } from '@/lib/types';

// FacetsPanel — Datadog-style trace tag explorer. Shows top-N
// values for each well-known facet column (service.name,
// http.route, db.system, …) over the current window + filter.
// Each value is clickable; clicking adds the value as an equality
// filter chip in the caller. Renders inline above the chart on
// /explore so the operator scans heavy tags and pivots without
// retyping.
//
// Self-fetches on (from, to, dsl, filters) change with a 300ms
// debounce. Server caches 30s per param tuple; the panel itself
// is lightweight to re-mount so we don't keep stale state when
// the operator changes the window.
export function FacetsPanel({
  from, to, dsl, filters, onPickValue,
}: {
  from: number;
  to: number;
  dsl?: string;
  filters?: string;
  onPickValue: (filter: FilterExpr) => void;
}) {
  const [data, setData] = useState<Facet[] | null | undefined>(undefined);
  useEffect(() => {
    setData(undefined);
    const h = setTimeout(() => {
      api.spanFacets({ from, to, dsl, filters, topValues: 8 })
        .then(r => setData(r ?? []))
        .catch(() => setData(null));
    }, 300);
    return () => clearTimeout(h);
  }, [from, to, dsl, filters]);

  if (data === undefined) return <div style={{ padding: 14 }}><Spinner /></div>;
  if (data === null) return (
    <div style={{ padding: 12, fontSize: 12, color: 'var(--err)' }}>
      Facet query failed.
    </div>
  );
  if (data.length === 0) return (
    <div style={{ padding: 12, fontSize: 12, color: 'var(--text3)' }}>
      No facets in this window. Widen the time range or relax filters.
    </div>
  );
  return (
    <div style={{
      display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))',
      gap: 10, padding: 12,
      background: 'var(--bg1)', border: '1px solid var(--border)', borderRadius: 6,
    }}>
      {data.map(f => <FacetColumn key={f.key} facet={f} onPickValue={onPickValue} />)}
    </div>
  );
}

function FacetColumn({ facet, onPickValue }: {
  facet: Facet;
  onPickValue: (filter: FilterExpr) => void;
}) {
  // Bar width relative to the top value so the proportions read
  // at a glance without per-row %% math.
  const max = facet.values[0]?.count ?? 1;
  return (
    <div style={{
      background: 'var(--bg2)', border: '1px solid var(--border)',
      borderRadius: 4, padding: 8, minWidth: 0,
    }}>
      <div style={{
        display: 'flex', alignItems: 'baseline', gap: 6, marginBottom: 6,
      }}>
        <span style={{ fontSize: 11, fontWeight: 700,
                       fontFamily: 'ui-monospace, SFMono-Regular, monospace' }}>
          {facet.key}
        </span>
        <span style={{ fontSize: 10, color: 'var(--text3)' }}>
          {facet.distinctValues > facet.values.length
            ? `${facet.values.length} of ${facet.distinctValues}`
            : `${facet.distinctValues}`}
        </span>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
        {facet.values.map(v => {
          const w = (v.count / max) * 100;
          return (
            <button key={v.value}
              type="button"
              onClick={() => onPickValue({ k: facet.key, op: '=', v: [v.value] })}
              title={`Click to add ${facet.key} = "${v.value}" as a filter`}
              style={{
                position: 'relative', textAlign: 'left',
                padding: '3px 6px', fontSize: 11,
                background: 'transparent', border: '1px solid transparent',
                borderRadius: 3, color: 'var(--text)', cursor: 'pointer',
                overflow: 'hidden',
              }}
              onMouseEnter={e => (e.currentTarget.style.borderColor = 'var(--border)')}
              onMouseLeave={e => (e.currentTarget.style.borderColor = 'transparent')}>
              <span style={{
                position: 'absolute', inset: 0, width: `${w}%`,
                background: 'rgba(56,139,253,0.10)', borderRadius: 3,
                pointerEvents: 'none',
              }} />
              <span style={{
                position: 'relative', display: 'flex',
                justifyContent: 'space-between', gap: 8,
              }}>
                <span style={{
                  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  fontFamily: 'ui-monospace, SFMono-Regular, monospace',
                }}>{v.value || '(empty)'}</span>
                <span style={{ color: 'var(--text3)', fontVariantNumeric: 'tabular-nums' }}>
                  {fmtNum(v.count)}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
