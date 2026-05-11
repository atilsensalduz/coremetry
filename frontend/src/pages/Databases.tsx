import { useEffect, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { Spinner } from '@/components/Spinner';
import { DependenciesTable } from '@/components/DependenciesTable';
import { api } from '@/lib/api';
import { timeRangeToNs } from '@/lib/utils';
import type { TimeRange, DBInstance } from '@/lib/types';

// /databases — Dynatrace-style top-level Database technologies
// overview. One row per (db_system, instance) the platform's
// services have called over the chosen window. Same SRE
// question the Services page answers for live applications:
// "which deps are heavy, which are slow, which are erroring".
export default function DatabasesPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '1h' });
  const [data, setData] = useState<DBInstance[] | null | undefined>(undefined);

  useEffect(() => {
    setData(undefined);
    const { from, to } = timeRangeToNs(range);
    api.databases(from, to)
      .then(r => setData(r ?? []))
      .catch(() => setData(null));
  }, [range]);

  return (
    <>
      <Topbar title="Databases" range={range} onRangeChange={setRange} />
      <div id="content">
        <div style={{ marginBottom: 14, fontSize: 12, color: 'var(--text2)' }}>
          Every database the platform's services called in the selected window.
          Derived from spans with a populated <code>db.system</code> attribute.
          {' '}Click a row to drill into matching traces.
        </div>
        {data === undefined && <Spinner />}
        {data === null && (
          <div style={{ color: 'var(--err)', fontSize: 12 }}>
            Failed to load databases overview.
          </div>
        )}
        {data && (
          <DependenciesTable
            rows={data.map(d => ({
              system: d.system,
              instance: d.instance,
              spanCount: d.spanCount,
              errorCount: d.errorCount,
              errorRate: d.errorRate,
              avgDurationMs: d.avgDurationMs,
              p99DurationMs: d.p99DurationMs,
              callers: d.callers ?? [],
            }))}
            kind="db"
            range={range} />
        )}
      </div>
    </>
  );
}
