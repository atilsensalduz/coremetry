import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import type { LogsParams } from '@/lib/api';
import type { LogsResponse } from '@/lib/types';

// /api/logs query — keyed on the full filter object so a
// pagination click or filter change caches separately.
// staleTime is 0 so a re-mount of the page (back-nav) re-runs
// the query immediately rather than showing 30s-stale rows.
// Live-tail mode is handled by the page directly because it
// needs custom from/to that move every poll tick — that's
// orthogonal to the "view a window" behaviour cached here.
export function useLogs(params: LogsParams) {
  return useQuery<LogsResponse>({
    queryKey: ['logs', 'list', params],
    queryFn: () => api.logs(params),
    staleTime: 0,
  });
}
