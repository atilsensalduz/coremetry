import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Empty } from './Spinner';
import { fmtNum } from '@/lib/utils';

// Row is the shape both /databases and /messaging hand to this
// component. We type-erase the row-specific labelling (Instance
// vs Destination, System vs System) via the props above; the
// table logic is otherwise identical.
export interface DepRow {
  system: string;
  // EITHER instance (DB) OR destination (messaging). Both are
  // optional on the type so the caller can wire whichever it
  // has — the table renders whichever is non-empty.
  instance?: string;
  destination?: string;
  spanCount: number;
  errorCount: number;
  errorRate: number;
  avgDurationMs: number;
  p99DurationMs: number;
  callers: string[];
}

type SortKey = 'system' | 'name' | 'spanCount' | 'errorRate' | 'avg' | 'p99';
const NATURAL: Record<SortKey, 'asc' | 'desc'> = {
  system: 'asc', name: 'asc', spanCount: 'desc',
  errorRate: 'desc', avg: 'desc', p99: 'desc',
};

// DependenciesTable renders the system+instance+RED+callers grid
// shared by /databases and /messaging. Kind controls the column
// header label and the click-through DSL pre-filter so a row
// click lands on /explore scoped to that system+instance.
export function DependenciesTable({
  rows, kind,
}: {
  rows: DepRow[];
  // 'db' → uses instance + filters by db.system; 'queue' → uses
  // destination + filters by messaging.system.
  kind: 'db' | 'queue';
}) {
  const [sortBy, setSortBy] = useState<SortKey>('spanCount');
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc');
  const [systemFilter, setSystemFilter] = useState<string>('');
  const [search, setSearch] = useState('');

  const systems = useMemo(() => {
    const s = new Set<string>();
    for (const r of rows) s.add(r.system);
    return Array.from(s).sort();
  }, [rows]);

  const nameOf = (r: DepRow) => r.instance ?? r.destination ?? '';

  const filtered = useMemo(() => {
    const term = search.trim().toLowerCase();
    return rows.filter(r => {
      if (systemFilter && r.system !== systemFilter) return false;
      if (term) {
        return r.system.toLowerCase().includes(term)
            || nameOf(r).toLowerCase().includes(term)
            || r.callers.some(c => c.toLowerCase().includes(term));
      }
      return true;
    });
  }, [rows, systemFilter, search]);

  const sorted = useMemo(() => {
    const arr = [...filtered];
    const cmp = (a: DepRow, b: DepRow): number => {
      switch (sortBy) {
        case 'system':    return a.system.localeCompare(b.system);
        case 'name':      return nameOf(a).localeCompare(nameOf(b));
        case 'spanCount': return a.spanCount - b.spanCount;
        case 'errorRate': return a.errorRate - b.errorRate;
        case 'avg':       return a.avgDurationMs - b.avgDurationMs;
        case 'p99':       return a.p99DurationMs - b.p99DurationMs;
      }
    };
    arr.sort(cmp);
    return sortDir === 'desc' ? arr.reverse() : arr;
  }, [filtered, sortBy, sortDir]);

  const toggleSort = (col: SortKey) => {
    if (sortBy === col) setSortDir(d => d === 'desc' ? 'asc' : 'desc');
    else { setSortBy(col); setSortDir(NATURAL[col]); }
  };

  // Click-through DSL — pre-filters /explore by the chosen
  // system + instance. For DBs the key is db.system; for
  // messaging it's messaging.system + messaging.destination.name.
  const exploreHref = (r: DepRow) => {
    if (kind === 'db') {
      const dsl =
        `db.system = "${r.system}"\n` +
        (r.instance && r.instance !== 'unknown'
          ? `peer.service = "${r.instance}"` : '');
      return `/explore?dsl=${encodeURIComponent(dsl)}&mode=advanced&result=traces`;
    }
    const dsl =
      `messaging.system = "${r.system}"\n` +
      (r.destination && r.destination !== 'unknown'
        ? `messaging.destination.name = "${r.destination}"` : '');
    return `/explore?dsl=${encodeURIComponent(dsl)}&mode=advanced&result=traces`;
  };

  const nameLabel = kind === 'db' ? 'Instance' : 'Destination';

  if (rows.length === 0) {
    return (
      <Empty icon="◯" title={kind === 'db'
        ? 'No database calls in this window'
        : 'No messaging activity in this window'}>
        {kind === 'db'
          ? 'Coremetry derives this view from spans with a populated db.system attribute.'
          : 'Derived from spans with a populated messaging.system attribute.'}
      </Empty>
    );
  }

  return (
    <>
      <div className="controls" style={{ marginBottom: 12, flexWrap: 'wrap' }}>
        <span style={{ color: 'var(--text2)', fontSize: 12 }}>System:</span>
        <select value={systemFilter} onChange={e => setSystemFilter(e.target.value)}
                style={{ fontSize: 12 }}>
          <option value="">All ({rows.length})</option>
          {systems.map(s => <option key={s} value={s}>{s}</option>)}
        </select>
        <input value={search} onChange={e => setSearch(e.target.value)}
               placeholder={kind === 'db'
                 ? 'Search system / instance / caller…'
                 : 'Search system / destination / caller…'}
               style={{ width: 280 }} />
        <span style={{ color: 'var(--text3)', fontSize: 11, marginLeft: 'auto' }}>
          {sorted.length} of {rows.length}
        </span>
      </div>

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <SortHeader col="system"    label="System"      sort={sortBy} dir={sortDir} onSort={toggleSort} />
              <SortHeader col="name"      label={nameLabel}   sort={sortBy} dir={sortDir} onSort={toggleSort} />
              <SortHeader col="spanCount" label="Calls"       sort={sortBy} dir={sortDir} onSort={toggleSort} align="right" />
              <SortHeader col="errorRate" label="Err %"       sort={sortBy} dir={sortDir} onSort={toggleSort} align="right" />
              <SortHeader col="avg"       label="Avg"         sort={sortBy} dir={sortDir} onSort={toggleSort} align="right" />
              <SortHeader col="p99"       label="P99"         sort={sortBy} dir={sortDir} onSort={toggleSort} align="right" />
              <th>Top callers</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((r, i) => {
              const errCls = r.errorRate > 5 ? 'err' : r.errorRate > 0 ? 'warn' : 'ok';
              return (
                <tr key={`${r.system}|${nameOf(r)}|${i}`}>
                  <td>
                    <SystemBadge system={r.system} kind={kind} />
                  </td>
                  <td>
                    <Link to={exploreHref(r)}
                          style={{ fontFamily: 'monospace', fontSize: 12, fontWeight: 500 }}
                          title="Open in Explore (spans pre-filtered)">
                      {nameOf(r) || <span style={{ color: 'var(--text3)' }}>(unknown)</span>}
                    </Link>
                  </td>
                  <td className="mono" style={{ textAlign: 'right' }}>{fmtNum(r.spanCount)}</td>
                  <td className="mono" style={{ textAlign: 'right' }}>
                    <span className={`badge b-${errCls}`}>{r.errorRate.toFixed(2)}%</span>
                  </td>
                  <td className="mono" style={{ textAlign: 'right' }}>{r.avgDurationMs.toFixed(1)}ms</td>
                  <td className="mono" style={{ textAlign: 'right' }}>{r.p99DurationMs.toFixed(1)}ms</td>
                  <td style={{ fontSize: 11 }}>
                    {r.callers.length === 0
                      ? <span style={{ color: 'var(--text3)' }}>—</span>
                      : r.callers.slice(0, 3).map((c, idx) => (
                          <span key={c}>
                            <Link to={`/service?name=${encodeURIComponent(c)}`}
                                  style={{ fontFamily: 'monospace' }}>{c}</Link>
                            {idx < Math.min(2, r.callers.length - 1) && <span style={{ color: 'var(--text3)' }}>, </span>}
                          </span>
                        ))}
                    {r.callers.length > 3 && (
                      <span style={{ color: 'var(--text3)' }}> +{r.callers.length - 3}</span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}

function SortHeader({ col, label, sort, dir, onSort, align }: {
  col: SortKey; label: string;
  sort: SortKey; dir: 'asc' | 'desc';
  onSort: (c: SortKey) => void;
  align?: 'left' | 'right';
}) {
  const active = sort === col;
  return (
    <th className={`sortable${active ? ' sorted' : ''}`}
        style={{ textAlign: align ?? 'left' }}
        aria-sort={active ? (dir === 'desc' ? 'descending' : 'ascending') : 'none'}>
      <button type="button" onClick={() => onSort(col)}
        style={{
          all: 'unset', display: 'inline-flex', alignItems: 'baseline',
          gap: 4, width: '100%', cursor: 'pointer',
          justifyContent: align === 'right' ? 'flex-end' : 'flex-start',
        }}>
        {label}
        <span className="sort-arrow">{active ? (dir === 'desc' ? '▼' : '▲') : '↕'}</span>
      </button>
    </th>
  );
}

// SystemBadge renders the system name in its conventional colour
// — Postgres blue, Redis red, Kafka dark, etc. — so an operator
// scanning the list recognises the technology at a glance.
function SystemBadge({ system, kind }: { system: string; kind: 'db' | 'queue' }) {
  const s = system.toLowerCase();
  const tone: Record<string, { bg: string; fg: string }> = {
    postgresql: { bg: 'rgba(51,103,145,0.18)', fg: '#5b8fb9' },
    postgres:   { bg: 'rgba(51,103,145,0.18)', fg: '#5b8fb9' },
    mysql:      { bg: 'rgba(0,117,143,0.18)',  fg: '#21a0a0' },
    mariadb:    { bg: 'rgba(0,117,143,0.18)',  fg: '#21a0a0' },
    oracle:     { bg: 'rgba(216,72,57,0.18)',  fg: '#d84839' },
    redis:      { bg: 'rgba(220,38,38,0.18)',  fg: '#dc2626' },
    mongodb:    { bg: 'rgba(76,175,80,0.18)',  fg: '#5cb85c' },
    mongo:      { bg: 'rgba(76,175,80,0.18)',  fg: '#5cb85c' },
    cassandra:  { bg: 'rgba(34,87,180,0.18)',  fg: '#5b8fff' },
    elasticsearch: { bg: 'rgba(0,127,127,0.18)', fg: '#1a8c8c' },
    clickhouse: { bg: 'rgba(252,212,52,0.18)', fg: '#e0b400' },
    kafka:      { bg: 'rgba(30,30,30,0.25)',   fg: 'var(--text)' },
    rabbitmq:   { bg: 'rgba(255,102,0,0.18)',  fg: '#ff6600' },
    ibmmq:      { bg: 'rgba(15,98,254,0.18)',  fg: '#0f62fe' },
    nats:       { bg: 'rgba(39,174,96,0.18)',  fg: '#27ae60' },
    sqs:        { bg: 'rgba(255,153,0,0.18)',  fg: '#ff9900' },
    kinesis:    { bg: 'rgba(255,153,0,0.18)',  fg: '#ff9900' },
  };
  const t = tone[s] ?? { bg: 'var(--bg3)', fg: 'var(--text2)' };
  return (
    <span style={{
      display: 'inline-flex', alignItems: 'center', gap: 6,
      padding: '2px 8px', borderRadius: 4, fontSize: 11, fontWeight: 600,
      fontFamily: 'ui-monospace, SFMono-Regular, monospace',
      background: t.bg, color: t.fg,
      border: `1px solid ${t.fg}33`,
    }}>
      <span aria-hidden style={{ fontSize: 10 }}>{kind === 'db' ? '⛁' : '⌬'}</span>
      {system}
    </span>
  );
}
