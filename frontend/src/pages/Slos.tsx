import { useEffect, useState, FormEvent } from 'react';
import { Topbar } from '@/components/Topbar';
import { Spinner, Empty } from '@/components/Spinner';
import { ServicePicker } from '@/components/ServicePicker';
import { useAuth } from '@/components/AuthProvider';
import { Modal, Field, SelectField, Button, Stack } from '@/components/ui';
import { useSLOs, useCreateSLO, useDeleteSLO } from '@/lib/queries';
import { api } from '@/lib/api';
import type { SLIType } from '@/lib/types';

export default function SLOsPage() {
  const { user } = useAuth();
  const [services, setServices] = useState<string[]>([]);
  const [showNew, setShowNew] = useState(false);
  const isAdmin = user?.role === 'admin' || user?.role === 'editor';

  // useSLOs polls every 60s + auto-invalidates on
  // create/delete via the hook's onSuccess.
  const slosQ = useSLOs();
  const items = slosQ.isLoading ? undefined : slosQ.isError ? null : slosQ.data ?? [];
  const deleteSLO = useDeleteSLO();

  // Service list for the picker — one-shot lookup, not
  // worth a hook abstraction.
  useEffect(() => {
    api.services({ from: 0, to: 0 })
      .then(s => setServices((s ?? []).map(x => x.name))).catch(() => {});
  }, []);

  const onDelete = async (id: string) => {
    if (!confirm('Delete this SLO?')) return;
    await deleteSLO.mutateAsync(id);
  };

  return (
    <>
      <Topbar title="SLOs" />
      <div id="content">
        <div className="controls">
          <span style={{ color: 'var(--text2)', fontSize: 12 }}>
            Service Level Objectives — track availability and latency targets with error-budget burn down.
          </span>
          {isAdmin && (
            <button onClick={() => setShowNew(true)} style={{ marginLeft: 'auto' }}>+ New SLO</button>
          )}
        </div>

        {items === undefined && <Spinner />}
        {items !== undefined && (!items || items.length === 0) && (
          <Empty icon="◉" title="No SLOs defined">
            {isAdmin ? 'Create one to start tracking error budgets.' : 'Ask an admin to define SLOs.'}
          </Empty>
        )}
        {items && items.length > 0 && (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Service</th>
                  <th>Target</th>
                  <th>SLI ({items[0].windowDays}d)</th>
                  <th>Budget left</th>
                  <th>Burn rate</th>
                  <th>Status</th>
                  {isAdmin && <th></th>}
                </tr>
              </thead>
              <tbody>
                {items.map(o => (
                  <tr key={o.id}>
                    <td>
                      <div style={{ fontWeight: 600 }}>{o.name}</div>
                      <div style={{ fontSize: 11, color: 'var(--text3)' }}>
                        {o.sliType === 'latency'
                          ? `latency ≤ ${o.thresholdMs}ms`
                          : 'availability'}
                        {o.operation && <> · op=<code>{o.operation}</code></>}
                      </div>
                    </td>
                    <td className="mono">{o.service}</td>
                    <td className="mono">{(o.target * 100).toFixed(2)}%</td>
                    <td className="mono">
                      {o.status ? (o.status.sli * 100).toFixed(3) + '%' : '—'}
                    </td>
                    <td className="mono">
                      {o.status ? <BudgetBar value={o.status.budgetRemaining} /> : '—'}
                    </td>
                    <td className="mono">
                      {o.status ? <BurnBadge rate={o.status.burnRate} /> : '—'}
                    </td>
                    <td>
                      {o.status?.healthy
                        ? <span className="badge b-ok">Healthy</span>
                        : <span className="badge b-err">Breached</span>}
                    </td>
                    {isAdmin && (
                      <td style={{ display: 'flex', gap: 6 }}>
                        <BurnExplainButton sloId={o.id} />
                        <button className="sec" onClick={() => onDelete(o.id)}>Delete</button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {showNew && isAdmin && (
          <NewSLOModal services={services}
            onClose={() => setShowNew(false)}
            onCreated={() => setShowNew(false)} />
        )}
      </div>
    </>
  );
}

function BudgetBar({ value }: { value: number }) {
  const pct = Math.max(0, Math.min(1, value)) * 100;
  const color = pct > 50 ? 'var(--ok)' : pct > 20 ? 'var(--warn)' : 'var(--err)';
  return (
    <div title={`${pct.toFixed(1)}% of error budget remaining`} style={{
      display: 'inline-block', width: 100, height: 10, position: 'relative',
      background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 3,
      verticalAlign: 'middle',
    }}>
      <div style={{
        position: 'absolute', left: 0, top: 0, bottom: 0, width: `${pct}%`,
        background: color, borderRadius: 2,
      }} />
    </div>
  );
}

function BurnBadge({ rate }: { rate: number }) {
  if (!isFinite(rate)) return <span style={{ color: 'var(--text3)' }}>—</span>;
  const cls = rate > 2 ? 'b-err' : rate > 1 ? 'b-warn' : 'b-ok';
  return <span className={`badge ${cls}`}>{rate.toFixed(2)}×</span>;
}

function NewSLOModal({ services, onClose, onCreated }: {
  services: string[]; onClose: () => void; onCreated: () => void;
}) {
  const [name, setName] = useState('');
  const [service, setService] = useState('');
  const [sliType, setSliType] = useState<SLIType>('availability');
  const [target, setTarget] = useState('99.0');
  const [windowDays, setWindowDays] = useState('30');
  const [thresholdMs, setThresholdMs] = useState('500');
  const [operation, setOperation] = useState('');
  const [error, setError] = useState<string | null>(null);

  // useCreateSLO handles busy state via isPending and
  // auto-invalidates the SLOs list on success — no manual
  // refresh() in the parent.
  const createSLO = useCreateSLO();
  const busy = createSLO.isPending;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      await createSLO.mutateAsync({
        name, service, sliType,
        target: parseFloat(target) / 100,
        windowDays: parseInt(windowDays || '30'),
        thresholdMs: sliType === 'latency' ? parseFloat(thresholdMs) : 0,
        operation,
      });
      onCreated();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      try { setError(JSON.parse(msg.replace(/^HTTP \d+:\s*/, ''))?.error ?? msg); }
      catch { setError(msg); }
    }
  };

  return (
    <Modal
      open={true}
      onClose={onClose}
      title="New SLO"
      size="md"
      initialFocus="input[name=name]"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button type="submit" form="new-slo-form" loading={busy}>Create</Button>
        </>
      }>
      <form id="new-slo-form" onSubmit={submit}>
        <Stack gap={3}>
          <Field
            label="Name"
            name="name"
            required
            value={name}
            onChange={e => setName(e.target.value)} />
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <div className="field">
              <label className="field-label">Service</label>
              <ServicePicker value={service} onChange={setService} placeholder="…" />
            </div>
            <Field
              label="Operation (optional)"
              value={operation}
              onChange={e => setOperation(e.target.value)} />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <SelectField
              label="SLI type"
              value={sliType}
              onChange={e => setSliType(e.target.value as SLIType)}>
              <option value="availability">Availability</option>
              <option value="latency">Latency</option>
            </SelectField>
            <Field
              label="Window (days)"
              type="number" min={1} max={365}
              value={windowDays}
              onChange={e => setWindowDays(e.target.value)} />
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <Field
              label="Target %"
              hint="e.g. 99.9"
              required type="number" min={0} max={100} step="0.001"
              value={target}
              onChange={e => setTarget(e.target.value)} />
            {sliType === 'latency' && (
              <Field
                label="Threshold (ms)"
                required type="number" min={0} step="0.1"
                value={thresholdMs}
                onChange={e => setThresholdMs(e.target.value)} />
            )}
          </div>
          {error && (
            <div style={{ color: 'var(--err)', fontSize: 12 }}>{error}</div>
          )}
        </Stack>
      </form>
    </Modal>
  );
}

// BurnExplainButton — feeds the SLO's current status + fast
// + slow burn-rate samples to /api/copilot/explain-slo and
// renders the model's verdict inline. Self-hides when the
// copilot isn't configured (same gate the other CopilotExplain
// surfaces use). Operator clicks → modal with budget
// trajectory + recommended first investigation.
function BurnExplainButton({ sloId }: { sloId: string }) {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const [resp, setResp] = useState<Awaited<ReturnType<typeof api.copilotExplainSLO>> | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.copilotConfig().then(c => setEnabled(c.enabled)).catch(() => setEnabled(false));
  }, []);
  if (enabled !== true) return null;

  const run = async () => {
    setBusy(true); setError(null); setResp(null); setOpen(true);
    try {
      const r = await api.copilotExplainSLO(sloId);
      setResp(r);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Explain failed');
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <button onClick={run} disabled={busy} className="sec"
        title="Ask copilot whether this SLO's budget is on track or burning fast"
        style={{ fontSize: 12, padding: '4px 10px', color: 'var(--accent2)' }}>
        ✨ Explain burn
      </button>
      {open && (
        <Modal open={open} onClose={() => setOpen(false)} title="SLO burn analysis">
          {busy && <Spinner />}
          {error && <div style={{ color: 'var(--err)', fontSize: 12 }}>{error}</div>}
          {resp && (
            <div style={{ fontSize: 13, lineHeight: 1.5 }}>
              <div style={{
                display: 'flex', gap: 12, fontSize: 11,
                color: 'var(--text3)', marginBottom: 10,
                fontFamily: 'ui-monospace, monospace',
              }}>
                <span>fast burn: {resp.fastBurn.toFixed(2)}×</span>
                <span>slow burn: {resp.slowBurn.toFixed(2)}×</span>
                {resp.status && (
                  <>
                    <span>SLI: {(resp.status.sli * 100).toFixed(3)}%</span>
                    <span>budget: {(resp.status.budgetRemaining * 100).toFixed(2)}%</span>
                  </>
                )}
              </div>
              <div style={{ whiteSpace: 'pre-wrap' }}>{resp.explanation}</div>
            </div>
          )}
        </Modal>
      )}
    </>
  );
}

