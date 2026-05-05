'use client';
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';

// CopilotExplain — drop-in "🤖 Explain" button that calls the
// Coremetry AI copilot endpoint for the given subject and renders the
// reply inline beneath the button. Self-hides when the copilot is
// not configured (no API key on the server).
//
// Two subject types share this component to avoid a button per call site:
//   - kind="trace"   → POST /api/copilot/explain-trace/{id}
//   - kind="problem" → POST /api/copilot/explain-problem/{id}
export function CopilotExplain({ kind, id, label }: {
  kind: 'trace' | 'problem';
  id: string;
  label?: string;
}) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.copilotConfig().then(c => setEnabled(c.enabled)).catch(() => setEnabled(false));
  }, []);

  if (enabled !== true) return null;

  const run = async () => {
    setBusy(true); setError(null); setText(null);
    try {
      const r = kind === 'trace'
        ? await api.copilotExplainTrace(id)
        : await api.copilotExplainProblem(id);
      setText(r.explanation);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Explain failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ display: 'inline-flex', flexDirection: 'column', gap: 8, alignItems: 'flex-start' }}>
      <button onClick={run} disabled={busy} className="sec"
        style={{ padding: '5px 12px', fontSize: 12, color: 'var(--accent2)' }}>
        {busy ? '🤖 Thinking…' : (label ?? '🤖 AI explain')}
      </button>
      {error && (
        <div style={{
          padding: 10, borderRadius: 6, fontSize: 12,
          background: 'rgba(255,82,82,.10)', color: 'var(--err)',
          border: '1px solid rgba(255,82,82,.25)', maxWidth: 720,
        }}>{error}</div>
      )}
      {text && (
        <div style={{
          padding: 12, borderRadius: 6, fontSize: 13, lineHeight: 1.5,
          background: 'rgba(56,139,253,.08)',
          border: '1px solid rgba(56,139,253,.25)',
          color: 'var(--text)', whiteSpace: 'pre-wrap', maxWidth: 720,
        }}>
          <div style={{ fontSize: 10, color: 'var(--accent2)', marginBottom: 6, fontWeight: 700, letterSpacing: '.5px' }}>
            🤖 COPILOT
          </div>
          {text}
        </div>
      )}
    </div>
  );
}
