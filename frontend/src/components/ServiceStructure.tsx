'use client';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import type { ServiceEdgeStats } from '@/lib/types';
import { fmtNum } from '@/lib/utils';

// Tempo-drilldown style three-tier flow:
//
//   [ caller1 ]  [ caller2 ]  [ caller3 ]   ← upstream
//        \           |           /
//         \          ↓          /
//          ┌──────────────────┐
//          │  THIS SERVICE    │
//          └──────────────────┘
//          /          ↓          \
//         /           ↓           \
//   [ callee1 ] [ callee2 ] [ callee3 ]    ← downstream
//
// Each peripheral box is clickable and navigates to that service's
// own structure view, so an operator can hop through dependencies.
export function ServiceStructure({ service, callers, callees }: {
  service: string;
  callers: ServiceEdgeStats[];
  callees: ServiceEdgeStats[];
}) {
  const router = useRouter();

  const goto = (svc: string) => router.push(`/service?name=${encodeURIComponent(svc)}`);

  const Tier = ({ items, side }: { items: ServiceEdgeStats[]; side: 'up' | 'down' }) => {
    if (items.length === 0) {
      return (
        <div style={{
          color: 'var(--text3)', fontSize: 11, fontStyle: 'italic',
          textAlign: 'center', padding: '8px 0',
        }}>
          {side === 'up' ? 'no upstream callers' : 'no downstream calls'}
        </div>
      );
    }
    // Limit to top 8 per tier to keep the layout legible. The caller can
    // always see the full list in the tables further down the page.
    const top = items.slice(0, 8);
    const more = items.length - top.length;
    return (
      <div style={{
        display: 'flex', flexWrap: 'wrap', gap: 8,
        justifyContent: 'center', padding: '4px 0',
      }}>
        {top.map(e => (
          <Node key={e.service} edge={e} onClick={() => goto(e.service)} />
        ))}
        {more > 0 && (
          <div style={{
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            padding: '0 12px', fontSize: 11, color: 'var(--text3)',
          }}>+{more} more</div>
        )}
      </div>
    );
  };

  return (
    <div style={{
      background: 'var(--bg1)', border: '1px solid var(--border)',
      borderRadius: 8, padding: 18, marginBottom: 14,
    }}>
      <div style={{ fontSize: 12, color: 'var(--text2)', marginBottom: 14, fontWeight: 600 }}>
        Service structure · {callers.length} upstream · {callees.length} downstream
      </div>

      <Tier items={callers} side="up" />

      {/* Down-arrows from upstream to centre */}
      <Centre>
        <Arrow direction="down" />
      </Centre>

      {/* Middle node — this service */}
      <div style={{ display: 'flex', justifyContent: 'center', padding: '4px 0' }}>
        <div style={{
          padding: '12px 22px', borderRadius: 8,
          background: 'rgba(66,92,199,0.18)',
          border: '1.5px solid rgba(66,92,199,0.55)',
          fontWeight: 700, color: 'var(--text)',
          minWidth: 200, textAlign: 'center',
        }}>
          {service}
        </div>
      </div>

      {/* Down-arrows from centre to downstream */}
      <Centre>
        <Arrow direction="down" />
      </Centre>

      <Tier items={callees} side="down" />
    </div>
  );
}

// Coloured edge box — picks a hue per error rate so the worst caller
// jumps out at first glance.
function Node({ edge, onClick }: {
  edge: ServiceEdgeStats;
  onClick: () => void;
}) {
  const errCls = edge.errorRate > 5 ? 'b-err' : edge.errorRate > 0 ? 'b-warn' : 'b-ok';
  const accent =
    edge.errorRate > 5 ? 'rgba(255,82,82,.45)'  :
    edge.errorRate > 0 ? 'rgba(212,153,34,.45)' :
                         'rgba(63,185,80,.35)';
  return (
    <button onClick={onClick}
      title={`${edge.service} · ${fmtNum(edge.calls)} calls · ${edge.errorRate.toFixed(1)}% err · p99 ${edge.p99Ms.toFixed(0)}ms`}
      style={{
        display: 'flex', flexDirection: 'column', gap: 3,
        padding: '8px 12px', minWidth: 140,
        background: 'var(--bg2)', border: `1px solid ${accent}`,
        borderRadius: 6, cursor: 'pointer', color: 'inherit',
        textAlign: 'left',
      }}>
      <span style={{ fontWeight: 600, fontSize: 12,
                     overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
        {edge.service}
      </span>
      <span style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 10 }}>
        <span className={`badge ${errCls}`} style={{ padding: '0 5px' }}>
          {edge.errorRate.toFixed(1)}%
        </span>
        <span style={{ color: 'var(--text3)', fontFamily: 'monospace' }}>
          {fmtNum(edge.calls)} · {edge.p99Ms.toFixed(0)}ms
        </span>
      </span>
    </button>
  );
}

function Centre({ children }: { children: React.ReactNode }) {
  return <div style={{ display: 'flex', justifyContent: 'center' }}>{children}</div>;
}

function Arrow({ direction }: { direction: 'down' }) {
  return (
    <svg width="14" height="20" viewBox="0 0 14 20" style={{ display: 'block' }}>
      <line x1="7" y1="0" x2="7" y2="14"
            stroke="var(--text3)" strokeWidth="1.4" />
      <polygon points="2,12 7,18 12,12" fill="var(--text3)" />
    </svg>
  );
}
