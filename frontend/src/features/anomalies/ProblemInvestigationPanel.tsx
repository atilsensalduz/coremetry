// ProblemInvestigationPanel — v0.10.1282 (AI SRE Faz 0a). Problem detayında
// AI SRE ajanının araştırma kaydı: kuyruk sırası, "Araştırılıyor…", sonuç
// (servis taraması hükmü + bulgular + yaş) ve editor için "Araştır / Yeniden
// araştır". Ajan YALNIZ öneri üretir; burada hiçbir eylem bir şeyi değiştirmez.
// v0.10.1288 (Faz 2) — sonuç gövdesinde hipotez kartları + zaman çizelgesi.
//
// Veri: GET /api/problems/{id}/investigation (10 s cache); yoklama yalnız iş
// kuyrukta / koşarken (10 s, gizli sekmede durur); değişim SSE
// 'sre.investigation' ile. Kuyruk durumu (GET /api/sre/status) yalnız kayıt
// kuyruktayken okunur: rol yoksa banner "SRE rolü yok — kuyrukta N iş
// bekliyor". Viewer durumu GÖRÜR, düğmeyi görmez. Bulgular ≤8 satır, sabit
// sıra (taramanın sıralaması) → liste, tablo değil (tablo standardı).
import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useAuth } from '@/components/AuthProvider';
import { Badge, Button } from '@/components/ui';
import type { Tone } from '@/components/ui';
import { Spinner } from '@/components/Spinner';
import { keys, useProblemInvestigation, useSREStatus } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { agoTR, tsLong } from '@/lib/utils';
import type { ProblemInvestigationResponse, SREInvestigation, SREStatus } from '@/lib/types';
import { Sect } from './detailSections';
// v0.10.1287 — Faz 1b sonuç gövdesi (adım tablosu, bulgular + kanıt, AI özeti, Sohbette sor).
import { InvestigationResult } from './InvestigationResult';
import { SRE_SETTINGS_PATH, sreRoleMissing } from './sreInvestigationView';

const STATE_LABEL: Record<string, { label: string; tone: Tone }> = {
  none: { label: 'araştırılmadı', tone: 'neutral' },
  queued: { label: 'kuyrukta', tone: 'info' },
  running: { label: 'araştırılıyor', tone: 'accent' },
  done: { label: 'tamam', tone: 'success' },
  partial: { label: 'kısmi', tone: 'warning' },
  shared: { label: 'paylaşılan', tone: 'neutral' },
  error: { label: 'hata', tone: 'danger' },
  skipped: { label: 'atlandı', tone: 'neutral' },
};

export function ProblemInvestigationPanel({ problemId }: { problemId: string }) {
  const q = useProblemInvestigation(problemId);
  const data = q.data;
  const status = useSREStatus(data?.state === 'queued');
  const { user } = useAuth();
  const canEdit = user?.role === 'admin' || user?.role === 'editor';
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);

  if (q.isLoading) {
    return <Sect title="AI SRE araştırması"><Spinner label="Araştırma kaydı yükleniyor…" /></Sect>;
  }
  if (!data) {
    // İlk okuma düştü: boş kart yerine dürüst tek satır.
    return (
      <Sect title="AI SRE araştırması">
        <div style={{ fontSize: 13, color: 'var(--text3)' }}>Araştırma kaydı okunamadı.</div>
      </Sect>
    );
  }
  const st = STATE_LABEL[data.state] ?? STATE_LABEL.none;
  const inv = data.investigation;

  const enqueue = async () => {
    setBusy(true);
    try {
      const r = await api.enqueueProblemInvestigation(problemId);
      if (r.created) toast.success('Araştırma kuyruğa alındı');
      else toast.info(r.reason ? `Zaten ${r.reason}` : 'Zaten kuyrukta');
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(problemId) });
      await qc.invalidateQueries({ queryKey: keys.sre.all });
    } catch (e) {
      toast.error(`Kuyruğa alınamadı: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };
  const finished = data.state === 'done' || data.state === 'partial' || data.state === 'error' || data.state === 'shared' || data.state === 'skipped';
  const showButton = canEdit && data.enabled && (data.state === 'none' || finished);

  return (
    <Sect title="AI SRE araştırması"
      sub={<span><Badge tone={st.tone}>{st.label}</Badge>{inv?.finishedAt ? <span title={tsLong(inv.finishedAt)}> · {agoTR(inv.finishedAt)}</span> : null}</span>}>
      {!data.enabled && (
        <div className="cm-callout" role="status">
          AI SRE kapalı — yeni araştırma açılmaz.{' '}
          {user?.role === 'admin'
            ? <Link to={SRE_SETTINGS_PATH}>Ayarlar → AI SRE</Link>
            : 'Yönetici Ayarlar → AI SRE bölümünden açabilir.'}
        </div>
      )}
      <StateBody data={data} status={status.data} canEdit={canEdit} />
      {showButton && (
        <div style={{ marginTop: 10 }}>
          <Button variant="secondary" size="sm" onClick={enqueue} loading={busy}>
            {data.state === 'none' ? 'Araştır' : 'Yeniden araştır'}
          </Button>
        </div>
      )}
    </Sect>
  );
}

function StateBody({ data, status, canEdit }: { data: ProblemInvestigationResponse; status?: SREStatus; canEdit: boolean }) {
  const inv = data.investigation;
  switch (data.state) {
    case 'none':
      return <div style={{ fontSize: 13, color: 'var(--text3)' }}>Bu problem henüz araştırılmadı.</div>;
    case 'queued':
      return (
        <>
          <div style={{ fontSize: 13 }}>
            Kuyrukta{data.position ? ` (${data.position}. sırada)` : ''}
            {inv?.enqueuedAt ? <span style={{ color: 'var(--text3)' }} title={tsLong(inv.enqueuedAt)}> · {agoTR(inv.enqueuedAt)} alındı</span> : null}
          </div>
          {sreRoleMissing(status) && (
            <div className="cm-callout is-warning" role="alert" style={{ marginTop: 8 }}>
              SRE rolü yok — kuyrukta {status!.queueDepth} iş bekliyor
            </div>
          )}
        </>
      );
    case 'running':
      return <Spinner label="Araştırılıyor…" hint={inv?.claimedBy ? `pod ${inv.claimedBy}` : undefined} />;
    case 'shared':
      return (
        <div style={{ fontSize: 13 }}>
          Aynı serviste ({inv?.service}) yakın tarihli bir araştırma var — tekrar koşulmadı.{' '}
          {inv?.sharedFrom && <Link to={`/problems?problem=${encodeURIComponent(inv.sharedFrom)}`}>O araştırmayı aç</Link>}
        </div>
      );
    default:
      return inv ? <Result inv={inv} canEdit={canEdit} /> : null;
  }
}

function Result({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  // v0.10.1287 — gövde InvestigationResult'ta (0a tek-adım kaydı da orada çizilir).
  // v0.10.1288 — editor hipotez kartlarında Doğrula / Ele / Onayla görür.
  return <InvestigationResult inv={inv} canEdit={canEdit} />;
}
