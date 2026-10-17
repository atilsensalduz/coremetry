// sreEval.ts — v0.10.1301 — AI SRE Faz 5: geçmiş problemlerle doğruluk ölçümü
// (/ai › SREEvalPanel). staleTime = sunucu TTL (15 s / 60 s). Yoklama YALNIZ
// bir koşu sürerken, 15 s (RQ gizli sekmede durur); ilerleme ayrıca SSE
// 'sre.investigation' ile (keys.sre.all dalı) gelir.
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';

export const SRE_EVAL_POLL_MS = 15_000;

export function sreEvalInterval(running: boolean): number | false {
  return running ? SRE_EVAL_POLL_MS : false;
}

export function useSREEvalRuns() {
  return useQuery({
    queryKey: keys.sre.evalRuns,
    queryFn: ({ signal }) => api.sreEvalRuns(signal),
    staleTime: SRE_EVAL_POLL_MS,
    refetchInterval: q => sreEvalInterval(!!q.state.data?.runs.some(r => r.state !== 'done')),
  });
}

export function useSREEvalRun(id: string) {
  return useQuery({
    queryKey: keys.sre.evalRun(id),
    queryFn: ({ signal }) => api.sreEvalRun(id, signal),
    enabled: !!id,
    staleTime: SRE_EVAL_POLL_MS,
    refetchInterval: q => sreEvalInterval(q.state.data?.state === 'running'),
  });
}

// 👎 inceleme adayları — admin ucu; viewer/editor için hiç istenmez (enabled).
export function useSREEvalReview(days: number, enabled: boolean) {
  return useQuery({
    queryKey: keys.sre.evalReview(days),
    queryFn: ({ signal }) => api.sreEvalReview(days, signal),
    enabled,
    staleTime: 60_000,
  });
}
