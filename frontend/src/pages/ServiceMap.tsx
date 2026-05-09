import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { ServiceMapGraph } from '@/components/ServiceMapGraph';
import { useServiceMap } from '@/lib/queries';
import { fmtNum } from '@/lib/utils';
import type { TimeRange, ServiceMap, ServiceMapNode } from '@/lib/types';

const PRESETS: { key: TimeRange['preset']; secs: number; label: string }[] = [
  { key: '5m',  secs: 300,    label: '5m'  },
  { key: '15m', secs: 900,    label: '15m' },
  { key: '1h',  secs: 3600,   label: '1h'  },
  { key: '6h',  secs: 21600,  label: '6h'  },
  { key: '24h', secs: 86400,  label: '24h' },
];

// Service map: global topology view + a focus mode that
// narrows to a single service's 1-hop neighbourhood. The
// picker is the primary interaction — pick a service →
// the graph re-lays out radially around it (caller on the
// left, callee on the right) so the operator can read
// who depends on this service and who it depends on at
// a glance, like Datadog / Honeycomb service maps.
//
// Performance posture: the underlying CH query already caps
// work to a fixed sample of recent traces, so the network
// payload stays tiny regardless of cluster size. The focus
// filter happens client-side over that small payload — no
// extra round trip — and the radial layout is closed-form
// (no physics), so swap-in is instant.
export default function ServiceMapPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '15m' });
  const [samples, setSamples] = useState(200);
  const [focus, setFocus] = useState<string>('');
  const [hoverNode, setHoverNode] = useState<string | null>(null);
  const since = (PRESETS.find(p => p.key === range.preset)?.secs ?? 900) + 's';

  const mapQ = useServiceMap(since, samples);
  const data = mapQ.isLoading
    ? undefined
    : mapQ.isError
      ? null
      : mapQ.data ?? { nodes: [], edges: [], sampledFrom: 0, totalSpans: 0 };

  // Filter to the 1-hop neighbourhood of the focused service
  // (focused + every direct caller + every direct callee).
  // Edges are kept iff both endpoints survived. Memoised so
  // hover-induced re-renders don't recompute.
  const filtered = useMemo<ServiceMap | undefined>(() => {
    if (!data) return undefined;
    if (!focus) return data;
    const keep = new Set<string>([focus]);
    for (const e of data.edges) {
      if (e.caller === focus) keep.add(e.callee);
      if (e.callee === focus) keep.add(e.caller);
    }
    return {
      nodes:    data.nodes.filter(n => keep.has(n.service)),
      edges:    data.edges.filter(e => keep.has(e.caller) && keep.has(e.callee)),
      sampledFrom: data.sampledFrom,
      totalSpans:  data.totalSpans,
    };
  }, [data, focus]);

  const focusNode: ServiceMapNode | undefined = focus && data
    ? data.nodes.find(n => n.service === focus)
    : undefined;
  const callers = data && focus
    ? data.edges.filter(e => e.callee === focus).length
    : 0;
  const callees = data && focus
    ? data.edges.filter(e => e.caller === focus).length
    : 0;

  return (
    <>
      <Topbar title="Service map" range={range} onRangeChange={setRange} />
      <div id="content">
        <div style={{
          display: 'flex', gap: 10, alignItems: 'center',
          marginBottom: 14, flexWrap: 'wrap',
        }}>
          {/* Picker — datalist autocomplete fed by the current
              map's services. Plain <input> over a custom
              dropdown so keyboard nav / clear / autocomplete
              all just work. */}
          <label style={{ fontSize: 12, color: 'var(--text2)' }}>Focus</label>
          <input list="svc-map-services"
                 placeholder="select a service…"
                 value={focus}
                 onChange={e => setFocus(e.target.value)}
                 style={{
                   minWidth: 240, fontSize: 13,
                   padding: '4px 8px',
                   border: '1px solid var(--border)',
                   borderRadius: 6,
                   background: 'var(--bg1)',
                   color: 'var(--text)',
                 }} />
          <datalist id="svc-map-services">
            {data?.nodes.map(n => <option key={n.service} value={n.service} />)}
          </datalist>
          {focus && (
            <button className="sec"
              onClick={() => setFocus('')}
              style={{ fontSize: 12, padding: '3px 10px' }}>
              ← Clear focus
            </button>
          )}
          {focus && focusNode && (
            <Link to={`/service?name=${encodeURIComponent(focus)}`}
                  className="sec"
                  style={{
                    fontSize: 12, padding: '3px 10px',
                    textDecoration: 'none',
                    color: 'var(--text)',
                    border: '1px solid var(--border)',
                    borderRadius: 6,
                  }}>
              View {focus} detail →
            </Link>
          )}

          <span style={{ flex: 1 }} />

          <span style={{ fontSize: 12, color: 'var(--text2)' }}>Samples</span>
          <select value={samples}
                  onChange={e => setSamples(Number(e.target.value))}
                  style={{ fontSize: 12 }}>
            <option value={50}>50 traces</option>
            <option value={100}>100 traces</option>
            <option value={200}>200 traces</option>
            <option value={500}>500 traces</option>
          </select>
        </div>

        {/* Focus header — when a service is selected, surface
            its KPIs above the graph so the operator doesn't
            have to navigate away to read them. */}
        {focus && focusNode && (
          <div style={{
            display: 'flex', gap: 14, alignItems: 'center',
            padding: '10px 14px', marginBottom: 12,
            background: 'var(--bg1)',
            border: '1px solid var(--border)',
            borderRadius: 8,
            flexWrap: 'wrap',
          }}>
            <span style={{ fontSize: 14, fontWeight: 600 }}>{focus}</span>
            <span className={`badge b-${focusNode.errorRate > 0.05 ? 'err' : focusNode.errorRate > 0.01 ? 'warn' : 'ok'}`}>
              {(focusNode.errorRate * 100).toFixed(2)}% error
            </span>
            <Chip label="Spans"   value={fmtNum(focusNode.spanCount)} />
            <Chip label="Callers" value={`${callers}`} />
            <Chip label="Callees" value={`${callees}`} />
            <span style={{ flex: 1 }} />
            <span style={{ fontSize: 11, color: 'var(--text3)' }}>
              showing {focus}'s 1-hop neighbourhood — {filtered?.nodes.length ?? 0} services
            </span>
          </div>
        )}

        {data === undefined && <Spinner />}
        {data === null && (
          <Empty icon="!" title="Failed to load service map">
            Check that ClickHouse is reachable and the spans table has recent data.
          </Empty>
        )}
        {data && data.nodes.length === 0 && (
          <Empty icon="◯" title="No services in this window">
            Try widening the time range or check whether OTLP ingest is flowing
            (System → ClickHouse stats).
          </Empty>
        )}
        {filtered && filtered.nodes.length > 0 && (
          <ServiceMapGraph
            data={filtered}
            focus={focus || null}
            hoverNode={hoverNode}
            onHoverNode={setHoverNode}
            onSelectNode={setFocus}
          />
        )}

        <div style={{ marginTop: 8, fontSize: 11, color: 'var(--text3)' }}>
          {focus
            ? 'Click any node to switch focus · auto-refresh 30 s'
            : 'Click a node to focus on its 1-hop neighbourhood · auto-refresh 30 s'}
        </div>
      </div>
    </>
  );
}

function Chip({ label, value }: { label: string; value: string }) {
  return (
    <span style={{
      fontSize: 11, color: 'var(--text2)',
      display: 'inline-flex', gap: 6, alignItems: 'baseline',
    }}>
      <span style={{ color: 'var(--text3)' }}>{label}</span>
      <span style={{ fontFamily: 'monospace', color: 'var(--text)' }}>{value}</span>
    </span>
  );
}
