import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type {
  LogPatternAnomaly, TraceOpAnomaly, AnomalyEvent, AnomalySilence, Problem,
} from '@/lib/types';

// /anomalies is the heaviest fan-out page in the app — five
// independent feeds (log patterns, trace ops, metric anomalies,
// history, silences) all polled together. With React Query each
// gets its own background poll + own cache key + own retry
// state, so a transient failure in one feed doesn't blank the
// page; the others keep rendering their cached data.

export function useLogPatternAnomalies() {
  return useQuery<LogPatternAnomaly[]>({
    queryKey: keys.anomalies.logPatterns,
    queryFn: async () => (await api.logPatternAnomalies()) ?? [],
    refetchInterval: 60_000,
    staleTime: 50_000,
  });
}

export function useTraceOpAnomalies() {
  return useQuery<TraceOpAnomaly[]>({
    queryKey: keys.anomalies.traceOps,
    queryFn: async () => (await api.traceOpAnomalies()) ?? [],
    refetchInterval: 60_000,
    staleTime: 50_000,
  });
}

export function useMetricAnomalies() {
  return useQuery<Problem[]>({
    queryKey: keys.anomalies.metrics,
    queryFn: async () => (await api.metricAnomalies()) ?? [],
    refetchInterval: 60_000,
    staleTime: 50_000,
  });
}

export function useAnomalyEvents() {
  return useQuery<AnomalyEvent[]>({
    queryKey: keys.anomalies.events,
    queryFn: async () => (await api.anomalyEvents()) ?? [],
    refetchInterval: 60_000,
    staleTime: 50_000,
  });
}

export function useAnomalySilences() {
  return useQuery<AnomalySilence[]>({
    queryKey: keys.anomalies.silences,
    queryFn: async () => (await api.anomalySilences()) ?? [],
    refetchInterval: 60_000,
    staleTime: 50_000,
  });
}

// Mutations — the create / delete silence calls. Both
// invalidate the anomaly feed cache so the muted item drops
// out of the live sections on the next refresh, without us
// having to manage the optimistic state by hand.
export function useCreateAnomalySilence() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.createAnomalySilence,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.anomalies.all });
    },
  });
}

export function useDeleteAnomalySilence() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: api.deleteAnomalySilence,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.anomalies.all });
    },
  });
}
