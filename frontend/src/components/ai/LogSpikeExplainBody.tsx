import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { Chip } from '@/components/ui/Chip';
import { Spinner, Empty } from '@/components/Spinner';
import { DrawerSection } from '@/components/ui/Drawer';
import { TimeChart } from '@/components/charts/TimeChart';
import { api } from '@/lib/api';
import { logsHref } from '@/lib/logsUrl';
import { fmtClock } from '@/lib/utils';
import type { LogSpikeMode, LogSpikeLevel } from '@/lib/aiSubject';
import type { LogSpikeExplain } from '@/lib/types';
import { ExplainBody } from './ExplainBody';
import { AIFeedbackButtons } from './AIFeedbackButtons';
import { EvidenceTable } from './EvidenceChips';
import { logSpikeChart, logSpikeContributorTable, logSpikeDimNotes, logSpikeSummary } from './logSpikeView';

// LogSpikeExplainBody — v0.10.1284 (AI SRE Faz 1a): AI çekmecesinin
// `log-spike` öznesi ("bu log artışını ne açıklıyor"). ServiceChartsExplainBody
// emsali: KANIT ile ANLATIM ayrı — grafik (taban + artış bölgeli), katkı
// tablosu ve boyut notları sunucunun deterministik sonucundan çizilir; model
// düşse bile doğru kalır. Anlatım ExplainBody + 👍/👎.
//
// Maliyet: fetch YALNIZ açılışta (çekmece açıkken mount), poll yok, odakta
// yeniden çekme yok; sunucu 60 s / 5 dk önbellekli. "Yeniden üret" bilinçli
// olarak yeni bir ai_calls satırı.
export function LogSpikeExplainBody({ service, fromNs, toNs, mode, env, q, level, onAnswer }: {
  service: string;
  fromNs: number;
  toNs: number;
  mode: LogSpikeMode;
  env: string;
  q: string;
  level: LogSpikeLevel;
  /** Çekmece-içi sohbetin bağlamı (v0.9.479 sözleşmesi). */
  onAnswer?: (text: string) => void;
}) {
  const qr = useQuery<LogSpikeExplain>({
    // Anahtar TÜM girdileri taşır.
    queryKey: ['explain-log-spike', service, fromNs, toNs, mode, env, q, level],
    queryFn: async () => {
      const r = await api.copilotExplainLogSpike({ service, env, query: q, level, mode, fromNs, toNs });
      onAnswer?.(r.explanation);
      return r;
    },
    refetchOnWindowFocus: false,
    refetchOnMount: false,
    staleTime: Infinity,
    retry: false,
  });

  return (
    <div className="ls-explain">
      <div className="ls-explain__meta">
        <Chip pill>{service || 'tüm servisler'}</Chip>
        <Chip pill>{`${fmtClock(fromNs / 1e6)} – ${fmtClock(toNs / 1e6)}`}</Chip>
        <Chip pill>{mode === 'sel' ? 'seçili aralık' : 'otomatik tespit'}</Chip>
        {env && <Chip pill>{env}</Chip>}
        {level === 'error' && <Chip pill>yalnız ERROR</Chip>}
        {q && <Chip pill title={q}>{q.length > 40 ? `${q.slice(0, 40)}…` : q}</Chip>}
      </div>
      {qr.isPending ? (
        <div className="ls-explain__wait">
          <Spinner />
          <div className="field-hint">log hacmi ve alan payları okunuyor, model yazıyor…</div>
        </div>
      ) : qr.isError ? (
        <Empty icon="✨" title="Log artışı açıklanamadı" compact
          action={<Button variant="secondary" size="sm" onClick={() => void qr.refetch()}>↺ Yeniden dene</Button>}>
          {qr.error instanceof Error ? qr.error.message : 'Copilot yanıt vermedi.'}
        </Empty>
      ) : (
        <LogSpikeAnswer data={qr.data} service={service} env={env} q={q} level={level}
          busy={qr.isFetching} onRegenerate={() => void qr.refetch()} />
      )}
    </div>
  );
}

export function LogSpikeAnswer({ data, service, env, q, level, busy, onRegenerate }: {
  data: LogSpikeExplain;
  service: string;
  env: string;
  q: string;
  level: LogSpikeLevel;
  busy: boolean;
  onRegenerate: () => void;
}) {
  const res = data.spike;
  const chart = useMemo(() => logSpikeChart(res), [res]);
  const table = useMemo(() => logSpikeContributorTable(res), [res]);
  const notes = useMemo(() => logSpikeDimNotes(res), [res]);
  const summary = logSpikeSummary(res, fmtClock);
  const sp = res.spike;
  const sample = res.data?.samples?.[0];

  return (
    <>
      <DrawerSection title="Artış">
        {summary && <div className="ls-explain__summary">{summary}</div>}
        {res.message && <div className="field-hint">{res.message}</div>}
        {chart.times.length >= 2
          ? <TimeChart times={chart.times} series={chart.series} regions={chart.regions} height={130} hideLegend leftInteger />
          : <div className="field-hint">grafik için veri yok</div>}
      </DrawerSection>

      <DrawerSection title="Kaynak">
        {table
          ? <EvidenceTable p={table} />
          : <div className="field-hint">{sp ? 'artış tek bir boyutta yoğunlaşmıyor (eşik: artışın ≥%10’u, ≥20 satır)' : 'karşılaştırılacak artış yok'}</div>}
        {notes.length > 0 && (
          <ul className="ls-explain__notes">
            {notes.map(n => <li key={n} className="field-hint">{n}</li>)}
          </ul>
        )}
        {(res.warnings ?? []).map(w => <div key={w} className="field-hint ls-explain__warn">⚠ {w}</div>)}
      </DrawerSection>

      <DrawerSection title="Anlatım">
        {data.explanation.trim()
          ? <ExplainBody text={data.explanation} busy={false} />
          : (
            <div className="field-hint">
              {data.error ? `Anlatım üretilemedi (${data.error}).` : 'Model boş yanıt döndürdü.'}
              {' '}Yukarıdaki grafik ve tablo yine de ölçülmüş veridir.
            </div>
          )}
        {sample && !data.explanation.trim() && (
          <div className="field-hint mono">{`${fmtClock(sample.tsMs)} ${sample.severity} ${sample.pod || sample.service || ''}: ${sample.body}`}</div>
        )}
      </DrawerSection>

      {sp && (
        <DrawerSection title="Sonraki adım">
          <Link className="ls-explain__cta" to={logsHref({
            window: { fromNs: sp.fromMs * 1e6, toNs: sp.toMs * 1e6 }, service: service || undefined,
            env: env || undefined, q: q || undefined, severity: level === 'error' ? 17 : undefined,
          })}>Artış penceresinin loglarını aç →</Link>
        </DrawerSection>
      )}

      <div className="ls-explain__foot">
        <span>AI çıkarımı — veriyle doğrula</span>
        <Link to="/ai">/ai kaydı</Link>
        <AIFeedbackButtons exchangeId={data.exchangeId} />
        <span className="ls-explain__spacer" />
        <Button variant="secondary" size="xs" onClick={onRegenerate} loading={busy}
          title="Yeni bir üretim çalıştır (yeni ai_calls kaydı)">
          ↺ Yeniden üret
        </Button>
      </div>
    </>
  );
}
