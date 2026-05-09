import { useQuery } from '@tanstack/react-query';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { keys } from './keys';
import type { SpanExemplar } from '@/lib/types';

// Span exemplar — the bridge from a metric chart point to a
// representative trace in that bucket. The hook is `enabled`-
// gated because we don't want to fire it eagerly on every
// chart mount; pages call `useExemplar(…, { enabled: open })`
// or use the imperative `prefetchExemplar` for click handlers.
//
// Cache lifetime: 60s. The bucket data shifts after a window
// rolls over, but a user clicking the same point twice within
// a minute should hit the cache. Anything older is cheap
// enough to refetch.
export function useExemplar(
  args: { service: string; op?: string; from: number; to: number; kind?: 'slow' | 'error' | 'any' },
  opts?: { enabled?: boolean },
) {
  return useQuery<SpanExemplar | null>({
    queryKey: keys.spans.exemplar(args.service, args.op ?? '', args.from, args.to, args.kind ?? 'slow'),
    queryFn: () => api.spanExemplar(args),
    enabled: !!args.service && !!args.from && !!args.to && (opts?.enabled ?? true),
    staleTime: 60_000,
  });
}

// Imperative prefetch — for chart click handlers that fire a
// one-shot fetch and navigate; we don't want a hook re-render
// cascade for that. Use the QueryClient cache so a subsequent
// useExemplar with the same key picks up the result.
export function useExemplarFetcher() {
  const qc = useQueryClient();
  return (args: { service: string; op?: string; from: number; to: number; kind?: 'slow' | 'error' | 'any' }) =>
    qc.fetchQuery<SpanExemplar | null>({
      queryKey: keys.spans.exemplar(args.service, args.op ?? '', args.from, args.to, args.kind ?? 'slow'),
      queryFn: () => api.spanExemplar(args),
      staleTime: 60_000,
    });
}
