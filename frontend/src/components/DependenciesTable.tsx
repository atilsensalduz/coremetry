import { Fragment, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Empty, Spinner } from './Spinner';
import { api } from '@/lib/api';
import { fmtNum, timeRangeToNs } from '@/lib/utils';
import type { TimeRange, DBDetail, MessagingDetail } from '@/lib/types';

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
  rows, kind, range,
}: {
  rows: DepRow[];
  // 'db' → uses instance + filters by db.system; 'queue' → uses
  // destination + filters by messaging.system.
  kind: 'db' | 'queue';
  // Time range — drives the detail drawer's per-(service, pod)
  // breakdown query. Same window the parent /databases or
  // /messaging page uses for the overview.
  range: TimeRange;
}) {
  const [sortBy, setSortBy] = useState<SortKey>('spanCount');
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc');
  const [systemFilter, setSystemFilter] = useState<string>('');
  const [search, setSearch] = useState('');
  // Which row's drawer is open. Stores `system|name` so the
  // drawer survives sort + filter changes (system+name are
  // stable identifiers).
  const [openKey, setOpenKey] = useState<string | null>(null);

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
              <th style={{ width: 24 }} aria-label="Expand"></th>
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
              const rowKey = `${r.system}|${nameOf(r)}`;
              const isOpen = openKey === rowKey;
              return (
                <Fragment key={`${rowKey}|${i}`}>
                  <tr onClick={() => setOpenKey(isOpen ? null : rowKey)}
                      style={{ cursor: 'pointer',
                               background: isOpen ? 'var(--bg2)' : undefined }}>
                    <td style={{ color: 'var(--text3)', width: 24, textAlign: 'center' }}>
                      {isOpen ? '▾' : '▸'}
                    </td>
                    <td>
                      <SystemBadge system={r.system} kind={kind} />
                    </td>
                    <td onClick={e => e.stopPropagation()}>
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
                    <td style={{ fontSize: 11 }} onClick={e => e.stopPropagation()}>
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
                  {isOpen && (
                    <tr>
                      <td colSpan={8} style={{
                        background: 'var(--bg1)', padding: '12px 16px',
                        borderTop: '1px solid var(--border)',
                      }}>
                        <DetailDrawer
                          system={r.system}
                          name={nameOf(r)}
                          kind={kind}
                          range={range} />
                      </td>
                    </tr>
                  )}
                </Fragment>
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

// DetailDrawer fetches and renders the per-(service, pod) caller
// breakdown + top operations for one (system, instance) tuple.
// Lazy — only fires when the row is expanded; bounded server-
// side at LIMIT 100 callers / LIMIT 20 ops so the response stays
// cheap even for a 50-pod fleet.
function DetailDrawer({ system, name, kind, range }: {
  system: string;
  name: string;
  kind: 'db' | 'queue';
  range: TimeRange;
}) {
  type D = DBDetail | MessagingDetail;
  const [data, setData] = useState<D | null | undefined>(undefined);

  useEffect(() => {
    setData(undefined);
    const { from, to } = timeRangeToNs(range);
    const p = kind === 'db'
      ? api.databaseDetail(system, name, from, to)
      : api.messagingDetail(system, name, from, to);
    p.then(r => setData(r ?? null))
     .catch(() => setData(null));
  }, [system, name, kind, range]);

  if (data === undefined) return <Spinner />;
  if (data === null) return (
    <div style={{ fontSize: 12, color: 'var(--err)' }}>
      Detail query failed.
    </div>
  );

  // Defensive null-coalesce — pre-v0.4.87 the backend returned
  // null for empty slices (Go nil → JSON null), which crashed
  // [...data.callers]. The store now emits [] but we keep the
  // guard in case the cache returns a stale payload across an
  // upgrade.
  const allCallers = data.callers ?? [];
  const allTopOps  = data.topOps ?? [];

  // Worst-impact callers first — operator's first triage
  // question is "which client is hitting this DB hardest?".
  // We sort by spanCount × avgMs (impact, Elastic-APM style)
  // since a 200ms call made 10k times beats a 5s call made
  // twice for cumulative load on the backend.
  const callers = [...allCallers].sort((a, b) =>
    (b.spanCount * b.avgDurationMs) - (a.spanCount * a.avgDurationMs));

  // For messaging detail we split Producers / Consumers visually
  // — the SRE's "who's publishing" and "who's consuming"
  // questions are different (publisher is usually the load
  // generator, consumer is where back-pressure shows up).
  const producers = kind === 'queue'
    ? callers.filter(c => c.role === 'producer')
    : [];
  const consumers = kind === 'queue'
    ? callers.filter(c => c.role === 'consumer')
    : [];
  const otherClients = kind === 'queue'
    ? callers.filter(c => c.role && c.role !== 'producer' && c.role !== 'consumer')
    : callers;

  return (
    <div>
      {/* Aggregate strip on top — same numbers as the row but
          repeated here so the drawer reads on its own when
          screenshotted into a postmortem. */}
      <div style={{
        display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))',
        gap: 10, marginBottom: 14,
      }}>
        <Stat label="Calls"     value={fmtNum(data.spanCount)} />
        <Stat label="Errors"    value={fmtNum(data.errorCount)} />
        <Stat label="Err rate"  value={`${data.errorRate.toFixed(2)}%`}
              tone={data.errorRate > 5 ? 'err' : data.errorRate > 0 ? 'warn' : 'ok'} />
        <Stat label="Avg"       value={`${data.avgDurationMs.toFixed(1)} ms`} />
        <Stat label="P99"       value={`${data.p99DurationMs.toFixed(1)} ms`} />
      </div>

      {/* Per-(service, pod) breakdown — the SRE's "which client
          is shouting at this DB / queue" answer. Sorted by impact
          (spanCount × avgMs) so the heaviest cumulative consumer
          surfaces first. For messaging we split Producers /
          Consumers since they answer different questions. */}
      {kind === 'queue' ? (
        <>
          <CallerSection
            title={`Publishers · ${producers.length} ${producers.length === 1 ? 'row' : 'rows'}`}
            rows={producers}
            emptyMessage="No producer spans for this destination in the window."
            tone="producer" />
          <CallerSection
            title={`Consumers · ${consumers.length} ${consumers.length === 1 ? 'row' : 'rows'}`}
            rows={consumers}
            emptyMessage="No consumer spans for this destination in the window."
            tone="consumer" />
          {otherClients.length > 0 && (
            <CallerSection
              title={`Other clients · ${otherClients.length}`}
              rows={otherClients}
              emptyMessage=""
              tone="other" />
          )}
        </>
      ) : (
        <CallerSection
          title={`By client (service + pod) · ${callers.length} ${callers.length === 1 ? 'row' : 'rows'}`}
          rows={callers}
          emptyMessage="No callers in this window."
          tone="db" />
      )}

      {/* Top operations — for DBs the first 80 chars of
          db_statement (collapses unparameterised SQL); for
          messaging the span name (publish / consume / process). */}
      {allTopOps.length > 0 && (
        <div>
          <div style={{ fontSize: 12, fontWeight: 700, marginBottom: 6,
                         color: 'var(--text2)' }}>
            {kind === 'db'
              ? `Top ${allTopOps.length} statements (first 80 chars)`
              : `Top ${allTopOps.length} operations`}
          </div>
          <div className="table-wrap" style={{ maxHeight: 240, overflowY: 'auto' }}>
            <table>
              <thead style={{ position: 'sticky', top: 0, background: 'var(--bg1)', zIndex: 1 }}>
                <tr>
                  <th>{kind === 'db' ? 'Statement' : 'Operation'}</th>
                  <th className="num">Count</th>
                  <th className="num">Avg</th>
                </tr>
              </thead>
              <tbody>
                {allTopOps.map((o, i) => (
                  <tr key={i}>
                    <td style={{
                      fontFamily: 'ui-monospace, SFMono-Regular, monospace',
                      fontSize: 11, wordBreak: 'break-word', maxWidth: 600,
                    }}>{o.statement || <span style={{ color: 'var(--text3)' }}>(empty)</span>}</td>
                    <td className="num mono">{fmtNum(o.count)}</td>
                    <td className="num mono">{o.avgDurationMs.toFixed(1)}ms</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

// CallerSection renders one labelled table of (service, pod) rows
// with their RED metrics. Tone colours the header so producers /
// consumers / DB clients read at a glance. Empty sections render
// a one-line placeholder so the operator sees that we did look
// for the data and there just isn't any in this window.
function CallerSection({ title, rows, emptyMessage, tone }: {
  title: string;
  rows: import('@/lib/types').DBCallerBreakdown[];
  emptyMessage: string;
  tone: 'producer' | 'consumer' | 'other' | 'db';
}) {
  const dotColor =
    tone === 'producer' ? 'var(--accent2)' :
    tone === 'consumer' ? 'var(--ok)' :
    tone === 'other'    ? 'var(--text3)' :
                          'var(--accent2)';
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 6,
        fontSize: 12, fontWeight: 700, marginBottom: 6, color: 'var(--text2)',
      }}>
        <span aria-hidden style={{
          width: 8, height: 8, borderRadius: 2, background: dotColor,
        }} />
        {title}
      </div>
      {rows.length === 0 ? (
        emptyMessage && <div style={{ fontSize: 12, color: 'var(--text3)' }}>{emptyMessage}</div>
      ) : (
        <div className="table-wrap" style={{ maxHeight: 320, overflowY: 'auto' }}>
          <table>
            <thead style={{ position: 'sticky', top: 0, background: 'var(--bg1)', zIndex: 1 }}>
              <tr>
                <th>Service</th>
                <th>Pod / host</th>
                {rows.some(r => r.role) && <th>Role</th>}
                <th className="num">Calls</th>
                <th className="num">Err %</th>
                <th className="num">Avg</th>
                <th className="num">P99</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((c, i) => {
                const errCls = c.errorRate > 5 ? 'err' : c.errorRate > 0 ? 'warn' : 'ok';
                return (
                  <tr key={`${c.service}|${c.pod}|${c.role ?? ''}|${i}`}>
                    <td>
                      <Link to={`/service?name=${encodeURIComponent(c.service)}`}
                            style={{ fontFamily: 'monospace', fontSize: 12 }}>
                        {c.service}
                      </Link>
                    </td>
                    <td style={{ fontFamily: 'monospace', fontSize: 11, color: 'var(--text2)' }}>
                      {c.pod}
                    </td>
                    {rows.some(r => r.role) && (
                      <td>
                        {c.role && <RoleBadge role={c.role} />}
                      </td>
                    )}
                    <td className="num mono">{fmtNum(c.spanCount)}</td>
                    <td className="num mono">
                      <span className={`badge b-${errCls}`} style={{ fontSize: 9 }}>
                        {c.errorRate.toFixed(2)}%
                      </span>
                    </td>
                    <td className="num mono">{c.avgDurationMs.toFixed(1)}ms</td>
                    <td className="num mono">{c.p99DurationMs.toFixed(1)}ms</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function RoleBadge({ role }: { role: string }) {
  const r = role.toLowerCase();
  const tone =
    r === 'producer' ? { bg: 'rgba(56,139,253,0.15)', fg: 'var(--accent2)' } :
    r === 'consumer' ? { bg: 'rgba(63,185,80,0.15)',  fg: 'var(--ok)' } :
    r === 'client'   ? { bg: 'var(--bg3)',            fg: 'var(--text2)' } :
                       { bg: 'var(--bg3)',            fg: 'var(--text3)' };
  return (
    <span style={{
      fontSize: 10, padding: '1px 6px', borderRadius: 3, fontWeight: 600,
      fontFamily: 'ui-monospace, SFMono-Regular, monospace',
      background: tone.bg, color: tone.fg,
      textTransform: 'uppercase', letterSpacing: '.5px',
    }}>{role}</span>
  );
}

function Stat({ label, value, tone }: {
  label: string; value: string; tone?: 'ok' | 'warn' | 'err';
}) {
  const color = tone === 'err' ? 'var(--err)'
              : tone === 'warn' ? 'var(--warn)'
              : tone === 'ok'  ? 'var(--ok)'
              : 'var(--text)';
  return (
    <div style={{
      padding: '8px 10px', borderRadius: 4,
      background: 'var(--bg2)', border: '1px solid var(--border)',
    }}>
      <div style={{
        fontSize: 9, color: 'var(--text3)',
        textTransform: 'uppercase', letterSpacing: 0.4, fontWeight: 600,
      }}>{label}</div>
      <div style={{ fontSize: 16, fontWeight: 700, color,
                     fontFamily: 'ui-monospace, SFMono-Regular, monospace' }}>
        {value}
      </div>
    </div>
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
