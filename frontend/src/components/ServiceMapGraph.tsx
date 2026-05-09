import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { ServiceMap, ServiceMapNode, ServiceMapEdge } from '@/lib/types';

// ServiceMapGraph renders the {nodes, edges} response as an SVG
// force-directed layout. No external lib — a tiny Verlet-style
// physics simulation runs once on mount (until energy decays
// below an idle threshold) and then stops, so we don't burn CPU
// after the initial settle. Subsequent data refreshes re-seed
// positions from the current state, so a few new edges nudge
// the layout instead of jumping from scratch.
//
// Why not d3-force? Pulls in ~30kB and we only need the basic
// charge / link / center forces. The implementation here is
// ~80 lines and tuned for ≤300 nodes — at our sampling cap of
// 200 traces the node count stays well below that.
export function ServiceMapGraph({ data, height = 520 }: {
  data: ServiceMap;
  height?: number;
}) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(900);

  useEffect(() => {
    const el = wrapRef.current;
    if (!el) return;
    const ro = new ResizeObserver(entries => {
      for (const e of entries) setWidth(Math.max(400, e.contentRect.width));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const { positioned, edgeMaxTraces } = useLayout(data, width, height);
  const edgeMaxErr = useMemo(
    () => data.edges.reduce((m, e) => Math.max(m, e.errorCount), 0),
    [data.edges]
  );

  return (
    <div ref={wrapRef} style={{ width: '100%', position: 'relative' }}>
      <svg viewBox={`0 0 ${width} ${height}`}
           style={{ width: '100%', height, display: 'block', background: 'var(--bg1)', borderRadius: 8 }}>
        <defs>
          <marker id="svc-arrow" viewBox="0 0 10 10"
                  refX="10" refY="5" markerWidth="6" markerHeight="6"
                  orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--text3)" />
          </marker>
          <marker id="svc-arrow-err" viewBox="0 0 10 10"
                  refX="10" refY="5" markerWidth="6" markerHeight="6"
                  orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--err)" />
          </marker>
        </defs>

        {data.edges.map((e, i) => {
          const a = positioned[e.caller], b = positioned[e.callee];
          if (!a || !b) return null;
          // Strokes scale with relative trace count; minimum 0.6
          // so rare edges remain visible. Error rate flips colour
          // to var(--err) once any callee error appears on the
          // edge.
          const w = 0.6 + 2.6 * (e.traceCount / Math.max(1, edgeMaxTraces));
          const errorish = e.errorCount > 0;
          // Endpoint trim — pull both ends inward by the node
          // radius so the arrowhead lands on the circle, not the
          // centre.
          const ra = nodeRadius(positioned[e.caller].n);
          const rb = nodeRadius(positioned[e.callee].n);
          const dx = b.x - a.x, dy = b.y - a.y;
          const d  = Math.max(1, Math.hypot(dx, dy));
          const x1 = a.x + dx * ra / d, y1 = a.y + dy * ra / d;
          const x2 = b.x - dx * rb / d, y2 = b.y - dy * rb / d;
          return (
            <g key={i}>
              <line x1={x1} y1={y1} x2={x2} y2={y2}
                    stroke={errorish ? 'var(--err)' : 'var(--text3)'}
                    strokeWidth={w}
                    opacity={errorish ? 0.85 : 0.55}
                    markerEnd={errorish ? 'url(#svc-arrow-err)' : 'url(#svc-arrow)'}>
                <title>
                  {`${e.caller} → ${e.callee}\n` +
                   `${e.traceCount} traces · ${e.spanCount} spans` +
                   (errorish ? ` · ${e.errorCount} errors` : '')}
                </title>
              </line>
            </g>
          );
        })}

        {Object.values(positioned).map(p => (
          <NodeCircle key={p.n.service} x={p.x} y={p.y} n={p.n} />
        ))}
      </svg>

      {/* Legend strip below the graph */}
      <div style={{
        marginTop: 6, display: 'flex', gap: 16, alignItems: 'center',
        fontSize: 11, color: 'var(--text3)',
      }}>
        <span>{data.nodes.length} services · {data.edges.length} edges · sampled {data.sampledFrom} traces ({data.totalSpans} spans)</span>
        <span style={{ marginLeft: 'auto', display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ width: 10, height: 10, borderRadius: '50%', background: 'var(--ok)', display: 'inline-block' }} />
          healthy
          <span style={{ width: 10, height: 10, borderRadius: '50%', background: 'var(--warn)', display: 'inline-block', marginLeft: 8 }} />
          &gt;1% error
          <span style={{ width: 10, height: 10, borderRadius: '50%', background: 'var(--err)', display: 'inline-block', marginLeft: 8 }} />
          &gt;5% error
        </span>
      </div>
      {edgeMaxErr === 0 && data.edges.length > 0 && (
        <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 2 }}>
          No edge errors in the sampled window.
        </div>
      )}
    </div>
  );
}

function NodeCircle({ x, y, n }: { x: number; y: number; n: ServiceMapNode }) {
  const r = nodeRadius(n);
  const fill = n.errorRate > 0.05 ? 'var(--err)'
            : n.errorRate > 0.01 ? 'var(--warn)'
            : 'var(--ok)';
  return (
    <Link to={`/service?name=${encodeURIComponent(n.service)}`}>
      <g style={{ cursor: 'pointer' }}>
        <circle cx={x} cy={y} r={r} fill={fill} fillOpacity={0.18}
                stroke={fill} strokeWidth={1.4} />
        <text x={x} y={y - r - 6}
              textAnchor="middle" fontSize={11} fontWeight={600}
              fill="var(--text)" style={{ pointerEvents: 'none' }}>
          {n.service}
        </text>
        <text x={x} y={y + 4}
              textAnchor="middle" fontSize={10}
              fill="var(--text2)" style={{ pointerEvents: 'none' }}>
          {n.spanCount.toLocaleString()}
        </text>
        <title>
          {`${n.service}\n${n.spanCount.toLocaleString()} spans · ${(n.errorRate * 100).toFixed(2)}% error`}
        </title>
      </g>
    </Link>
  );
}

function nodeRadius(n: ServiceMapNode): number {
  // log scale so a 100k-span service doesn't drown out a 100-span one.
  const log = Math.log10(Math.max(10, n.spanCount));
  return Math.min(34, Math.max(14, 8 + 5 * log));
}

interface PositionedNode {
  n: ServiceMapNode;
  x: number; y: number;
  vx: number; vy: number;
}

// useLayout runs a tiny Verlet simulation: charge repulsion between
// every pair, attractive spring on each edge, gentle pull toward
// centre. Runs ~250 iterations on (data, width, height) change and
// then freezes — we don't animate the simulation in real time.
function useLayout(
  data: ServiceMap, width: number, height: number,
): { positioned: Record<string, PositionedNode>; edgeMaxTraces: number } {
  return useMemo(() => {
    const cx = width / 2, cy = height / 2;
    const nodes: Record<string, PositionedNode> = {};
    // Deterministic seed by service-name hash → repeated renders of
    // the same data reach the same final layout, no jitter on every
    // 30s refresh.
    data.nodes.forEach((n, i) => {
      const seed = (hash(n.service) >>> 0);
      const angle = (seed / 0xffffffff) * Math.PI * 2;
      const ring  = Math.min(1, 0.4 + 0.6 * (i / Math.max(1, data.nodes.length)));
      nodes[n.service] = {
        n,
        x: cx + Math.cos(angle) * ring * Math.min(width, height) * 0.32,
        y: cy + Math.sin(angle) * ring * Math.min(width, height) * 0.32,
        vx: 0, vy: 0,
      };
    });
    if (data.nodes.length === 0) return { positioned: nodes, edgeMaxTraces: 0 };

    const linkLen = 110;
    const k_repel = 5800;
    const k_link  = 0.045;
    const k_centre = 0.012;
    const damping = 0.86;
    const iters = 260;

    const arr = Object.values(nodes);
    const edgeMaxTraces = data.edges.reduce((m, e) => Math.max(m, e.traceCount), 0);

    for (let it = 0; it < iters; it++) {
      // Pairwise repulsion.
      for (let i = 0; i < arr.length; i++) {
        for (let j = i + 1; j < arr.length; j++) {
          const a = arr[i], b = arr[j];
          let dx = a.x - b.x, dy = a.y - b.y;
          let d2 = dx*dx + dy*dy;
          if (d2 < 1) { dx = (Math.random() - 0.5); dy = (Math.random() - 0.5); d2 = 1; }
          const f = k_repel / d2;
          const d = Math.sqrt(d2);
          a.vx += f * dx / d; a.vy += f * dy / d;
          b.vx -= f * dx / d; b.vy -= f * dy / d;
        }
      }
      // Spring along each edge.
      for (const e of data.edges) {
        const a = nodes[e.caller], b = nodes[e.callee];
        if (!a || !b) continue;
        const dx = b.x - a.x, dy = b.y - a.y;
        const d = Math.max(0.01, Math.hypot(dx, dy));
        const f = k_link * (d - linkLen);
        a.vx += f * dx / d; a.vy += f * dy / d;
        b.vx -= f * dx / d; b.vy -= f * dy / d;
      }
      // Gentle centre pull so disconnected components don't drift away.
      for (const n of arr) {
        n.vx += (cx - n.x) * k_centre;
        n.vy += (cy - n.y) * k_centre;
      }
      // Integrate.
      for (const n of arr) {
        n.vx *= damping; n.vy *= damping;
        n.x += n.vx;     n.y += n.vy;
        // Clamp inside the viewport with a margin for label space.
        n.x = Math.max(40, Math.min(width  - 40, n.x));
        n.y = Math.max(40, Math.min(height - 40, n.y));
      }
    }
    return { positioned: nodes, edgeMaxTraces };
  }, [data, width, height]);
}

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h;
}
