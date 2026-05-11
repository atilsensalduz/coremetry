import { useEffect, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { Spinner } from '@/components/Spinner';
import { DependenciesTable } from '@/components/DependenciesTable';
import { api } from '@/lib/api';
import { timeRangeToNs } from '@/lib/utils';
import type { TimeRange, MessagingInstance } from '@/lib/types';

// /messaging — top-level queue / topic technologies overview.
// Kafka brokers, RabbitMQ vhosts, IBM MQ queues, NATS subjects,
// SQS / Kinesis streams — anything OTel's messaging.system
// semconv touches. Derived from spans with the messaging
// attribute set; destination resolved from
// messaging.destination.name → messaging.destination →
// peer.service in that priority order.
export default function MessagingPage() {
  const [range, setRange] = useState<TimeRange>({ preset: '1h' });
  const [data, setData] = useState<MessagingInstance[] | null | undefined>(undefined);

  useEffect(() => {
    setData(undefined);
    const { from, to } = timeRangeToNs(range);
    api.messaging(from, to)
      .then(r => setData(r ?? []))
      .catch(() => setData(null));
  }, [range]);

  return (
    <>
      <Topbar title="Messaging" range={range} onRangeChange={setRange} />
      <div id="content">
        <div style={{ marginBottom: 14, fontSize: 12, color: 'var(--text2)' }}>
          Every queue / topic the platform's services produced to or consumed from
          in the selected window. Derived from spans with a populated
          {' '}<code>messaging.system</code> attribute. Click a row to drill into
          matching traces.
        </div>
        {data === undefined && <Spinner />}
        {data === null && (
          <div style={{ color: 'var(--err)', fontSize: 12 }}>
            Failed to load messaging overview.
          </div>
        )}
        {data && (
          <DependenciesTable
            rows={data.map(d => ({
              system: d.system,
              destination: d.destination,
              spanCount: d.spanCount,
              errorCount: d.errorCount,
              errorRate: d.errorRate,
              avgDurationMs: d.avgDurationMs,
              p99DurationMs: d.p99DurationMs,
              callers: d.callers ?? [],
            }))}
            kind="queue" />
        )}
      </div>
    </>
  );
}
