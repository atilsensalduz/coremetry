// ProblemInvestigationCards — v0.10.1288 (AI SRE Faz 2). AI SRE araştırmasının
// hipotez kartları + zaman çizelgesi (InvestigationResult çağırır):
//   - en olası ≤3 hipotez: bant + KOD skoru (öncül + kontrol ağırlıkları; LLM,
//     oy ya da operatör işareti skora girmez), her kontrolün sonucu ✓/✗/? +
//     gerekçe + kanıt çipi;
//   - editor: "Doğrula" (hipotezin derin okumalarıyla yeniden koşu, POST
//     investigation {kind:'verify'}), "Ele" / "Onayla" (+ isteğe bağlı not;
//     işaret yalnız etiket, skoru DEĞİŞTİRMEZ). Viewer kartları GÖRÜR, düğmeyi
//     görmez;
//   - kök-neden sentezleyicisi (korelatör) AYRI motor olarak ayrı kutuda —
//     iki motorun sonucu birbirine karıştırılmaz;
//   - zaman çizelgesi ≤30 olay (sıralı liste; tablo değil — tek sütunlu olay
//     akışı, tablo standardının "kayıt listesi" sınıfına girmez).
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { EvidenceChips } from '@/components/ai/EvidenceChips';
import { Badge, Button, Field, LinkButton } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { fmtClock, tsLong } from '@/lib/utils';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import {
  ROOT_CAUSE_ANCHOR, TIMELINE_KIND, bandTone, canAct, checkEvidence, fmtScore, humanLabel, outcomeView, shownHypotheses,
} from './hypothesisView';

const muted = { color: 'var(--text3)' } as const;

export function ProblemInvestigationCards({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  const hyps = shownHypotheses(inv);
  const timeline = inv.timeline ?? [];
  const synth = inv.synthRef;
  if (hyps.length === 0 && timeline.length === 0 && !synth?.topSuspect) return null;
  return (
    <div style={{ display: 'grid', gap: 10 }}>
      {hyps.length > 0 && (
        <div>
          <div style={{ ...muted, fontSize: 12, marginBottom: 6 }}>
            Hipotezler — AI SRE ajanı · kod skoru (öncül + kontrol ağırlıkları; AI ve işaretler skoru değiştirmez)
            {inv.kind === 'verify' && inv.hypothesis ? ` · son koşu: ${inv.hypothesis} doğrulaması` : ''}
          </div>
          <div style={{ display: 'grid', gap: 8 }}>
            {hyps.map(h => <HypothesisCard key={h.kind} inv={inv} h={h} canEdit={canEdit} />)}
          </div>
        </div>
      )}
      {synth?.topSuspect && (
        <div className="cm-callout" style={{ margin: 0 }}>
          <b>Korelatör</b> (kök-neden sentezleyicisi — ayrı motor, ajanın skoru değil):{' '}
          şüpheli <span className="mono">{synth.topSuspect}</span> (güven %{Math.round(synth.confidence * 100)}){' '}
          <LinkButton onClick={() => document.getElementById(ROOT_CAUSE_ANCHOR)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}>
            Kök neden paneline git
          </LinkButton>
        </div>
      )}
      {timeline.length > 0 && (
        <div>
          <div style={{ ...muted, fontSize: 12, marginBottom: 4 }}>Zaman çizelgesi ({timeline.length} olay)</div>
          <ol aria-label="AI SRE zaman çizelgesi" style={{ margin: 0, paddingLeft: 0, listStyle: 'none', display: 'grid', gap: 2 }}>
            {timeline.map((e, i) => (
              <li key={`${e.kind}:${e.at}:${i}`} style={{ display: 'grid', gridTemplateColumns: '72px 96px 1fr', gap: 8 }}>
                <span className="num" style={muted} title={tsLong(e.at)}>{fmtClock(e.at / 1e6)}</span>
                <span style={muted}>{TIMELINE_KIND[e.kind] ?? e.kind}</span>
                <span style={{ fontWeight: e.kind === 'problem_start' ? 600 : undefined }}>{e.label}</span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  );
}

function HypothesisCard({ inv, h, canEdit }: { inv: SREInvestigation; h: SREHypothesis; canEdit: boolean }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState<'' | 'verify' | 'mark'>('');
  const [marking, setMarking] = useState<'' | 'dismissed' | 'confirmed'>('');
  const [note, setNote] = useState('');
  const human = humanLabel(h);
  const actionable = canEdit && canAct(inv);

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: keys.problems.investigation(inv.problemId) });
    await qc.invalidateQueries({ queryKey: keys.sre.all });
  };
  const verify = async () => {
    setBusy('verify');
    try {
      const r = await api.enqueueProblemInvestigation(inv.problemId, { kind: 'verify', hypothesis: h.kind });
      if (r.created) toast.success(`${h.label} hipotezi doğrulama için kuyruğa alındı`);
      else toast.info(r.reason ? `Zaten ${r.reason}` : 'Zaten kuyrukta');
      await refresh();
    } catch (e) {
      toast.error(`Kuyruğa alınamadı: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy('');
    }
  };
  const mark = async () => {
    if (!marking) return;
    setBusy('mark');
    try {
      await api.markProblemHypothesis(inv.problemId, h.kind, { state: marking, note: note.trim() || undefined });
      toast.success(marking === 'dismissed' ? 'Hipotez elendi (skor değişmedi)' : 'Hipotez onaylandı (skor değişmedi)');
      setMarking('');
      setNote('');
      await refresh();
    } catch (e) {
      toast.error(`İşaretlenemedi: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy('');
    }
  };

  return (
    <div data-hypothesis={h.kind} style={{
      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 10px',
      opacity: h.humanState?.state === 'dismissed' ? 0.7 : undefined,
    }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <b>{h.label}</b>
        <Badge tone={bandTone(h.band)}>{h.band}</Badge>
        <span className="num" title={`öncül ${fmtScore(h.prior)} + Σ ağırlık × sonuç`}>{fmtScore(h.score)}</span>
        <span style={{ ...muted, fontSize: 12 }}>kategori: {h.category}</span>
        {human && (
          <Badge tone={h.humanState?.state === 'confirmed' ? 'success' : 'neutral'} title={h.humanState?.at ? tsLong(h.humanState.at) : undefined}>
            {human}
          </Badge>
        )}
      </div>
      {h.humanState?.note && <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>Not: {h.humanState.note}</div>}
      <ul style={{ margin: '6px 0 0', paddingLeft: 0, listStyle: 'none', display: 'grid', gap: 3 }}>
        {h.checks.map(c => {
          const v = outcomeView(c.outcome);
          return (
            <li key={c.name} style={{ display: 'grid', gridTemplateColumns: '18px 1fr', gap: 4 }}>
              <span aria-label={v.label} title={`${v.label} · ağırlık ${c.weight}`}
                style={{ color: c.outcome > 0 ? 'var(--ok)' : c.outcome < 0 ? 'var(--err)' : 'var(--text3)' }}>{v.glyph}</span>
              <span style={c.outcome === 0 ? muted : undefined}>
                {c.reason}
                <EvidenceChips items={checkEvidence(inv, c.evidenceRef)} />
              </span>
            </li>
          );
        })}
      </ul>
      {actionable && !marking && (
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
          <Button variant="secondary" size="sm" loading={busy === 'verify'} disabled={!!busy} onClick={() => { void verify(); }}>
            Doğrula
          </Button>
          <Button variant="ghost" size="sm" disabled={!!busy} onClick={() => setMarking('dismissed')}>Ele</Button>
          <Button variant="ghost" size="sm" disabled={!!busy} onClick={() => setMarking('confirmed')}>Onayla</Button>
        </div>
      )}
      {actionable && marking && (
        <div style={{ display: 'grid', gap: 6, marginTop: 8 }}>
          <Field label={marking === 'dismissed' ? 'Eleme notu (isteğe bağlı)' : 'Onay notu (isteğe bağlı)'} value={note} maxLength={500}
            onChange={e => setNote(e.target.value)} hint="İşaret yalnız etikettir — ajanın skoru değişmez." />
          <div style={{ display: 'flex', gap: 6 }}>
            <Button variant="primary" size="sm" loading={busy === 'mark'} onClick={() => { void mark(); }}>
              {marking === 'dismissed' ? 'Ele' : 'Onayla'}
            </Button>
            <Button variant="ghost" size="sm" disabled={busy === 'mark'} onClick={() => { setMarking(''); setNote(''); }}>Vazgeç</Button>
          </div>
        </div>
      )}
    </div>
  );
}
