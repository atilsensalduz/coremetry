'use client';
import { useEffect, useState } from 'react';
import { Topbar } from '@/components/Topbar';
import { Empty, Spinner } from '@/components/Spinner';
import { api } from '@/lib/api';

// /profiling/flame — embeds the Pyroscope UI inline via the Coremetry
// reverse-proxy at /pyroscope/. Single origin (no CORS), single auth
// boundary (Coremetry's), single URL the operator already trusts.
//
// Falls back to a "configure Pyroscope" hint when the operator hasn't
// pointed COREMETRY_PYROSCOPE_URL at an upstream server.
export default function FlamegraphsPage() {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  useEffect(() => {
    api.profilingConfig()
      .then(c => setEnabled(c.pyroscopeEmbedded))
      .catch(() => setEnabled(false));
  }, []);

  return (
    <>
      <Topbar title="Flame Graphs" />
      <div id="content" style={{ display: 'flex', flexDirection: 'column' }}>
        {enabled === null && <Spinner />}
        {enabled === false && (
          <Empty icon="🔥" title="Pyroscope embed not configured">
            Set <code>COREMETRY_PYROSCOPE_URL</code> on the coremetry container
            (e.g. <code>http://pyroscope:4040</code>) to embed the Pyroscope UI here.
            Until then, run Pyroscope alongside and open it directly in a new tab.
          </Empty>
        )}
        {enabled === true && (
          <iframe
            src="/pyroscope/"
            title="Pyroscope"
            style={{
              width: '100%', minHeight: 'calc(100vh - 110px)',
              border: 0, borderRadius: 8, background: 'var(--bg2)',
            }}
          />
        )}
      </div>
    </>
  );
}
