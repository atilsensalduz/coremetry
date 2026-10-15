// IncidentCaseCard — CoSRE öğrenme Faz 3: kapanan Problem / çözülen exception
// grubunun vaka kaydı. Taslak (CoSRE kurdu) → editor tek "Onayla" ile nihai
// nedeni doğrular; form CoSRE'nin önerisiyle önceden dolu, değiştirilebilir.
// Onaylanmamış vaka hiçbir yerde olgu sayılmaz (similar_incidents, çekmece
// kanıtı "ONAYSIZ" işaretler). Viewer kartı salt-okunur görür. Vaka yoksa
// (açık problem / taslak henüz kurulmadı) kart çizilmez.
import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Button, SelectField, TextareaField } from '@/components/ui';
import { toast } from '@/lib/toast';
import type { IncidentCase } from '@/lib/types';
import { Sect } from './detailSections';
import { caseSignalLines, categoryLabel, correctnessLabel } from './incidentCaseView';

export function IncidentCaseCard({ source, sourceId }: { source: 'problem' | 'exception'; sourceId: string }) {
  const q = useQuery({
    queryKey: ['incident-case', source, sourceId],
    queryFn: () => api.incidentCase(source, sourceId),
    staleTime: 60_000,
  });
  const c = q.data?.case;
  if (!c) return null;
  return (
    <Sect title="Vaka kaydı" accent={c.status === 'draft'}
      sub={c.status === 'confirmed'
        ? `onaylı · ${c.confirmedBy ?? ''}`
        : 'CoSRE taslağı — onaylanana dek olgu sayılmaz'}>
      <CaseSignals c={c} />
      {c.status === 'confirmed'
        ? <ConfirmedCase c={c} />
        : <ConfirmForm c={c} categories={q.data?.categories ?? []} />}
    </Sect>
  );
}

function CaseSignals({ c }: { c: IncidentCase }) {
  const lines = caseSignalLines(c);
  if (lines.length === 0) return null;
  return (
    <ul style={{ margin: '0 0 10px', paddingLeft: 18, fontSize: 13, color: 'var(--text2)' }}>
      {lines.map(l => <li key={l}>{l}</li>)}
    </ul>
  );
}

function ConfirmedCase({ c }: { c: IncidentCase }) {
  return (
    <div style={{ fontSize: 13, display: 'grid', gap: 4 }}>
      <div><b>Neden:</b> {c.finalCause}</div>
      <div><b>Kategori:</b> {categoryLabel(c.category)}</div>
      {c.resolution && <div><b>Çözüm:</b> {c.resolution}</div>}
      <div style={{ color: 'var(--text3)', fontSize: 12 }}>CoSRE önerisi: {correctnessLabel(c.cosreCorrect)}</div>
    </div>
  );
}

function ConfirmForm({ c, categories }: { c: IncidentCase; categories: string[] }) {
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  const [cause, setCause] = useState(c.suggestedCause);
  const [category, setCategory] = useState(c.deployInWindow ? 'deploy' : 'other');
  const [resolution, setResolution] = useState('');
  const [busy, setBusy] = useState(false);
  useEffect(() => { setCause(c.suggestedCause); }, [c.caseId, c.suggestedCause]);
  if (!isEditor) {
    return (
      <div style={{ fontSize: 13 }}>
        <div><b>CoSRE önerisi (doğrulanmadı):</b> {c.suggestedCause || '—'}</div>
        <div style={{ color: 'var(--text3)', fontSize: 12, marginTop: 4 }}>Onay editor ya da admin rolü ister.</div>
      </div>
    );
  }
  const confirm = async () => {
    setBusy(true);
    try {
      await api.confirmIncidentCase(c.caseId, { finalCause: cause, category, resolution });
      toast.success('Vaka onaylandı — CoSRE benzer olaylarda bunu kullanacak');
      await qc.invalidateQueries({ queryKey: ['incident-case', c.source, c.sourceId] });
    } catch (e) {
      toast.error(`Onaylanamadı: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div style={{ display: 'grid', gap: 8, maxWidth: 720 }}>
      <TextareaField label="Kök neden" rows={3} value={cause} onChange={e => setCause(e.target.value)}
        hint={c.suggestedCause ? 'CoSRE önerisiyle dolu — doğru değilse düzeltin' : 'CoSRE bir neden önermedi'} />
      <SelectField label="Kategori" value={category} onChange={e => setCategory(e.target.value)}>
        {categories.map(k => <option key={k} value={k}>{categoryLabel(k)}</option>)}
      </SelectField>
      <TextareaField label="Çözüm (isteğe bağlı)" rows={2} value={resolution} onChange={e => setResolution(e.target.value)} />
      <div>
        <Button variant="primary" size="sm" onClick={confirm} loading={busy} disabled={!cause.trim()}>
          Onayla
        </Button>
      </div>
    </div>
  );
}
