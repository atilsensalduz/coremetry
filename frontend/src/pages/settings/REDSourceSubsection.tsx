import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import type { AnomalyREDSourceStats } from '@/lib/types';
import { humanize } from './shared';

// REDSourceSubsection — v0.10.1478 (operatör: "Servislerin kendi içerisinde
// haftalık olağan akışlarını takip etse ve gerçek bir anomaly öyle yakalasa").
// Metrik dedektörü + davranış motorunun RED serisi kaynağı
// (anomaly_sensitivity.redSource / redSourceEnv; Dedektör hassasiyeti bölümü,
// aynı Kaydet). Altında dedektörün son tikinin ölçümü (yalnız okunur; açılışta
// bir kez — ayar sayfası, polling yok).
export function REDSourceSubsection({ source, env, onChange }: {
  source?: 'auto' | 'span';
  env?: string;
  onChange: (source: 'auto' | 'span', env: string) => void;
}) {
  const mode = source === 'span' ? 'span' : 'auto';
  const [st, setSt] = useState<AnomalyREDSourceStats | null>(null);
  const [err, setErr] = useState<string | null>(null);
  useEffect(() => {
    api.getAnomalyREDSourceStatus().then(setSt).catch(e => setErr(humanize(e)));
  }, []);
  return (
    <div style={{ borderTop: '1px solid var(--border)', paddingTop: 16, marginTop: 4 }}>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <span style={{ fontSize: 13, color: 'var(--text)' }}>RED serisi kaynağı</span>
        <select aria-label="RED serisi kaynağı" value={mode}
          onChange={e => onChange(e.target.value === 'span' ? 'span' : 'auto', env ?? '')}>
          <option value="auto">Otomatik — VictoriaMetrics varsa örneklenmemiş metrik</option>
          <option value="span">Span — örneklenmiş trace (eski davranış)</option>
        </select>
      </label>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Otomatik (varsayılan): servisin OTel HTTP/gRPC sunucu histogramı VictoriaMetrics&apos;te varsa hata
        oranı, istek hızı ve p99 oradan (örneklenmemiş) okunur; yoksa servis başına span&apos;e düşülür.
        Taban (14 gün aynı saat, 28 gün haftanın saati), sürdürme ve eşikler aynıdır. Problem açıklaması
        kaynağı yazar: &quot;kaynak: VictoriaMetrics (örneklenmemiş)&quot; ya da &quot;kaynak: span (örneklenmiş)&quot;.
      </div>
      <label style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10, marginLeft: 24 }}>
        <span style={{ fontSize: 12, color: 'var(--text2)' }}>Ortam</span>
        <input type="text" aria-label="RED kaynağı ortamı" placeholder="boş = tüm ortamlar" maxLength={200}
          value={env ?? ''} disabled={mode === 'span'}
          onChange={e => onChange(mode, e.target.value)} />
      </label>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 4, marginLeft: 24, lineHeight: 1.5 }}>
        Doluysa VM serisi bu ortama süzülür (deployment_environment_name; toleranslı eşleme). Eşleşmezse
        VM kullanılmaz, span&apos;e düşülür.
      </div>
      <div style={{ fontSize: 11, color: 'var(--text2)', marginTop: 8, marginLeft: 24, lineHeight: 1.6 }}>
        {err && <span>Durum okunamadı: {err}</span>}
        {!err && st && !st.lastUnix && <span>Bu pod dedektörü koşturmuyor (lider başka pod) — ölçüm yok.</span>}
        {!err && st && !!st.lastUnix && (
          <>
            <div>
              Son tik: {st.configured ? 'VictoriaMetrics bağlı' : (st.reason || 'VM yok')} · {st.lastQueries} sorgu
              {st.lastQueryErrors ? ` (${st.lastQueryErrors} hata)` : ''} · {st.lastMs} ms · ısınma: {st.pendingBuckets === 0 ? 'tamam' : `${st.pendingBuckets} kova bekliyor`}
            </div>
            {st.families.filter(f => f.present || f.usable).map(f => (
              <div key={f.id}>
                {f.id}: {f.usable ? `hazır (${f.readyDetector || '—'})` : f.reason}
              </div>
            ))}
            {st.sources && Object.entries(st.sources).map(([m, c]) => (
              <div key={m}>{m}: VM {c.vm ?? 0} servis · span {c.span ?? 0} servis</div>
            ))}
            {st.lastError && <div>Son hata: {st.lastError}</div>}
          </>
        )}
      </div>
    </div>
  );
}
