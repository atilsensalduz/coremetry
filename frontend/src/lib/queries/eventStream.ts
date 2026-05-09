import { useEffect } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { keys } from './keys';

// useEventStream subscribes to /api/events (SSE) and invalidates
// the relevant React Query caches when events arrive. Mount once,
// at the app shell level — duplicate mounts would open duplicate
// connections, which is wasteful (one bus broadcasts to all
// browser tabs anyway, but each EventSource is a separate
// long-lived TCP/HTTP/2 stream).
//
// Auto-reconnect: native EventSource handles this. On a network
// blip we get onerror, then onopen again on restore; React
// Query's natural staleness check fills any gap when the
// reconnect lands.
//
// Disable when running tests / SSR (typeof EventSource ===
// 'undefined') so this hook is safe to import unconditionally.
export function useEventStream(enabled: boolean) {
  const qc = useQueryClient();

  useEffect(() => {
    if (!enabled || typeof EventSource === 'undefined') return;

    const es = new EventSource('/api/events', { withCredentials: true });

    // Listener function: route by `event:` line, not by JSON
    // parsing every payload. The Go side emits events with
    // explicit `event: <kind>` headers; EventSource fires the
    // matching listener.
    const onProblemOpen    = () => {
      qc.invalidateQueries({ queryKey: keys.problems.all });
      qc.invalidateQueries({ queryKey: keys.anomalies.metrics });
      qc.invalidateQueries({ queryKey: keys.incidents.all });
    };
    const onProblemResolve = () => {
      qc.invalidateQueries({ queryKey: keys.problems.all });
      qc.invalidateQueries({ queryKey: keys.anomalies.metrics });
      qc.invalidateQueries({ queryKey: keys.incidents.all });
    };
    const onAnomalyOpen    = () => {
      qc.invalidateQueries({ queryKey: keys.anomalies.all });
    };
    const onAnomalyClear   = () => {
      qc.invalidateQueries({ queryKey: keys.anomalies.all });
    };

    es.addEventListener('problem.open',    onProblemOpen);
    es.addEventListener('problem.resolve', onProblemResolve);
    es.addEventListener('anomaly.open',    onAnomalyOpen);
    es.addEventListener('anomaly.clear',   onAnomalyClear);

    es.addEventListener('error', () => {
      // EventSource will auto-reconnect; nothing to do here.
      // Logging on every reconnect attempt is noisy on a
      // backend restart, so we just stay silent.
    });

    return () => {
      es.removeEventListener('problem.open',    onProblemOpen);
      es.removeEventListener('problem.resolve', onProblemResolve);
      es.removeEventListener('anomaly.open',    onAnomalyOpen);
      es.removeEventListener('anomaly.clear',   onAnomalyClear);
      es.close();
    };
  }, [enabled, qc]);
}
