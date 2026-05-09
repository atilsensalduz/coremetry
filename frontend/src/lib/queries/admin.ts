import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { CardinalityReport, SamplingSettings, SystemStats } from '@/lib/types';

// Admin-only queries. /admin/cardinality and /admin/system-stats
// are the heaviest read-side endpoints (system.parts + uniqExact
// scans), already cached 60s/5min server-side. The client-side
// cache here just dedups parallel mounts and gives us a free
// "stale while we refetch" UX on tab switches.

export function useSystemStats() {
  return useQuery<SystemStats>({
    queryKey: keys.admin.systemStats,
    queryFn: api.systemStats,
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

export function useCardinality() {
  return useQuery<CardinalityReport>({
    queryKey: keys.admin.cardinality,
    queryFn: api.cardinality,
    staleTime: 5 * 60_000,
  });
}

export function useSamplingSettings() {
  return useQuery<SamplingSettings>({
    queryKey: keys.admin.sampling,
    queryFn: api.getSampling,
    staleTime: 30_000,
  });
}

export function useUpdateSampling() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.putSampling,
    onSuccess: (next) => {
      qc.setQueryData(keys.admin.sampling, next);
    },
  });
}
