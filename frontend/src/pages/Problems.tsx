import { Suspense, useMemo, useState } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { Link } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { useProblems } from '@/lib/queries';
import { useTableNav } from '@/lib/useTableNav';
import { tsLong } from '@/lib/utils';
import { CopilotExplain } from '@/components/CopilotExplain';
import { IconBell, IconSparkles } from '@/components/icons';
import type { Problem, TimeRange } from '@/lib/types';

type SortKey = 'severity' | 'service' | 'metric' | 'value' | 'rule' | 'started' | 'status';
type SortDir = 'asc' | 'desc';

// Severity order for sorting (higher = worse)
const SEV_RANK: Record<string, number> = { critical: 3, warning: 2, info: 1 };

// Each column's natural starting direction the first time it's clicked.
const NATURAL_DIR: Record<SortKey, SortDir> = {
  severity: 'desc',  // critical first
  service:  'asc',
  metric:   'asc',
  value:    'desc',  // worst breach first
  rule:     'asc',
  started:  'desc',  // newest first
  status:   'asc',   // open before resolved alphabetically
};

function ProblemsPageInner() {
  const [searchParams] = useSearchParams();
  // Optional ?service= URL filter — driven by the "View N
  // problem(s) →" link on the service detail page so the
  // operator lands on the same scope they were looking at.
  const serviceFilter = searchParams.get('service') ?? '';
  const [range, setRange] = useState<TimeRange>({ preset: '15m' });
  const [statusFilter, setStatusFilter] = useState<'open' | 'all' | 'resolved'>('open');
  const [sortBy, setSortBy] = useState<SortKey>('started');
  const [sortDir, setSortDir] = useState<SortDir>('desc');

  // Single useQuery replaces the previous useState/useEffect/
  // setInterval triple. The hook handles cache, dedup, polling
  // (30s), focus refetch, and undefined→[] mapping. isError
  // surfaces the previous "set null on failure" state.
  const problemsQ = useProblems({
    status: statusFilter === 'all' ? undefined : statusFilter,
    service: serviceFilter || undefined,
    limit: 200,
  });
  const data: Problem[] | null | undefined = problemsQ.isLoading
    ? undefined
    : problemsQ.isError
      ? null
      : (problemsQ.data ?? []);

  const open = data?.filter(p => p.status === 'open').length ?? 0;
  const resolved = data?.filter(p => p.status === 'resolved').length ?? 0;

  const sorted = useMemo(() => {
    if (!data) return data;
    const cmp = (a: Problem, b: Problem): number => {
      switch (sortBy) {
        case 'severity': return (SEV_RANK[a.severity] ?? 0) - (SEV_RANK[b.severity] ?? 0);
        case 'service':  return a.service.localeCompare(b.service);
        case 'metric':   return a.metric.localeCompare(b.metric);
        case 'value':    return a.value - b.value;
        case 'rule':     return a.ruleName.localeCompare(b.ruleName);
        case 'started':  return a.startedAt - b.startedAt;
        case 'status':   return a.status.localeCompare(b.status);
      }
    };
    const arr = [...data].sort(cmp);
    return sortDir === 'desc' ? arr.reverse() : arr;
  }, [data, sortBy, sortDir]);

  const toggleSort = (col: SortKey) => {
    if (sortBy === col) setSortDir(sortDir === 'desc' ? 'asc' : 'desc');
    else { setSortBy(col); setSortDir(NATURAL_DIR[col]); }
  };

  // j/k row navigation. Enter / o opens the selected problem's
  // service detail page (the most common drill-down). Esc
  // clears the selection.
  const navigate = useNavigate();
  const nav = useTableNav<Problem>(sorted ?? [], {
    pageId: 'problems',
    onOpen: (p) => navigate(`/service?name=${encodeURIComponent(p.service)}`),
  });

  return (
    <>
      <Topbar title="Problems" range={range} onRangeChange={setRange} />
      <div id="content">
        <div className="controls" style={{ marginBottom: 14 }}>
          <div style={{ display: 'flex', border: '1px solid var(--border)', borderRadius: 6, overflow: 'hidden' }}>
            {(['open', 'resolved', 'all'] as const).map(s => (
              <button key={s} onClick={() => setStatusFilter(s)}
                className={statusFilter === s ? '' : 'sec'}
                style={{ borderRadius: 0, borderRight: '1px solid var(--border)' }}>
                {s.charAt(0).toUpperCase() + s.slice(1)}
              </button>
            ))}
          </div>
          <span style={{ color: 'var(--text2)', fontSize: 12 }}>
            {open} open · {resolved} resolved
          </span>
          <Link to="/alerts" className="sec" style={{
            marginLeft: 'auto', textDecoration: 'none', padding: '5px 12px',
            border: '1px solid var(--border)', borderRadius: 6, fontSize: 12, color: 'var(--text)',
            display: 'inline-flex', alignItems: 'center', gap: 6,
          }}><IconBell /> <span>Manage alert rules</span></Link>
        </div>

        {data === undefined && <Spinner />}
        {data && data.length === 0 && (
          <Empty icon="✓" title={statusFilter === 'open' ? 'No open problems — all clear!' : 'No problems'}>
            The evaluator runs once per minute. Built-in rules cover error rate and P99 latency.
          </Empty>
        )}
        {sorted && sorted.length > 0 && (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <SortTh col="severity" label="Severity" sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <SortTh col="service"  label="Service"  sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <SortTh col="metric"   label="Metric"   sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <SortTh col="value"    label="Value"    sort={sortBy} dir={sortDir} onSort={toggleSort} align="right" />
                  <SortTh col="rule"     label="Rule"     sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <SortTh col="started"  label="Started"  sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <SortTh col="status"   label="Status"   sort={sortBy} dir={sortDir} onSort={toggleSort} />
                  <th>AI</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((p, i) => {
                  const isAnomaly = p.ruleId?.startsWith('anomaly:');
                  const isSelected = nav.selected === i;
                  return (
                    <tr key={p.id}
                        data-row-idx={i}
                        className={isSelected ? 'row-selected' : undefined}
                        onMouseEnter={() => nav.setSelected(i)}>
                      <td><SeverityBadge s={p.severity} /></td>
                      <td>
                        <Link to={`/service?name=${encodeURIComponent(p.service)}`}
                          style={{ fontWeight: 600 }}>
                          {p.service}
                        </Link>
                      </td>
                      <td className="mono">{p.metric}</td>
                      <td className="mono" style={{ textAlign: 'right' }}>
                        <b style={{ color: 'var(--err)' }}>{p.value.toFixed(2)}</b>
                        <span style={{ color: 'var(--text3)' }}> / {p.threshold.toFixed(2)}</span>
                      </td>
                      <td style={{ fontSize: 12 }}>
                        {isAnomaly && (
                          <span className="badge b-info" style={{ marginRight: 6 }}>ANOMALY</span>
                        )}
                        {p.ruleName}
                        {/* Runbook chip — surfaced when the
                            firing rule or the service catalog
                            carries a runbook URL. Stops row-
                            click propagation so opening the
                            playbook doesn't also open the
                            problem detail. */}
                        {p.runbookUrl && (
                          <a href={p.runbookUrl} target="_blank" rel="noopener"
                            onClick={e => e.stopPropagation()}
                            title="Open team runbook"
                            style={{
                              marginLeft: 8, fontSize: 11,
                              padding: '2px 8px', borderRadius: 12,
                              background: 'rgba(56,139,253,0.10)',
                              border: '1px solid rgba(56,139,253,0.35)',
                              color: 'var(--accent2)', textDecoration: 'none',
                              whiteSpace: 'nowrap',
                            }}>
                            📘 Runbook ↗
                          </a>
                        )}
                        {isAnomaly && (
                          <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 2 }}>
                            {p.description}
                          </div>
                        )}
                      </td>
                      <td className="mono">{tsLong(p.startedAt)}</td>
                      <td>
                        {p.status === 'open'
                          ? <span className="badge b-err">OPEN</span>
                          : <span className="badge b-ok">RESOLVED</span>}
                      </td>
                      <td>
                        {/* Self-hides when COREMETRY_AI_API_KEY isn't set;
                            renders as a small button + inline reply when it is. */}
                        <CopilotExplain kind="problem" id={p.id} label={<IconSparkles />} />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}

export default function ProblemsPage() {
  return (
    <Suspense fallback={<Spinner />}>
      <ProblemsPageInner />
    </Suspense>
  );
}

function SeverityBadge({ s }: { s: string }) {
  const cls = s === 'critical' ? 'b-err' : s === 'warning' ? 'b-warn' : 'b-info';
  return <span className={`badge ${cls}`}>{s.toUpperCase()}</span>;
}

function SortTh({ col, label, sort, dir, onSort, align }: {
  col: SortKey; label: string;
  sort: SortKey; dir: SortDir;
  onSort: (c: SortKey) => void;
  align?: 'left' | 'right';
}) {
  const active = sort === col;
  return (
    <th className={`sortable${active ? ' sorted' : ''}`}
        onClick={() => onSort(col)}
        style={{ textAlign: align ?? 'left' }}>
      {label}
      <span className="sort-arrow">{active ? (dir === 'desc' ? '▼' : '▲') : '↕'}</span>
    </th>
  );
}
