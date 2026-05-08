'use client';
import { useEffect, useMemo, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { ServiceGraphSVG } from '@/components/ServiceGraphSVG';
import { ServicePicker } from '@/components/ServicePicker';
import { GraphErrorBoundary } from '@/components/GraphErrorBoundary';
import { api } from '@/lib/api';
import { timeRangeToNs, timeRangeLabel } from '@/lib/utils';
import type { Service, ServiceEdge, TimeRange } from '@/lib/types';

// Service Graph is now strictly single-service-scoped: the page
// will not attempt to fetch / render the full topology. Past ~500
// services the force-directed layout stalls the browser and the
// view becomes unreadable anyway. Picking a service shows its
// direct neighbours — the services / databases / brokers it talks
// to — which is the actually useful operator view.
export default function GraphPage() {
  const router = useRouter();
  const searchParams = useSearchParams();

  const [range, setRange] = useState<TimeRange>({ preset: '10m' });
  const [filter, setFilter] = useState(() => searchParams.get('service') ?? '');
  const [edges, setEdges] = useState<ServiceEdge[] | null | undefined>(undefined);

  // Mirror the selection into the URL so back-button + bookmarks restore
  // the same view. Range is left to Topbar's own URL handling.
  useEffect(() => {
    const usp = new URLSearchParams(searchParams);
    if (filter) usp.set('service', filter);
    else usp.delete('service');
    const next = usp.toString();
    if (typeof window !== 'undefined' && next !== window.location.search.slice(1)) {
      router.replace(`/graph${next ? `?${next}` : ''}`, { scroll: false });
    }
  }, [filter, router, searchParams]);

  // Fetch only when a service is selected. Without a selection the
  // backend now returns 400, and rendering a thousand-node SVG would
  // be unusable anyway.
  useEffect(() => {
    if (!filter) {
      setEdges(undefined);
      return;
    }
    setEdges(undefined);
    api.graph(timeRangeToNs(range), filter)
      .then(e => setEdges(e ?? []))
      .catch(() => setEdges(null));
  }, [range, filter]);

  // Derive the service list directly from the returned edges — every
  // visible node has at least one edge, so there's no need to pull
  // a separate services payload.
  const services = useMemo<Service[]>(() => {
    if (!edges || edges.length === 0) {
      return filter ? [{ name: filter, spanCount: 0, errorCount: 0, errorRate: 0,
                          avgDurationMs: 0, p99DurationMs: 0, apdex: 1, apdexThresholdMs: 200 }] : [];
    }
    const seen = new Map<string, Service>();
    const blank: Omit<Service, 'name'> = {
      spanCount: 0, errorCount: 0, errorRate: 0,
      avgDurationMs: 0, p99DurationMs: 0, apdex: 1, apdexThresholdMs: 200,
    };
    edges.forEach(e => {
      if (!seen.has(e.source)) seen.set(e.source, { name: e.source, ...blank });
      if (!seen.has(e.target)) seen.set(e.target, { name: e.target, ...blank });
    });
    return Array.from(seen.values());
  }, [edges, filter]);

  return (
    <>
      <Topbar title="Service Graph" range={range} onRangeChange={setRange} />
      <div id="content">
        <div className="controls">
          <ServicePicker value={filter} onChange={setFilter}
            placeholder="Pick a service to inspect…" width={280} />
          {filter && (
            <button className="sec" onClick={() => setFilter('')}>Clear</button>
          )}
          <span style={{ color: 'var(--text2)', fontSize: 12, marginLeft: 'auto' }}>
            {filter
              ? <>
                  Direct neighbours of <b>{filter}</b> · {timeRangeLabel(range)}
                  {edges && (
                    <> · {edges.length} edge{edges.length === 1 ? '' : 's'} · {services.length} service{services.length === 1 ? '' : 's'}</>
                  )}
                </>
              : <>Pick a service from the dropdown to view its dependencies</>}
          </span>
        </div>

        {!filter && (
          <Empty icon="⬡" title="No service selected">
            The service graph now renders one service at a time —
            its direct callers and downstream dependencies (other
            services, databases, message brokers). Use the picker
            above to pick one.
          </Empty>
        )}
        {filter && edges === undefined && <Spinner />}
        {filter && edges === null && (
          <Empty icon="⚠" title={`Failed to load graph for "${filter}"`} />
        )}
        {filter && edges && edges.length === 0 && (
          <Empty icon="⬡" title={`No connections for "${filter}"`}>
            This service emits no recent cross-service spans in this window.
          </Empty>
        )}
        {filter && edges && edges.length > 0 && (
          <GraphErrorBoundary>
            <ServiceGraphSVG services={services} edges={edges}
              highlightService={filter}
              onNodeClick={n => {
                if (filter === n) router.push(`/service?name=${encodeURIComponent(n)}`);
                else setFilter(n);
              }} />
          </GraphErrorBoundary>
        )}
      </div>
    </>
  );
}
