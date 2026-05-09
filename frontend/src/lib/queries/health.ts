import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';

// /api/health is the cheapest endpoint we have — surfaces queue
// depths and connectivity. Sidebar polls it every 5s for the
// "Backend offline" sentinel. Short staleTime so it actually
// re-fetches; lower priority than user-driven queries so the
// 401 redirect handler still wins.
export function useHealth() {
  return useQuery({
    queryKey: keys.health,
    queryFn: api.health,
    refetchInterval: 5_000,
    staleTime: 4_000,
    // Don't blow up the whole UI if /api/health momentarily 502s
    // — the sidebar already shows a "Backend offline" message in
    // the error case. Failure is informational, not blocking.
    retry: false,
    refetchOnWindowFocus: false,
  });
}
