'use client';
import { useEffect, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { ServiceMapGraph } from '@/components/ServiceMapGraph';
import { api } from '@/lib/api';
import type { ServiceMap, TimeRange } from '@/lib/types';

const PRESETS: { key: TimeRange['preset']; secs: number; label: string }[] = [
  { key: '5m',  secs: 300,    label: '5m'  },
  { key: '15m', secs: 900,    label: '15m' },
  { key: '1h',  secs: 3600,   label: '1h'  },
  { key: '6h',  secs: 21600,  label: '6h'  },
  { key: '24h', secs: 86400,  label: '24h' },
];

// Service map: global view of every service Coremetry has seen in
// the window, with directed edges showing who calls whom and how
// hot each path is. The CH side bounds work to a sample of recent
// traces (default 200), so this stays cheap even at 1B spans/day —
// the heaviest 200 traces by span count is enough to surface every
// edge that matters operationally; cold paths self-disclose by
// just not appearing.
export default function ServiceMapPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '15m' });
  const [samples, setSamples] = useState(200);
  const [data, setData] = useState<ServiceMap | null | undefined>(undefined);

  useEffect(() => {
    setData(undefined);
    const since = (PRESETS.find(p => p.key === range.preset)?.secs ?? 900) + 's';
    const fetchOnce = () => api.serviceMap(since, samples)
      .then(d => setData(d ?? { nodes: [], edges: [], sampledFrom: 0, totalSpans: 0 }))
      .catch(() => setData(null));
    fetchOnce();
    const t = setInterval(fetchOnce, 30_000);
    return () => clearInterval(t);
  }, [range, samples]);

  return (
    <>
      <Topbar title="Service map" range={range} onRangeChange={setRange} />
      <div id="content">
        <div className="controls" style={{ marginBottom: 14 }}>
          <span style={{ fontSize: 12, color: 'var(--text2)' }}>Samples</span>
          <select value={samples} onChange={e => setSamples(Number(e.target.value))}>
            <option value={50}>50 traces</option>
            <option value={100}>100 traces</option>
            <option value={200}>200 traces</option>
            <option value={500}>500 traces</option>
          </select>
          <span style={{ fontSize: 11, color: 'var(--text3)', marginLeft: 'auto' }}>
            Auto-refreshes every 30s · click a node to drill into the service detail page
          </span>
        </div>

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
        {data && data.nodes.length > 0 && (
          <ServiceMapGraph data={data} />
        )}
      </div>
    </>
  );
}
