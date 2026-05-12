import { useState } from 'react';
import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { Topbar } from '@/components/Topbar';
import { Spinner } from '@/components/Spinner';
import { DependenciesTable } from '@/components/DependenciesTable';
import { api } from '@/lib/api';
import { timeRangeToNs } from '@/lib/utils';
import type { TimeRange, DBInstance } from '@/lib/types';

// /databases — Dynatrace-style top-level Database technologies
// overview. One row per (db_system, instance) the platform's
// services have called over the chosen window.
//
// Fetch is via useQuery with keepPreviousData so:
//   • revisits land instantly on the cached snapshot
//   • range changes re-render with the old data still
//     visible until the new payload arrives (no full
//     spinner flash on every range tweak)
//   • the 30s server-side cache aligns with our 30s staleTime
//     so refetches almost always hit the warm backend slot
export default function DatabasesPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '1h' });
  const { from, to } = timeRangeToNs(range);
  const q = useQuery({
    queryKey: ['databases', from, to],
    queryFn: () => api.databases(from, to).then(r => r ?? []),
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  });

  return (
    <>
      <Topbar title="Databases" range={range} onRangeChange={setRange} />
      <div id="content">
        <div style={{ marginBottom: 14, fontSize: 12, color: 'var(--text2)' }}>
          Every database the platform's services called in the selected window.
          Derived from spans with a populated <code>db.system</code> attribute.
          {' '}Click a row to drill into matching traces.
        </div>
        {q.isPending && <Spinner />}
        {q.isError && (
          <div style={{ color: 'var(--err)', fontSize: 12 }}>
            Failed to load databases overview.
          </div>
        )}
        {q.data && (
          <DependenciesTable
            rows={(q.data as DBInstance[]).map(d => ({
              system: d.system,
              instance: d.instance,
              spanCount: d.spanCount,
              errorCount: d.errorCount,
              errorRate: d.errorRate,
              avgDurationMs: d.avgDurationMs,
              p99DurationMs: d.p99DurationMs,
              callers: d.callers ?? [],
              source: d.source,
            }))}
            kind="db"
            range={range} />
        )}
      </div>
    </>
  );
}
