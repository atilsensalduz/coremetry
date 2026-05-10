import { useEffect, useMemo, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { MultiLineChart } from '@/components/MultiLineChart';
import { Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';
import type { TimeRange, SpanMetricSeries } from '@/lib/types';
import { timeRangeToNs } from '@/lib/utils';

// Investigation Notebook — Datadog Notebooks / Honeycomb Boards /
// Lightstep Notebooks-style ad-hoc workspace. The operator stitches
// together text observations + live queries while debugging, then
// shares the URL or copy-pastes the markdown into a postmortem.
//
// v1 scope is intentionally narrow:
//   • Two cell types: markdown (text + rendered preview) and query
//     (DSL → SpanMetric chart).
//   • Persistence is localStorage only — survives refresh, scoped
//     per-browser, no backend round-trip. The backend variant lands
//     in a follow-up so a notebook can be shared by URL.
//   • Time range applies to every query cell on the page (one
//     Topbar control, no per-cell range — keeps the cognitive
//     load low while triaging).
//
// The point of v1 is to prove the workflow: operator opens a fresh
// notebook during an incident, paragraphs the hypothesis,
// drops three queries to test it, links the URL into the incident
// channel. Anything fancier (collaboration, re-run on schedule,
// import from a saved view) is a follow-up.

type CellKind = 'markdown' | 'query';

type Cell =
  | { id: string; kind: 'markdown'; text: string }
  | {
      id: string;
      kind: 'query';
      title: string;
      // DSL string fed to /api/spans/metric. Multi-line; ParseDSL
      // splits by newline AND " AND " so either form works.
      dsl: string;
      agg: SpanAgg;
      groupBy: string;     // optional, single attribute key
      field: string;       // duration_ms (default) for quantiles
    };

type SpanAgg = 'count' | 'rate' | 'error_rate' | 'avg' | 'p50' | 'p95' | 'p99';

const STORAGE_KEY = 'coremetry-notebook-v1';

// Empty notebook starts with one helpful markdown cell + one query
// cell so the page isn't a blank slate. Users delete what they don't
// want.
function defaultCells(): Cell[] {
  return [
    {
      id: rid(),
      kind: 'markdown',
      text:
        "# Investigation\n\n" +
        "Notes go here. Use markdown — `code`, **bold**, [links](https://…), bullets.\n\n" +
        "Add query cells below to test hypotheses. Time range applies to every query.",
    },
    {
      id: rid(),
      kind: 'query',
      title: 'Request rate per service',
      dsl: 'service.name exists',
      agg: 'rate',
      groupBy: 'service.name',
      field: 'duration_ms',
    },
  ];
}

export default function NotebookPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '1h' });
  const [cells, setCells] = useState<Cell[]>(() => loadCells() ?? defaultCells());

  // Persist on every change. localStorage write is sub-ms; doing it
  // synchronously inside the state setter would force callers to
  // pre-compute next state for the write — cleaner here as an effect.
  useEffect(() => {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(cells)); }
    catch { /* quota / private mode — silently drop, in-memory state still works */ }
  }, [cells]);

  const addCell = (kind: CellKind) => {
    setCells(c => [...c, kind === 'markdown'
      ? { id: rid(), kind: 'markdown', text: '' }
      : { id: rid(), kind: 'query', title: '', dsl: '', agg: 'rate', groupBy: '', field: 'duration_ms' }]);
  };
  const deleteCell = (id: string) => {
    if (!confirm('Delete this cell?')) return;
    setCells(c => c.filter(x => x.id !== id));
  };
  const moveCell = (id: string, dir: -1 | 1) => {
    setCells(c => {
      const i = c.findIndex(x => x.id === id);
      if (i < 0) return c;
      const j = i + dir;
      if (j < 0 || j >= c.length) return c;
      const next = [...c];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  };
  const updateCell = (id: string, patch: Partial<Cell>) => {
    setCells(c => c.map(x => x.id === id ? ({ ...x, ...patch } as Cell) : x));
  };

  const reset = () => {
    if (!confirm('Reset notebook? Your cells will be lost (only this browser is affected — there is no backend save in v1).')) return;
    setCells(defaultCells());
  };

  return (
    <>
      <Topbar title="Notebook" range={range} onRangeChange={setRange} />
      <div id="content">
        <div style={{
          display: 'flex', alignItems: 'center', gap: 8, marginBottom: 14,
          paddingBottom: 8, borderBottom: '1px solid var(--border)',
        }}>
          <span style={{ fontSize: 12, color: 'var(--text2)' }}>
            {cells.length} cell{cells.length === 1 ? '' : 's'}
            {' · '}
            saved in your browser
          </span>
          <span style={{ flex: 1 }} />
          <button className="sec" onClick={reset}
            style={{ fontSize: 11, padding: '4px 10px', color: 'var(--err)' }}>
            Reset
          </button>
        </div>

        {cells.map((c, i) => (
          <CellShell
            key={c.id}
            index={i + 1}
            kind={c.kind}
            canMoveUp={i > 0}
            canMoveDown={i < cells.length - 1}
            onMoveUp={() => moveCell(c.id, -1)}
            onMoveDown={() => moveCell(c.id, 1)}
            onDelete={() => deleteCell(c.id)}>
            {c.kind === 'markdown'
              ? <MarkdownCell cell={c} onChange={p => updateCell(c.id, p)} />
              : <QueryCell cell={c} range={range} onChange={p => updateCell(c.id, p)} />}
          </CellShell>
        ))}

        <div style={{
          display: 'flex', gap: 8, marginTop: 18, padding: 14,
          borderRadius: 8, background: 'var(--bg2)', border: '1px dashed var(--border)',
        }}>
          <span style={{ fontSize: 12, color: 'var(--text2)', alignSelf: 'center' }}>
            Add cell:
          </span>
          <button onClick={() => addCell('markdown')} className="sec"
            style={{ fontSize: 12, padding: '4px 12px' }}>
            + Markdown
          </button>
          <button onClick={() => addCell('query')} className="sec"
            style={{ fontSize: 12, padding: '4px 12px' }}>
            + Query
          </button>
        </div>
      </div>
    </>
  );
}

// CellShell — wraps every cell with the gutter (index + reorder +
// delete) so the cell components themselves only worry about their
// content. Keeps the visual rhythm consistent across cell types.
function CellShell({ children, index, kind, canMoveUp, canMoveDown, onMoveUp, onMoveDown, onDelete }: {
  children: React.ReactNode;
  index: number;
  kind: CellKind;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onMoveUp: () => void;
  onMoveDown: () => void;
  onDelete: () => void;
}) {
  return (
    <div style={{
      marginBottom: 14, border: '1px solid var(--border)', borderRadius: 8,
      background: 'var(--bg1)', overflow: 'hidden',
    }}>
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8,
        padding: '6px 10px', borderBottom: '1px solid var(--border)',
        background: 'var(--bg2)',
      }}>
        <span style={{
          fontSize: 10, fontFamily: 'monospace', color: 'var(--text3)',
          minWidth: 24,
        }}>[{index}]</span>
        <span style={{
          fontSize: 10, padding: '1px 6px', borderRadius: 3, fontWeight: 600,
          background: kind === 'query' ? 'rgba(56,139,253,.15)' : 'var(--bg3)',
          color: kind === 'query' ? 'var(--accent2)' : 'var(--text2)',
          textTransform: 'uppercase', letterSpacing: '.5px',
        }}>{kind}</span>
        <span style={{ flex: 1 }} />
        <button onClick={onMoveUp} disabled={!canMoveUp} className="sec"
          title="Move up" style={btn}>↑</button>
        <button onClick={onMoveDown} disabled={!canMoveDown} className="sec"
          title="Move down" style={btn}>↓</button>
        <button onClick={onDelete} className="sec" title="Delete cell"
          style={{ ...btn, color: 'var(--err)' }}>×</button>
      </div>
      <div style={{ padding: 12 }}>{children}</div>
    </div>
  );
}
const btn: React.CSSProperties = {
  fontSize: 11, padding: '2px 8px', minWidth: 28,
};

// MarkdownCell — split editor / preview. Editor on the left, rendered
// preview on the right. We render markdown ourselves with a tiny
// in-file walker (no extra dependency) — the v1 supported set is
// headings, bold/italic/code, links, bullet lists. Anything beyond
// passes through as plain text.
function MarkdownCell({ cell, onChange }: {
  cell: Extract<Cell, { kind: 'markdown' }>;
  onChange: (p: Partial<Cell>) => void;
}) {
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
      <textarea value={cell.text}
        onChange={e => onChange({ text: e.target.value })}
        placeholder="# Hypothesis&#10;…"
        spellCheck={false}
        style={{
          minHeight: 120, fontFamily: 'ui-monospace, SFMono-Regular, monospace',
          fontSize: 13, padding: 8, resize: 'vertical',
          background: 'var(--bg2)', border: '1px solid var(--border)',
          borderRadius: 4, color: 'var(--text)',
        }} />
      <div style={{
        minHeight: 120, padding: 8, fontSize: 13, lineHeight: 1.55,
        background: 'var(--bg2)', border: '1px solid var(--border)', borderRadius: 4,
        overflowWrap: 'break-word',
      }}>
        {cell.text.trim() === ''
          ? <span style={{ color: 'var(--text3)', fontStyle: 'italic' }}>preview</span>
          : <RenderedMarkdown text={cell.text} />}
      </div>
    </div>
  );
}

// QueryCell — compact form (DSL + agg + group-by + field) on top,
// inline chart below. Re-runs on every form change with a 400ms
// debounce so the operator can type without spamming the API.
function QueryCell({ cell, range, onChange }: {
  cell: Extract<Cell, { kind: 'query' }>;
  range: TimeRange;
  onChange: (p: Partial<Cell>) => void;
}) {
  const [data, setData] = useState<SpanMetricSeries[] | null | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);

  // Debounced fetch — recompute after the operator pauses typing.
  // Re-keys on the cell config + the page-level time range so a
  // range change refetches every query cell.
  const dslKey = cell.dsl; // local alias for stable reference in deps
  useEffect(() => {
    if (dslKey.trim() === '') { setData(null); setError(null); return; }
    const handle = setTimeout(() => {
      const r = timeRangeToNs(range);
      setData(undefined); setError(null);
      api.spanMetric({
        agg: cell.agg, dsl: dslKey, from: r.from, to: r.to,
        groupBy: cell.groupBy.trim() || undefined,
        field: needsField(cell.agg) ? (cell.field || 'duration_ms') : undefined,
      })
        .then(s => setData(s ?? []))
        .catch(err => {
          setData(null);
          setError(err instanceof Error ? err.message : 'Query failed');
        });
    }, 400);
    return () => clearTimeout(handle);
  }, [dslKey, cell.agg, cell.groupBy, cell.field, range]);

  const unit = useMemo(() => {
    if (cell.agg === 'rate') return ' /s';
    if (cell.agg === 'error_rate') return '%';
    if (needsField(cell.agg) && cell.field === 'duration_ms') return 'ms';
    return '';
  }, [cell.agg, cell.field]);

  return (
    <div>
      <input value={cell.title}
        onChange={e => onChange({ title: e.target.value })}
        placeholder="Cell title (optional)"
        style={{
          width: '100%', marginBottom: 8,
          fontSize: 13, fontWeight: 600, padding: '4px 8px',
          background: 'transparent', border: 'none',
          borderBottom: '1px solid var(--border)',
          color: 'var(--text)',
        }} />
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'minmax(260px, 1fr) 130px 200px 130px',
        gap: 8, marginBottom: 10,
      }}>
        <textarea value={cell.dsl}
          onChange={e => onChange({ dsl: e.target.value })}
          placeholder='service.name = "api"&#10;duration > 500ms'
          spellCheck={false}
          style={{
            fontFamily: 'ui-monospace, SFMono-Regular, monospace',
            fontSize: 12, padding: 6, minHeight: 60,
            background: 'var(--bg2)', border: '1px solid var(--border)',
            borderRadius: 4, color: 'var(--text)', resize: 'vertical',
          }} />
        <select value={cell.agg}
          onChange={e => onChange({ agg: e.target.value as SpanAgg })}
          style={{ fontSize: 12 }}>
          <option value="count">count</option>
          <option value="rate">rate</option>
          <option value="error_rate">error_rate</option>
          <option value="avg">avg</option>
          <option value="p50">p50</option>
          <option value="p95">p95</option>
          <option value="p99">p99</option>
        </select>
        <input value={cell.groupBy}
          onChange={e => onChange({ groupBy: e.target.value })}
          placeholder="group by (e.g. service.name)"
          style={{ fontSize: 12 }} />
        <input value={cell.field}
          onChange={e => onChange({ field: e.target.value })}
          placeholder={needsField(cell.agg) ? 'field (duration_ms)' : 'unused'}
          disabled={!needsField(cell.agg)}
          style={{ fontSize: 12 }} />
      </div>

      {data === undefined && <Spinner />}
      {data === null && error && (
        <div style={{
          padding: 10, fontSize: 12, color: 'var(--err)',
          background: 'rgba(220,38,38,.08)', border: '1px solid rgba(220,38,38,.3)',
          borderRadius: 4,
        }}>
          {error}
        </div>
      )}
      {data === null && !error && (
        <div style={{ fontSize: 12, color: 'var(--text3)', fontStyle: 'italic', padding: 6 }}>
          Type a DSL filter above to run.
        </div>
      )}
      {data && data.length > 0 && (
        <MultiLineChart series={data} unit={unit} height={220} />
      )}
      {data && data.length === 0 && (
        <div style={{ fontSize: 12, color: 'var(--text3)', padding: 6 }}>
          No matching spans in this window.
        </div>
      )}
    </div>
  );
}

// needsField gates the "field" input: count / rate / error_rate
// don't take an aggregation field, the rest do.
function needsField(agg: SpanAgg): boolean {
  return agg === 'avg' || agg === 'p50' || agg === 'p95' || agg === 'p99';
}

// Tiny markdown renderer — no third-party dep. Supports the subset
// useful for incident notes: # / ## / ### headings, **bold**,
// *italic*, `code`, [link](url), - bullets, ``` fenced blocks.
// Unknown markdown passes through as-is so the operator isn't
// surprised by silently-stripped content.
function RenderedMarkdown({ text }: { text: string }) {
  const blocks: React.ReactNode[] = [];
  const lines = text.split('\n');
  let i = 0;
  let bulletBuf: string[] = [];
  const flushBullets = () => {
    if (bulletBuf.length === 0) return;
    blocks.push(
      <ul key={blocks.length} style={{ paddingLeft: 20, margin: '6px 0' }}>
        {bulletBuf.map((b, k) => <li key={k}>{renderInline(b)}</li>)}
      </ul>
    );
    bulletBuf = [];
  };
  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith('```')) {
      // fenced code block
      flushBullets();
      i++;
      const code: string[] = [];
      while (i < lines.length && !lines[i].startsWith('```')) {
        code.push(lines[i]);
        i++;
      }
      blocks.push(
        <pre key={blocks.length} style={{
          padding: 8, background: 'var(--bg)', borderRadius: 4,
          fontSize: 12, overflowX: 'auto',
          fontFamily: 'ui-monospace, SFMono-Regular, monospace',
        }}>{code.join('\n')}</pre>
      );
      i++; continue;
    }
    if (line.startsWith('### ')) {
      flushBullets();
      blocks.push(<h3 key={blocks.length} style={{ margin: '8px 0 4px' }}>{renderInline(line.slice(4))}</h3>);
    } else if (line.startsWith('## ')) {
      flushBullets();
      blocks.push(<h2 key={blocks.length} style={{ margin: '10px 0 4px' }}>{renderInline(line.slice(3))}</h2>);
    } else if (line.startsWith('# ')) {
      flushBullets();
      blocks.push(<h1 key={blocks.length} style={{ margin: '12px 0 6px', fontSize: 18 }}>{renderInline(line.slice(2))}</h1>);
    } else if (line.match(/^[-*] /)) {
      bulletBuf.push(line.slice(2));
    } else if (line.trim() === '') {
      flushBullets();
      blocks.push(<div key={blocks.length} style={{ height: 6 }} />);
    } else {
      flushBullets();
      blocks.push(<p key={blocks.length} style={{ margin: '4px 0' }}>{renderInline(line)}</p>);
    }
    i++;
  }
  flushBullets();
  return <>{blocks}</>;
}

// Inline markdown — bold, italic, inline code, links. Walks the
// string once, emitting React fragments. The regex is anchored to
// each delimiter so unmatched ones (** without closing **) pass
// through unchanged rather than swallowing the rest of the line.
function renderInline(s: string): React.ReactNode[] {
  const out: React.ReactNode[] = [];
  let rest = s;
  let key = 0;
  // Order matters: link regex before bold/italic so [**bold**](url)
  // doesn't get consumed by the bold pass first.
  const patterns: { re: RegExp; render: (m: RegExpMatchArray) => React.ReactNode }[] = [
    { re: /^\[([^\]]+)\]\(([^)]+)\)/,
      render: m => <a key={key++} href={m[2]} target="_blank" rel="noopener"
                       style={{ color: 'var(--accent2)' }}>{m[1]}</a> },
    { re: /^\*\*([^*]+)\*\*/,
      render: m => <b key={key++}>{m[1]}</b> },
    { re: /^\*([^*]+)\*/,
      render: m => <i key={key++}>{m[1]}</i> },
    { re: /^`([^`]+)`/,
      render: m => <code key={key++} style={{
        background: 'var(--bg)', padding: '0 4px', borderRadius: 3,
        fontFamily: 'ui-monospace, SFMono-Regular, monospace', fontSize: 12,
      }}>{m[1]}</code> },
  ];
  while (rest.length > 0) {
    let matched = false;
    for (const p of patterns) {
      const m = rest.match(p.re);
      if (m) {
        out.push(p.render(m));
        rest = rest.slice(m[0].length);
        matched = true;
        break;
      }
    }
    if (!matched) {
      out.push(rest[0]);
      rest = rest.slice(1);
    }
  }
  return out;
}

function loadCells(): Cell[] | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return null;
    return parsed.filter(isCell);
  } catch {
    return null;
  }
}

function isCell(v: unknown): v is Cell {
  if (!v || typeof v !== 'object') return false;
  const o = v as Record<string, unknown>;
  if (typeof o.id !== 'string') return false;
  if (o.kind === 'markdown' && typeof o.text === 'string') return true;
  if (o.kind === 'query'
      && typeof o.dsl === 'string'
      && typeof o.agg === 'string'
      && typeof o.groupBy === 'string') return true;
  return false;
}

function rid(): string {
  return Math.random().toString(36).slice(2, 10);
}
