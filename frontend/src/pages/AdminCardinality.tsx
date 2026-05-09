import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { useAuth } from '@/components/AuthProvider';
import { Card, Badge, Stack, Row } from '@/components/ui';
import { useCardinality, keys } from '@/lib/queries';
import { useQueryClient } from '@tanstack/react-query';
import { fmtBytes, fmtNum } from '@/lib/utils';

// /admin/cardinality — meta-observability dashboard answering
// "which service / metric / label is eating my ClickHouse?".
// Four panels: top services by 24h spans, top metrics by 24h
// points, top attribute keys by distinct cardinality (sampled
// from the last 100k spans), and top columns by compressed
// disk bytes.
//
// The attribute-key panel is the actual operational lever —
// when a label transitions from controlled (e.g. http.method
// with 5 values) to unbounded (e.g. user.id with 50k values),
// it's invisible until storage starts to bleed. Surfacing it
// here lets the admin drop the offending label before it costs
// an order of magnitude in storage.
export default function AdminCardinalityPage() {
  const { user } = useAuth();
  const qc = useQueryClient();
  // useQuery enabled-gated on admin role so a viewer never
  // triggers the report (the API also enforces it, but skipping
  // the request keeps the network tab clean for non-admins).
  const cardinalityQ = useCardinality();
  const data = cardinalityQ.isLoading
    ? undefined
    : cardinalityQ.isError
      ? null
      : cardinalityQ.data;

  if (!user) return null;
  if (user.role !== 'admin') {
    return (
      <>
        <Topbar title="Cardinality" />
        <div id="content">
          <Empty icon="◇" title="Admin access required">
            The cardinality report is only available to administrators —
            it surfaces every service / metric / label name in the cluster.
          </Empty>
        </div>
      </>
    );
  }

  return (
    <>
      <Topbar title="Cardinality" />
      <div id="content">
        <Row gap={3} style={{ marginBottom: 14, alignItems: 'center' }}>
          <span style={{ fontSize: 12, color: 'var(--text2)' }}>
            What is eating ClickHouse — top emitters across services, metrics, labels, and stored columns. 5-min server cache.
          </span>
          <span style={{ flex: 1 }} />
          <button className="sec"
                  onClick={() => qc.invalidateQueries({ queryKey: keys.admin.cardinality })}>
            Refresh
          </button>
        </Row>

        {data === undefined && <Spinner />}
        {data === null && (
          <Empty icon="!" title="Failed to load cardinality report">
            Check that ClickHouse is reachable and that you have admin access.
          </Empty>
        )}
        {data && (
          <Stack gap={4}>
            <Row gap={4} wrap>
              <Card style={{ flex: '1 1 380px', minWidth: 0 }}
                    header={<>Top services by 24h spans</>}>
                <TopRowList rows={data.services} unit="spans" />
              </Card>

              <Card style={{ flex: '1 1 380px', minWidth: 0 }}
                    header={<>Top metrics by 24h points</>}>
                <TopRowList rows={data.metrics} unit="points" />
              </Card>
            </Row>

            <Card header={<>
              Top attribute keys by distinct cardinality
              <span style={{ fontSize: 11, color: 'var(--text3)', fontWeight: 400, marginLeft: 8 }}>
                — sampled from the last 100k spans of the most recent hour. High counts here = unbounded labels (user IDs, raw URLs, request IDs); the worst storage offenders.
              </span>
            </>}>
              <AttrKeyTable rows={data.attrKeys} />
            </Card>

            <Card header={<>Top columns by compressed bytes</>}>
              <ColumnTable rows={data.columns} />
            </Card>
          </Stack>
        )}
      </div>
    </>
  );
}

function TopRowList({ rows, unit }: { rows: { name: string; rows: number }[]; unit: string }) {
  if (rows.length === 0) {
    return <div style={{ fontSize: 12, color: 'var(--text3)' }}>No data in the last 24h.</div>;
  }
  const max = Math.max(...rows.map(r => r.rows));
  return (
    <div className="stack gap-1">
      {rows.map((r, i) => (
        <Row key={i} gap={2} style={{ fontSize: 12 }}>
          <span style={{ width: 22, color: 'var(--text3)', fontFamily: 'monospace', textAlign: 'right' }}>
            {i + 1}.
          </span>
          <span style={{ flex: 1, fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                title={r.name}>
            {r.name}
          </span>
          {/* Inline horizontal bar — width proportional to top
              row, gives a Datadog-like top-N glance without a
              heavy chart dependency. */}
          <span style={{
            display: 'inline-block',
            width: 100,
            height: 4,
            background: 'var(--bg3)',
            borderRadius: 2,
            position: 'relative',
            overflow: 'hidden',
          }}>
            <span style={{
              position: 'absolute', left: 0, top: 0, bottom: 0,
              width: `${Math.max(2, (r.rows / max) * 100)}%`,
              background: 'var(--accent)',
            }} />
          </span>
          <span style={{ width: 80, textAlign: 'right', fontFamily: 'monospace', color: 'var(--text2)' }}>
            {fmtNum(r.rows)} {unit}
          </span>
        </Row>
      ))}
    </div>
  );
}

function AttrKeyTable({ rows }: { rows: { key: string; distinctValues: number; occurrences: number; source: string }[] }) {
  if (rows.length === 0) {
    return <div style={{ fontSize: 12, color: 'var(--text3)' }}>No attributes sampled.</div>;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead><tr>
          <th>Key</th>
          <th className="num">Distinct values</th>
          <th className="num">Sampled occurrences</th>
          <th>Source</th>
        </tr></thead>
        <tbody>
          {rows.map((r, i) => {
            // Heuristic: > 1000 distinct values in a 100k-span sample
            // is the unbounded-label red flag. Yellow at > 200.
            const tone = r.distinctValues > 1000 ? 'danger'
                       : r.distinctValues > 200 ? 'warning' : 'neutral';
            return (
              <tr key={i}>
                <td className="mono">{r.key}</td>
                <td className="num mono">
                  <Badge tone={tone}>{fmtNum(r.distinctValues)}</Badge>
                </td>
                <td className="num mono" style={{ color: 'var(--text2)' }}>
                  {fmtNum(r.occurrences)}
                </td>
                <td style={{ fontSize: 11, color: 'var(--text3)' }}>{r.source}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function ColumnTable({ rows }: { rows: { table: string; column: string; compressedBytes: number; uncompressedBytes: number; compressionRatio: number }[] }) {
  if (rows.length === 0) {
    return <div style={{ fontSize: 12, color: 'var(--text3)' }}>system.columns empty.</div>;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead><tr>
          <th>Table</th>
          <th>Column</th>
          <th className="num">On disk (compressed)</th>
          <th className="num">Uncompressed</th>
          <th className="num">Ratio</th>
        </tr></thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              <td className="mono" style={{ color: 'var(--text2)' }}>{r.table}</td>
              <td className="mono">{r.column}</td>
              <td className="num mono">{fmtBytes(r.compressedBytes)}</td>
              <td className="num mono" style={{ color: 'var(--text3)' }}>{fmtBytes(r.uncompressedBytes)}</td>
              <td className="num mono" style={{ color: 'var(--text2)' }}>
                {r.compressionRatio.toFixed(1)}×
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
