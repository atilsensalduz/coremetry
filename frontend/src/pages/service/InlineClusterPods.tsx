// InlineClusterPods — Infrastructure sekmesinde cluster satırına tıklayınca
// altında o cluster'ın pod'ları (operatör isteği 2026-10-15: "çok cluster'lı
// projelerde Pods sayfası kötü görünüyor; Infrastructure'da tıklayınca
// inline görebilsinler"). Veri useServicePods'un Thanos satırları — ek
// istek yok. Satır = pod detay sayfası.
import { Link } from 'react-router-dom';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef } from '@/components/ui/DataTable';
import { entityHref } from '@/lib/entityHref';
import { fmtBytes, fmtNum } from '@/lib/utils';
import { fmtCores, podPhaseBadge } from '@/pages/clusters/thresholds';
import type { ClusterPodRow, TimeRange } from '@/lib/types';

const COLS: ColumnDef<ClusterPodRow>[] = [
  { id: 'pod',      label: 'Pod',      sortValue: r => r.pod, naturalDir: 'asc', flex: true, mono: true },
  { id: 'phase',    label: 'Durum',    sortValue: r => r.phase ?? '', naturalDir: 'asc', width: 100 },
  { id: 'restarts', label: 'Restarts', sortValue: r => r.restarts ?? -1, numeric: true, width: 80 },
  { id: 'cpu',      label: 'CPU',      sortValue: r => r.cpuCores, numeric: true, width: 80 },
  { id: 'mem',      label: 'Memory',   sortValue: r => r.memBytes, numeric: true, width: 90 },
];

export function InlineClusterPods({ pods, range, service }: { pods: ClusterPodRow[]; range: TimeRange; service: string }) {
  const dt = useDataTable<ClusterPodRow>({
    storageKey: 'svc-infra-inline-pods', columns: COLS, rows: pods,
    initialSort: { id: 'cpu', dir: 'desc' },
  });
  return (
    <div style={{ padding: '4px 0 8px 24px' }}>
      <table {...dt.tableProps}>
        <DataTableColgroup dt={dt} />
        <DataTableHead dt={dt} />
        <tbody>
          {dt.sortedRows.length === 0 ? <DataTableState dt={dt} kind="empty" message="Bu cluster için pod satırı yok (Thanos envanteri boş)" /> : dt.sortedRows.map(r => (
            <tr key={`${r.namespace}/${r.pod}`} style={pods.length > 100 ? { contentVisibility: 'auto' } : undefined}>
              <DataTableCell dt={dt} col="pod" row={r}>
                <Link to={entityHref({ type: 'pod', id: r.pod, name: r.pod, clusterId: r.cluster, namespace: r.namespace }, { range, service, clusterName: r.cluster })}
                  className="row-link" title={r.pod}>{r.pod}</Link>
              </DataTableCell>
              <DataTableCell dt={dt} col="phase" row={r}>
                {r.phase ? <span className={`badge ${podPhaseBadge(r.phase)}`}>{r.phase}</span> : '—'}
              </DataTableCell>
              <DataTableCell dt={dt} col="restarts" row={r} value={r.restarts == null ? '—' : fmtNum(r.restarts)} />
              <DataTableCell dt={dt} col="cpu" row={r} value={fmtCores(r.cpuCores)} />
              <DataTableCell dt={dt} col="mem" row={r} value={fmtBytes(r.memBytes)} />
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
