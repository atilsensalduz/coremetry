// ProblemInvestigationRecommendations — v0.10.1293 — AI SRE Faz 4: yalnız öneri
// (runbook eşleme + geri alma metni). InvestigationResult çağırır.
//   - her kart "öneri — uygulanmadı" rozeti taşır: ajan HİÇBİR şeyi uygulamaz;
//   - metin + gerekçe; geri alma önerisinde rollout satırı (şimdiki → önceki
//     sürüm) ve okunduysa Argo CD durumu — yalnız bilgi, düğme değil;
//   - "Runbook'u aç": mevcut runbook sayfasına problemId önceden dolu gider
//     (/runbook?id=…&problemId=…); çalıştırmayı İNSAN başlatır (mevcut
//     runbook.execute audit'i). Dış bağlantı yeni sekmede açılır;
//   - editor: "Uygulandı" / "Ele" — POST …/recommendations/{rid}; yalnız
//     etiket (audit sre.recommendation.ack). Viewer kartları GÖRÜR, düğmeyi
//     görmez.
import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { Badge, Button } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { tsLong } from '@/lib/utils';
import type { SRERecommendation, SREInvestigation } from '@/lib/types';
import { REC_PENDING, canAckRecommendation, recHumanLabel, runbookTarget, shownRecommendations } from './recommendationView';

const muted = { color: 'var(--text3)' } as const;

export function ProblemInvestigationRecommendations({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  const recs = shownRecommendations(inv);
  if (recs.length === 0) return null;
  return (
    <div>
      <div style={{ ...muted, fontSize: 12, marginBottom: 6 }}>
        Öneriler — AI SRE ajanı · yalnız öneri: ajan hiçbir şeyi uygulamaz; kararı ve uygulamayı insan verir
      </div>
      <div style={{ display: 'grid', gap: 8 }}>
        {recs.map(r => <RecommendationCard key={r.id} inv={inv} r={r} canEdit={canEdit} />)}
      </div>
    </div>
  );
}

function RecommendationCard({ inv, r, canEdit }: { inv: SREInvestigation; r: SRERecommendation; canEdit: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState<'' | 'ack' | 'dismissed'>('');
  const human = recHumanLabel(r);
  const target = runbookTarget(r);
  const actionable = canEdit && canAckRecommendation(inv, r);

  const mark = async (state: 'ack' | 'dismissed') => {
    setBusy(state);
    try {
      await api.ackProblemRecommendation(inv.problemId, r.id, { state });
      toast.success(state === 'ack' ? 'Öneri "uygulandı" olarak işaretlendi (ajan bir şey uygulamadı)' : 'Öneri elendi');
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(inv.problemId) });
    } catch (e) {
      toast.error(`İşaretlenemedi: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy('');
    }
  };

  return (
    <div data-recommendation={r.id} style={{
      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 10px',
      opacity: r.state === 'dismissed' ? 0.7 : undefined,
    }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <Badge tone="neutral" title="Ajan yalnız öneri üretir — hiçbir şeyi uygulamaz">{REC_PENDING}</Badge>
        {human && <Badge tone={human.tone} title={r.ackAt ? tsLong(r.ackAt) : undefined}>{human.text}</Badge>}
      </div>
      <div style={{ marginTop: 4 }}>{r.text}</div>
      {r.rationale && <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>Gerekçe: {r.rationale}</div>}
      {r.rollout && (
        <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>
          Rollout: <span className="mono">{r.rollout.workload}</span> {r.rollout.from} → {r.rollout.to}
          {r.argo ? <> · Argo CD <span className="mono">{r.argo.app}</span>: {r.argo.status}</> : null}
        </div>
      )}
      {!r.rollout && r.argo && (
        <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>Argo CD <span className="mono">{r.argo.app}</span>: {r.argo.status}</div>
      )}
      {(target || actionable) && (
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          {target?.kind === 'internal' && (
            <Button variant="secondary" size="sm" onClick={() => navigate(target.href)}
              title="Runbook sayfası problem bağlı açılır — çalıştırmayı siz başlatırsınız">
              Runbook&apos;u aç
            </Button>
          )}
          {target?.kind === 'external' && (
            <a href={target.href} target="_blank" rel="noopener noreferrer" title="Problemin runbook bağlantısı (yeni sekme)">
              Runbook&apos;u aç ↗
            </a>
          )}
          {actionable && (
            <>
              <Button variant="ghost" size="sm" loading={busy === 'ack'} disabled={!!busy} onClick={() => { void mark('ack'); }}
                title="Öneriyi siz uyguladıysanız işaretleyin — yalnız etiket">
                Uygulandı
              </Button>
              <Button variant="ghost" size="sm" loading={busy === 'dismissed'} disabled={!!busy} onClick={() => { void mark('dismissed'); }}>
                Ele
              </Button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
